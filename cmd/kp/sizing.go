// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/audit"
	"github.com/Ixecd/kubepivot/internal/metrics"
	"github.com/Ixecd/kubepivot/internal/rbac"
	"github.com/Ixecd/kubepivot/internal/sizing"
)

// runSizing 是 kp sizing 的入口 (dispatch 子命令)
func runSizing(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: kp sizing <subcommand>\nAvailable: recommend\n")
		osExitFunc(1)
	}

	switch args[0] {
	case "recommend":
		runSizingRecommend(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n", args[0])
		osExitFunc(1)
	}
}

// runSizingRecommend 实现 kp sizing recommend --pod=xxx --profile=web [--output=patch.yaml]
func runSizingRecommend(args []string) {
	// 1. 参数解析 (对齐 runDeploy/runScan 风格)
	flags := flag.NewFlagSet("recommend", flag.ExitOnError)
	podName := flags.String("pod", "", "Pod name (required)")
	namespace := flags.String("namespace", "default", "Kubernetes namespace")
	profileStr := flags.String("profile", "default", "Business profile: web/batch/db/default")
	output := flags.String("output", "", "Output patch to file (default: stdout)")
	samples := flags.Int("samples", 5, "Number of metric samples to collect (2-10)")
	interval := flags.Duration("interval", 2*time.Second, "Interval between samples")

	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "parse flags: %v\n", err)
		osExitFunc(1)
	}

	if *podName == "" {
		fmt.Fprintf(os.Stderr, "Error: --pod is required\n")
		fmt.Fprintf(os.Stderr, "Usage: kp sizing recommend --pod=xxx [options]\n")
		osExitFunc(1)
	}

	// 1.5 RBAC 检查
	mustCheck(audit.ResolveActor(), *namespace, rbac.PermSizing)

	// 2. 解析 profile
	profile := sizing.Profile(*profileStr)
	// 简单校验，非法值降级为 default
	// 实际可扩展: 枚举校验 + 错误提示

	// 3. 初始化 metrics client (复用既有模式)
	//    注意: kubeconfig 从 env 或 flag 获取，简化先用空字符串 (默认 ~/.kube/config)
	client := metrics.NewKubectlMetricsClient("")

	// 4. 采样历史指标 (模拟短期历史)
	//    用 samplePodMetrics 封装，避免在 CLI 层重复逻辑
	//    实际: 可提取到 internal/sizing/sample.go 供复用
	P.Start("📊", fmt.Sprintf("Collecting %d samples for %s/%s", *samples, *namespace, *podName))
	samplesData, err := samplePodMetrics(context.Background(), client, *namespace, *podName, *samples, *interval)
	if err != nil {
		P.Fail("✗ Failed to collect metrics")
		fmt.Fprintf(os.Stderr, "  Details: %v\n", err)
		osExitFunc(2)
	}
	P.Done(fmt.Sprintf("Collected %d samples", len(samplesData)))

	// 5. 执行 DP 计算
	//    注意: Compute 返回 Suggestion，包含推荐值 + 置信度 + 节省率
	//    当前配置 (Plan.CPU/Memory) 需从 components.yaml 解析，简化先传 0
	//    实际: 调用 planner.LoadComponents + 提取对应 Pod 的 CPU/Memory
	sug, err := sizing.Compute(context.Background(), samplesData, profile)
	if err != nil {
		P.Fail("✗ Sizing computation failed")
		fmt.Fprintf(os.Stderr, "  Details: %v\n", err)
		osExitFunc(2)
	}

	// 6. 输出结果
	//    格式: sizing-patch.yaml (Q4=B: patch/suggest, not auto-apply)
	patch := sug.ToPatch(*namespace, *podName)

	if *output != "" {
		// 写文件
		// 复用项目既有文件写入模式 (如 internal/scaffold/writeIfChanged)
		// 简化: 直接用 os.WriteFile
		if err := os.WriteFile(*output, []byte(patch), 0644); err != nil {
			P.Fail("✗ Failed to write patch file")
			fmt.Fprintf(os.Stderr, "  Details: %v\n", err)
			osExitFunc(2)
		}
		P.Info("✓", fmt.Sprintf("Sizing patch written to %s", *output))
	} else {
		// stdout
		fmt.Print(patch)
	}

	// 7. 置信度提示 (帮助用户决策)
	if sug.Confidence < 0.7 {
		P.Info("⚠", fmt.Sprintf("Low confidence (%.2f): consider collecting more samples or checking data quality", sug.Confidence))
	}
}

// // samplePodMetrics 封装采样逻辑 (复用 internal/sizing/dp.go 的私有函数)
// // 注意: 实际应提取到 internal/sizing/sample.go 供 dp.go + sizing.go 共用
// // Level1 先内联，避免跨包依赖复杂化
// func samplePodMetrics(ctx context.Context, client metrics.MetricsClient, namespace, name string, count int, interval time.Duration) ([]*metrics.PodMetrics, error) {
// 	var samples []*metrics.PodMetrics
// 	for i := 0; i < count; i++ {
// 		m, err := client.GetPodMetrics(ctx, namespace, name)
// 		if err != nil {
// 			if len(samples) == 0 {
// 				return nil, fmt.Errorf("initial sample failed: %w", err)
// 			}
// 			// 已有样本则容忍单次失败
// 			// P.Warn("⚠", fmt.Sprintf("sample %d/%d failed: %v", i+1, count, err))
// 			break
// 		}
// 		samples = append(samples, m)
// 		if i < count-1 {
// 			select {
// 			case <-time.After(interval):
// 			case <-ctx.Done():
// 				return samples, ctx.Err()
// 			}
// 		}
// 	}
// 	// 按时间排序
// 	// sort.Slice(samples, func(i, j int) bool { return samples[i].Timestamp.Before(samples[j].Timestamp) })
// 	return samples, nil
// }

// parseMemoryResource 解析 Plan.Memory string → bytes (int64)
// 复用 metrics.Quantity 解析逻辑，避免重复造轮子
// 支持: "512Mi", "1Gi", "256Ki", "1073741824" (bytes)
func parseMemoryResource(s string) (int64, error) {
	// 简化: 硬编码常见格式
	// 实际: 复用 k8s.io/apimachinery/pkg/api/resource.ParseQuantity
	if strings.HasSuffix(s, "Gi") {
		val, err := strconv.ParseInt(strings.TrimSuffix(s, "Gi"), 10, 64)
		if err != nil {
			return 0, err
		}
		return val * 1024 * 1024 * 1024, nil
	}
	if strings.HasSuffix(s, "Mi") {
		val, err := strconv.ParseInt(strings.TrimSuffix(s, "Mi"), 10, 64)
		if err != nil {
			return 0, err
		}
		return val * 1024 * 1024, nil
	}
	if strings.HasSuffix(s, "Ki") {
		val, err := strconv.ParseInt(strings.TrimSuffix(s, "Ki"), 10, 64)
		if err != nil {
			return 0, err
		}
		return val * 1024, nil
	}
	// 默认按 bytes 解析
	return strconv.ParseInt(s, 10, 64)
}
