package eventstream

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// ── PodCache ────────────────────────────────────────────────────

func TestPodCache_PutBulk_And_ListAll(t *testing.T) {
	c := NewPodCache()
	pods := []*PodEntry{
		{Namespace: "default", Name: "pod-a", NodeName: "node1", Phase: "Running"},
		{Namespace: "default", Name: "pod-b", NodeName: "node2", Phase: "Running"},
	}
	c.PutBulk(pods)

	if !c.IsReady() {
		t.Error("cache should be ready after PutBulk")
	}

	all := c.ListAll()
	if len(all) != 2 {
		t.Fatalf("ListAll = %d, want 2", len(all))
	}
}

func TestPodCache_ListByNode(t *testing.T) {
	c := NewPodCache()
	c.PutBulk([]*PodEntry{
		{Namespace: "ns", Name: "pod-a", NodeName: "node1"},
		{Namespace: "ns", Name: "pod-b", NodeName: "node1"},
		{Namespace: "ns", Name: "pod-c", NodeName: "node2"},
	})

	node1 := c.ListByNode("node1")
	if len(node1) != 2 {
		t.Errorf("node1 pods = %d, want 2", len(node1))
	}
	node2 := c.ListByNode("node2")
	if len(node2) != 1 {
		t.Errorf("node2 pods = %d, want 1", len(node2))
	}
}

func TestPodCache_Get(t *testing.T) {
	c := NewPodCache()
	c.PutBulk([]*PodEntry{
		{Namespace: "default", Name: "test-pod"},
	})

	p, ok := c.Get("default", "test-pod")
	if !ok || p.Name != "test-pod" {
		t.Error("Get failed for existing pod")
	}

	_, ok = c.Get("default", "nonexistent")
	if ok {
		t.Error("Get should return false for missing pod")
	}
}

func TestPodCache_Put_CrossNode(t *testing.T) {
	c := NewPodCache()
	c.PutBulk([]*PodEntry{
		{Namespace: "ns", Name: "migrant", NodeName: "node1"},
	})

	// 跨节点迁移
	c.Put(&PodEntry{Namespace: "ns", Name: "migrant", NodeName: "node2"}, "node1")

	if len(c.ListByNode("node1")) != 0 {
		t.Error("old node should have 0 pods after migration")
	}
	if len(c.ListByNode("node2")) != 1 {
		t.Error("new node should have 1 pod after migration")
	}
}

func TestPodCache_Delete(t *testing.T) {
	c := NewPodCache()
	c.PutBulk([]*PodEntry{
		{Namespace: "ns", Name: "pod-a", NodeName: "node1"},
	})

	c.Delete("ns", "pod-a", "node1")
	if len(c.ListAll()) != 0 {
		t.Error("ListAll should be empty after delete")
	}
	if len(c.ListByNode("node1")) != 0 {
		t.Error("byNode should be empty after delete")
	}
}

// ── NodeCache ────────────────────────────────────────────────────

func TestNodeCache_PutBulk_And_ListAll(t *testing.T) {
	c := NewNodeCache()
	c.PutBulk([]*NodeEntry{
		{Name: "node1", AllocatableCPU: 4000},
		{Name: "node2", AllocatableCPU: 8000},
	})

	if !c.IsReady() {
		t.Error("cache should be ready after PutBulk")
	}

	all := c.ListAll()
	if len(all) != 2 {
		t.Fatalf("ListAll = %d, want 2", len(all))
	}
}

func TestNodeCache_Get(t *testing.T) {
	c := NewNodeCache()
	c.PutBulk([]*NodeEntry{
		{Name: "node1", AllocatableCPU: 4000},
	})

	n, ok := c.Get("node1")
	if !ok || n.AllocatableCPU != 4000 {
		t.Error("Get failed")
	}

	_, ok = c.Get("node99")
	if ok {
		t.Error("Get should return false for missing node")
	}
}

