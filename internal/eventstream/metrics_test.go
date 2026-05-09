package eventstream

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// ─── Mock Informer ────────────────────────────────────────────────

// staticMockInformer 静态 stats（每次 Stats() 返回相同值）。
// 仅 Stats() 方法可用，其他方法 panic 暴露误用。
type staticMockInformer struct {
	stats InformerStats
}

func newMockInformer(stats InformerStats) Informer {
	return &staticMockInformer{stats: stats}
}

func (m *staticMockInformer) Start(_ context.Context) <-chan error {
	panic("not used in metrics tests")
}
func (m *staticMockInformer) Get(_, _ string) (*Resource, bool)     { panic("not used") }
func (m *staticMockInformer) List(_ string) []*Resource             { panic("not used") }
func (m *staticMockInformer) ListAll() []*Resource                  { panic("not used") }
func (m *staticMockInformer) Subscribe(_ EventHandler) Subscription { panic("not used") }
func (m *staticMockInformer) ForceResync()                          { panic("not used") }
func (m *staticMockInformer) Stop()                                 { panic("not used") }
func (m *staticMockInformer) Stats() InformerStats                  { return m.stats }

// dynamicMockInformer 支持运行时更新 stats（验证 collector 不缓存）。
type dynamicMockInformer struct {
	current atomic.Pointer[InformerStats]
}

func newDynamicMockInformer(initial InformerStats) *dynamicMockInformer {
	m := &dynamicMockInformer{}
	m.current.Store(&initial)
	return m
}

func (m *dynamicMockInformer) update(s InformerStats) {
	m.current.Store(&s)
}

func (m *dynamicMockInformer) Start(_ context.Context) <-chan error {
	panic("not used in metrics tests")
}
func (m *dynamicMockInformer) Get(_, _ string) (*Resource, bool)     { panic("not used") }
func (m *dynamicMockInformer) List(_ string) []*Resource             { panic("not used") }
func (m *dynamicMockInformer) ListAll() []*Resource                  { panic("not used") }
func (m *dynamicMockInformer) Subscribe(_ EventHandler) Subscription { panic("not used") }
func (m *dynamicMockInformer) ForceResync()                          { panic("not used") }
func (m *dynamicMockInformer) Stop()                                 { panic("not used") }
func (m *dynamicMockInformer) Stats() InformerStats {
	if p := m.current.Load(); p != nil {
		return *p
	}
	return InformerStats{}
}

// ─── 测试 NewInformerCollector ───────────────────────────────────

func TestNewInformerCollector_NotNil(t *testing.T) {
	collector := NewInformerCollector()
	if collector == nil {
		t.Fatal("NewInformerCollector returned nil")
	}
}

func TestInformerCollector_AddInformer(t *testing.T) {
	collector := NewInformerCollector()
	informer := newMockInformer(InformerStats{Resource: "test"})
	collector.AddInformer(informer)

	// 验证 informer 已加入
	reg := prometheus.NewRegistry()
	if err := reg.Register(collector); err != nil {
		t.Fatalf("Register: %v", err)
	}

	count := testutil.CollectAndCount(reg, "kubepivot_informer_cache_size")
	if count != 1 {
		t.Errorf("AddInformer 后应能输出 metrics, got count=%d", count)
	}
}

func TestInformerCollector_AddInformer_NilSafe(t *testing.T) {
	collector := NewInformerCollector()
	collector.AddInformer(nil) // 不应 panic

	reg := prometheus.NewRegistry()
	if err := reg.Register(collector); err != nil {
		t.Fatalf("Register: %v", err)
	}

	count := testutil.CollectAndCount(reg, "kubepivot_informer_cache_size")
	if count != 0 {
		t.Errorf("AddInformer(nil) 不应注册任何 informer, got count=%d", count)
	}
}

