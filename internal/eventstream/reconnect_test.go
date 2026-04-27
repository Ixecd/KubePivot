package eventstream

import (
	"math/rand"
	"testing"
	"time"
)

// ─── DefaultReconnectPolicy 测试 ──────────────────────────────────

func TestDefaultReconnectPolicy(t *testing.T) {
	p := DefaultReconnectPolicy()

	if p.InitialBackoff != 1*time.Second {
		t.Errorf("InitialBackoff = %v, want 1s", p.InitialBackoff)
	}
	if p.MaxBackoff != 30*time.Second {
		t.Errorf("MaxBackoff = %v, want 30s", p.MaxBackoff)
	}
	if p.BackoffFactor != 2.0 {
		t.Errorf("BackoffFactor = %v, want 2.0", p.BackoffFactor)
	}
	if p.Jitter != 0.2 {
		t.Errorf("Jitter = %v, want 0.2", p.Jitter)
	}
	if p.MaxAttempts != 0 {
		t.Errorf("MaxAttempts = %d, want 0 (unlimited)", p.MaxAttempts)
	}
}

// ─── NextBackoff 增长序列测试（无 jitter）────────────────────────

func TestNextBackoff_NoJitter(t *testing.T) {
	p := ReconnectPolicy{
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
		Jitter:         0, // 无 jitter，可精确预期
	}

	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 1 * time.Second},   // 1
		{1, 2 * time.Second},   // 2
		{2, 4 * time.Second},   // 4
		{3, 8 * time.Second},   // 8
		{4, 16 * time.Second},  // 16
		{5, 30 * time.Second},  // 32 → 封顶 30
		{6, 30 * time.Second},  // 64 → 封顶 30
		{10, 30 * time.Second}, // 1024 → 封顶 30
	}

	for _, tt := range tests {
		got := p.NextBackoff(tt.attempt)
		if got != tt.want {
			t.Errorf("NextBackoff(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

// ─── NextBackoff 带 jitter 测试 ───────────────────────────────────

func TestNextBackoff_WithJitter(t *testing.T) {
	// deterministic rng（测试可重现）
	p := ReconnectPolicy{
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
		Jitter:         0.2,
		rng:            rand.New(rand.NewSource(42)), //nolint:gosec
	}

	// attempt=0：base=1s，jitter ±20% → [0.8s, 1.2s]
	// 用 100 次采样验证范围
	const samples = 100
	for i := 0; i < samples; i++ {
		got := p.NextBackoff(0)
		minBackoff := 800 * time.Millisecond
		maxBackoff := 1200 * time.Millisecond
		if got < minBackoff || got > maxBackoff {
			t.Errorf("attempt=0 sample %d: got %v, want in [%v, %v]",
				i, got, minBackoff, maxBackoff)
		}
	}
}

// ─── NextBackoff jitter 范围验证 ─────────────────────────────────

func TestNextBackoff_JitterRange(t *testing.T) {
	tests := []struct {
		name    string
		attempt int
		base    time.Duration
		jitter  float64
	}{
		{"attempt=0 base=1s jitter=0.2", 0, 1 * time.Second, 0.2},
		{"attempt=2 base=4s jitter=0.2", 2, 4 * time.Second, 0.2},
		{"attempt=10 capped jitter=0.2", 10, 30 * time.Second, 0.2},
		{"attempt=0 jitter=0.5", 0, 1 * time.Second, 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := ReconnectPolicy{
				InitialBackoff: 1 * time.Second,
				MaxBackoff:     30 * time.Second,
				BackoffFactor:  2.0,
				Jitter:         tt.jitter,
				rng:            rand.New(rand.NewSource(int64(tt.attempt) + 1)), //nolint:gosec
			}

			minBackoff := tt.base - time.Duration(float64(tt.base)*tt.jitter)
			maxBackoff := tt.base + time.Duration(float64(tt.base)*tt.jitter)

			// 100 次采样
			for i := 0; i < 100; i++ {
				got := p.NextBackoff(tt.attempt)
				if got < minBackoff || got > maxBackoff {
					t.Errorf("sample %d: got %v, want in [%v, %v]",
						i, got, minBackoff, maxBackoff)
				}
			}
		})
	}
}

// ─── NextBackoff 防雷鸣群测试 ─────────────────────────────────────

func TestNextBackoff_AvoidsThunderingHerd(t *testing.T) {
	// 模拟 100 个 informer 同时断线，应在不同时刻重连
	// 用真实随机源（非 deterministic），观察分散程度

	p := DefaultReconnectPolicy() // jitter=0.2

	const numInformers = 100
	values := make([]time.Duration, numInformers)
	for i := 0; i < numInformers; i++ {
		values[i] = p.NextBackoff(0)
	}

	// 验证：值应该有分散（不全相等）
	// 至少 90% 不同（jitter 0.2 在 1s base 上有 0.4s 范围，纳秒级差异多到一定程度）
	uniqueCount := 0
	seen := make(map[time.Duration]bool)
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			uniqueCount++
		}
	}
	if uniqueCount < numInformers*9/10 {
		t.Errorf("100 个 informer 重连时间不够分散：unique=%d/%d", uniqueCount, numInformers)
	}
}

// ─── NextBackoff 边界场景 ────────────────────────────────────────

func TestNextBackoff_ZeroJitter(t *testing.T) {
	p := ReconnectPolicy{
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
		Jitter:         0,
	}

	// 多次调用应返回完全相同的值（无 jitter）
	v1 := p.NextBackoff(2)
	v2 := p.NextBackoff(2)
	if v1 != v2 {
		t.Errorf("无 jitter 时多次调用应相同：v1=%v, v2=%v", v1, v2)
	}
}

func TestNextBackoff_NegativeAttempt(t *testing.T) {
	p := DefaultReconnectPolicy()
	// attempt=-1 不应 panic
	// math.Pow(2, -1) = 0.5
	// base = 0.5s
	got := p.NextBackoff(-1)

	// 应该返回 ~0.5s ± 0.1s
	minBackoff := 400 * time.Millisecond
	maxBackoff := 600 * time.Millisecond
	if got < minBackoff || got > maxBackoff {
		t.Errorf("NextBackoff(-1) = %v, want in [%v, %v]", got, minBackoff, maxBackoff)
	}
}

// ─── ShouldGiveUp 测试 ────────────────────────────────────────────

func TestShouldGiveUp_Unlimited(t *testing.T) {
	p := DefaultReconnectPolicy() // MaxAttempts=0

	if p.ShouldGiveUp(0) || p.ShouldGiveUp(1000000) {
		t.Error("MaxAttempts=0 应永不放弃")
	}
}

func TestShouldGiveUp_WithLimit(t *testing.T) {
	p := ReconnectPolicy{MaxAttempts: 5}

	tests := []struct {
		attempt int
		giveUp  bool
	}{
		{0, false},
		{4, false},
		{5, true},
		{100, true},
	}

	for _, tt := range tests {
		got := p.ShouldGiveUp(tt.attempt)
		if got != tt.giveUp {
			t.Errorf("attempt=%d: ShouldGiveUp() = %v, want %v",
				tt.attempt, got, tt.giveUp)
		}
	}
}
