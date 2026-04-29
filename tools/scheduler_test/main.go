// tools/scheduler_test/main.go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Ixecd/kubepivot/internal/scheduler"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "deploy" {
		certPEM, keyPEM, caPEM, err := scheduler.GenerateSelfSignedCert()
		if err != nil {
			fmt.Fprintf(os.Stderr, "生成证书失败: %v\n", err)
			os.Exit(1)
		}

		// 生成 Secret
		secretYAML, err := scheduler.GenerateCertSecretYAML(certPEM, keyPEM, "kubepivot-system")
		if err != nil {
			fmt.Fprintf(os.Stderr, "生成 Secret YAML 失败: %v\n", err)
			os.Exit(1)
		}

		// 生成 Webhook 配置
		webhookYAML, err := scheduler.GenerateWebhookYAML(scheduler.WebhookDeployConfig{
			ServiceName:      "kubepivot-controller",
			ServiceNamespace: "kubepivot-system",
			ServicePort:      443,
			CAPEM:            caPEM,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "生成 Webhook YAML 失败: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("---")
		fmt.Println(secretYAML)
		fmt.Println("---")
		fmt.Println(webhookYAML)
		return
	}

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
