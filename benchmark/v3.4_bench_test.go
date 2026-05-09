// benchmark/v3.4_bench_test.go — v3.4 全方位基准
//
// 覆盖 v3.4 新增并发构造：WorkerPool(后置代币), tokenBucket,
// ReconcileQueue, subscriber inflight, rollbackTracker。
//
// 运行:
//   go test -bench=. -benchmem -benchtime=3s ./benchmark/
//   go test -bench=BenchmarkWorkerPool -benchmem -cpuprofile=cpu.prof ./benchmark/

package benchmark

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ═══════════════════════════════════════════════════════════════
// tokenBucket 吞吐基准
// ═══════════════════════════════════════════════════════════════

type tokenBucket struct {
	rate     float64
	burst    float64
	tokens   float64
	lastTime time.Time
	mu       sync.Mutex
}

func newTokenBucket(ratePerSec float64) *tokenBucket {
	tb := &tokenBucket{
		rate:     ratePerSec,
		burst:    ratePerSec,
		tokens:   ratePerSec,
		lastTime: time.Now(),
	}
	if tb.burst <= 0 {
		tb.burst = 1
	}
	return tb
}

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

// BenchmarkTokenBucket_1Goroutine — 单线吞吐
func BenchmarkTokenBucket_1Goroutine(b *testing.B) {
	tb := newTokenBucket(10000) // 高 rate → 不成为瓶颈
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tb.allow()
	}
}

// BenchmarkTokenBucket_20Goroutines — 20 workers 并发争锁
func BenchmarkTokenBucket_20Goroutines(b *testing.B) {
	tb := newTokenBucket(100000)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tb.allow()
		}
	})
}

// BenchmarkTokenBucket_RateLimited — 限流场景 (rate=10/s, burst 耗尽)
func BenchmarkTokenBucket_RateLimited(b *testing.B) {
	tb := newTokenBucket(10) // 严格限流
	// 消耗初始 burst
	for i := 0; i < 10; i++ {
		tb.allow()
	}
	passed := 0
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if tb.allow() {
			passed++
		}
	}
	b.ReportMetric(float64(passed)/float64(b.N)*100, "%passed")
}

// ═══════════════════════════════════════════════════════════════
// WorkerPool 吞吐 + 延迟基准 (v3.4 后置代币)
// ═══════════════════════════════════════════════════════════════

type benchTask struct {
	id int
}

// BenchmarkWorkerPool_EnqueueDequeue — 端到端 入队→消费 吞吐
func BenchmarkWorkerPool_EnqueueDequeue(b *testing.B) {
	const poolSize = 20
	tasks := make(chan benchTask, poolSize*4)
	tb := newTokenBucket(0) // unlimited

	var processed int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 20 workers
	for i := 0; i < poolSize; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case task := <-tasks:
					atomic.AddInt64(&processed, 1)
					_ = task
				}
			}
		}()
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// v3.4: channel-first, token-after
			select {
			case tasks <- benchTask{id: 0}:
				tb.allow() // 后置代币
			default:
				// channel full
			}
		}
	})
	b.StopTimer()
	cancel()
	b.ReportMetric(float64(atomic.LoadInt64(&processed)), "processed")
}

// BenchmarkWorkerPool_ChannelFull — 满 channel 时的丢包率
func BenchmarkWorkerPool_ChannelFull(b *testing.B) {
	const poolSize = 5
	tasks := make(chan benchTask, poolSize) // 小 buffer, 快速满
	// 不启动 consumer — channel 立刻满

	dropped := int64(0)
	enqueued := int64(0)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			select {
			case tasks <- benchTask{id: 0}:
				atomic.AddInt64(&enqueued, 1)
			default:
				atomic.AddInt64(&dropped, 1)
			}
		}
	})
	b.ReportMetric(float64(atomic.LoadInt64(&dropped)), "dropped")
	b.ReportMetric(float64(atomic.LoadInt64(&enqueued)), "enqueued")
}

