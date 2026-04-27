package eventstream

import (
	"math"
	"math/rand"
	"time"
)

// ReconnectPolicy 控制 watch 断线后的重连退避策略。
//
// 设计目标（Q7=C）：
//   - 指数退避：避免短时间疯狂重连压垮 K8s API server
//   - jitter：避免大集群所有 informer 同时重连（雷鸣群效应）
//   - 上限：避免长时间不连，导致用户感知断流
//
// 重连退避序列示例（jitter=0.2）：
//
//	尝试 1: 1s   ± 0.2s  → [0.8s, 1.2s]
//	尝试 2: 2s   ± 0.4s  → [1.6s, 2.4s]
//	尝试 3: 4s   ± 0.8s  → [3.2s, 4.8s]
//	尝试 4: 8s   ± 1.6s  → [6.4s, 9.6s]
//	尝试 5: 16s  ± 3.2s  → [12.8s, 19.2s]
//	尝试 6+: 30s ± 6s    → [24s, 36s]   (封顶 + jitter)
type ReconnectPolicy struct {
	// InitialBackoff 第一次重连前等待时间。
	// 默认 1s。
	InitialBackoff time.Duration

	// MaxBackoff 退避时间上限。
	// 默认 30s。
	// 超过 MaxBackoff 后不再增长，但仍施加 jitter。
	MaxBackoff time.Duration

	// BackoffFactor 退避增长因子。
	// 默认 2.0（每次翻倍）。
	BackoffFactor float64

	// Jitter 抖动比例（0.0-1.0）。
	// 默认 0.2（±20%）。
	// 0 表示无 jitter（不推荐，会导致雷鸣群）。
	Jitter float64

	// MaxAttempts 最大重连次数。
	// 0 表示无限重试（推荐：watch 应该长期保活）。
	MaxAttempts int

	// rng 注入式随机源（便于测试 deterministic）。
	// 生产代码不需要设置（使用 math/rand 全局源）。
	rng *rand.Rand
}

// DefaultReconnectPolicy 返回推荐的默认配置。
//
// 基于 v2.5 / v2.6 实测调优：
//   - 初始 1s（k8s API server 短暂抖动通常 < 1s）
//   - 最大 30s（用户可感知但仍可接受）
//   - 因子 2.0（标准指数退避）
//   - jitter 0.2（行业经验值，client-go 也用此值）
//   - 无限重试（watch 应永远保活）
func DefaultReconnectPolicy() ReconnectPolicy {
	return ReconnectPolicy{
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
		Jitter:         0.2,
		MaxAttempts:    0, // 0 = 无限重试
	}
}

// NextBackoff 计算第 attempt 次重连前的等待时间。
//
// attempt 从 0 开始计数：
//   - attempt=0：第一次重连前等待 InitialBackoff（带 jitter）
//   - attempt=1：第二次重连前等待 InitialBackoff * BackoffFactor（带 jitter）
//   - ...
//   - 超过 MaxBackoff 后封顶（仍带 jitter）
//
// jitter 实现：
//
//	base ± (base * Jitter)
//	即对称分布于 base 周围
//
// 注意：返回值可能小于 0（jitter 范围太大），调用方应保护。
// 默认 jitter=0.2 不会出现负值。
func (p *ReconnectPolicy) NextBackoff(attempt int) time.Duration {
	// 1. 计算 base（指数增长，封顶 MaxBackoff）
	base := time.Duration(
		float64(p.InitialBackoff) * math.Pow(p.BackoffFactor, float64(attempt)),
	)
	if base > p.MaxBackoff {
		base = p.MaxBackoff
	}
	if base < 0 {
		// 极端情况：BackoffFactor^attempt 溢出
		base = p.MaxBackoff
	}

	// 2. 应用 jitter（无 jitter 时直接返回）
	if p.Jitter <= 0 {
		return base
	}

	jitterRange := float64(base) * p.Jitter
	r := p.randFloat() // [-1.0, 1.0]
	jitterAmount := time.Duration(r * jitterRange)

	return base + jitterAmount
}

// randFloat 返回 [-1.0, 1.0] 的随机浮点数。
//
// 优先用注入的 rng（测试场景 deterministic）。
// 否则用 math/rand 全局源（生产场景）。
func (p *ReconnectPolicy) randFloat() float64 {
	var f float64
	if p.rng != nil {
		f = p.rng.Float64()
	} else {
		f = rand.Float64() //nolint:gosec // 非加密用途
	}
	// rand.Float64() 返回 [0, 1)
	// 转换到 [-1, 1)：2 * f - 1
	return 2*f - 1
}

// ShouldGiveUp 判断是否已达到最大重连次数。
//
// MaxAttempts=0 时永不放弃（持续重试）。
func (p *ReconnectPolicy) ShouldGiveUp(attempt int) bool {
	if p.MaxAttempts == 0 {
		return false
	}
	return attempt >= p.MaxAttempts
}
