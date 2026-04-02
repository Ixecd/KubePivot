package controller

import (
	"context"
	"log/slog"
	"sync"
)

const queueCap = 256

// ReconcileQueue 三集合 WorkQueue
// 保证同一 key 不并发处理，处理期间的新事件不丢失
type ReconcileQueue struct {
	mu         sync.Mutex
	queue      []string          // 有序待处理列表
	dirty      map[string]struct{} // 已入队 or 处理中又来了新事件
	processing map[string]struct{} // 正在处理中
	cond       *sync.Cond
}

func NewReconcileQueue() *ReconcileQueue {
	q := &ReconcileQueue{
		dirty:      make(map[string]struct{}),
		processing: make(map[string]struct{}),
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Add 入队，三集合语义
func (q *ReconcileQueue) Add(key string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, inDirty := q.dirty[key]; inDirty {
		// 已在 dirty（待处理或处理中），不重复入队
		slog.Debug("WorkQueue 去重", "key", key)
		return
	}

	q.dirty[key] = struct{}{}

	if _, inProcessing := q.processing[key]; inProcessing {
		// 正在处理中，只加 dirty，等 Done 后重新入队
		slog.Debug("WorkQueue 处理中，暂存 dirty", "key", key)
		return
	}

	q.queue = append(q.queue, key)
	q.cond.Signal()
}

// get 取出一个 key 开始处理（阻塞直到有元素或 ctx 取消）
func (q *ReconcileQueue) get(ctx context.Context) (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for len(q.queue) == 0 {
		// 用 channel 监听 ctx，避免 cond.Wait 无法响应 ctx
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				q.cond.Signal() // 唤醒 Wait
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

// done 处理完成，如果 dirty 里还有则重新入队
func (q *ReconcileQueue) done(key string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	delete(q.processing, key)

	if _, inDirty := q.dirty[key]; inDirty {
		// 处理期间有新事件，重新入队
		slog.Debug("WorkQueue 处理完成，dirty 中有新事件，重新入队", "key", key)
		q.queue = append(q.queue, key)
		q.cond.Signal()
	}
}

// Run 启动 Worker
func (q *ReconcileQueue) Run(ctx context.Context, fn func(key string)) {
	go func() {
		for {
			key, ok := q.get(ctx)
			if !ok {
				return
			}
			fn(key)
			q.done(key)
		}
	}()
}
