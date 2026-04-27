package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/Ixecd/kubepivot/internal/eventstream"
	"github.com/Ixecd/kubepivot/internal/sharding"

	"github.com/prometheus/client_golang/prometheus"
)

// ─── Mock Informer & NewInformer 注入 ────────────────────────────

// fakeInformer 测试用最小 Informer 实现。
// Stop / Stats / Get / List 等都是 no-op。
type fakeInformer struct {
	resource string
	stopped  bool
}

func (f *fakeInformer) Start(_ context.Context) <-chan error {
	ch := make(chan error)
	close(ch) // 立即关闭，模拟 watch 循环已退出
	return ch
}
func (f *fakeInformer) Get(_, _ string) (*eventstream.Resource, bool) { return nil, false }
func (f *fakeInformer) List(_ string) []*eventstream.Resource         { return nil }
func (f *fakeInformer) ListAll() []*eventstream.Resource              { return nil }
func (f *fakeInformer) Subscribe(_ eventstream.EventHandler) eventstream.Subscription {
	return nil
}
func (f *fakeInformer) Stop()                              { f.stopped = true }
func (f *fakeInformer) Stats() eventstream.InformerStats   { return eventstream.InformerStats{Resource: f.resource} }

// withMockedNewInformer 替换 newInformerFunc 为返回成功 fakeInformer 的函数。
// 返回 cleanup 函数。
func withMockedNewInformer(t *testing.T) func() {
	t.Helper()
	old := newInformerFunc
	newInformerFunc = func(_ context.Context, opts eventstream.InformerOptions) (eventstream.Informer, error) {
		return &fakeInformer{resource: opts.Resource}, nil
	}
	return func() { newInformerFunc = old }
}

// withFailingNewInformer 替换 newInformerFunc 为返回 error 的函数。
func withFailingNewInformer(t *testing.T) func() {
	t.Helper()
	old := newInformerFunc
	newInformerFunc = func(_ context.Context, _ eventstream.InformerOptions) (eventstream.Informer, error) {
		return nil, errors.New("simulated NewInformer failure")
	}
	return func() { newInformerFunc = old }
}

// ─── 基础测试 ────────────────────────────────────────────────────

func TestNewInformerPool_Empty(t *testing.T) {
	shardMgr := sharding.NewMultiLeaseManager(sharding.MultiLeaseConfig{
		TotalShards: 10,
		Replicas:    1,
	})
	pool := NewInformerPool(shardMgr, 10, "")
	if pool == nil {
		t.Fatal("NewInformerPool returned nil")
	}
	if pool.Size() != 0 {
		t.Errorf("空 pool Size=%d, want 0", pool.Size())
	}
	if pool.Get("deployments") != nil {
		t.Error("未启动的 informer 应返回 nil")
	}
}

// ─── Start 成功路径 ──────────────────────────────────────────────

func TestInformerPool_Start_Success(t *testing.T) {
	restore := withMockedNewInformer(t)
	defer restore()

	shardMgr := sharding.NewMultiLeaseManager(sharding.MultiLeaseConfig{
		TotalShards: 10,
		Replicas:    1,
	})
	pool := NewInformerPool(shardMgr, 10, "")
	pool.Start(context.Background(), "deployments", "apps/v1")

	if pool.Size() != 1 {
		t.Errorf("启动后 Size=%d, want 1", pool.Size())
	}
	informer := pool.Get("deployments")
	if informer == nil {
		t.Fatal("启动成功后 Get 应返回非 nil")
	}
	// 验证 Stats 含正确 resource
	if informer.Stats().Resource != "deployments" {
		t.Errorf("Stats.Resource=%q, want deployments", informer.Stats().Resource)
	}
}

// ─── Start 失败路径 (fail soft) ──────────────────────────────────

func TestInformerPool_Start_FailSoft(t *testing.T) {
	restore := withFailingNewInformer(t)
	defer restore()

	shardMgr := sharding.NewMultiLeaseManager(sharding.MultiLeaseConfig{
		TotalShards: 10,
		Replicas:    1,
	})
	pool := NewInformerPool(shardMgr, 10, "")

	// fail soft：Start 不应 panic 或返回 error
	pool.Start(context.Background(), "deployments", "apps/v1")

	// 失败的 informer 不应进入 pool
	if pool.Size() != 0 {
		t.Errorf("失败后 Size=%d, want 0", pool.Size())
	}
	if pool.Get("deployments") != nil {
		t.Error("启动失败的 informer Get 应返回 nil")
	}
}

