package controller

import (
	"sync"
	"testing"
	"time"
)

func TestTokenBucket_Unlimited(t *testing.T) {
	tb := newTokenBucket(0)
	for i := 0; i < 1000; i++ {
		if !tb.allow() {
			t.Fatal("rate=0 应无限制")
		}
	}
}

func TestTokenBucket_RateLimit(t *testing.T) {
	tb := newTokenBucket(100) // 100 tokens/sec
	// 初始满桶，应该能立即通过 burst 个请求
	passed := 0
	for i := 0; i < 100; i++ {
		if tb.allow() {
			passed++
		}
	}
	if passed < 90 { // burst=100, 允许少量时间偏差
		t.Errorf("初始 burst 应 >=90, got %d", passed)
	}
}

func TestTokenBucket_Refill(t *testing.T) {
	tb := newTokenBucket(100) // 100 tokens/sec
	// 消耗全部令牌
	for i := 0; i < 100; i++ {
		tb.allow()
	}
	// 令牌桶空了
	if tb.allow() {
		t.Log("bucket 未完全空（可能因时间流逝有补充），继续...")
	}

	// 等 50ms → 应有 ~5 个令牌补充
	time.Sleep(50 * time.Millisecond)
	refilled := 0
	for i := 0; i < 10; i++ {
		if tb.allow() {
			refilled++
		}
	}
	if refilled < 3 || refilled > 8 {
		t.Errorf("50ms 后应有 3-8 个令牌，got %d", refilled)
	}
}

func TestTokenBucket_Concurrent(t *testing.T) {
	tb := newTokenBucket(1000) // high rate for concurrent test
	var wg sync.WaitGroup
	passed := make([]int32, 10)
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if tb.allow() {
					passed[idx]++
				}
			}
		}(g)
	}
	wg.Wait()
	total := int32(0)
	for _, p := range passed {
		total += p
	}
	if total < 900 {
		t.Errorf("10×100 并发调用, 通过 %d, 应 >=900", total)
	}
}
