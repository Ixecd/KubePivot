// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Ixecd/kubepivot/internal/metrics"
	"github.com/Ixecd/kubepivot/internal/planner"
	"github.com/Ixecd/kubepivot/internal/sizing"
	"gopkg.in/yaml.v3"
)

// runSizingHook 资源优化 sizing 挂钩 (部署前自动计算建议)
//
// 位置: Plan 已构建，但尚未执行任何 kubectl/helm 操作
// 目的: 早期失败 + 建议可审计 + 人类最终确认
//
// 参数:
//   - cfg: 部署配置 (sizingMode/sizingProfile/sizingForce)
//   - plan: 部署计划列表 (用于筛选目标 Pod)
//   - root: 项目根目录 (用于定位 components.yaml)
//   - kubeconfig: K8s 配置路径 (用于 metrics 采样)
//   - namespace: 目标 namespace
//
// Level1: 仅支持命令行 flag 触发，简化单 Pod 优化
// Level2: 支持 components.yaml 的 sizing 字段 + 全量 Pod 优化
func runSizingHook(cfg *deployConfig, plan []planner.Plan, root, kubeconfig, namespace string) {
	// 1. 检查是否启用 auto 模式
	if cfg.sizingMode != "auto" {
		return
	}

	P.Start("📊", "Running resource sizing optimization")

	// 2. 收集待优化 Pod (简化: 只优化第一个有镜像 + 资源的 Plan)
	var targetPlan *planner.Plan
	for i := range plan {
		if plan[i].Image != "" && plan[i].CPU != "" && plan[i].Memory != "" {
			targetPlan = &plan[i]
			break
		}
	}
	if targetPlan == nil {
		P.Info("⚠", "No eligible Pod for sizing (missing image/cpu/memory)")
		return
	}

	// 3. 采样历史指标 (复用 sizing 包逻辑)
	client := metrics.NewKubectlMetricsClient(kubeconfig)
	sampleCount := 5  // Level1 硬编码，Level2 可从 flag 解析
	sampleInterval := 2 * time.Second
	samples, err := samplePodMetrics(context.Background(), client, namespace, targetPlan.Name, sampleCount, sampleInterval)
	if err != nil {
		P.Info("⚠", fmt.Sprintf("Sizing sample failed: %v (continuing deploy)", err))
		return
	}

	// 4. 执行 DP 计算
	profile := sizing.Profile(cfg.sizingProfile)
	sug, err := sizing.Compute(context.Background(), samples, profile)
	if err != nil {
		P.Info("⚠", fmt.Sprintf("Sizing compute failed: %v (continuing deploy)", err))
		return
	}

	// 5. 对比历史均值，判断是否显著差异 (>20%)
	avgCPU := int64(0)
	avgMem := int64(0)
	for _, s := range samples {
		avgCPU += s.TotalCPU.Value
		avgMem += s.TotalMemory.Value
	}
	avgCPU /= int64(len(samples))
	avgMem /= int64(len(samples))

	diffCPU := math.Abs(float64(sug.RecommendedCPU-avgCPU)) / math.Max(1, float64(avgCPU))
	diffMem := math.Abs(float64(sug.RecommendedMem-avgMem)) / math.Max(1, float64(avgMem))
	if diffCPU < 0.2 && diffMem < 0.2 {
		P.Info("✓", "Current resources already optimal (diff < 20%)")
		return
	}

	// 6. 原地修改 components.yaml + 输出 git diff 提示
	componentsPath := filepath.Join(root, cfg.components)
	if err := updateComponentsSizingAtomic(componentsPath, targetPlan.Name, sug); err != nil {
		P.Fail("✗ Failed to update components.yaml")
		fmt.Fprintf(os.Stderr, "  Details: %v\n", err)
		if !cfg.sizingForce {
			osExitFunc(1)
		}
		return
	}

	P.Info("✓", "components.yaml updated with sizing suggestions")
	fmt.Fprintf(os.Stderr, "\n📢 Review changes: git diff %s\n", cfg.components)
	if !cfg.sizingForce {
		fmt.Fprintf(os.Stderr, "💡 To apply: git add %s && git commit -m 'sizing: optimize %s'\n", cfg.components, targetPlan.Name)
		fmt.Fprintf(os.Stderr, "💡 Or bypass: kp deploy --sizing-force\n")
		osExitFunc(0) // 成功但阻断，等待用户确认
	}
}

// samplePodMetrics 封装采样逻辑 (Level1 内联，Level2 提取到 internal/sizing/sample.go)
func samplePodMetrics(ctx context.Context, client metrics.MetricsClient, namespace, name string, count int, interval time.Duration) ([]*metrics.PodMetrics, error) {
	var samples []*metrics.PodMetrics
	for i := 0; i < count; i++ {
		m, err := client.GetPodMetrics(ctx, namespace, name)
		if err != nil {
			if len(samples) == 0 {
				return nil, fmt.Errorf("initial sample failed: %w", err)
			}
			// 已有样本则容忍单次失败
			break
		}
		samples = append(samples, m)
		if i < count-1 {
			select {
			case <-time.After(interval):
			case <-ctx.Done():
				return samples, ctx.Err()
			}
		}
	}
	// 按时间排序确保计算顺序一致
	sort.Slice(samples, func(i, j int) bool {
		return samples[i].Timestamp.Before(samples[j].Timestamp)
	})
	return samples, nil
}

// updateComponentsSizingAtomic 原子写入 components.yaml 的 sizing 建议
// 原理: 写临时文件 → 原子重命名 (避免写入中断导致文件损坏)
// Level1 实现，Level2 可升级备份 + 回滚
func updateComponentsSizingAtomic(path, podName string, sug *sizing.Suggestion) error {
	// 1. 读取原文件
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read components: %w", err)
	}

	// 2. 解析 YAML
	var raw struct {
		Components []planner.Component `yaml:"components"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse components: %w", err)
	}

	// 3. 修改目标组件
	found := false
	for i := range raw.Components {
		if raw.Components[i].Name == podName {
			raw.Components[i].CPU = fmt.Sprintf("%dm", sug.RecommendedCPU)
			raw.Components[i].Memory = fmt.Sprintf("%dMi", sug.RecommendedMem>>20) // bytes → MiB
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("component %q not found in components.yaml", podName)
	}

	// 4. 序列化
	newData, err := yaml.Marshal(&raw)
	if err != nil {
		return fmt.Errorf("marshal components: %w", err)
	}

	// 5. 原子写入: 临时文件 + rename
	// 原理: rename 在同一文件系统内是原子的，即使进程崩溃也不会留半写文件
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, newData, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		// 清理临时文件 (忽略错误，避免覆盖主错误)
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic rename: %w", err)
	}

	return nil
}