func TestNodeCache_Put_Update(t *testing.T) {
	c := NewNodeCache()
	c.PutBulk([]*NodeEntry{
		{Name: "node1", AllocatableCPU: 4000},
	})

	c.Put(&NodeEntry{Name: "node1", AllocatableCPU: 8000})

	n, _ := c.Get("node1")
	if n.AllocatableCPU != 8000 {
		t.Errorf("AllocatableCPU = %d, want 8000 after update", n.AllocatableCPU)
	}
}

func TestNodeCache_Delete(t *testing.T) {
	c := NewNodeCache()
	c.PutBulk([]*NodeEntry{
		{Name: "node1"},
	})

	c.Delete("node1")
	if len(c.ListAll()) != 0 {
		t.Error("ListAll should be empty after delete")
	}
}

// ── Heartbeat / Stale ────────────────────────────────────────────

func TestPodCache_Heartbeat_NotZero(t *testing.T) {
	c := NewPodCache()
	c.PutBulk([]*PodEntry{{Namespace: "ns", Name: "p", NodeName: "n1"}})
	if c.StaleDuration() == 0 {
		t.Error("after PutBulk, stale duration should be > 0")
	}
}

func TestPodCache_Heartbeat_Manual(t *testing.T) {
	c := NewPodCache()
	c.PutBulk([]*PodEntry{{Namespace: "ns", Name: "p", NodeName: "n1"}})
	time.Sleep(10 * time.Millisecond)
	c.Heartbeat()
	d := c.StaleDuration()
	if d > 100*time.Millisecond {
		t.Errorf("after heartbeat, stale should be < 100ms, got %v", d)
	}
}

func TestPodCache_IsReady_Atomic(t *testing.T) {
	c := NewPodCache()
	// ready=false 直到 PutBulk 完成初始填充（区分"空集群"和"未填充"）
	if c.IsReady() {
		t.Error("empty cache should NOT be ready before initial fill")
	}
	c.PutBulk([]*PodEntry{{Namespace: "ns", Name: "p", NodeName: "n1"}})
	if !c.IsReady() {
		t.Error("cache should be ready after PutBulk")
	}
}

// ── CoW correctness ──────────────────────────────────────────────

func TestPodCache_Put_CoW_OtherNodesUntouched(t *testing.T) {
	c := NewPodCache()
	c.PutBulk([]*PodEntry{
		{Namespace: "ns", Name: "pod-a", NodeName: "node1"},
		{Namespace: "ns", Name: "pod-b", NodeName: "node2"},
	})

	// 更新 node1 上的 pod-a
	c.Put(&PodEntry{Namespace: "ns", Name: "pod-a", NodeName: "node1", Phase: "Running"}, "")

	// node2 上的 pod-b 应该不受影响
	if len(c.ListByNode("node2")) != 1 {
		t.Error("node2 pods should be untouched after updating node1 pod")
	}
}

func TestPodCache_Delete_IndexLeak(t *testing.T) {
	c := NewPodCache()
	c.PutBulk([]*PodEntry{
		{Namespace: "ns", Name: "only-pod", NodeName: "node1"},
	})

	c.Delete("ns", "only-pod", "node1")
	// 节点上没有 Pod 了，byNode key 应该被清理
	if len(c.ListByNode("node1")) != 0 {
		t.Error("byNode key should be cleaned when last pod deleted")
	}
}

// ── Put 不信任 oldNodeName ───────────────────────────────────────

func TestPodCache_Put_IgnoreWrongOldNodeName(t *testing.T) {
	c := NewPodCache()
	c.PutBulk([]*PodEntry{
		{Namespace: "ns", Name: "p", NodeName: "real-node"},
	})

	// 故意传错误的 oldNodeName
	c.Put(&PodEntry{Namespace: "ns", Name: "p", NodeName: "new-node"}, "wrong-node")

	// 应该是从缓存内部读出 real-node 并正确迁移
	if len(c.ListByNode("real-node")) != 0 {
		t.Error("real-node should be empty after migration")
	}
	if len(c.ListByNode("new-node")) != 1 {
		t.Error("new-node should have the pod")
	}
	if p, _ := c.Get("ns", "p"); p.NodeName != "new-node" {
		t.Errorf("pod NodeName = %s, want new-node", p.NodeName)
	}
}

