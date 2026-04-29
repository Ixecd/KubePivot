// internal/scheduler/coordinator.go

package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Ixecd/kubepivot/internal/sizing"
)

// coordinator 负责双 DP 协同求解。
// 当 BinPack 无法将所有 Pod 装入节点时，coordinator 对未分配 Pod 进行资源裁剪后重试。
// 迭代上限 5 次；裁剪策略为每次将 requestCap 下调 10%，并用 sizing 建议与 cap 的最小值更新 Pod 资源。
func (s *Scheduler) coordinate(ctx context.Context) (*SchedulingPlan, error) {
	pods, err := s.pods.ListAllPods(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	nodes, err := s.nodes.ListAllNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	// 第 0 次：不做裁剪，直接用原始请求装箱
	plan, err := BinPack(nodes, pods)
	if err != nil {
		return nil, err
	}
	if plan.Converged {
		return plan, nil
	}

	// 协同循环
	maxIterations := 5
	requestCap := make(map[string]ResourceRequest) // 记录每个 Pod 的资源上限

	for i := 1; i <= maxIterations; i++ {
		// 收集未分配 Pod
		assigned := make(map[string]bool)
		for key := range plan.PodAssignments {
			assigned[key] = true
		}
		unassigned := make([]*PodInfo, 0)
		for _, p := range pods {
			key := p.Namespace + "/" + p.Name
			if !assigned[key] {
				unassigned = append(unassigned, p)
			}
		}
		if len(unassigned) == 0 {
			plan.Converged = true
			return plan, nil
		}

		// 裁剪未分配 Pod
		for _, p := range unassigned {
			key := p.Namespace + "/" + p.Name

			cap, exists := requestCap[key]
			if !exists {
				cap = p.Requests
			}

			// 下调上限：每次削减 10%
			cap.CPU = int64(float64(cap.CPU) * 0.9)
			cap.Memory = int64(float64(cap.Memory) * 0.9)

			// 低于最低合理资源则跳过
			if cap.CPU < 50 || cap.Memory < 64*1024*1024 {
				slog.Warn("Pod 已达最低资源阈值，停止裁剪", "pod", key)
				continue
			}

			requestCap[key] = cap

			// 调用维度 B 重算，并用 cap 作为硬上限
			samples, err := s.metrics.QueryRange(ctx, "", "", time.Now().Add(-7*24*time.Hour), 15*time.Minute)
			if err != nil {
				return nil, fmt.Errorf("query metrics for %s: %w", key, err)
			}
			sug, err := s.sizing.Compute(ctx, samples, sizing.ProfileDefault)
			if err != nil {
				return nil, fmt.Errorf("compute sizing for %s: %w", key, err)
			}
			// 实际资源取 sizing 建议与上限的较小值
			p.Requests = ResourceRequest{
				CPU:    min(sug.RecommendedCPU, cap.CPU),
				Memory: min(sug.RecommendedMem, cap.Memory),
			}
		}

		// 用裁剪后的请求重新装箱
		plan, err = BinPack(nodes, pods)
		if err != nil {
			return nil, err
		}
		if plan.Converged {
			return plan, nil
		}
	}

	slog.Warn("调度未收敛：达到最大迭代次数", "maxIterations", maxIterations)
	return plan, nil
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
