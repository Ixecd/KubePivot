package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
	"github.com/Ixecd/kubepivot/internal/sharding"
)

// StartGlobal 启动 global 模式 controller（v2.5.0+ 分片版）
//
// v2.5.0 架构变化（vs v2.4.0）：
//   - 业务路径（reconcile / watchers / worker pool）在每个 pod 都跑
//     由 sharding manager 限制每个 pod 只处理自己持有的 shard
//   - leader lease 仅用于决定"谁跑 sweeper"（清理孤儿 lease 等全局任务）
//   - 老的"runAsLeader"改名"runSweeperLoop"，仅 leader 跑
//
// 目标：把 v2.4.0 实测的 leader 16.93% CPU 通过分片均摊到 ~6%/pod
func StartGlobal(ctx context.Context) {
	kubeconfig := getenv("KUBE_CONFIG", "")

	totalShards := getenvInt("KUBEPIVOT_SHARDS", 10)
	replicas := getControllerReplicas(ctx, kubeconfig)

	slog.Info("🌐 global controller 启动中（v2.5.0 分片模式）",
		"total_shards", totalShards,
		"replicas", replicas,
		"quota_per_pod", sharding.QuotaPerPod(totalShards, replicas),
	)

	// ── 业务路径（每个 pod 都跑）──────────────────────────────────────────────

	gs := NewGlobalState()

	// v2.7 Step 2b-1: informer pool 提前创建（在 worker pool 之前）
	//
	// 这样 handleTask 闭包可以引用 informerPool，让 reconciler 用 InformerDetector。
	// 注意：NewInformerPool 创建空 pool 不依赖 shardMgr，
	//       informerPool.Start 在 shardMgr 创建后调用（见下方 step 7）。
	// shardMgr 此时还未创建，先传 nil；Start 时 pool 会用此时已创建的 shardMgr。
	//
	// 双保险：
	//   - InformerDetector 优先查 cache（命中即返回 true）
	//   - cache 未命中或 informer 未启动 → fallback 到 KubectlDetector
	//   - 既有 v2.5/v2.6 的 reconcile 行为完全保留
	informerPool := NewInformerPool(nil, totalShards, kubeconfig)

	pool := NewWorkerPool(20, func(taskCtx context.Context, task ReconcileTask) error {
		return handleTask(taskCtx, gs, kubeconfig, task, informerPool)
	})

	var shardMgr *sharding.MultiLeaseManager
	shardMgr = sharding.NewMultiLeaseManager(sharding.MultiLeaseConfig{
		TotalShards: totalShards,
		Replicas:    replicas,
		Kubeconfig:  kubeconfig,
		// v2.5.0 Step 3：分片变化时即时清理孤儿（5s grace period）
		OnShardChanged: func(added, removed []int) {
			if len(removed) == 0 {
				return // 仅持有增多 → 无需清理
			}
			// Grace period：让正在跑的 reconcile 完成（Q3 决策 B 方案）
			time.Sleep(5 * time.Second)
			cleaned := gs.RemoveOrphanProjects(func(ns string) bool {
				return shardMgr.Shards().OwnsNamespace(ns, totalShards)
			})
			if len(cleaned) > 0 {
				slog.Info("🧹 OnShardChanged 触发的孤儿清理完成",
					"removed_shards", removed, "cleaned_namespaces", cleaned)
			}
		},
	})

	// v2.7 Step 2b-1: shardMgr 创建后，绑到 informerPool（informerPool 之前用 nil 占位）
	informerPool.SetShardMgr(shardMgr)

	var wg sync.WaitGroup

	// 1. shard lease 管理（每个 pod 独立抢占）
	wg.Add(1)
	go func() {
		defer wg.Done()
		shardMgr.Run(ctx)
	}()

	// 2. worker pool
	wg.Add(1)
	go func() {
		defer wg.Done()
		pool.Start(ctx)
	}()

	// 3. Namespace Watcher（每 pod 都 watch，但 enqueue 时按 shard 过滤）
	wg.Add(1)
	go func() {
		defer wg.Done()
		watchNamespaces(ctx, kubeconfig, gs, pool, shardMgr, totalShards)
	}()

	// 4. ConfigMap Watcher
	wg.Add(1)
	go func() {
		defer wg.Done()
		watchConfigMaps(ctx, kubeconfig, gs, pool, shardMgr, totalShards)
	}()

	// 5. Reconcile Loop
	wg.Add(1)
	go func() {
		defer wg.Done()
		reconcileLoop(ctx, gs, pool, shardMgr, totalShards)
	}()

	// 6. v2.5.0 Step 3：周期性兜底自扫孤儿（防 OnShardChanged 漏触发）
	wg.Add(1)
	go func() {
		defer wg.Done()
		orphanSweeper(ctx, gs, shardMgr, totalShards)
	}()

	// 7. v2.7 Step 2a-2 / 2b-1: informer pool 启动 + 接入 reconcile
	//
	// informerPool 在 NewWorkerPool 之前已创建（见上方 "v2.7 Step 2b-1" 注释块）
	// 此处启动 informer（依赖 shardMgr.Shards()，要等 shardMgr 创建后）
	//
	// v2.7.x 计划：
	//   - 加 controller HTTP server 暴露 /metrics
	//   - 调 informerPool.RegisterMetrics(prometheus.DefaultRegisterer)
	informerPool.Start(ctx, "deployments", "apps/v1")
	defer informerPool.StopAll()

	// 8. v2.6.1: VerifiedTrafficWriter (每 pod 跑, shardSet 过滤)
	// 周期扫描 RUNNING + 5min 稳态的项目, 把 K8s 实际 traffic 写入
	// kubepivot-verified-traffic ConfigMap (多环境流量传播链生产者)
	wg.Add(1)
	go func() {
		defer wg.Done()
		runVerifiedTrafficWriter(ctx, gs, shardMgr, totalShards, kubeconfig)
	}()

	// ── Leader 选举（仅为 sweeper）──────────────────────────────────────────────

	wg.Add(1)
	go func() {
		defer wg.Done()
		runGlobalLeaderElection(ctx, kubeconfig, func(leaderCtx context.Context) {
			runSweeperLoop(leaderCtx, kubeconfig, totalShards)
		})
	}()

	wg.Wait()
	slog.Info("🌐 global controller 所有 goroutine 已退出")
}