// ── Subscribe ────────────────────────────────────────────────────

type testSubscriber struct {
	events []CacheChangeEvent
}

func (s *testSubscriber) OnChange(e CacheChangeEvent) {
	s.events = append(s.events, e)
}

func TestPodCache_Subscribe_PutBulk(t *testing.T) {
	c := NewPodCache()
	sub := &testSubscriber{}
	c.Subscribe(sub)

	c.PutBulk([]*PodEntry{{Namespace: "ns", Name: "p", NodeName: "n1"}})

	if len(sub.events) != 1 || sub.events[0].Type != ChangeBulkResync {
		t.Errorf("PutBulk should fire BulkResync, got %+v", sub.events)
	}
}

func TestPodCache_Subscribe_Put_Added(t *testing.T) {
	c := NewPodCache()
	sub := &testSubscriber{}
	c.Subscribe(sub)

	c.Put(&PodEntry{Namespace: "ns", Name: "new-pod", NodeName: "n1"}, "")

	if len(sub.events) != 1 || sub.events[0].Type != ChangePodAdded {
		t.Errorf("new pod should fire PodAdded, got %+v", sub.events)
	}
}

func TestPodCache_Subscribe_Put_Modified(t *testing.T) {
	c := NewPodCache()
	c.PutBulk([]*PodEntry{{Namespace: "ns", Name: "p", NodeName: "n1"}})

	sub := &testSubscriber{}
	c.Subscribe(sub)

	c.Put(&PodEntry{Namespace: "ns", Name: "p", NodeName: "n1", Phase: "Running"}, "")

	if len(sub.events) != 1 || sub.events[0].Type != ChangePodModified {
		t.Errorf("existing pod update should fire PodModified, got %+v", sub.events)
	}
}

func TestPodCache_Subscribe_Delete(t *testing.T) {
	c := NewPodCache()
	c.PutBulk([]*PodEntry{{Namespace: "ns", Name: "p", NodeName: "n1"}})

	sub := &testSubscriber{}
	c.Subscribe(sub)

	c.Delete("ns", "p", "n1")

	if len(sub.events) != 1 || sub.events[0].Type != ChangePodDeleted {
		t.Errorf("delete should fire PodDeleted, got %+v", sub.events)
	}
}

func TestShardedPodCache_ShardIsolation(t *testing.T) {
	sc := NewShardedPodCache(4)
	pods := []*PodEntry{
		{Namespace: "ns-a", Name: "p1", NodeName: "n1"},
		{Namespace: "ns-b", Name: "p2", NodeName: "n2"},
		{Namespace: "ns-c", Name: "p3", NodeName: "n3"},
	}
	sc.PutBulk(pods)
	if !sc.IsReady() {
		t.Fatal("all shards should be ready")
	}
	if p, _ := sc.Get("ns-a", "p1"); p == nil {
		t.Error("should find p1 in shard")
	}
	if p, _ := sc.Get("ns-nonexistent", "p1"); p != nil {
		t.Error("wrong shard lookup should not find p1")
	}
}

func TestShardedPodCache_ConcurrentPut(t *testing.T) {
	sc := NewShardedPodCache(4)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ns := fmt.Sprintf("ns-%d", idx%20)
			sc.Put(&PodEntry{Namespace: ns, Name: fmt.Sprintf("p-%d", idx), NodeName: "n1"}, "")
		}(i)
	}
	wg.Wait()
	all := sc.ListAll()
	if len(all) == 0 {
		t.Error("concurrent puts should produce results")
	}
	t.Logf("concurrent 100 puts → %d pods listed", len(all))
}

func TestShardedPodCache_Generation(t *testing.T) {
	sc := NewShardedPodCache(2)
	g1 := sc.Generation()
	if g1 != 0 {
		t.Errorf("empty cache gen = %d, want 0", g1)
	}
	sc.Put(&PodEntry{Namespace: "ns-a", Name: "p1", NodeName: "n1"}, "")
	g2 := sc.Generation()
	if g2 == g1 {
		t.Error("gen should change after Put")
	}
}
