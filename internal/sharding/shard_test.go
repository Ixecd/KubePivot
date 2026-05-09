package sharding

import (
	"fmt"
	"sync"
	"testing"
)

// ── ShardOf 测试 ──────────────────────────────────────────────────────────────

func TestShardOf_Stable(t *testing.T) {
	for i := 0; i < 10; i++ {
		if got := ShardOf("kp-auth-service", 10); got != ShardOf("kp-auth-service", 10) {
			t.Fatalf("jump hash 应稳定，第 %d 次结果不同", i)
		}
	}
}

// TestJumpHash_SequentialNames v3.3: 验证 jump hash 在连续短名场景（FNV 弱项）的均匀性。
// kp-bench-001..050 等连续命名在 FNV % N 下有 ±40% 不均（<5 shard 被命中），
// jump hash 应命中 ≥7 shard（10 个中）。
func TestJumpHash_SequentialNames(t *testing.T) {
	const N = 10
	dist := make(map[int]int)
	for i := 0; i < 50; i++ {
		ns := fmt.Sprintf("kp-bench-%03d", i+1)
		dist[ShardOf(ns, N)]++
	}
	if len(dist) < 7 {
		t.Errorf("50 个连续短名仅命中 %d 个 shard，jump hash 应 ≥7: %v", len(dist), dist)
	}
	// 无单个 shard 超过 3x 理想值（5*3=15）
	for shard, count := range dist {
		if count > 15 {
			t.Errorf("shard %d count=%d，严重偏斜", shard, count)
		}
	}
	t.Logf("50 连续短名分布 (jump hash, %d/10 shard 命中): %v", len(dist), dist)
}

func TestShardOf_DistributesEvenly(t *testing.T) {
	projects := []string{
		"kp-auth-service", "kp-gateway", "kp-user-profile",
		"kp-order-api", "kp-payment-worker", "kp-notification",
		"kp-search-engine", "kp-analytics", "kp-media-processor",
		"kp-admin-dashboard",
	}
	const N = 10
	dist := make(map[int]int)
	for _, p := range projects {
		dist[ShardOf(p, N)]++
	}
	if len(dist) < 5 {
		t.Errorf("10 项目分布到 < 5 个 shard，hash 分布不良: %v", dist)
	}
	t.Logf("10 项目分布: %v", dist)
}

func TestShardOf_RangeCorrect(t *testing.T) {
	for i := 0; i < 100; i++ {
		ns := "test-namespace-" + string(rune('a'+i%26))
		idx := ShardOf(ns, 10)
		if idx < 0 || idx >= 10 {
			t.Errorf("ShardOf(%q) = %d, 超出 [0, 10) 范围", ns, idx)
		}
	}
}

func TestShardOf_ZeroShards(t *testing.T) {
	if got := ShardOf("any", 0); got != 0 {
		t.Errorf("totalShards=0 应返回 0，got %d", got)
	}
}

// ── QuotaPerPod 测试 ─────────────────────────────────────────────────────────

func TestQuotaPerPod(t *testing.T) {
	tests := []struct {
		shards, replicas, want int
		desc                   string
	}{
		{10, 3, 4, "N=10, R=3 → ceil(10/3)=4"},
		{10, 2, 5, "N=10, R=2 → ceil(10/2)=5"},
		{3, 3, 1, "N=3, R=3 → 每 pod 1 个"},
		{1, 3, 1, "N=1, R=3 → 至少 1"},
		{10, 1, 10, "单副本 → 全拿"},
		{0, 3, 1, "N=0 → 至少 1"},
		{10, 0, 10, "R=0 → 全拿（防除零）"},
		{20, 3, 7, "N=20, R=3 → ceil(20/3)=7"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if got := QuotaPerPod(tt.shards, tt.replicas); got != tt.want {
				t.Errorf("QuotaPerPod(%d, %d) = %d, want %d",
					tt.shards, tt.replicas, got, tt.want)
			}
		})
	}
}

// ── ShardSet 测试 ────────────────────────────────────────────────────────────

func TestShardSet_BasicOps(t *testing.T) {
	s := NewShardSet()

	if s.Size() != 0 {
		t.Errorf("初始 Size 应为 0，got %d", s.Size())
	}

	s.Add(3)
	s.Add(7)
	s.Add(1)

	if s.Size() != 3 {
		t.Errorf("加 3 个后 Size 应为 3，got %d", s.Size())
	}

	if !s.Contains(3) || !s.Contains(7) || !s.Contains(1) {
		t.Error("Contains 应返回 true")
	}
	if s.Contains(99) {
		t.Error("Contains 不存在的应返回 false")
	}

	got := s.List()
	if len(got) != 3 || got[0] != 1 || got[1] != 3 || got[2] != 7 {
		t.Errorf("List = %v, want [1 3 7]", got)
	}

	s.Remove(3)
	if s.Contains(3) {
		t.Error("Remove 后不应 Contains")
	}
	if s.Size() != 2 {
		t.Errorf("移除后 Size 应为 2，got %d", s.Size())
	}
}

func TestShardSet_OwnsNamespace(t *testing.T) {
	s := NewShardSet()
	const N = 10

	authShard := ShardOf("kp-auth-service", N)
	s.Add(authShard)

	if !s.OwnsNamespace("kp-auth-service", N) {
		t.Error("应拥有 kp-auth-service")
	}

	for _, candidate := range []string{
		"kp-gateway", "kp-other-service", "kp-not-mine",
		"kp-something-else",
	} {
		if ShardOf(candidate, N) != authShard {
			if s.OwnsNamespace(candidate, N) {
				t.Errorf("不应拥有 %s（不同 shard）", candidate)
			}
			return
		}
	}
	t.Skip("所有候选 ns 巧合都和 auth-service 同 shard，跳过反向验证")
}

func TestShardSet_Concurrent(t *testing.T) {
	s := NewShardSet()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.Add(idx)
				s.Contains(idx)
				s.Size()
				s.List()
				if j%2 == 0 {
					s.Remove(idx)
				}
			}
		}(i % 10)
	}
	wg.Wait()
	// 主要看 go test -race 不报数据竞争
}
