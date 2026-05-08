package eventstream

import (
	"context"
	"testing"
)

// ─── EventType 测试 ────────────────────────────────────────────────

func TestEventType_String(t *testing.T) {
	tests := []struct {
		typ  EventType
		want string
	}{
		{EventAdd, "ADD"},
		{EventUpdate, "UPDATE"},
		{EventDelete, "DELETE"},
		{EventResync, "RESYNC"},
		{EventType(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		if got := tt.typ.String(); got != tt.want {
			t.Errorf("EventType(%d).String() = %q, want %q", tt.typ, got, tt.want)
		}
	}
}

// ─── Event 结构测试 ────────────────────────────────────────────────

func TestEvent_Struct(t *testing.T) {
	// 简单 sanity check：Event 字段都能赋值
	r := &Resource{Namespace: "default", Name: "app"}
	e := Event{
		Type:      EventUpdate,
		Old:       r,
		New:       r,
	}

	if e.Type != EventUpdate {
		t.Errorf("Type = %v, want EventUpdate", e.Type)
	}
	if e.Old != r || e.New != r {
		t.Error("Old / New 字段未正确保存")
	}
}

// ─── SubscriberStats 原子计数器测试 ────────────────────────────────

func TestSubscriberStats_AtomicCounters(t *testing.T) {
	var s SubscriberStats

	s.EventsDelivered.Add(5)
	s.EventsDropped.Add(2)
	s.Panics.Add(1)

	if got := s.EventsDelivered.Load(); got != 5 {
		t.Errorf("EventsDelivered = %d, want 5", got)
	}
	if got := s.EventsDropped.Load(); got != 2 {
		t.Errorf("EventsDropped = %d, want 2", got)
	}
	if got := s.Panics.Load(); got != 1 {
		t.Errorf("Panics = %d, want 1", got)
	}
}

// ─── InformerOptions 默认值合理性测试 ──────────────────────────────

func TestInformerOptions_ZeroValue(t *testing.T) {
	// 零值 InformerOptions 应该可以构造（不 panic）
	// 实际 Informer 创建时会用默认值填充
	var opts InformerOptions

	// 这些字段应该都是 zero value
	if opts.Resource != "" {
		t.Errorf("Resource zero value = %q, want empty", opts.Resource)
	}
	if opts.ShardSet != nil {
		t.Errorf("ShardSet zero value should be nil")
	}
	if opts.ResyncPeriod != 0 {
		t.Errorf("ResyncPeriod zero value should be 0")
	}
}

// ─── NewInformer 参数验证测试 ─────────────────────────────────────

func TestNewInformer_RequiresResource(t *testing.T) {
	_, err := NewInformer(context.Background(), InformerOptions{
		APIVersion: "apps/v1",
	})
	if err == nil {
		t.Error("缺 Resource 应返回 error")
	}
}

func TestNewInformer_RequiresAPIVersion(t *testing.T) {
	_, err := NewInformer(context.Background(), InformerOptions{
		Resource: "deployments",
	})
	if err == nil {
		t.Error("缺 APIVersion 应返回 error")
	}
}

func TestNewInformer_ValidOptions(t *testing.T) {
	informer, err := NewInformer(context.Background(), InformerOptions{
		Resource:     "deployments",
		APIVersion:   "apps/v1",
		APIServerURL: "http://localhost:8001",
	})
	if err != nil {
		t.Fatalf("有效 options 应成功，got err: %v", err)
	}
	if informer == nil {
		t.Fatal("应返回非 nil informer")
	}
	// Stats 应可调用，零值
	stats := informer.Stats()
	if stats.Resource != "deployments" {
		t.Errorf("Stats().Resource = %q, want deployments", stats.Resource)
	}
	if stats.CacheSize != 0 {
		t.Errorf("初始 CacheSize = %d, want 0", stats.CacheSize)
	}
	// Stop 应安全
	informer.Stop()
	informer.Stop() // 多次调用安全
}

func TestNewInformer_DefaultsApplied(t *testing.T) {
	// 未指定 ResyncPeriod / ReconnectPolicy 应自动用默认值
	// 通过 Stats / 行为间接验证（无 panic 即通过）
	informer, err := NewInformer(context.Background(), InformerOptions{
		Resource:     "pods",
		APIVersion:   "v1",
		APIServerURL: "http://localhost:8001",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer informer.Stop()

	stats := informer.Stats()
	if stats.Resource != "pods" {
		t.Errorf("Resource = %q, want pods", stats.Resource)
	}
}

// ─── InformerStats 字段测试 ────────────────────────────────────────

func TestInformerStats_EventsByTypeMap(t *testing.T) {
	stats := InformerStats{
		EventsByType: map[EventType]uint64{},
	}

	stats.EventsByType[EventAdd] = 100
	stats.EventsByType[EventUpdate] = 50

	if got := stats.EventsByType[EventAdd]; got != 100 {
		t.Errorf("EventsByType[EventAdd] = %d, want 100", got)
	}
}
