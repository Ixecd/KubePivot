package controller

import (
	"context"
	"log/slog"
	"sync"

	"github.com/Ixecd/kubepivot/internal/eventstream"
	"github.com/Ixecd/kubepivot/internal/sharding"

	"github.com/prometheus/client_golang/prometheus"
)

// ─── Informer Pool（v2.7 Step 2a-2）────────────────────────────────
//
// InformerPool 管理 controller 范围内的 eventstream.Informer 实例。
//
// 双保险渐进引入策略：
//   - informer 启动失败不阻塞 controller (fail soft，与 KubectlWatcher 同谱)
//   - 既有 KubectlWatcher / kubectl get 路径继续工作
//   - Step 2b/c/d 逐步迁移调用方到 informer.Get() / informer.Subscribe()
//
// Step 2a-2 范围：
//   ✓ informer 在 controller 启动时跑起来
//   ✓ cache 自动维护（无 subscriber，事件流入即丢）
//   ✗ metrics 不注册（HTTP server 未实施，留 v2.7.x）
//   ✗ controller 业务路径（reconcile/heal）不切换
//
// v2.7.x 计划：
//   - 加 controller HTTP server 暴露 /metrics
//   - 调用 RegisterMetrics(prometheus.DefaultRegisterer)
//
// v2.7.x / v2.8 计划：
//   - 调用方迁移：reconciler / drift_sync 用 informer.Get 替换 kubectl get

// newInformerFunc 是 NewInformer 的可注入函数变量。
//
// 生产时调用 eventstream.NewInformer。
// 测试时替换为 fake，避免依赖真实 K8s API server / in-cluster 文件。
//
// 模式与 internal/eventstream/auth.go 的 readTokenFile / readCAFile 一致。
var newInformerFunc = eventstream.NewInformer

// InformerPool 管理 controller 内多个 Informer 实例。
//
// 并发安全：informers map 用 RWMutex 保护。
// Start / StopAll / RegisterMetrics 都是并发安全的。
type InformerPool struct {
	mu        sync.RWMutex
	informers map[string]eventstream.Informer

	shardMgr    *sharding.MultiLeaseManager
	totalShards int
	kubeconfig  string
}

// NewInformerPool 创建空的 informer pool。
//
// 参数：
//   - shardMgr：v2.5 sharding 系统，用于按 namespace 过滤事件
//   - totalShards：分片总数（与 v2.5 配置一致，默认 10）
//   - kubeconfig：本地开发时的 kubeconfig 路径，生产为空（走 in-cluster）
func NewInformerPool(
	shardMgr *sharding.MultiLeaseManager,
	totalShards int,
	kubeconfig string,
) *InformerPool {
	return &InformerPool{
		informers:   make(map[string]eventstream.Informer),
		shardMgr:    shardMgr,
		totalShards: totalShards,
		kubeconfig:  kubeconfig,
	}
}

// Start 启动一个 informer (fail soft)。
//
// 失败时 log warn 但不返回 error；既有 controller 路径不受影响。
//
// 参数：
//   - resource：K8s 资源 plural name (如 "deployments" / "pods")
//   - apiVersion：API 版本组 (如 "apps/v1" / "v1")
//
// 重复 Start 同一 resource 行为：
//   旧 informer 仍在运行（不会 stop）
//   新 informer 替换 map 里的引用
//   旧 informer 成为孤儿（不推荐重复 Start）
//
// 调用方应在 controller 退出前调 StopAll。
func (p *InformerPool) Start(
	ctx context.Context,
	resource string,
	apiVersion string,
) {
	// 用 ShardSetAdapter 桥接 v2.5 sharding（按 namespace hash 过滤）
	var shardSet eventstream.ShardSet
	if p.shardMgr != nil {
		shardSet = eventstream.NewShardSetAdapter(
			p.shardMgr.Shards(),
			p.totalShards,
		)
	}
	// shardMgr=nil 时 shardSet=nil，informer 不过滤事件（测试场景）

	informer, err := newInformerFunc(ctx, eventstream.InformerOptions{
		Resource:   resource,
		APIVersion: apiVersion,
		ShardSet:   shardSet,
		KubeConfig: p.kubeconfig,
		// APIServerURL 留空：
		//   - kubeconfig 非空 → 走路径 2（v2.7.0 暂未实施，会 fail soft）
		//   - kubeconfig 为空 → 走路径 3（in-cluster ServiceAccount）
	})
	if err != nil {
		slog.Warn("⚠️ informer 创建失败 (fail soft)",
			"resource", resource,
			"apiVersion", apiVersion,
			"err", err)
		return
	}

	errCh := informer.Start(ctx)

	// 监听 informer 错误（不阻塞 Start，独立 goroutine）
	go func() {
		for err := range errCh {
			slog.Warn("informer error",
				"resource", resource,
				"err", err)
		}
	}()

	p.mu.Lock()
	p.informers[resource] = informer
	p.mu.Unlock()

	slog.Info("✅ informer 已启动",
		"resource", resource,
		"apiVersion", apiVersion)
}

// Get 获取已启动的 informer。
//
// 返回 nil 表示未启动（启动失败或未调 Start）。
// 调用方应检查 nil 后再使用。
//
// 例：
//
//	if informer := pool.Get("deployments"); informer != nil {
//	    if obj, ok := informer.Get(ns, name); ok {
//	        // ...
//	    }
//	}
func (p *InformerPool) Get(resource string) eventstream.Informer {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.informers[resource]
}

// StopAll 停止所有 informer 并清空 pool。
//
// 应在 controller 退出前调用（通常 defer）。
// 幂等：多次调用安全，第二次起为空操作。
func (p *InformerPool) StopAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for resource, informer := range p.informers {
		informer.Stop()
		slog.Info("informer 已停止", "resource", resource)
	}
	p.informers = make(map[string]eventstream.Informer)
}

// RegisterMetrics 注册所有 informer 到给定的 Prometheus registry。
//
// Step 2a-2 阶段调用方通常不调（HTTP server 未实施）。
// 留 v2.7.x：在 controller 启动 HTTP server 时调用：
//
//	pool.RegisterMetrics(prometheus.DefaultRegisterer)
//	http.Handle("/metrics", promhttp.Handler())
//
// 空 pool（无 informer）返回 nil，不报错。
//
// 重复调用不安全：第二次会因 Prometheus desc 冲突报错。
// 调用方应保证只调用一次（通常 controller 启动时）。
func (p *InformerPool) RegisterMetrics(reg prometheus.Registerer) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	informers := make([]eventstream.Informer, 0, len(p.informers))
	for _, i := range p.informers {
		informers = append(informers, i)
	}
	if len(informers) == 0 {
		return nil
	}
	return eventstream.RegisterInformerMetrics(reg, informers...)
}

// SetShardMgr 设置 sharding manager（延迟绑定）。
//
// 用于 controller 启动时的特殊场景：
//   informerPool 必须先创建（让 worker pool 闭包引用）
//   shardMgr 后创建（依赖 OnShardChanged 回调）
//
// 调用顺序：
//   1. NewInformerPool(nil, totalShards, kubeconfig)
//   2. ... shardMgr 创建 ...
//   3. pool.SetShardMgr(shardMgr)
//   4. pool.Start(...)
//
// SetShardMgr 必须在 Start 之前调用。
// Start 后调用不会影响已启动 informer 的 ShardSet。
func (p *InformerPool) SetShardMgr(shardMgr *sharding.MultiLeaseManager) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shardMgr = shardMgr
}

// Size 返回当前持有的 informer 数量（debug 用）。
func (p *InformerPool) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.informers)
}