func TestInformerCollector_RemoveInformer(t *testing.T) {
	collector := NewInformerCollector()
	informer := newMockInformer(InformerStats{Resource: "test"})
	collector.AddInformer(informer)
	collector.RemoveInformer(informer)

	reg := prometheus.NewRegistry()
	if err := reg.Register(collector); err != nil {
		t.Fatalf("Register: %v", err)
	}

	count := testutil.CollectAndCount(reg, "kubepivot_informer_cache_size")
	if count != 0 {
		t.Errorf("RemoveInformer 后不应输出 metrics, got count=%d", count)
	}
}

// ─── 测试 Describe 输出全部描述符 ────────────────────────────────

func TestInformerCollector_DescribeAllDescriptors(t *testing.T) {
	collector := NewInformerCollector()

	ch := make(chan *prometheus.Desc, 20)
	go func() {
		collector.Describe(ch)
		close(ch)
	}()

	count := 0
	for range ch {
		count++
	}

	// 9 个描述符
	const expected = 9
	if count != expected {
		t.Errorf("Describe 输出 %d 个描述符，期望 %d", count, expected)
	}
}

// ─── 测试 Collect 基础 Gauge 指标 ────────────────────────────────

func TestInformerCollector_CollectAllGauges(t *testing.T) {
	informer := newMockInformer(InformerStats{
		Resource:        "deployments",
		CacheSize:       100,
		HotCount:        80,
		WarmCount:       15,
		ColdCount:       5,
		WatchReconnects: 3,
		CacheHitRate:    0.95,
		MemoryBytes:     1024 * 1024,
	})

	reg := prometheus.NewRegistry()
	if err := RegisterInformerMetrics(reg, informer); err != nil {
		t.Fatalf("Register: %v", err)
	}

	expected := `
# HELP kubepivot_informer_cache_size Number of objects currently in informer cache.
# TYPE kubepivot_informer_cache_size gauge
kubepivot_informer_cache_size{resource="deployments"} 100
# HELP kubepivot_informer_cache_hot_count Number of objects in cache hot layer (full skeleton + raw object).
# TYPE kubepivot_informer_cache_hot_count gauge
kubepivot_informer_cache_hot_count{resource="deployments"} 80
# HELP kubepivot_informer_cache_warm_count Number of objects in cache warm layer (skeleton + raw JSON).
# TYPE kubepivot_informer_cache_warm_count gauge
kubepivot_informer_cache_warm_count{resource="deployments"} 15
# HELP kubepivot_informer_cache_cold_count Number of objects in cache cold layer (disk-backed).
# TYPE kubepivot_informer_cache_cold_count gauge
kubepivot_informer_cache_cold_count{resource="deployments"} 5
`

	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"kubepivot_informer_cache_size",
		"kubepivot_informer_cache_hot_count",
		"kubepivot_informer_cache_warm_count",
		"kubepivot_informer_cache_cold_count",
	); err != nil {
		t.Errorf("metric output mismatch:\n%v", err)
	}
}

// ─── 测试 events_total 按 type partition ─────────────────────────

func TestInformerCollector_EventsTotalByType(t *testing.T) {
	informer := newMockInformer(InformerStats{
		Resource: "pods",
		EventsByType: map[EventType]uint64{
			EventAdd:    100,
			EventUpdate: 50,
			EventDelete: 10,
			EventResync: 3,
		},
	})

	reg := prometheus.NewRegistry()
	if err := RegisterInformerMetrics(reg, informer); err != nil {
		t.Fatalf("Register: %v", err)
	}

	expected := `
# HELP kubepivot_informer_events_total Total number of events processed by informer, partitioned by type.
# TYPE kubepivot_informer_events_total counter
kubepivot_informer_events_total{resource="pods",type="add"} 100
kubepivot_informer_events_total{resource="pods",type="delete"} 10
kubepivot_informer_events_total{resource="pods",type="resync"} 3
kubepivot_informer_events_total{resource="pods",type="update"} 50
`

	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"kubepivot_informer_events_total"); err != nil {
		t.Errorf("events_total mismatch:\n%v", err)
	}
}

// ─── 测试 last_resync_timestamp_seconds: 未 resync 时不暴露 ─────

