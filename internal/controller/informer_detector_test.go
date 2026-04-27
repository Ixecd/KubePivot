package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/Ixecd/kubepivot/internal/eventstream"
)

// ─── Mock Informer (with cache) for InformerDetector tests ───────

// cacheBackedFakeInformer 是 fakeInformer 的扩展，支持 Get 返回真值。
// fakeInformer (在 informer_pool_test.go) 的 Get 始终返回 (nil, false)。
// 这里需要一个能"找到资源"的 mock。
type cacheBackedFakeInformer struct {
	resource string
	items    map[string]*eventstream.Resource // key: ns/name
}

func newCacheBackedFakeInformer(resource string) *cacheBackedFakeInformer {
	return &cacheBackedFakeInformer{
		resource: resource,
		items:    make(map[string]*eventstream.Resource),
	}
}

func (f *cacheBackedFakeInformer) addResource(ns, name string) {
	f.items[ns+"/"+name] = &eventstream.Resource{
		Namespace: ns,
		Name:      name,
	}
}

// addResourceWithLabels 跟 addResource 一样但支持自定义 labels（Step 2b-2 测试用）
func (f *cacheBackedFakeInformer) addResourceWithLabels(ns, name string, labels map[string]string) {
	f.items[ns+"/"+name] = &eventstream.Resource{
		Namespace: ns,
		Name:      name,
		Labels:    labels,
	}
}

func (f *cacheBackedFakeInformer) Start(_ context.Context) <-chan error {
	ch := make(chan error)
	close(ch)
	return ch
}
func (f *cacheBackedFakeInformer) Get(ns, name string) (*eventstream.Resource, bool) {
	r, ok := f.items[ns+"/"+name]
	return r, ok
}
func (f *cacheBackedFakeInformer) List(_ string) []*eventstream.Resource { return nil }
func (f *cacheBackedFakeInformer) ListAll() []*eventstream.Resource      { return nil }
func (f *cacheBackedFakeInformer) Subscribe(_ eventstream.EventHandler) eventstream.Subscription {
	return nil
}
func (f *cacheBackedFakeInformer) Stop() {}
func (f *cacheBackedFakeInformer) Stats() eventstream.InformerStats {
	return eventstream.InformerStats{Resource: f.resource}
}

// ─── Mock Fallback Detector ──────────────────────────────────────

// recordingDetector 记录调用次数，用于验证 fallback 是否被触发。
// 内部委托给一个底层 Detector。
type recordingDetector struct {
	calls    int
	delegate Detector
}

func (d *recordingDetector) ResourceExists(kind, name, namespace string) (bool, error) {
	d.calls++
	return d.delegate.ResourceExists(kind, name, namespace)
}

// ─── Test Helpers ────────────────────────────────────────────────

// poolWithInformer 创建一个手动塞入 informer 的 pool（绕过真实 NewInformer）。
func poolWithInformer(t *testing.T, resource string, informer eventstream.Informer) *InformerPool {
	t.Helper()
	pool := NewInformerPool(nil, 10, "")
	// 直接操作 informers map（仅测试用）
	pool.mu.Lock()
	pool.informers[resource] = informer
	pool.mu.Unlock()
	return pool
}

// ─── 测试 kindToResource 映射 ────────────────────────────────────

func TestKindToResource(t *testing.T) {
	tests := []struct {
		kind         string
		wantResource string
		wantOk       bool
	}{
		{"Deployment", "deployments", true},
		{"deployment", "deployments", true}, // 大小写不敏感
		{"DEPLOYMENT", "deployments", true},
		{"Service", "", false}, // v2.7.0 暂不支持
		{"Pod", "", false},
		{"ConfigMap", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			got, ok := kindToResource(tt.kind)
			if got != tt.wantResource || ok != tt.wantOk {
				t.Errorf("kindToResource(%q) = (%q, %v), want (%q, %v)",
					tt.kind, got, ok, tt.wantResource, tt.wantOk)
			}
		})
	}
}

// ─── Cache Hit 路径：不 fallback ──────────────────────────────────

func TestInformerDetector_CacheHit_DoesNotCallFallback(t *testing.T) {
	informer := newCacheBackedFakeInformer("deployments")
	informer.addResource("default", "myapp")

	pool := poolWithInformer(t, "deployments", informer)
	fallback := &recordingDetector{
		delegate: newMockDetector(),
	}
	detector := NewInformerDetector(pool, fallback)

	// 资源在 cache 中 → 应直接返回 true，不调 fallback
	exists, err := detector.ResourceExists("Deployment", "myapp", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Error("cache hit 应返回 true")
	}
	if fallback.calls != 0 {
		t.Errorf("cache hit 不应调 fallback, calls=%d", fallback.calls)
	}
}

