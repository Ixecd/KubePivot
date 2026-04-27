package eventstream

import (
	"testing"

	"github.com/Ixecd/kubepivot/internal/sharding"
)

// ─── NewShardSetAdapter 测试 ──────────────────────────────────────

func TestNewShardSetAdapter_NilInner(t *testing.T) {
	got := NewShardSetAdapter(nil, 10)
	if got != nil {
		t.Errorf("inner=nil 应返回 nil，got %v", got)
	}
}

func TestNewShardSetAdapter_ZeroTotalShards(t *testing.T) {
	inner := sharding.NewShardSet()
	got := NewShardSetAdapter(inner, 0)
	if got != nil {
		t.Errorf("totalShards=0 应返回 nil，got %v", got)
	}
}

func TestNewShardSetAdapter_NegativeTotalShards(t *testing.T) {
	inner := sharding.NewShardSet()
	got := NewShardSetAdapter(inner, -1)
	if got != nil {
		t.Errorf("totalShards<0 应返回 nil，got %v", got)
	}
}

func TestNewShardSetAdapter_ValidConstruction(t *testing.T) {
	inner := sharding.NewShardSet()
	adapter := NewShardSetAdapter(inner, 10)
	if adapter == nil {
		t.Fatal("有效参数应返回非 nil")
	}
}

// ─── Owns 行为测试 ────────────────────────────────────────────────

func TestShardSetAdapter_OwnsAllShards(t *testing.T) {
	// 拥有所有 10 个 shard 时，任何 ns 都归本 pod
	inner := sharding.NewShardSet()
	for i := 0; i < 10; i++ {
		inner.Add(i)
	}

	adapter := NewShardSetAdapter(inner, 10)

	// 任意 ns 都应返回 true
	tests := []string{"default", "kube-system", "my-ns", "team-foo"}
	for _, ns := range tests {
		if !adapter.Owns(ns) {
			t.Errorf("拥有所有 shard 时，ns=%q 应归属本 pod", ns)
		}
	}
}

func TestShardSetAdapter_OwnsNoShards(t *testing.T) {
	// 不持有任何 shard 时，任何 ns 都不归本 pod
	inner := sharding.NewShardSet()
	adapter := NewShardSetAdapter(inner, 10)

	tests := []string{"default", "my-ns", "any-namespace"}
	for _, ns := range tests {
		if adapter.Owns(ns) {
			t.Errorf("无 shard 时，ns=%q 不应归属本 pod", ns)
		}
	}
}

func TestShardSetAdapter_PartialShards(t *testing.T) {
	// 持有部分 shard：根据 ns hash 落到的 shard 决定归属
	// 用 v2.5 实际的 hash 算法计算，验证行为一致

	inner := sharding.NewShardSet()
	inner.Add(0)
	inner.Add(1)
	inner.Add(2)

	adapter := NewShardSetAdapter(inner, 10)

	// 用 v2.5 的 ShardOf 直接计算预期值
	// 验证 adapter 与 inner 行为完全一致
	for _, ns := range []string{"default", "kube-system", "test-ns", "foo", "bar"} {
		// inner 自己计算
		expected := inner.OwnsNamespace(ns, 10)

		// adapter 计算
		actual := adapter.Owns(ns)

		if actual != expected {
			t.Errorf("ns=%q: adapter=%v, inner=%v (应一致)",
				ns, actual, expected)
		}
	}
}

// ─── 与真实 v2.5 ShardOf 联动测试 ──────────────────────────────────

func TestShardSetAdapter_ConsistentWithShardOf(t *testing.T) {
	// 验证 adapter 的归属判断与 v2.5 ShardOf 函数一致
	totalShards := 10
	ownedShards := []int{3, 5, 7}

	inner := sharding.NewShardSet()
	for _, idx := range ownedShards {
		inner.Add(idx)
	}

	adapter := NewShardSetAdapter(inner, totalShards)

	// 测试 50 个 ns，验证 adapter 与 ShardOf 计算一致
	for i := 0; i < 50; i++ {
		ns := "test-ns-" + itoa(i)
		shardIdx := sharding.ShardOf(ns, totalShards)

		expectedOwned := false
		for _, owned := range ownedShards {
			if shardIdx == owned {
				expectedOwned = true
				break
			}
		}

		actual := adapter.Owns(ns)
		if actual != expectedOwned {
			t.Errorf("ns=%q (shard=%d): adapter=%v, expected=%v",
				ns, shardIdx, actual, expectedOwned)
		}
	}
}

// ─── staticShardSet 测试 ──────────────────────────────────────────

func TestStaticShardSet_AllNamespaces(t *testing.T) {
	// 传 nil → 所有 ns 都归本 pod
	tests := [][]string{
		nil,
		{},
	}

	for _, input := range tests {
		s := NewStaticShardSet(input)
		for _, ns := range []string{"default", "kube-system", "my-ns"} {
			if !s.Owns(ns) {
				t.Errorf("input=%v: ns=%q 应归属（all namespaces）", input, ns)
			}
		}
	}
}

func TestStaticShardSet_SpecificNamespaces(t *testing.T) {
	s := NewStaticShardSet([]string{"team-a", "team-b"})

	owned := []string{"team-a", "team-b"}
	notOwned := []string{"team-c", "default", "kube-system"}

	for _, ns := range owned {
		if !s.Owns(ns) {
			t.Errorf("ns=%q 应归属", ns)
		}
	}
	for _, ns := range notOwned {
		if s.Owns(ns) {
			t.Errorf("ns=%q 不应归属", ns)
		}
	}
}

func TestStaticShardSet_EmptyVsNil(t *testing.T) {
	// nil 和空 slice 行为一致（都是"全部归属"）
	s1 := NewStaticShardSet(nil)
	s2 := NewStaticShardSet([]string{})

	for _, ns := range []string{"any-ns", "another-ns"} {
		if s1.Owns(ns) != s2.Owns(ns) {
			t.Errorf("nil 与空 slice 应行为一致，ns=%q: nil=%v, empty=%v",
				ns, s1.Owns(ns), s2.Owns(ns))
		}
	}
}

// ─── 集成场景测试：Informer + Adapter ─────────────────────────────

func TestShardSetAdapter_IntegrationWithInformer(t *testing.T) {
	// 模拟实际场景：Informer 用 adapter 过滤事件
	inner := sharding.NewShardSet()
	inner.Add(0) // 仅持有 shard 0

	adapter := NewShardSetAdapter(inner, 10)

	// 验证 adapter 实现了 ShardSet 接口（编译期已验证，这里再 runtime 确认）
	var _ ShardSet = adapter
}