func TestInformerCollector_LastResync_NotExposedBeforeFirstResync(t *testing.T) {
	informer := newMockInformer(InformerStats{
		Resource:       "deployments",
		LastResyncTime: time.Time{}, // zero value
	})

	reg := prometheus.NewRegistry()
	if err := RegisterInformerMetrics(reg, informer); err != nil {
		t.Fatalf("Register: %v", err)
	}

	count := testutil.CollectAndCount(reg, "kubepivot_informer_last_resync_timestamp_seconds")
	if count != 0 {
		t.Errorf("last_resync 应在 zero LastResyncTime 时不暴露，但 count=%d", count)
	}
}

// ─── 测试 last_resync_timestamp_seconds: 已 resync 时正常暴露 ──

func TestInformerCollector_LastResync_ExposedAfterFirstResync(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	informer := newMockInformer(InformerStats{
		Resource:       "deployments",
		LastResyncTime: now,
	})

	reg := prometheus.NewRegistry()
	if err := RegisterInformerMetrics(reg, informer); err != nil {
		t.Fatalf("Register: %v", err)
	}

	count := testutil.CollectAndCount(reg, "kubepivot_informer_last_resync_timestamp_seconds")
	if count != 1 {
		t.Errorf("last_resync 应在有 LastResyncTime 时暴露，count=%d", count)
	}
}

// ─── 测试 RegisterInformerMetrics 错误情形 ────────────────────────

func TestRegisterInformerMetrics_NilRegisterer(t *testing.T) {
	informer := newMockInformer(InformerStats{Resource: "test"})
	if err := RegisterInformerMetrics(nil, informer); err != ErrNilRegisterer {
		t.Errorf("nil registerer should return ErrNilRegisterer, got %v", err)
	}
}

func TestRegisterInformerMetrics_NoInformers(t *testing.T) {
	reg := prometheus.NewRegistry()
	if err := RegisterInformerMetrics(reg); err != ErrNilInformer {
		t.Errorf("no informers should return ErrNilInformer, got %v", err)
	}
}

func TestRegisterInformerMetrics_NilInformer(t *testing.T) {
	reg := prometheus.NewRegistry()
	if err := RegisterInformerMetrics(reg, nil); err != ErrNilInformer {
		t.Errorf("nil informer should return ErrNilInformer, got %v", err)
	}
}

func TestRegisterInformerMetrics_DuplicateRegisterCollectors(t *testing.T) {
	// 同 registry 注册两个 collector：第二个会因 desc 冲突而失败
	informer1 := newMockInformer(InformerStats{Resource: "deploys"})
	informer2 := newMockInformer(InformerStats{Resource: "pods"})
	reg := prometheus.NewRegistry()

	if err := RegisterInformerMetrics(reg, informer1); err != nil {
		t.Fatalf("first register failed: %v", err)
	}
	// 第二次创建一个新 collector → desc 冲突
	if err := RegisterInformerMetrics(reg, informer2); err == nil {
		t.Error("registering second collector should fail due to desc conflict")
	}
}

// ─── 测试多个 informer 共存（一次性注册）─────────────────────────

func TestRegisterInformerMetrics_MultipleInformers(t *testing.T) {
	deployInformer := newMockInformer(InformerStats{
		Resource:  "deployments",
		CacheSize: 100,
	})
	podInformer := newMockInformer(InformerStats{
		Resource:  "pods",
		CacheSize: 500,
	})

	reg := prometheus.NewRegistry()
	if err := RegisterInformerMetrics(reg, deployInformer, podInformer); err != nil {
		t.Fatalf("register multiple informers: %v", err)
	}

	expected := `
# HELP kubepivot_informer_cache_size Number of objects currently in informer cache.
# TYPE kubepivot_informer_cache_size gauge
kubepivot_informer_cache_size{resource="deployments"} 100
kubepivot_informer_cache_size{resource="pods"} 500
`

	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"kubepivot_informer_cache_size"); err != nil {
		t.Errorf("multi-informer metrics mismatch:\n%v", err)
	}
}

// ─── 测试动态添加 informer ────────────────────────────────────────