// ─── Cache Miss 路径：fallback ──────────────────────────────────

func TestInformerDetector_CacheMiss_FallsBack(t *testing.T) {
	informer := newCacheBackedFakeInformer("deployments")
	// 不 add 资源 → cache miss

	pool := poolWithInformer(t, "deployments", informer)
	mockFallback := newMockDetector()
	mockFallback.withResource("Deployment", "myapp", "default", true) // fallback 说存在

	fallback := &recordingDetector{delegate: mockFallback}
	detector := NewInformerDetector(pool, fallback)

	exists, err := detector.ResourceExists("Deployment", "myapp", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Error("fallback 说存在，应返回 true")
	}
	if fallback.calls != 1 {
		t.Errorf("cache miss 应调 fallback 1 次, calls=%d", fallback.calls)
	}
}

// ─── 不支持的 kind：直接 fallback ──────────────────────────────

func TestInformerDetector_UnsupportedKind_FallsBack(t *testing.T) {
	informer := newCacheBackedFakeInformer("deployments")
	pool := poolWithInformer(t, "deployments", informer)

	mockFallback := newMockDetector()
	mockFallback.withResource("Service", "myservice", "default", true)

	fallback := &recordingDetector{delegate: mockFallback}
	detector := NewInformerDetector(pool, fallback)

	// Service 不在 kind 映射中 → 直接 fallback，不查 cache
	exists, err := detector.ResourceExists("Service", "myservice", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Error("fallback 说存在，应返回 true")
	}
	if fallback.calls != 1 {
		t.Errorf("不支持的 kind 应 fallback, calls=%d", fallback.calls)
	}
}

// ─── informer 未启动：fallback ─────────────────────────────────

func TestInformerDetector_InformerNotStarted_FallsBack(t *testing.T) {
	// pool 为空（没有任何 informer 启动）
	pool := NewInformerPool(nil, 10, "")

	mockFallback := newMockDetector()
	mockFallback.withResource("Deployment", "myapp", "default", false)

	fallback := &recordingDetector{delegate: mockFallback}
	detector := NewInformerDetector(pool, fallback)

	exists, err := detector.ResourceExists("Deployment", "myapp", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("fallback 说不存在，应返回 false")
	}
	if fallback.calls != 1 {
		t.Errorf("informer 未启动应 fallback, calls=%d", fallback.calls)
	}
}

// ─── pool 为 nil：fallback（防御性） ────────────────────────────

func TestInformerDetector_NilPool_FallsBack(t *testing.T) {
	mockFallback := newMockDetector()
	mockFallback.withResource("Deployment", "myapp", "default", true)

	fallback := &recordingDetector{delegate: mockFallback}
	detector := NewInformerDetector(nil, fallback) // pool=nil

	exists, err := detector.ResourceExists("Deployment", "myapp", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Error("nil pool 时应 fallback 并返回 fallback 结果")
	}
	if fallback.calls != 1 {
		t.Errorf("nil pool 应 fallback, calls=%d", fallback.calls)
	}
}

// ─── fallback error 透传 ───────────────────────────────────────

func TestInformerDetector_FallbackError_IsPropagated(t *testing.T) {
	pool := NewInformerPool(nil, 10, "") // 无 informer，必然 fallback

	mockFallback := newMockDetector()
	expectedErr := errors.New("kubectl get failed")
	mockFallback.withError(expectedErr)

	fallback := &recordingDetector{delegate: mockFallback}
	detector := NewInformerDetector(pool, fallback)

	_, err := detector.ResourceExists("Deployment", "myapp", "default")
	if !errors.Is(err, expectedErr) {
		t.Errorf("fallback error 应透传, got %v want %v", err, expectedErr)
	}
}

// ─── Cache hit 不暴露 fallback 错误（关键性质）────────────────

