package eventstream

import (
	"context"
	"testing"
)

func TestResourceToPodEntry(t *testing.T) {
	// Simulate a minimal Pod JSON from K8s API
	podJSON := []byte(`{
		"metadata": {"name": "test-pod", "namespace": "default", "labels": {"app": "nginx", "tier": "frontend"}},
		"spec": {
			"nodeName": "node-1",
			"containers": [
				{"name": "nginx", "resources": {"requests": {"cpu": "100m", "memory": "128Mi"}}},
				{"name": "sidecar", "resources": {"requests": {"cpu": "50m", "memory": "64Mi"}}}
			]
		},
		"status": {"phase": "Running"}
	}`)

	r := &Resource{
		APIVersion:      "v1",
		Kind:            "Pod",
		Namespace:       "default",
		Name:            "test-pod",
		Phase:           "Running",
		ResourceVersion: "123456",
		Labels:          map[string]string{"app": "nginx", "tier": "frontend"},
		RawJSON:         podJSON,
	}

	entry, err := ResourceToPodEntry(r)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Namespace != "default" || entry.Name != "test-pod" {
		t.Errorf("identity mismatch: %s/%s", entry.Namespace, entry.Name)
	}
	if entry.NodeName != "node-1" {
		t.Errorf("NodeName = %s, want node-1", entry.NodeName)
	}
	if entry.Phase != "Running" {
		t.Errorf("Phase = %s, want Running", entry.Phase)
	}
	if entry.RV != 123456 {
		t.Errorf("RV = %d, want 123456", entry.RV)
	}
	// CPU: 100m + 50m = 150m
	if entry.Requests.CPU != 150 {
		t.Errorf("CPU = %d, want 150 (100m+50m)", entry.Requests.CPU)
	}
	// Mem: 128Mi + 64Mi = 128*1024*1024 + 64*1024*1024
	expectedMem := int64(128*1024*1024 + 64*1024*1024)
	if entry.Requests.Memory != expectedMem {
		t.Errorf("Memory = %d, want %d", entry.Requests.Memory, expectedMem)
	}
	// Labels compressed
	if v, ok := entry.GetLabel("app"); !ok || v != "nginx" {
		t.Errorf("GetLabel(app) = (%s, %v), want (nginx, true)", v, ok)
	}
	if v, ok := entry.GetLabel("tier"); !ok || v != "frontend" {
		t.Errorf("GetLabel(tier) = (%s, %v), want (frontend, true)", v, ok)
	}
}

func TestParseQuantityToMilli(t *testing.T) {
	tests := []struct {
		in  string
		out int64
	}{
		{"100m", 100},
		{"1", 1000},
		{"1.5", 1500},
		{"", 0},
	}
	for _, tt := range tests {
		if got := parseQuantityToMilli(tt.in); got != tt.out {
			t.Errorf("parseQuantityToMilli(%q) = %d, want %d", tt.in, got, tt.out)
		}
	}
}

func TestResourceToPodEntry_NoContainers(t *testing.T) {
	podJSON := []byte(`{"spec":{},"status":{}}`)
	r := &Resource{Kind: "Pod", RawJSON: podJSON}
	entry, err := ResourceToPodEntry(r)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Requests.CPU != 0 || entry.Requests.Memory != 0 {
		t.Error("empty containers should give zero requests")
	}
}

func TestResourceToPodEntry_EmptyJSON(t *testing.T) {
	_, err := ResourceToPodEntry(&Resource{Kind: "Pod", RawJSON: []byte(`{}`)})
	if err != nil {
		t.Fatal("empty JSON should not error (zero values)")
	}
}

// ─── PodCacheBridge ─────────────────────────────────────────────

type fakeInformer struct {
	handler EventHandler
	resources []*Resource
}

