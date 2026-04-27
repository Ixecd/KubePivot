package eventstream

import (
	"time"
)

// ProjectState 表示一个 namespace 对应项目的运行时状态。
//
// 由 v2.5 状态机驱动，本包不直接维护，由调用方传入。
//
// 三层架构的"状态机驱动"维度依据这个值。
type ProjectState string

const (
	// StateRunning 项目正在运行（Pod 正常，业务处理中）。
	// → 资源应升级到 Hot 层（高频读）。
	StateRunning ProjectState = "RUNNING"

	// StateValidating 项目正在 sandbox 验证中（v2.6 蓝绿/迁移）。
	// → 资源应在 Hot 层（reconcile 高频检查）。
	StateValidating ProjectState = "VALIDATING"

	// StateIdle 项目已部署但当前没有活跃操作。
	// → 资源在 Warm 层即可（保留 Skeleton 做漂移检测）。
	StateIdle ProjectState = "IDLE"

	// StateTerminated 项目已下线（卸载但保留历史档案）。
	// → 资源应降到 Cold 层（仅作为审计 / 回溯）。
	StateTerminated ProjectState = "TERMINATED"

	// StateUnknown 状态未知（项目未注册到 v2.5 状态机）。
	// → 默认 Warm 层（保守策略）。
	StateUnknown ProjectState = ""
)

// CachePolicy 定义 Cache 三层升降级判定规则。
//
// 双驱动并集（Q2=C 拍板）：
//   - 状态机驱动：根据 ProjectState 升级（业务语义）
//   - 访问频率驱动：根据 access count / 时间窗口（实际热度）
//   - 任一条件成立 → 升级到对应层
//
// 默认值：
//   AccessWindow: 5min
//   AccessThreshold: 3 次
//   IdleWindow: 30min
//   WarmSizeThreshold: 1000 对象
type CachePolicy struct {
	// StateBasedPromotion 是否启用状态机驱动升级
	// 默认 true
	StateBasedPromotion bool

	// AccessBasedPromotion 是否启用访问频率驱动升级
	// 默认 true
	AccessBasedPromotion bool

	// AccessWindow 访问频率统计窗口
	// 默认 5min（最近 5min 内 ≥ AccessThreshold 次 → Hot）
	AccessWindow time.Duration

	// AccessThreshold 升级到 Hot 的访问阈值
	// 默认 3
	AccessThreshold uint32

	// IdleWindow 闲置降级窗口
	// 默认 30min（30min 没访问 → 降到 Cold）
	IdleWindow time.Duration

	// WarmSizeThreshold Warm 层最大对象数
	// 超过则触发 LRU 降级到 Cold
	// 默认 1000
	WarmSizeThreshold int
}

// DefaultCachePolicy 返回推荐的默认策略。
//
// 基于 v2.5 / v2.6 实际场景调优：
//   - 多数项目在 RUNNING 状态 → Hot
//   - sandbox 验证期 → Hot（10-30 分钟内频繁访问）
//   - IDLE 项目 → Warm（每分钟漂移检测一次）
//   - TERMINATED → Cold（很少访问，仅审计用）
func DefaultCachePolicy() CachePolicy {
	return CachePolicy{
		StateBasedPromotion:  true,
		AccessBasedPromotion: true,
		AccessWindow:         5 * time.Minute,
		AccessThreshold:      3,
		IdleWindow:           30 * time.Minute,
		WarmSizeThreshold:    1000,
	}
}

// Decide 决定一个 Resource 应该归属哪一层。
//
// 双驱动并集判定（Q2=C）：
//   1. 状态机驱动（StateBasedPromotion=true 时）：
//      RUNNING / VALIDATING → Hot
//      IDLE                  → Warm
//      TERMINATED            → Cold
//   2. 访问频率驱动（AccessBasedPromotion=true 时）：
//      AccessWindow 内 ≥ AccessThreshold → Hot
//      IdleWindow 内未访问 → Cold
//   3. 合并：
//      双驱动都有意见 → 取较热者（任一说重要则升级，并集语义）
//      仅一个有意见   → 直接采用
//      都禁用         → 默认 Warm
//
// 参数：
//   - r: 待评估的 Resource（提供 lastAccessed / accessCount）
//   - state: 该 Resource 所属项目的状态（由调用方提供）
//   - now: 当前时间（注入便于测试）
func (p *CachePolicy) Decide(r *Resource, state ProjectState, now time.Time) CacheLayer {
	if r == nil {
		return LayerWarm
	}

	// ─── 1. 状态机驱动 ─────────────────────────────────
	stateLayer, hasState := p.decideByState(state)

	// ─── 2. 访问频率驱动 ───────────────────────────────
	accessLayer, hasAccess := p.decideByAccess(r, now)

	// ─── 3. 合并 ───────────────────────────────────────
	switch {
	case hasState && hasAccess:
		// 双驱动都有意见 → 取较热者（并集语义）
		return mergeLayers(stateLayer, accessLayer)
	case hasState:
		return stateLayer
	case hasAccess:
		return accessLayer
	default:
		// 双驱动都禁用 → 默认 Warm
		return LayerWarm
	}
}

