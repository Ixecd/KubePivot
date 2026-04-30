// internal/scheduler/scheduler.go
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Scheduler 是乾枢调度器的顶层入口。
// 它持有所有依赖接口，不直接依赖任何 internal 包的具体实现。
type Scheduler struct {
	pods    PodLister
	nodes   NodeLister
	metrics MetricsProvider
	sizing  SizingProvider
	writer  PlanWriter
}

// NewScheduler 构造函数。
// 所有参数都是接口——调用方可以注入真实实现（kubectl adapter）或测试 mock。
func NewScheduler(
	pods PodLister,
	nodes NodeLister,
	metrics MetricsProvider,
	sizing SizingProvider,
	writer PlanWriter,
) *Scheduler {
	return &Scheduler{
		pods:    pods,
		nodes:   nodes,
		metrics: metrics,
		sizing:  sizing,
		writer:  writer,
	}
}

// Schedule 执行一次完整的调度决策。
// Phase 1 桩实现：将所有 Running Pod 分配到第一个可用节点。
// 后续 Level 2-3 将替换为维度 A bin packing + 双 DP 协同。
func (s *Scheduler) Schedule(ctx context.Context) (*SchedulingPlan, error) {
	start := time.Now()
	metrics := GetSchedulerMetrics()
	defer func() {
		metrics.IncIterations()
		metrics.SetSolveDuration(time.Since(start))
		slog.Debug("乾枢调度完成", "elapsed", time.Since(start))
	}()

	return s.coordinate(ctx)
}

func RegisterMetrics(reg prometheus.Registerer) {
	reg.MustRegister(GetSchedulerMetrics())
}