func TestInformerCollector_DynamicAdd(t *testing.T) {
	collector := NewInformerCollector()
	reg := prometheus.NewRegistry()
	if err := reg.Register(collector); err != nil {
		t.Fatalf("Register collector: %v", err)
	}

	// 初始无 informer
	count := testutil.CollectAndCount(reg, "kubepivot_informer_cache_size")
	if count != 0 {
		t.Errorf("无 informer 时不应输出, count=%d", count)
	}

	// 动态添加
	collector.AddInformer(newMockInformer(InformerStats{
		Resource:  "deployments",
		CacheSize: 50,
	}))

	count = testutil.CollectAndCount(reg, "kubepivot_informer_cache_size")
	if count != 1 {
		t.Errorf("AddInformer 后应输出 1 个，count=%d", count)
	}

	// 再加一个
	collector.AddInformer(newMockInformer(InformerStats{
		Resource:  "pods",
		CacheSize: 200,
	}))

	count = testutil.CollectAndCount(reg, "kubepivot_informer_cache_size")
	if count != 2 {
		t.Errorf("第二次 AddInformer 后应输出 2 个，count=%d", count)
	}
}

// ─── 测试 Watch reconnects counter ────────────────────────────────

func TestInformerCollector_WatchReconnects(t *testing.T) {
	informer := newMockInformer(InformerStats{
		Resource:        "deployments",
		WatchReconnects: 42,
	})

	reg := prometheus.NewRegistry()
	if err := RegisterInformerMetrics(reg, informer); err != nil {
		t.Fatalf("Register: %v", err)
	}

	expected := `
# HELP kubepivot_informer_watch_reconnects_total Total number of watch reconnections (includes server-initiated closes).
# TYPE kubepivot_informer_watch_reconnects_total counter
kubepivot_informer_watch_reconnects_total{resource="deployments"} 42
`

	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"kubepivot_informer_watch_reconnects_total"); err != nil {
		t.Errorf("watch_reconnects mismatch:\n%v", err)
	}
}

// ─── 测试动态 Stats 变化（验证 collector 不缓存） ────────────────

func TestInformerCollector_StatsUpdated(t *testing.T) {
	informer := newDynamicMockInformer(InformerStats{
		Resource:  "deployments",
		CacheSize: 100,
	})

	reg := prometheus.NewRegistry()
	if err := RegisterInformerMetrics(reg, informer); err != nil {
		t.Fatalf("Register: %v", err)
	}

	expected1 := `
# HELP kubepivot_informer_cache_size Number of objects currently in informer cache.
# TYPE kubepivot_informer_cache_size gauge
kubepivot_informer_cache_size{resource="deployments"} 100
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected1),
		"kubepivot_informer_cache_size"); err != nil {
		t.Fatalf("first collect:\n%v", err)
	}

	informer.update(InformerStats{
		Resource:  "deployments",
		CacheSize: 200,
	})

	expected2 := `
# HELP kubepivot_informer_cache_size Number of objects currently in informer cache.
# TYPE kubepivot_informer_cache_size gauge
kubepivot_informer_cache_size{resource="deployments"} 200
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected2),
		"kubepivot_informer_cache_size"); err != nil {
		t.Errorf("second collect (after update):\n%v", err)
	}
}

// ─── 测试 cache_hit_ratio 边界值都能输出 ─────────────────────────

func TestInformerCollector_CacheHitRatio(t *testing.T) {
	tests := []struct {
		name string
		rate float64
	}{
		{"zero_no_hits", 0.0},
		{"all_hits", 1.0},
		{"half", 0.5},
		{"typical", 0.95},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			informer := newMockInformer(InformerStats{
				Resource:     "deployments",
				CacheHitRate: tt.rate,
			})

			reg := prometheus.NewRegistry()
			if err := RegisterInformerMetrics(reg, informer); err != nil {
				t.Fatalf("Register: %v", err)
			}

			count := testutil.CollectAndCount(reg, "kubepivot_informer_cache_hit_ratio")
			if count != 1 {
				t.Errorf("cache_hit_ratio count=%d, want 1", count)
			}
		})
	}
}
