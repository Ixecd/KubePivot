package controller

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestWorkQueue_Dedup(t *testing.T) {
	q := NewReconcileQueue()
	q.Add("reconcile")
	q.Add("reconcile") // 重复，应被忽略
	q.Add("reconcile")

	q.mu.Lock()
	qLen := len(q.queue)
	q.mu.Unlock()

	if qLen != 1 {
		t.Errorf("去重后队列长度应为 1，got %d", qLen)
	}
}

func TestWorkQueue_ProcessingDedup(t *testing.T) {
	// 处理中再来新事件 → 只加 dirty，不重复入 queue
	q := NewReconcileQueue()
	q.Add("reconcile")

	// 模拟 get（取出进入 processing）
	q.mu.Lock()
	key := q.queue[0]
	q.queue = q.queue[1:]
	q.processing[key] = struct{}{}
	delete(q.dirty, key)
	q.mu.Unlock()

	// 处理中再 Add
	q.Add("reconcile")

	q.mu.Lock()
	qLen := len(q.queue)
	_, inDirty := q.dirty["reconcile"]
	q.mu.Unlock()

	if qLen != 0 {
		t.Errorf("处理中时 queue 应为空，got %d", qLen)
	}
	if !inDirty {
		t.Error("处理中再 Add 应该在 dirty 里")
	}
}

func TestWorkQueue_DoneRequeue(t *testing.T) {
	// Done 后 dirty 里有事件 → 重新入队
	q := NewReconcileQueue()
	q.Add("reconcile")

	// 模拟 get
	q.mu.Lock()
	key := q.queue[0]
	q.queue = q.queue[1:]
	q.processing[key] = struct{}{}
	delete(q.dirty, key)
	q.mu.Unlock()

	// 处理中来新事件
	q.Add("reconcile")

	// Done
	q.done("reconcile")

	q.mu.Lock()
	qLen := len(q.queue)
	q.mu.Unlock()

	if qLen != 1 {
		t.Errorf("Done 后 dirty 有事件，queue 应重新入队为 1，got %d", qLen)
	}
}

func TestWorkQueue_Run_ProcessesAll(t *testing.T) {
	q := NewReconcileQueue()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var mu sync.Mutex
	processed := []string{}

	q.Add("a")
	q.Add("b")
	q.Add("a") // 去重

	q.Run(ctx, func(key string) {
		mu.Lock()
		processed = append(processed, key)
		mu.Unlock()
	})

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	got := len(processed)
	mu.Unlock()

	// a 和 b 各处理一次
	if got != 2 {
		t.Errorf("应处理 2 个唯一 key，got %d", got)
	}
}
