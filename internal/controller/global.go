package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
	"github.com/Ixecd/kubepivot/internal/state"
)

// StartGlobal 启动 global 模式 controller（v2.3.0+）
//
// 架构：
//
//	Leader Election（/kubepivot/global/leader）
//	└── 成为 Leader 后启动：
//	    ├── Namespace Watcher       watch label=kubepivot.io/managed=true
//	    ├── ConfigMap Watcher       watch label=kubepivot.io/managed=true 的 kubepivot-resources
//	    ├── Resource Reconcile Loop 定时扫描每个 managed 项目的资源
//	    └── Worker Pool              20 个 goroutine 消费任务
//
// 退出条件：ctx.Done() 或 Leader 失效（自动进入下一轮选举）
func StartGlobal(ctx context.Context) {
	etcdEPs := os.Getenv("ETCD_ENDPOINTS")
	kubeconfig := getenv("KUBE_CONFIG", "")

	slog.Info("🌐 global controller 启动中",
		"leader_key", "/kubepivot/global/leader",
		"etcd", etcdEPs,
	)

	// Leader Election：只有一个 leader 处理所有项目的 reconcile
	runGlobalLeaderElection(ctx, etcdEPs, func(leaderCtx context.Context) {
		runAsLeader(leaderCtx, kubeconfig)
	})
}

// runAsLeader 成为 leader 后的主逻辑
func runAsLeader(ctx context.Context, kubeconfig string) {
	slog.Info("👑 已成为全局 Leader，启动 watchers + worker pool")

	// 1. 全局状态 + Worker Pool
	gs := NewGlobalState()

	pool := NewWorkerPool(20, func(taskCtx context.Context, task ReconcileTask) error {
		return handleTask(taskCtx, gs, kubeconfig, task)
	})

	// 2. 启动 worker pool（独立 goroutine）
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		pool.Start(ctx)
	}()

	// 3. 启动 Namespace Watcher：发现/移除 managed 项目
	wg.Add(1)
	go func() {
		defer wg.Done()
		watchNamespaces(ctx, kubeconfig, gs, pool)
	}()

	// 4. 启动 ConfigMap Watcher：热加载 resources.yaml
	wg.Add(1)
	go func() {
		defer wg.Done()
		watchConfigMaps(ctx, kubeconfig, gs, pool)
	}()

	// 5. 定时 Reconcile Loop：每 8s 对所有 managed 项目做一次全量对账
	wg.Add(1)
	go func() {
		defer wg.Done()
		reconcileLoop(ctx, gs, pool)
	}()

	wg.Wait()
	slog.Info("🌐 global controller 所有 goroutine 已退出")
}

// ── Namespace Watcher ───────────────────────────────────────────────────────

// watchNamespaces 监听带 kubepivot.io/managed=true label 的 namespace
// 新增 → 尝试加载该 ns 的 kubepivot-resources ConfigMap
// 删除/unlabel → 从全局状态移除
func watchNamespaces(ctx context.Context, kubeconfig string, gs *GlobalState, pool *WorkerPool) {
	w := NewKubectlWatcher("namespace", "kubepivot.io/managed=true")
	w.Kubeconfig = kubeconfig

	slog.Info("👀 Namespace Watcher 启动",
		"label", "kubepivot.io/managed=true")

	_ = w.Watch(ctx, func(ev WatchEvent) {
		meta := ev.Meta()
		if IsProtectedNamespace(meta.Name) {
			return
		}

		switch ev.Action {
		case WatchAdded, WatchModified:
			slog.Info("📥 发现 managed namespace", "ns", meta.Name, "action", ev.Action)
			// 主动拉取该 ns 的 ConfigMap，刷新 GlobalState
			loadResourcesConfigMap(ctx, kubeconfig, gs, meta.Name)
		case WatchDeleted:
			gs.RemoveProject(meta.Name)
		}
	})
}

// loadResourcesConfigMap 主动 kubectl get 某个 namespace 的 kubepivot-resources ConfigMap
// 用于初始化加载 + Namespace ADDED 事件补偿
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

// watchConfigMaps 监听所有 managed namespace 里 kubepivot-resources 的 ConfigMap 变化
// 变化 → sha256 比对 → 变了则刷新 GlobalState → 触发该 namespace 全量 reconcile
func watchConfigMaps(ctx context.Context, kubeconfig string, gs *GlobalState, pool *WorkerPool) {
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
		// 只关心名为 kubepivot-resources 的 CM
		if meta.Name != "kubepivot-resources" {
			return
		}

		switch ev.Action {
		case WatchAdded, WatchModified:
			// 从 object.data.resources.yaml 拿内容
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
			// 变化后立即把该项目所有资源入队
			enqueueProjectResources(gs, pool, meta.Namespace, "configmap-changed")

		case WatchDeleted:
			gs.RemoveProject(meta.Namespace)
		}
	})
}