// ── Helper：env / replicas 读取 ──────────────────────────────────────────────

func getenvInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 {
		slog.Warn("env 不是合法正整数，回退默认", "key", key, "value", v, "default", defaultVal)
		return defaultVal
	}
	return n
}

// getControllerReplicas 启动时读 deployment.spec.replicas
//
// 失败时回退到 3（最常见副本数），避免 quota 计算崩溃
func getControllerReplicas(ctx context.Context, kubeconfig string) int {
	out, err := executor.GetExecutor().Kubectl(ctx, kubeconfig,
		"get", "deployment", "kubepivot-controller",
		"-n", "kubepivot-system",
		"-o", "jsonpath={.spec.replicas}",
	)
	if err != nil {
		slog.Warn("无法读取 deployment replicas，回退默认 3", "err", err)
		return 3
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || n < 1 {
		slog.Warn("解析 replicas 失败，回退默认 3", "raw", string(out), "err", err)
		return 3
	}
	return n
}

// ── Namespace Watcher ───────────────────────────────────────────────────────

// watchNamespaces 监听带 kubepivot.io/managed=true label 的 namespace
// v2.5.0：watcher 仍 watch 全集群，但只对自己 shard 的 ns 做出反应
func watchNamespaces(
	ctx context.Context, kubeconfig string,
	gs *GlobalState, pool *WorkerPool,
	shardMgr *sharding.MultiLeaseManager, totalShards int,
) {
	w := NewKubectlWatcher("namespace", "kubepivot.io/managed=true")
	w.Kubeconfig = kubeconfig

	slog.Info("👀 Namespace Watcher 启动",
		"label", "kubepivot.io/managed=true")

	_ = w.Watch(ctx, func(ev WatchEvent) {
		meta := ev.Meta()
		if IsProtectedNamespace(meta.Name) {
			return
		}

		// v2.5.0：shard 过滤
		if !shardMgr.Shards().OwnsNamespace(meta.Name, totalShards) {
			return
		}

		switch ev.Action {
		case WatchAdded, WatchModified:
			slog.Info("📥 发现 managed namespace（属于本 shard）",
				"ns", meta.Name, "action", ev.Action)
			loadResourcesConfigMap(ctx, kubeconfig, gs, meta.Name)
		case WatchDeleted:
			gs.RemoveProject(meta.Name)
		}
	})
}

func loadResourcesConfigMap(ctx context.Context, kubeconfig string, gs *GlobalState, namespace string) {
	out, err := executor.GetExecutor().Kubectl(ctx, kubeconfig,
		"get", "configmap", "kubepivot-resources",
		"-n", namespace,
		"-o", "jsonpath={.data.resources\\.yaml}",
		"--ignore-not-found",
	)
	if err != nil {
		slog.Warn("读取 resources ConfigMap 失败",
			"namespace", namespace, "err", err)
		return
	}
	content := string(out)
	if content == "" {
		slog.Info("namespace 已 enrolled 但尚未 kp sync-resources",
			"namespace", namespace,
			"hint", "运行 kp controller enroll 推送 configs/resources.yaml")
		return
	}
	if _, err := gs.UpsertProject(namespace, content); err != nil {
		slog.Warn("upsert project state 失败",
			"namespace", namespace, "err", err)
	}
}

// ── ConfigMap Watcher ───────────────────────────────────────────────────────

func watchConfigMaps(
	ctx context.Context, kubeconfig string,
	gs *GlobalState, pool *WorkerPool,
	shardMgr *sharding.MultiLeaseManager, totalShards int,
) {
	w := NewKubectlWatcher("configmap", "kubepivot.io/managed=true")
	w.Kubeconfig = kubeconfig

	slog.Info("👀 ConfigMap Watcher 启动",
		"label", "kubepivot.io/managed=true",
		"scope", "all namespaces")

	_ = w.Watch(ctx, func(ev WatchEvent) {
		meta := ev.Meta()
		if IsProtectedNamespace(meta.Namespace) {
			return
		}
		if meta.Name != "kubepivot-resources" {
			return
		}

		// v2.5.0：shard 过滤
		if !shardMgr.Shards().OwnsNamespace(meta.Namespace, totalShards) {
			return
		}

		switch ev.Action {
		case WatchAdded, WatchModified:
			data, _ := ev.Object["data"].(map[string]interface{})
			if data == nil {
				return
			}
			content, _ := data["resources.yaml"].(string)
			if content == "" {
				return
			}

			changed, err := gs.UpsertProject(meta.Namespace, content)
			if err != nil || !changed {
				return
			}
			slog.Info("🔄 ConfigMap 变化触发全量 reconcile",
				"namespace", meta.Namespace)
			enqueueProjectResources(gs, pool, meta.Namespace, "configmap-changed", shardMgr, totalShards)

		case WatchDeleted:
			gs.RemoveProject(meta.Namespace)
		}
	})
}

// ── Reconcile Loop ──────────────────────────────────────────────────────────

func reconcileLoop(
	ctx context.Context,
	gs *GlobalState, pool *WorkerPool,
	shardMgr *sharding.MultiLeaseManager, totalShards int,
) {
	interval := 8 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("🔁 Reconcile Loop 启动", "interval", interval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			projects := gs.ListProjects()
			for _, ns := range projects {
				enqueueProjectResources(gs, pool, ns, "periodic-tick", shardMgr, totalShards)
			}
		}
	}
}

