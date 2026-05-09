package controller

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerPool_BasicDispatch(t *testing.T) {
	var processed int32
	handler := func(ctx context.Context, task ReconcileTask) error {
		atomic.AddInt32(&processed, 1)
		return nil
	}

	pool := NewWorkerPool(5, 0, handler)

	ctx, cancel := context.WithCancel(context.Background())
	go pool.Start(ctx)

	// 投 10 个任务
	for i := 0; i < 10; i++ {
		pool.Enqueue(ReconcileTask{
			Project:   "test-project",
			Namespace: "test-ns",
			Kind:      "Deployment",
			Name:      fmt.Sprintf("deploy-%d", i),
			Reason:    "test",
		})
	}

	// 等待处理
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&processed) == 10 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()

	if got := atomic.LoadInt32(&processed); got != 10 {
		t.Errorf("processed = %d, want 10", got)
	}

	enq, done, failed := pool.Stats()
	if enq != 10 || done != 10 || failed != 0 {
		t.Errorf("Stats: enqueued=%d done=%d failed=%d, want 10/10/0", enq, done, failed)
	}
}

func TestWorkerPool_RejectsProtectedNS(t *testing.T) {
	var processed int32
	handler := func(ctx context.Context, task ReconcileTask) error {
		atomic.AddInt32(&processed, 1)
		return nil
	}
	pool := NewWorkerPool(2, 0, handler)

	// 投递到黑名单 namespace
	if ok := pool.Enqueue(ReconcileTask{
		Namespace: "kube-system",
		Kind:      "Deployment",
		Name:      "bad",
	}); ok {
		t.Error("Enqueue 对 kube-system 不应成功")
	}

	enq, _, _ := pool.Stats()
	if enq != 0 {
		t.Errorf("enqueued = %d, want 0（protected ns 应被拒绝）", enq)
	}
}

func TestWorkerPool_PanicRecovery(t *testing.T) {
	var processed int32
	handler := func(ctx context.Context, task ReconcileTask) error {
		atomic.AddInt32(&processed, 1)
		if task.Name == "boom" {
			panic("intentional panic in test")
		}
		return nil
	}

	pool := NewWorkerPool(3, 0, handler)
	ctx, cancel := context.WithCancel(context.Background())
	go pool.Start(ctx)
	defer cancel()

	pool.Enqueue(ReconcileTask{Namespace: "ns", Name: "normal-1", Kind: "Deployment"})
	pool.Enqueue(ReconcileTask{Namespace: "ns", Name: "boom", Kind: "Deployment"})
	pool.Enqueue(ReconcileTask{Namespace: "ns", Name: "normal-2", Kind: "Deployment"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&processed) == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	_, done, failed := pool.Stats()
	if done != 2 || failed != 1 {
		t.Errorf("done=%d failed=%d, want 2/1（panic 计入 failed）", done, failed)
	}
}

func TestWorkerPool_SizeFromEnv(t *testing.T) {
	t.Setenv("KUBEPIVOT_WORKER_POOL_SIZE", "7")
	pool := NewWorkerPool(20, 0, func(ctx context.Context, t ReconcileTask) error { return nil })
	if pool.size != 7 {
		t.Errorf("pool.size = %d, want 7（env 应覆盖）", pool.size)
	}
}

func TestWorkerPool_SizeFallback(t *testing.T) {
	// 无效的 env 值
	t.Setenv("KUBEPIVOT_WORKER_POOL_SIZE", "not-a-number")
	pool := NewWorkerPool(15, 0, func(ctx context.Context, t ReconcileTask) error { return nil })
	if pool.size != 15 {
		t.Errorf("pool.size = %d, want 15（无效 env 应降级到 defaultSize）", pool.size)
	}

	// 负数降级到 20
	pool2 := NewWorkerPool(-1, 0, func(ctx context.Context, t ReconcileTask) error { return nil })
	if pool2.size != 20 {
		t.Errorf("pool.size = %d, want 20（负值 defaultSize 应降级到 20）", pool2.size)
	}
}
