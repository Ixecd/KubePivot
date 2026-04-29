// internal/scheduler/scheduler.go
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"
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
	defer func() {
		slog.Debug("乾枢调度完成", "elapsed", time.Since(start))
	}()

	// 1. 获取集群当前状态
	pods, err := s.pods.ListAllPods(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	if len(pods) == 0 {
		slog.Debug("集群无 Pod，跳过调度")
		return &SchedulingPlan{PodAssignments: map[string]string{}, Converged: true}, nil
	}

	nodes, err := s.nodes.ListAllNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("集群无可用节点")
	}
	// 2. 维度 A bin packing
	plan, err := BinPack(nodes, pods)
	if err != nil {
		return nil, fmt.Errorf("bin pack: %w", err)
	}

	// 3. 如果不收敛，记录日志但不阻断
	if !plan.Converged {
		assigned := len(plan.PodAssignments)
		total := len(pods)
		slog.Warn("调度未收敛：部分 Pod 未被分配",
			"assigned", assigned,
			"total", total,
			"unassigned", total-assigned)
	}

	return plan, nil
}