// enqueueProjectResources 把一个项目的所有资源投递到 worker pool
//
// v2.5.0 单层过滤设计：
//   - 仅在入队时检查 shard 归属，handleTask 不再二次过滤
//   - 边缘 case：task 入队后、处理前，shard 可能被其他 pod 抢走
//     → 此时本 pod 仍会处理这个 task（"过期"投递）
//     → 接管它的 pod 也会处理 → 两个 pod 同时 reconcile
//     → 实际无害：reconcile 是幂等的（detect → exists 则 return；
//     缺失则 helm rollback，多触发一次最多多消耗几次 kubectl）
//
// 设计权衡：双层过滤会引入 import cycle（handleTask 需访问 shardMgr），
// 而 race 期间的多余 reconcile 在 5s lease 续约周期下最多持续几秒，
// 代价远小于"防御性二次过滤"的复杂度。
func enqueueProjectResources(
	gs *GlobalState, pool *WorkerPool,
	namespace, reason string,
	shardMgr *sharding.MultiLeaseManager, totalShards int,
) {
	// v2.5.0 第一道过滤：不属于本 shard 的项目直接 drop
	if !shardMgr.Shards().OwnsNamespace(namespace, totalShards) {
		return
	}

	resources, ok := gs.GetProject(namespace)
	if !ok {
		return
	}
	for _, res := range resources {
		pool.Enqueue(ReconcileTask{
			Project:   namespace,
			Namespace: namespace,
			Kind:      res.Kind,
			Name:      res.Name,
			Reason:    reason,
			Key:       fmt.Sprintf("%s/%s/%s", namespace, res.Kind, res.Name),
		})
	}
}

