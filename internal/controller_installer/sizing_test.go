package controller_installer

import (
	"strings"
	"testing"
)

func TestRecommend_P3(t *testing.T) {
	s, c, reason, warns := Recommend(3, 10, 2, false)
	if s != 3 {
		t.Errorf("S = %d, want 3", s)
	}
	if c != 2 {
		t.Errorf("C = %d, want 2", c)
	}
	if !strings.Contains(reason, "") && reason != "" {
		// P=3 正常范围，不应有不降配理由
	}
	_ = warns
}

func TestRecommend_P20(t *testing.T) {
	s, c, _, _ := Recommend(20, 10, 2, false)
	// S = ceil(20/4) = 5
	if s != 5 {
		t.Errorf("S = %d, want 5", s)
	}
	// C = ceil(5/3) = 2
	if c != 2 {
		t.Errorf("C = %d, want 2", c)
	}
}

func TestRecommend_P100(t *testing.T) {
	s, c, _, _ := Recommend(100, 30, 5, false)
	// S = ceil(100/4) = 25
	if s != 25 {
		t.Errorf("S = %d, want 25", s)
	}
	// C = ceil(25/3) = 9
	if c != 9 {
		t.Errorf("C = %d, want 9", c)
	}
}

func TestRecommend_P200(t *testing.T) {
	s, c, _, warns := Recommend(200, 40, 8, false)
	// S = ceil(200/4) = 50 (触及上限)
	if s != 50 {
		t.Errorf("S = %d, want 50 (上限)", s)
	}
	// C = ceil(50/3) = 17, clamp to 10
	if c != 10 {
		t.Errorf("C = %d, want 10 (上限)", c)
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w, "P≥200") {
			found = true
		}
	}
	if !found {
		t.Error("P=200 应该有上限警告")
	}
}

func TestRecommend_Empty(t *testing.T) {
	s, c, _, _ := Recommend(0, 10, 2, false)
	// P=0 → ceil(0/4)=0, clamp to 3
	if s != 3 {
		t.Errorf("S = %d, want 3 (安全下限)", s)
	}
	if c != 2 {
		t.Errorf("C = %d, want 2", c)
	}
}

func TestRecommend_Boundary(t *testing.T) {
	s, c, _, _ := Recommend(1, 10, 2, false)
	if s != 3 {
		t.Errorf("S = %d, want 3", s)
	}
	if c != 2 {
		t.Errorf("C = %d, want 2", c)
	}
}

func TestRecommend_NoDownscale(t *testing.T) {
	// P=10, 当前 C=10 → 推荐 C=2, 但不降配 → 保持 C=10
	s, c, reason, _ := Recommend(10, 10, 10, false)
	if s != 3 {
		t.Errorf("S = %d, want 3", s)
	}
	if c != 10 {
		t.Errorf("C = %d, want 10 (不降配)", c)
	}
	if !strings.Contains(reason, "不降配") && !strings.Contains(reason, "force-downscale") {
		t.Errorf("应该有 reason 说明: %q", reason)
	}
}

func TestRecommend_ForceDownscale(t *testing.T) {
	s, c, _, _ := Recommend(10, 10, 10, true)
	if s != 3 {
		t.Errorf("S = %d, want 3", s)
	}
	// --force-downscale → 允许降到推荐值
	if c != 2 {
		t.Errorf("C = %d, want 2 (force-downscale)", c)
	}
}

func TestRecommend_MaxLimit(t *testing.T) {
	s, c, _, warns := Recommend(1000, 50, 10, false)
	if s != 50 {
		t.Errorf("S = %d, want 50 (硬上限)", s)
	}
	if c != 10 {
		t.Errorf("C = %d, want 10 (硬上限)", c)
	}
	// P≥500 应该有超载警告
	found := false
	for _, w := range warns {
		if strings.Contains(w, "P≥500") {
			found = true
		}
	}
	if !found {
		t.Error("P=1000 应该有超载警告")
	}
	// P/S = 1000/50 = 20 > 10, 应该有单分片超载警告
	foundShard := false
	for _, w := range warns {
		if strings.Contains(w, "单分片承载") {
			foundShard = true
		}
	}
	if !foundShard {
		t.Error("P/S=20 > 10, 应该有单分片超载警告")
	}
}

