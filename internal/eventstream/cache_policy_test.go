package eventstream

import (
	"testing"
	"time"
)

// 固定 now 便于测试
var testNow = time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)

// makeAccessed 创建一个带 access 元数据的 Resource。
func makeAccessed(lastAccessAgo time.Duration, count uint32, currentLayer CacheLayer) *Resource {
	return &Resource{
		Namespace:    "default",
		Name:         "test",
		lastAccessed: testNow.Add(-lastAccessAgo),
		accessCount:  count,
		cacheLayer:   currentLayer,
	}
}

// ─── DefaultCachePolicy 测试 ──────────────────────────────────────

func TestDefaultCachePolicy(t *testing.T) {
	p := DefaultCachePolicy()
	if !p.StateBasedPromotion {
		t.Error("默认应启用状态机驱动")
	}
	if !p.AccessBasedPromotion {
		t.Error("默认应启用访问频率驱动")
	}
	if p.AccessWindow != 5*time.Minute {
		t.Errorf("AccessWindow = %v, want 5m", p.AccessWindow)
	}
	if p.AccessThreshold != 3 {
		t.Errorf("AccessThreshold = %d, want 3", p.AccessThreshold)
	}
	if p.IdleWindow != 30*time.Minute {
		t.Errorf("IdleWindow = %v, want 30m", p.IdleWindow)
	}
}

// ─── 状态机驱动测试 ───────────────────────────────────────────────