// ── Task Handler ─────────────────────────────────────────────────────────────

// handleTask worker pool 的回调：单个资源的 reconcile
//
// v2.5.0：第二道 shard 过滤（防 task 入队后 shard 失主的 race）
// v2.7 Step 2b-1：detector 用 InformerDetector（informer cache + kubectl fallback）
func handleTask(ctx context.Context, gs *GlobalState, kubeconfig string, task ReconcileTask, informerPool *InformerPool) error {
	if IsProtectedNamespace(task.Namespace) {
		return fmt.Errorf("拒绝对 protected namespace 执行 reconcile: %s", task.Namespace)
	}

	// 资源不存在时启用自愈
	exists, err := DetectResourceExists(kubeconfig, task.Namespace, task.Kind, task.Name)
	if err != nil {
		return fmt.Errorf("检查资源状态失败: %w", err)
	}
	if exists {
		return nil
	}

	slog.Warn("资源缺失，启动自愈",
		"project", task.Project,
		"kind", task.Kind, "name", task.Name,
		"namespace", task.Namespace,
		"reason", task.Reason)

	// v2.4.0：从 GlobalState 缓存拿状态机
	sm, smLock, err := gs.GetOrCreateMachine(task.Namespace, getenv("VERSION", "latest"))
	if err != nil {
		return fmt.Errorf("状态机获取失败: %w", err)
	}
	smLock.Lock()
	defer smLock.Unlock()

	resources, ok := gs.GetProject(task.Namespace)
	if !ok {
		return fmt.Errorf("project %s 已从全局状态移除", task.Namespace)
	}

	var targetRes *Resource
	for i := range resources {
		if resources[i].Kind == task.Kind && resources[i].Name == task.Name {
			targetRes = &resources[i]
			break
		}
	}
	if targetRes == nil {
		return fmt.Errorf("资源 %s/%s 已从 resources.yaml 移除", task.Kind, task.Name)
	}

	// v2.7 Step 2b-1: InformerDetector (informer cache hit) + KubectlDetector (fallback)
	//
	// 双保险:
	//   - informer cache 命中 -> 直接 return true（避免 kubectl fork ~100ms）
	//   - cache miss / informer 未启动 -> fallback 到 kubectl 二次验证
	//   - 既有 v2.5/v2.6 行为完全保留（fallback 兜底）
	kubectlDetector := NewKubectlDetector(kubeconfig)
	detector := NewInformerDetector(informerPool, kubectlDetector)

	r := &Reconciler{
		sm:         sm,
		kubeconfig: kubeconfig,
		project:    task.Project,
		detector:   detector,
		helm:       &RealHelmClient{},
		resources:  &ResourcesConfig{Resources: resources},
	}

	return r.checkAndHeal(*targetRes)
}

// ── Leader Election ─────────────────────────────────────────────────────────

func runGlobalLeaderElection(ctx context.Context, kubeconfig string, run func(ctx context.Context)) {
	etcdEPs := os.Getenv("ETCD_ENDPOINTS")
	if etcdEPs != "" {
		RunWithLeaderElection(ctx, etcdEPs, "global", "leader", run)
		return
	}

	if canUseK8sLease(ctx, kubeconfig) {
		RunWithK8sLeaseElection(ctx,
			"kubepivot-controller-leader",
			"kubepivot-system",
			15*time.Second,
			kubeconfig,
			run,
		)
		return
	}

	slog.Warn("无 etcd 也无法访问 K8s Lease API，降级单机模式")
	run(ctx)
}

// orphanSweeper 周期性兜底自扫，每 30 秒一次
//
// v2.5.0 设计要点（Q1 决策 C 方案）：
//   - OnShardChanged 回调即时清理（响应快）
//   - orphanSweeper 周期兜底（防回调漏触发或 lease 过期但事件未触发的极端情况）
//   - 两者协同保证孤儿状态最多 30 秒内被清理
func orphanSweeper(
	ctx context.Context,
	gs *GlobalState,
	shardMgr *sharding.MultiLeaseManager,
	totalShards int,
) {
	interval := 30 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("🧹 OrphanSweeper 启动（v2.5.0 兜底）", "interval", interval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleaned := gs.RemoveOrphanProjects(func(ns string) bool {
				return shardMgr.Shards().OwnsNamespace(ns, totalShards)
			})
			if len(cleaned) > 0 {
				slog.Info("🧹 OrphanSweeper 周期兜底清理",
					"cleaned_namespaces", cleaned)
			}
		}
	}
}