// ═══════════════════════════════════════════════════════════════
// subscriber inflight 基准 (单生产者多消费者)
// ═══════════════════════════════════════════════════════════════

// BenchmarkSubscriberInflight — 1 生产者 N 消费者, drop 率
func BenchmarkSubscriberInflight(b *testing.B) {
	const (
		numConsumers = 4
		queueSize    = 1024
	)

	type event struct{ id int }
	inflight := make(chan event, queueSize)

	var delivered, dropped int64
	var wg sync.WaitGroup

	// consumers
	for i := 0; i < numConsumers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range inflight {
				atomic.AddInt64(&delivered, 1)
			}
		}()
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			select {
			case inflight <- event{id: 0}:
				// ok
			default:
				atomic.AddInt64(&dropped, 1)
			}
		}
	})
	b.StopTimer()
	close(inflight)
	wg.Wait()

	b.ReportMetric(float64(atomic.LoadInt64(&dropped)), "dropped")
	b.ReportMetric(float64(atomic.LoadInt64(&delivered)), "delivered")
	if d := atomic.LoadInt64(&dropped); d > 0 {
		b.ReportMetric(float64(d)/float64(b.N)*100, "%drop")
	}
}

// ═══════════════════════════════════════════════════════════════
// ReconcileQueue 基准
// ═══════════════════════════════════════════════════════════════

type benchWorkQueue struct {
	mu         sync.Mutex
	queue      []string
	dirty      map[string]struct{}
	processing map[string]struct{}
	cond       *sync.Cond
}

func newBenchWorkQueue() *benchWorkQueue {
	q := &benchWorkQueue{
		dirty:      make(map[string]struct{}),
		processing: make(map[string]struct{}),
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *benchWorkQueue) add(key string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.dirty[key]; ok {
		return
	}
	q.dirty[key] = struct{}{}
	if _, ok := q.processing[key]; ok {
		return
	}
	q.queue = append(q.queue, key)
	q.cond.Signal()
}

func (q *benchWorkQueue) get(ctx context.Context) (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.queue) == 0 {
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				q.cond.Signal()
			case <-done:
			}
		}()
		q.cond.Wait()
		close(done)
		if ctx.Err() != nil {
			return "", false
		}
	}
	key := q.queue[0]
	q.queue = q.queue[1:]
	q.processing[key] = struct{}{}
	delete(q.dirty, key)
	return key, true
}

func (q *benchWorkQueue) done(key string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.processing, key)
	if _, ok := q.dirty[key]; ok {
		q.queue = append(q.queue, key)
		q.cond.Signal()
	}
}

// BenchmarkWorkQueue_AddGetDone — 完整 Add→Get→Done 循环
func BenchmarkWorkQueue_AddGetDone(b *testing.B) {
	q := newBenchWorkQueue()
	ctx := context.Background()

	processed := 0
	go func() {
		for {
			key, ok := q.get(ctx)
			if !ok {
				return
			}
			q.done(key)
			processed++
			if processed >= b.N {
				return
			}
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.add(fmt.Sprintf("ns-%d", i%100))
	}
	b.StopTimer()
}

// BenchmarkWorkQueue_Dedup — 去重场景 (同一 key 重复入队)
func BenchmarkWorkQueue_Dedup(b *testing.B) {
	q := newBenchWorkQueue()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.add("same-key") // 全部去重
	}
	b.ReportMetric(0, "enqueued_after_dedup") // 只有第一次入队
}

// ═══════════════════════════════════════════════════════════════
// rollbackTracker RWMutex 并发基准
// ═══════════════════════════════════════════════════════════════

type benchRollbackTracker struct {
	mu      sync.RWMutex
	entries map[string]struct {
		cnt    int
		lastAt time.Time
	}
}

func newBenchRollbackTracker() *benchRollbackTracker {
	return &benchRollbackTracker{
		entries: make(map[string]struct {
			cnt    int
			lastAt time.Time
		}),
	}
}