func TestDecide_StateBasedOnly(t *testing.T) {
	// 仅启用状态机驱动
	p := CachePolicy{
		StateBasedPromotion:  true,
		AccessBasedPromotion: false,
	}
	r := makeAccessed(time.Hour, 0, LayerWarm) // 访问元数据故意"陈旧"，证明只看状态

	tests := []struct {
		state ProjectState
		want  CacheLayer
	}{
		{StateRunning, LayerHot},
		{StateValidating, LayerHot},
		{StateIdle, LayerWarm},
		{StateTerminated, LayerCold},
		{StateUnknown, LayerWarm},
		{ProjectState("INVALID"), LayerWarm},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			got := p.Decide(r, tt.state, testNow)
			if got != tt.want {
				t.Errorf("state=%s: got %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

// ─── 访问频率驱动测试 ─────────────────────────────────────────────

func TestDecide_AccessBasedOnly(t *testing.T) {
	// 仅启用访问频率驱动
	p := CachePolicy{
		StateBasedPromotion:  false,
		AccessBasedPromotion: true,
		AccessWindow:         5 * time.Minute,
		AccessThreshold:      3,
		IdleWindow:           30 * time.Minute,
	}

	tests := []struct {
		name        string
		lastAgo     time.Duration
		accessCount uint32
		want        CacheLayer
	}{
		{"刚访问 + 高频 → Hot", 1 * time.Minute, 5, LayerHot},
		{"刚访问 + 边界频率 → Hot", 1 * time.Minute, 3, LayerHot},
		{"刚访问 + 低频 → Warm", 1 * time.Minute, 2, LayerWarm},
		{"中度闲置 → Warm", 10 * time.Minute, 5, LayerWarm},
		{"长期未访问 → Cold", 1 * time.Hour, 100, LayerCold},
		{"刚到 IdleWindow 边界 → Warm", 30 * time.Minute, 5, LayerWarm},
		{"刚过 IdleWindow → Cold", 31 * time.Minute, 5, LayerCold},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := makeAccessed(tt.lastAgo, tt.accessCount, LayerWarm)
			got := p.Decide(r, StateUnknown, testNow)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// ─── 双驱动并集测试（核心）────────────────────────────────────────

func TestDecide_BothDriversUnion(t *testing.T) {
	p := DefaultCachePolicy()

	tests := []struct {
		name        string
		state       ProjectState
		lastAgo     time.Duration
		accessCount uint32
		want        CacheLayer
		why         string
	}{
		{
			name:    "状态 Hot + 访问 Hot → Hot",
			state:   StateRunning, lastAgo: 1 * time.Minute, accessCount: 5,
			want: LayerHot,
			why:  "双驱动一致升级",
		},
		{
			name:    "状态 Hot + 访问 Cold → Hot（取并集，Hot 胜）",
			state:   StateRunning, lastAgo: 1 * time.Hour, accessCount: 0,
			want: LayerHot,
			why:  "状态机说重要，即使访问冷也升级",
		},
		{
			name:    "状态 Cold + 访问 Hot → Hot（取并集，Hot 胜）",
			state:   StateTerminated, lastAgo: 1 * time.Minute, accessCount: 5,
			want: LayerHot,
			why:  "实际访问高频，即使状态终结也升级（避免颠簸）",
		},
		{
			name:    "状态 Idle + 访问 Cold → Cold",
			state:   StateIdle, lastAgo: 1 * time.Hour, accessCount: 0,
			want: LayerCold,
			why:  "双驱动都冷",
		},
		{
			name:    "状态 Idle + 访问 Warm → Warm",
			state:   StateIdle, lastAgo: 10 * time.Minute, accessCount: 1,
			want: LayerWarm,
			why:  "正常 Warm 场景",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := makeAccessed(tt.lastAgo, tt.accessCount, LayerWarm)
			got := p.Decide(r, tt.state, testNow)
			if got != tt.want {
				t.Errorf("%s: got %v, want %v (%s)", tt.name, got, tt.want, tt.why)
			}
		})
	}
}

// ─── ShouldDemote / ShouldPromote 测试 ────────────────────────────

func TestShouldDemote(t *testing.T) {
	p := DefaultCachePolicy()

	// 当前 Hot，状态变 Idle + 长时间未访问 → 应降级
	r := makeAccessed(1*time.Hour, 0, LayerHot)
	if !p.ShouldDemote(r, StateIdle, testNow) {
		t.Error("当前 Hot 但应在 Cold，应降级")
	}

	// 当前 Hot，状态 Running → 不应降级
	r2 := makeAccessed(1*time.Hour, 0, LayerHot)
	if p.ShouldDemote(r2, StateRunning, testNow) {
		t.Error("状态 Running 应保留 Hot，不应降级")
	}
}

func TestShouldPromote(t *testing.T) {
	p := DefaultCachePolicy()

	// 当前 Cold，状态变 Running → 应升级
	r := makeAccessed(1*time.Hour, 0, LayerCold)
	if !p.ShouldPromote(r, StateRunning, testNow) {
		t.Error("状态 Running 应升级到 Hot")
	}

	// 当前 Warm，状态 Idle → 不应升级
	r2 := makeAccessed(10*time.Minute, 1, LayerWarm)
	if p.ShouldPromote(r2, StateIdle, testNow) {
		t.Error("状态 Idle 应保留 Warm，不应升级")
	}
}

// ─── 工具函数测试 ────────────────────────────────────────────────

func TestMergeLayers(t *testing.T) {
	tests := []struct {
		a, b CacheLayer
		want CacheLayer
	}{
		{LayerHot, LayerCold, LayerHot},   // Hot 特权
		{LayerCold, LayerHot, LayerHot},   // 顺序无关
		{LayerHot, LayerWarm, LayerHot},   // Hot 特权（Warm 也降不下 Hot）
		{LayerWarm, LayerCold, LayerCold}, // 无 Hot 信号 → 取较冷者
		{LayerCold, LayerWarm, LayerCold}, // 顺序无关
		{LayerHot, LayerHot, LayerHot},    // 同层
		{LayerWarm, LayerWarm, LayerWarm}, // 同层
		{LayerCold, LayerCold, LayerCold}, // 同层
	}

	for _, tt := range tests {
		got := mergeLayers(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("mergeLayers(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestMarkAccessed(t *testing.T) {
	r := &Resource{}
	t1 := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)

	MarkAccessed(r, t1)
	if r.lastAccessed != t1 {
		t.Errorf("lastAccessed 未更新")
	}
	if r.accessCount != 1 {
		t.Errorf("accessCount = %d, want 1", r.accessCount)
	}

	// 再访问一次
	t2 := t1.Add(1 * time.Minute)
	MarkAccessed(r, t2)
	if r.lastAccessed != t2 {
		t.Errorf("lastAccessed 未更新到 t2")
	}
	if r.accessCount != 2 {
		t.Errorf("accessCount = %d, want 2", r.accessCount)
	}
}

func TestMarkAccessed_Nil(t *testing.T) {
	// 不应 panic
	MarkAccessed(nil, testNow)
}

func TestSetLayer(t *testing.T) {
	r := &Resource{}
	SetLayer(r, LayerHot)
	if r.cacheLayer != LayerHot {
		t.Errorf("cacheLayer = %v, want Hot", r.cacheLayer)
	}
}

func TestSetLayer_Nil(t *testing.T) {
	SetLayer(nil, LayerHot) // 不应 panic
}

// ─── 极端场景测试 ────────────────────────────────────────────────

func TestDecide_NilResource(t *testing.T) {
	p := DefaultCachePolicy()
	got := p.Decide(nil, StateRunning, testNow)
	if got != LayerWarm {
		t.Errorf("nil Resource 应返回 LayerWarm（默认值），got %v", got)
	}
}

func TestDecide_BothDisabled(t *testing.T) {
	// 双驱动都禁用 → 始终返回默认 Warm
	p := CachePolicy{
		StateBasedPromotion:  false,
		AccessBasedPromotion: false,
	}
	r := makeAccessed(1*time.Minute, 100, LayerWarm)
	got := p.Decide(r, StateRunning, testNow)
	if got != LayerWarm {
		t.Errorf("双驱动禁用应返回 Warm，got %v", got)
	}
}
