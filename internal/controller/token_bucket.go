package controller

import (
	"sync"
	"time"
)

// tokenBucket 简单令牌桶限流器。
//
// 0 外部依赖。用于 WorkerPool 入队速率保护。
// 令牌以固定速率（tokens/sec）补充，最大容量 = burst。
// burst <= 0 时默认 = rate（即允许 1s 突发）。
type tokenBucket struct {
	rate     float64   // 令牌补充速率（tokens/sec）
	burst    float64   // 最大令牌容量
	tokens   float64   // 当前令牌数
	lastTime time.Time // 上次补充时间
	mu       sync.Mutex
}

// newTokenBucket 创建令牌桶。
// rate: 令牌补充速率 (tokens/sec), <=0 表示无限制。
func newTokenBucket(ratePerSec float64) *tokenBucket {
	tb := &tokenBucket{
		rate:     ratePerSec,
		burst:    ratePerSec,
		tokens:   ratePerSec, // 初始满桶
		lastTime: time.Now(),
	}
	if tb.burst <= 0 {
		tb.burst = 1
	}
	return tb
}

// allow 尝试获取一个令牌。返回 true 表示允许通过。
// rate <= 0 时永远返回 true（无限流）。
func (tb *tokenBucket) allow() bool {
	if tb.rate <= 0 {
		return true
	}

	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastTime).Seconds()
	tb.tokens += elapsed * tb.rate
	if tb.tokens > tb.burst {
		tb.tokens = tb.burst
	}
	tb.lastTime = now

	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}