// decideByState 状态机驱动判定。
//
// 返回 (layer, ok)：
//   ok=false 表示该驱动未启用，不参与最终 merge
func (p *CachePolicy) decideByState(state ProjectState) (CacheLayer, bool) {
	if !p.StateBasedPromotion {
		return LayerWarm, false
	}
	switch state {
	case StateRunning, StateValidating:
		return LayerHot, true
	case StateIdle:
		return LayerWarm, true
	case StateTerminated:
		return LayerCold, true
	case StateUnknown:
		return LayerWarm, true
	default:
		// 未知 state 值（如 "INVALID"）→ 视为 Warm，保持启用语义
		return LayerWarm, true
	}
}

// decideByAccess 访问频率驱动判定。
//
// 返回 (layer, ok)：
//   ok=false 表示该驱动未启用，不参与最终 merge
func (p *CachePolicy) decideByAccess(r *Resource, now time.Time) (CacheLayer, bool) {
	if !p.AccessBasedPromotion {
		return LayerWarm, false
	}

	idleDuration := now.Sub(r.lastAccessed)

	if idleDuration > p.IdleWindow {
		// 长时间未访问 → Cold
		return LayerCold, true
	}
	if idleDuration <= p.AccessWindow && r.accessCount >= p.AccessThreshold {
		// 最近高频访问 → Hot
		return LayerHot, true
	}
	// 中间状态：访问过但不够热（或冷得不够）→ Warm
	return LayerWarm, true
}

// mergeLayers 合并两个层级判定结果。
//
// 双驱动并集语义：
//   - Hot 是特权信号（升级）：任一驱动说 Hot → Hot
//   - 否则取较冷者（保守降级）
//
// 规则表：
//   Hot  + Hot  = Hot
//   Hot  + Warm = Hot   (Hot 特权)
//   Hot  + Cold = Hot   (Hot 特权)
//   Warm + Warm = Warm
//   Warm + Cold = Cold  (无 Hot 信号 → 取较冷者)
//   Cold + Cold = Cold
//
// 工程意义：
//   Hot 资源消耗最高，必须有"明确理由"才升级
//   Cold 资源消耗最低，"无保活理由"就该降级
//   Warm 是中间态
//
// CacheLayer 数值定义：LayerHot=0, LayerWarm=1, LayerCold=2
func mergeLayers(a, b CacheLayer) CacheLayer {
	// Hot 特权：任一 Hot → Hot
	if a == LayerHot || b == LayerHot {
		return LayerHot
	}
	// 否则取较冷者（数值大者）
	if a > b {
		return a
	}
	return b
}

// ShouldDemote 判定一个 Resource 是否应该被降级。
//
// 用于 Cache 周期性 evict 任务（goroutine 每分钟扫一遍）：
//   - 当前在 Hot 但应在 Warm → 降级
//   - 当前在 Warm 但应在 Cold → 降级
//
// Hot → Warm 降级触发条件：
//   - 状态从 RUNNING/VALIDATING 转 IDLE
//   - 或：访问频率不再满足 AccessWindow 内 ≥ Threshold
//
// Warm → Cold 降级触发条件：
//   - 状态变为 TERMINATED
//   - 或：超过 IdleWindow 未访问
//   - 或：Warm 层超过 WarmSizeThreshold（LRU evict）
func (p *CachePolicy) ShouldDemote(r *Resource, state ProjectState, now time.Time) bool {
	if r == nil {
		return false
	}
	target := p.Decide(r, state, now)
	return target > r.cacheLayer // 数值变大 = 更冷
}

// ShouldPromote 判定一个 Resource 是否应该被升级。
//
// 用于 Cache 在访问时同步评估：
//   - 当前在 Cold 但应在 Warm → 升级（从 disk 加载）
//   - 当前在 Warm 但应在 Hot → 升级（反序列化对象到 hot snapshot）
func (p *CachePolicy) ShouldPromote(r *Resource, state ProjectState, now time.Time) bool {
	if r == nil {
		return false
	}
	target := p.Decide(r, state, now)
	return target < r.cacheLayer // 数值变小 = 更热
}

// MarkAccessed 在 Resource 被访问时更新 access 元数据。
//
// 由 Cache.Get 在 lock-free 读路径上调用。
//
// 注意：当前实现为 best-effort（非原子）。
// 多读者并发 access 同一 Resource 可能漏计数 1-2 次，对统计无显著影响。
// 如需精确原子计数，未来可改用 atomic.Uint32 + atomic.Pointer[time.Time]。
func MarkAccessed(r *Resource, now time.Time) {
	if r == nil {
		return
	}
	r.lastAccessed = now
	r.accessCount++ // best-effort，非原子
}

// SetLayer 显式设置 Resource 的当前层级。
//
// 用于 Cache 内部维护层级状态。
// 业务代码不应直接调用。
func SetLayer(r *Resource, layer CacheLayer) {
	if r == nil {
		return
	}
	r.cacheLayer = layer
}