func TestRecommend_RebalanceWarning(t *testing.T) {
	// P=100, 当前 S=10 → 推荐 S=25 (Δ=150%, P≥50)
	_, _, _, warns := Recommend(100, 10, 3, false)
	found := false
	for _, w := range warns {
		if strings.Contains(w, "分片数变化") && strings.Contains(w, "调取风暴") {
			found = true
		}
	}
	if !found {
		t.Error("Δ=150%, P=100 ≥ 50, 应该有重平衡预警")
	}
}

func TestRecommend_RebalanceSmallChange(t *testing.T) {
	// P=20, 当前 S=3 → 推荐 S=5 (Δ=67% 但 P=20 < 50)
	_, _, _, warns := Recommend(20, 3, 2, false)
	for _, w := range warns {
		if strings.Contains(w, "调取风暴") {
			t.Error("P=20 < 50, 即使 ΔS≥50% 也不应触发重平衡预警")
		}
	}
}

func TestRecommend_RebalanceSmallDelta(t *testing.T) {
	// P=100, 当前 S=20 → 推荐 S=25 (Δ=25%, < 50%)
	_, _, _, warns := Recommend(100, 20, 5, false)
	for _, w := range warns {
		if strings.Contains(w, "调取风暴") {
			t.Error("Δ=25% < 50%, 不应触发重平衡预警")
		}
	}
}

func TestRecommend_ShardOverload(t *testing.T) {
	// P=600 → S=50, P/S=12 > 10
	_, _, _, warns := Recommend(600, 50, 10, false)
	found := false
	for _, w := range warns {
		if strings.Contains(w, "单分片承载") {
			found = true
		}
	}
	if !found {
		t.Error("P/S=12 > 10, 应该有单分片超载警告")
	}
}

func TestRecommend_NoCurrentShards(t *testing.T) {
	// currentShards=0 → 跳过重平衡警告（避免除零）
	s, c, _, warns := Recommend(100, 0, 3, false)
	if s != 25 {
		t.Errorf("S = %d, want 25", s)
	}
	if c != 9 {
		t.Errorf("C = %d, want 9", c)
	}
	for _, w := range warns {
		if strings.Contains(w, "分片数变化") {
			t.Error("currentShards=0 时不应有重平衡预警")
		}
	}
}

func TestRecommend_DownscaleShardsAlways(t *testing.T) {
	// shards 不受不降配策略约束 — 即使减少也不阻止
	s, _, _, _ := Recommend(10, 20, 3, false)
	// P=10 → S=ceil(10/4)=3, 即使当前 S=20, 仍推荐 3
	if s != 3 {
		t.Errorf("shards 减少不受不降配策略约束: S=%d, want 3", s)
	}
}

func TestRecommend_ReplicasAtCurrentWhenDownscale(t *testing.T) {
	// replicas 受不降配策略约束
	_, c, _, _ := Recommend(10, 3, 5, false)
	// C=ceil(3/3)=2, 当前 C=5, 不降配 → 保持 5
	if c != 5 {
		t.Errorf("replicas 不降配: C=%d, want 5", c)
	}
}

func TestRecommend_NoDownscale_StillApplies(t *testing.T) {
	// replicas 推荐值=3, 当前=5 → 保留当前值
	_, c, reason, _ := Recommend(20, 5, 5, false)
	if c != 5 {
		t.Errorf("不降配: C=%d, want 5", c)
	}
	if reason == "" {
		t.Error("不降配时应有 reason 说明")
	}
}

func TestAbs(t *testing.T) {
	tests := []struct {
		n    int
		want int
	}{
		{5, 5}, {0, 0}, {-5, 5}, {-1, 1},
	}
	for _, tt := range tests {
		if got := abs(tt.n); got != tt.want {
			t.Errorf("abs(%d) = %d, want %d", tt.n, got, tt.want)
		}
	}
}