// ── Reconcile Loop ──────────────────────────────────────────────────────────

// reconcileLoop 每 8s 扫一次所有 managed 项目的所有资源，投递到 worker pool
func reconcileLoop(ctx context.Context, gs *GlobalState, pool *WorkerPool) {
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
				enqueueProjectResources(gs, pool, ns, "periodic-tick")
			}
		}
	}
}

// enqueueProjectResources 把一个项目的所有资源投递到 worker pool
func enqueueProjectResources(gs *GlobalState, pool *WorkerPool, namespace, reason string) {
	resources, ok := gs.GetProject(namespace)
	if !ok {
		return
	}
	for _, res := range resources {
		pool.Enqueue(ReconcileTask{
			Project:   namespace, // v2.3.0 项目名默认等于 namespace
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
// 当前 v2.3.0 仅支持资源缺失自愈（沿用 per-project 模式的 healRecreate/healRollback 逻辑）。
// 未来版本会扩展为：drift 治理 / OOM 处理 / CrashLoopBackOff 分析 等。
func handleTask(ctx context.Context, gs *GlobalState, kubeconfig string, task ReconcileTask) error {
	// 二次护栏
	if IsProtectedNamespace(task.Namespace) {
		return fmt.Errorf("拒绝对 protected namespace 执行 reconcile: %s", task.Namespace)
	}

	// 资源不存在时启用自愈
	exists, err := DetectResourceExists(kubeconfig, task.Namespace, task.Kind, task.Name)
	if err != nil {
		return fmt.Errorf("检查资源状态失败: %w", err)
	}
	if exists {
		return nil // 资源存在，无事可做（OOM/CrashLoop 的 deep check 后续版本再加）
	}

	slog.Warn("资源缺失，启动自愈",
		"project", task.Project,
		"kind", task.Kind, "name", task.Name,
		"namespace", task.Namespace,
		"reason", task.Reason)

	// 构造一个临时 reconciler，复用 heal.go 里的全部自愈逻辑
	// 每个 task 都新建一个是故意的：状态机不跨项目共享
	store := state.NewAutoStore(os.Getenv("ETCD_ENDPOINTS"))
	sm, err := state.New(store, task.Project, task.Namespace, getenv("VERSION", "latest"))
	if err != nil {
		return fmt.Errorf("状态机初始化失败: %w", err)
	}

	// 复用 v2.2.0 Reconciler 的 heal 方法（healRecreate / healRollback / healScaleDown 等）
	resources, ok := gs.GetProject(task.Namespace)
	if !ok {
		return fmt.Errorf("project %s 已从全局状态移除", task.Namespace)
	}

	// 找到目标资源的完整配置
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

	r := &Reconciler{
		sm:         sm,
		kubeconfig: kubeconfig,
		project:    task.Project,
		detector:   NewKubectlDetector(kubeconfig),
		helm:       &RealHelmClient{},
		resources:  &ResourcesConfig{Resources: resources},
	}

	return r.checkAndHeal(*targetRes)
}

// ── Leader Election ─────────────────────────────────────────────────────────

// runGlobalLeaderElection 全局 Leader 选举，key=/kubepivot/global/leader
//
// 和 v2.2.0 per-project leader 不同：这里的 key 是固定的，一个集群只有一个 leader。
// 无 etcd 时降级到单机模式（直接运行，不做选举）。
func runGlobalLeaderElection(ctx context.Context, etcdEPs string, run func(context.Context)) {
	if etcdEPs == "" {
		slog.Warn("未配置 ETCD_ENDPOINTS，降级为单机模式")
		run(ctx)
		return
	}

	// 复用 leader.go 里的 RunWithLeaderElection 能力，
	// 只是 project/namespace 位置传固定值 global/leader，
	// 最终 etcd key 会是 /kubepivot/global/leader/leader（见 leader.go 实现）
	// TODO(v2.3.0 后续): 如果需要完全对齐 /kubepivot/global/leader key 格式，
	//                    可以在 leader.go 加一个 RunWithGlobalLeaderElection 专用入口
	RunWithLeaderElection(ctx, etcdEPs, "global", "leader", run)
}