func TestInformerDetector_CacheHit_IgnoresFallbackError(t *testing.T) {
	informer := newCacheBackedFakeInformer("deployments")
	informer.addResource("default", "myapp") // cache 中有

	pool := poolWithInformer(t, "deployments", informer)
	mockFallback := newMockDetector()
	mockFallback.withError(errors.New("kubectl error - 不应被触发"))

	fallback := &recordingDetector{delegate: mockFallback}
	detector := NewInformerDetector(pool, fallback)

	// cache hit → 不调 fallback → fallback 的错误不会出现
	exists, err := detector.ResourceExists("Deployment", "myapp", "default")
	if err != nil {
		t.Errorf("cache hit 路径不应有 error, got %v", err)
	}
	if !exists {
		t.Error("cache hit 应返回 true")
	}
	if fallback.calls != 0 {
		t.Errorf("cache hit 不应调 fallback, calls=%d", fallback.calls)
	}
}

// ─── LabelGetter 测试（Step 2b-2）───────────────────────────────

func TestInformerDetector_GetResourceLabels_CacheHit_ReturnsLabels(t *testing.T) {
	informer := newCacheBackedFakeInformer("deployments")
	labels := map[string]string{
		"app":             "myapp",
		"app.kubernetes.io/managed-by": "Helm",
	}
	informer.addResourceWithLabels("default", "myapp", labels)

	pool := poolWithInformer(t, "deployments", informer)
	detector := NewInformerDetector(pool, newMockDetector())

	got, ok := detector.GetResourceLabels("Deployment", "myapp", "default")
	if !ok {
		t.Fatal("cache hit 应返回 ok=true")
	}
	if len(got) != 2 {
		t.Errorf("labels len=%d, want 2", len(got))
	}
	if got["app"] != "myapp" {
		t.Errorf("labels[\"app\"]=%q, want myapp", got["app"])
	}
}

func TestInformerDetector_GetResourceLabels_CacheHit_NilLabels(t *testing.T) {
	// K8s 对象本身可能没 labels (metadata.labels 缺失或空)
	// 此时 informer.Get 返回的 Resource.Labels 是 nil
	// LabelGetter 应返回 (nil, true) — 表示"找到了，但没 labels"
	informer := newCacheBackedFakeInformer("deployments")
	informer.addResource("default", "no-labels") // 默认无 labels

	pool := poolWithInformer(t, "deployments", informer)
	detector := NewInformerDetector(pool, newMockDetector())

	got, ok := detector.GetResourceLabels("Deployment", "no-labels", "default")
	if !ok {
		t.Error("cache hit (即使 labels=nil) 应返回 ok=true")
	}
	if got != nil {
		t.Errorf("无 labels 资源应返回 nil map, got %v", got)
	}
}

func TestInformerDetector_GetResourceLabels_CacheMiss_ReturnsFalse(t *testing.T) {
	informer := newCacheBackedFakeInformer("deployments")
	// 不 add 任何资源 → cache miss

	pool := poolWithInformer(t, "deployments", informer)
	detector := NewInformerDetector(pool, newMockDetector())

	got, ok := detector.GetResourceLabels("Deployment", "missing", "default")
	if ok {
		t.Error("cache miss 应返回 ok=false")
	}
	if got != nil {
		t.Errorf("cache miss 应返回 nil map, got %v", got)
	}
}

func TestInformerDetector_GetResourceLabels_UnsupportedKind(t *testing.T) {
	informer := newCacheBackedFakeInformer("deployments")
	pool := poolWithInformer(t, "deployments", informer)
	detector := NewInformerDetector(pool, newMockDetector())

	// Service 不在 kind 映射中 → 返回 (nil, false)
	got, ok := detector.GetResourceLabels("Service", "myservice", "default")
	if ok {
		t.Error("不支持的 kind 应返回 ok=false")
	}
	if got != nil {
		t.Errorf("不支持的 kind 应返回 nil map, got %v", got)
	}
}

func TestInformerDetector_GetResourceLabels_NilPool(t *testing.T) {
	detector := NewInformerDetector(nil, newMockDetector())

	got, ok := detector.GetResourceLabels("Deployment", "myapp", "default")
	if ok {
		t.Error("nil pool 应返回 ok=false")
	}
	if got != nil {
		t.Errorf("nil pool 应返回 nil map, got %v", got)
	}
}

// ─── LabelGetter 接口契约编译期验证 ───────────────────────────

func TestInformerDetector_ImplementsLabelGetter(t *testing.T) {
	var _ LabelGetter = (*InformerDetector)(nil)
}

// ─── 接口契约编译期验证 ────────────────────────────────────────

func TestInformerDetector_ImplementsDetector(t *testing.T) {
	// 编译期保证 InformerDetector 实现 Detector 接口
	var _ Detector = (*InformerDetector)(nil)
}