// ─── Start 多个 informer ─────────────────────────────────────────

func TestInformerPool_Start_Multiple(t *testing.T) {
	restore := withMockedNewInformer(t)
	defer restore()

	shardMgr := sharding.NewMultiLeaseManager(sharding.MultiLeaseConfig{
		TotalShards: 10,
		Replicas:    1,
	})
	pool := NewInformerPool(shardMgr, 10, "")
	pool.Start(context.Background(), "deployments", "apps/v1")
	pool.Start(context.Background(), "pods", "v1")
	pool.Start(context.Background(), "services", "v1")

	if pool.Size() != 3 {
		t.Errorf("Size=%d, want 3", pool.Size())
	}
	for _, resource := range []string{"deployments", "pods", "services"} {
		if pool.Get(resource) == nil {
			t.Errorf("Get(%q) 应返回非 nil", resource)
		}
	}
}

// ─── StopAll 测试 ────────────────────────────────────────────────

func TestInformerPool_StopAll(t *testing.T) {
	restore := withMockedNewInformer(t)
	defer restore()

	shardMgr := sharding.NewMultiLeaseManager(sharding.MultiLeaseConfig{
		TotalShards: 10,
		Replicas:    1,
	})
	pool := NewInformerPool(shardMgr, 10, "")
	pool.Start(context.Background(), "deployments", "apps/v1")
	pool.Start(context.Background(), "pods", "v1")

	// 拿引用验证 Stop 被调
	deployInformer := pool.Get("deployments").(*fakeInformer)
	podInformer := pool.Get("pods").(*fakeInformer)

	pool.StopAll()

	if pool.Size() != 0 {
		t.Errorf("StopAll 后 Size=%d, want 0", pool.Size())
	}
	if !deployInformer.stopped {
		t.Error("deployments informer 应已 stop")
	}
	if !podInformer.stopped {
		t.Error("pods informer 应已 stop")
	}
}

func TestInformerPool_StopAll_Idempotent(t *testing.T) {
	restore := withMockedNewInformer(t)
	defer restore()

	pool := NewInformerPool(nil, 10, "")
	pool.Start(context.Background(), "deployments", "apps/v1")

	// 多次 StopAll 不应 panic
	pool.StopAll()
	pool.StopAll()
	pool.StopAll()

	if pool.Size() != 0 {
		t.Errorf("Size=%d, want 0", pool.Size())
	}
}

// ─── nil shardMgr 测试 ───────────────────────────────────────────

func TestInformerPool_NilShardMgr(t *testing.T) {
	restore := withMockedNewInformer(t)
	defer restore()

	// nil shardMgr 应允许（测试场景）
	pool := NewInformerPool(nil, 10, "")
	pool.Start(context.Background(), "deployments", "apps/v1")

	if pool.Size() != 1 {
		t.Errorf("nil shardMgr 不应阻止启动, Size=%d", pool.Size())
	}
}

// ─── RegisterMetrics 测试 ────────────────────────────────────────

func TestInformerPool_RegisterMetrics_Empty(t *testing.T) {
	pool := NewInformerPool(nil, 10, "")
	reg := prometheus.NewRegistry()

	// 空 pool 注册 metrics 应不报错
	if err := pool.RegisterMetrics(reg); err != nil {
		t.Errorf("空 pool RegisterMetrics 不应报错, err=%v", err)
	}
}

func TestInformerPool_RegisterMetrics_WithInformers(t *testing.T) {
	restore := withMockedNewInformer(t)
	defer restore()

	pool := NewInformerPool(nil, 10, "")
	pool.Start(context.Background(), "deployments", "apps/v1")
	pool.Start(context.Background(), "pods", "v1")

	reg := prometheus.NewRegistry()
	if err := pool.RegisterMetrics(reg); err != nil {
		t.Errorf("RegisterMetrics 不应报错, err=%v", err)
	}
}

func TestInformerPool_RegisterMetrics_DuplicateError(t *testing.T) {
	restore := withMockedNewInformer(t)
	defer restore()

	pool := NewInformerPool(nil, 10, "")
	pool.Start(context.Background(), "deployments", "apps/v1")

	reg := prometheus.NewRegistry()
	if err := pool.RegisterMetrics(reg); err != nil {
		t.Fatalf("first RegisterMetrics: %v", err)
	}
	// 第二次注册到同 registry 应报错（desc 冲突）
	if err := pool.RegisterMetrics(reg); err == nil {
		t.Error("重复 RegisterMetrics 应报错")
	}
}
