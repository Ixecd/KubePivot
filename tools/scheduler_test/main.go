// tools/scheduler_test/main.go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Ixecd/kubepivot/internal/scheduler"
)

func main() {
	ctx := context.Background()

	// 使用 kubectl adapter（从真实集群拉数据）
	adapter := scheduler.NewKubectlAdapter("")

	// 构建调度器（仅维度 A，不需要 sizing）
	s := scheduler.NewScheduler(
		adapter, // PodLister
		adapter, // NodeLister
		nil,     // MetricsProvider（暂不需要）
		nil,     // SizingProvider（暂不需要）
		nil,     // PlanWriter（暂不需要）
	)

	// 执行调度
	plan, err := s.Schedule(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "调度失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== 调度结果 ===")
	fmt.Printf("收敛: %v\n", plan.Converged)
	fmt.Printf("分配数: %d\n", len(plan.PodAssignments))
	for key, node := range plan.PodAssignments {
		fmt.Printf("  %s → %s\n", key, node)
	}
}