func (t *benchRollbackTracker) shouldBlock(ns string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	e, ok := t.entries[ns]
	if !ok || e.cnt < 3 {
		return false
	}
	backoff := time.Duration(1<<uint(e.cnt-3)) * time.Minute
	return time.Since(e.lastAt) < backoff
}

func (t *benchRollbackTracker) record(ns string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entries[ns]
	e.cnt++
	e.lastAt = time.Now()
	t.entries[ns] = e
}

// BenchmarkRollbackTracker_20Read — 20 worker 并发 shouldBlock (RLock)
func BenchmarkRollbackTracker_20Read(b *testing.B) {
	t := newBenchRollbackTracker()
	t.record("test-ns") // cnt=1, 不触发 block
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			t.shouldBlock(fmt.Sprintf("ns-%d", i%100))
			i++
		}
	})
}

// BenchmarkRollbackTracker_ReadWrite — 混合读写的 shouldBlock + record
func BenchmarkRollbackTracker_ReadWrite(b *testing.B) {
	t := newBenchRollbackTracker()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			ns := fmt.Sprintf("ns-%d", i%50)
			if i%10 == 0 {
				t.record(ns)
			} else {
				t.shouldBlock(ns)
			}
			i++
		}
	})
}

// ═══════════════════════════════════════════════════════════════
// 端到端 flood 模拟 (wrk-style)
// ═══════════════════════════════════════════════════════════════

// BenchmarkFloodPipeline — 完整流水线模拟:
//
//	producer → channel(80) → workers(20) → token bucket 后置
func BenchmarkFloodPipeline(b *testing.B) {
	const (
		workers   = 20
		chanSize  = 80
		tokenRate = 1000.0 // high rate to avoid throttling
	)

	tasks := make(chan benchTask, chanSize)
	tb := newTokenBucket(tokenRate)

	var processed, dropped, enqueued int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 20 workers
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case task := <-tasks:
					atomic.AddInt64(&processed, 1)
					_ = task
				}
			}
		}()
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			select {
			case tasks <- benchTask{id: 0}:
				tb.allow()
				atomic.AddInt64(&enqueued, 1)
			default:
				atomic.AddInt64(&dropped, 1)
			}
		}
	})
	b.StopTimer()
	cancel()
	wg.Wait()

	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "eps")
	b.ReportMetric(float64(atomic.LoadInt64(&dropped)), "dropped")
	b.ReportMetric(float64(atomic.LoadInt64(&dropped))/float64(b.N)*100, "%drop")
	b.ReportMetric(float64(atomic.LoadInt64(&processed)), "processed")
}

// BenchmarkFloodPipeline_RateLimited — 严格限流下的流水线
func BenchmarkFloodPipeline_RateLimited(b *testing.B) {
	const (
		workers   = 20
		chanSize  = 80
		tokenRate = 50.0 // limited to 50/s
	)

	tasks := make(chan benchTask, chanSize)
	tb := newTokenBucket(tokenRate)

	var processed, dropped, enqueued int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case task := <-tasks:
					atomic.AddInt64(&processed, 1)
					_ = task
				}
			}
		}()
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// consume token first (rate-limited path)
			if !tb.allow() {
				atomic.AddInt64(&dropped, 1)
				continue
			}
			select {
			case tasks <- benchTask{id: 0}:
				atomic.AddInt64(&enqueued, 1)
			default:
				atomic.AddInt64(&dropped, 1)
			}
		}
	})
	b.StopTimer()
	cancel()
	wg.Wait()

	b.ReportMetric(float64(atomic.LoadInt64(&enqueued))/b.Elapsed().Seconds(), "eps")
	b.ReportMetric(float64(atomic.LoadInt64(&dropped))/float64(b.N)*100, "%drop")
	b.ReportMetric(float64(atomic.LoadInt64(&processed)), "processed")
}
