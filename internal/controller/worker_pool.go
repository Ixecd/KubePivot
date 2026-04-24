package controller

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

// ReconcileTask 一个待处理的 reconcile 任务
//
// 任务从 Watcher 层产生（namespace/configmap/resource 变化），
// 由 Leader 分发进 channel，Worker Pool 消费执行。
type ReconcileTask struct {
	// 任务元数据
	Project   string // 项目名（通常等于 namespace）
	Namespace string // K8s namespace
	Kind      string // 资源 Kind：Deployment/StatefulSet/ConfigMap/Namespace/...
	Name      string // 资源名
	Reason    string // 触发原因，用于日志（如 "configmap-changed", "resource-missing"）

	// 任务唯一标识，用于去重（可选）
	Key string

	// 入队时间，用于观测延迟
	EnqueuedAt time.Time
}

// TaskHandler 任务执行函数签名
//
// 实现者（通常是 GlobalReconciler）从任务中取出元数据，
// 决定走 healRecreate / healRollback / healScaleDown 等分支。
type TaskHandler func(ctx context.Context, task ReconcileTask) error

// WorkerPool 固定大小的 goroutine 池，消费 ReconcileTask channel
//
// 设计要点：
//   - 固定大小（默认 20），避免 Leader 被大量并发 goroutine 压垮
//   - channel buffer = poolSize × 4，允许短暂突发
//   - 每个任务独立 ctx + timeout，单任务失败不影响其他
//   - 优雅关闭：ctx.Done() → 停止接收新任务 → 等待 in-flight 任务完成
type WorkerPool struct {
	size    int
	tasks   chan ReconcileTask
	handler TaskHandler

	// 任务执行超时
	taskTimeout time.Duration

	// 观测
	enqueued uint64
	done     uint64
	failed   uint64
	mu       sync.Mutex

	wg sync.WaitGroup
}

// NewWorkerPool 构造 worker pool
//
// size 来源优先级：
//  1. KUBEPIVOT_WORKER_POOL_SIZE 环境变量（运行时覆盖）
//  2. 传入的 defaultSize 参数
//  3. 硬降级到 20（防止 0 或负值导致死锁）
func NewWorkerPool(defaultSize int, handler TaskHandler) *WorkerPool {
	size := defaultSize
	if envSize := getenv("KUBEPIVOT_WORKER_POOL_SIZE", ""); envSize != "" {
		if n, err := strconv.Atoi(envSize); err == nil && n > 0 {
			size = n
		}
	}
	if size <= 0 {
		size = 20
	}

	return &WorkerPool{
		size:        size,
		tasks:       make(chan ReconcileTask, size*4),
		handler:     handler,
		taskTimeout: 90 * time.Second,
	}
}

// Start 启动 worker 池。阻塞直到 ctx.Done()
func (p *WorkerPool) Start(ctx context.Context) {
	slog.Info("🧵 Worker Pool 启动", "size", p.size, "buffer", cap(p.tasks))

	for i := 0; i < p.size; i++ {
		p.wg.Add(1)
		go p.worker(ctx, i+1)
	}

	// 阻塞等待 ctx 关闭
	<-ctx.Done()

	// 优雅关闭：关闭 channel，worker 消费完剩余任务后退出
	close(p.tasks)
	p.wg.Wait()

	p.mu.Lock()
	defer p.mu.Unlock()
	slog.Info("🧵 Worker Pool 已退出",
		"enqueued", p.enqueued, "done", p.done, "failed", p.failed)
}

// Enqueue 投递一个任务到 channel。非阻塞：如果 channel 满则丢弃并告警
func (p *WorkerPool) Enqueue(task ReconcileTask) bool {
	if task.EnqueuedAt.IsZero() {
		task.EnqueuedAt = time.Now()
	}
	// 护栏：Protected namespace 在入队前就拒绝，避免消费侧浪费
	if IsProtectedNamespace(task.Namespace) {
		slog.Warn("🛡 拒绝对 protected namespace 入队",
			"namespace", task.Namespace, "kind", task.Kind, "name", task.Name)
		return false
	}

	select {
	case p.tasks <- task:
		p.mu.Lock()
		p.enqueued++
		p.mu.Unlock()
		return true
	default:
		slog.Warn("⚠️  Worker Pool channel 已满，丢弃任务",
			"namespace", task.Namespace, "kind", task.Kind, "name", task.Name)
		return false
	}
}

// worker 单个 worker goroutine
func (p *WorkerPool) worker(parentCtx context.Context, id int) {
	defer p.wg.Done()

	for task := range p.tasks {
		// 每个任务独立 timeout
		taskCtx, cancel := context.WithTimeout(parentCtx, p.taskTimeout)

		waited := time.Since(task.EnqueuedAt)
		slog.Debug("Worker 处理任务",
			"worker", id,
			"project", task.Project,
			"kind", task.Kind,
			"name", task.Name,
			"reason", task.Reason,
			"queue_wait_ms", waited.Milliseconds(),
		)

		err := p.safeHandle(taskCtx, task)
		cancel()

		p.mu.Lock()
		if err != nil {
			p.failed++
			slog.Error("任务执行失败",
				"worker", id,
				"project", task.Project,
				"kind", task.Kind, "name", task.Name,
				"err", err)
		} else {
			p.done++
		}
		p.mu.Unlock()
	}
}

// safeHandle 包一层 panic recovery，单任务崩溃不影响整个 worker
func (p *WorkerPool) safeHandle(ctx context.Context, task ReconcileTask) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return p.handler(ctx, task)
}

// Stats 返回当前统计（用于 kp controller status 等命令）
func (p *WorkerPool) Stats() (enqueued, done, failed uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.enqueued, p.done, p.failed
}