func (f *fakeInformer) Start(ctx context.Context) <-chan error { return nil }
func (f *fakeInformer) Stop()        {}
func (f *fakeInformer) ForceResync() {}
func (f *fakeInformer) Get(ns, name string) (*Resource, bool) { return nil, false }
func (f *fakeInformer) List(ns string) []*Resource { return nil }
func (f *fakeInformer) ListAll() []*Resource { return f.resources }
func (f *fakeInformer) Subscribe(h EventHandler) Subscription {
	f.handler = h
	return &fakeSubscription{}
}
func (f *fakeInformer) Stats() InformerStats { return InformerStats{} }

type fakeSubscription struct{}
func (s *fakeSubscription) Unsubscribe() {}
func (s *fakeSubscription) Stats() SubscriberStats { return SubscriberStats{} }

func TestPodCacheBridge_EventAdd(t *testing.T) {
	inf := &fakeInformer{}
	cache := NewPodCache()
	_ = NewPodCacheBridge(inf, cache)

	podJSON := []byte(`{"spec":{"nodeName":"n1","containers":[{"name":"app","resources":{"requests":{"cpu":"100m","memory":"128Mi"}}}]},"status":{"phase":"Running"}}`)
	inf.handler(Event{
		Type: EventAdd,
		New: &Resource{Kind: "Pod", Namespace: "ns", Name: "p1", ResourceVersion: "1", Labels: map[string]string{"app": "test"}, RawJSON: podJSON, Phase: "Running"},
	})

	p, _ := cache.Get("ns", "p1")
	if p == nil {
		t.Fatal("EventAdd should populate PodCache")
	}
	if p.NodeName != "n1" {
		t.Errorf("NodeName = %s, want n1", p.NodeName)
	}
}

func TestPodCacheBridge_EventDelete(t *testing.T) {
	inf := &fakeInformer{}
	cache := NewPodCache()
	cache.PutBulk([]*PodEntry{{Namespace: "ns", Name: "p1", NodeName: "n1", Phase: "Running", RV: 1}})
	_ = NewPodCacheBridge(inf, cache)

	inf.handler(Event{
		Type: EventDelete,
		Old: &Resource{Kind: "Pod", Namespace: "ns", Name: "p1"},
	})

	p, _ := cache.Get("ns", "p1")
	if p != nil {
		t.Error("EventDelete should remove from PodCache")
	}
}

func TestPodCacheBridge_IgnoresNonPod(t *testing.T) {
	inf := &fakeInformer{}
	cache := NewPodCache()
	_ = NewPodCacheBridge(inf, cache)

	inf.handler(Event{
		Type: EventAdd,
		New: &Resource{Kind: "Deployment", Namespace: "ns", Name: "d1", ResourceVersion: "1", Labels: map[string]string{"app": "test"}},
	})

	p, _ := cache.Get("ns", "d1")
	if p != nil {
		t.Error("non-Pod event should be ignored")
	}
}

func TestPodCacheBridge_EventResync(t *testing.T) {
	inf := &fakeInformer{
		resources: []*Resource{
			{Kind: "Pod", Namespace: "ns", Name: "p1", ResourceVersion: "1", Labels: map[string]string{"app": "test"}, Phase: "Running", RawJSON: []byte(`{"spec":{"nodeName":"n1","containers":[{"name":"app","resources":{"requests":{"cpu":"100m","memory":"128Mi"}}}]},"status":{"phase":"Running"}}`)},
		},
	}
	cache := NewPodCache()
	_ = NewPodCacheBridge(inf, cache)
	// EventResync triggers PutBulk from informer.ListAll
	inf.handler(Event{Type: EventResync})

	if cache.ListAll() == nil {
		t.Fatal("EventResync should populate cache")
	}
}

func TestParseQuantityToBytes(t *testing.T) {
	tests := []struct {
		in  string
		out int64
	}{
		{"128Mi", 128 * 1024 * 1024},
		{"1Gi", 1024 * 1024 * 1024},
		{"512M", 512 * 1000 * 1000},
		{"", 0},
	}
	for _, tt := range tests {
		if got := parseQuantityToBytes(tt.in); got != tt.out {
			t.Errorf("parseQuantityToBytes(%q) = %d, want %d", tt.in, got, tt.out)
		}
	}
}
