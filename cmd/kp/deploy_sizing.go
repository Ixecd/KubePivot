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
// Level2: 全量遍历 + Prometheus 历史查询 + 批量原子写入
func runSizingHook(cfg *deployConfig, plan []planner.Plan, projectRoot, kubeconfig, namespace string) {
	// 1. 检查是否启用 auto 模式
	if cfg.sizingMode != "auto" {
		return
	}

	// 2. 收集待优化 Pods (全量遍历)
	//    优先级: plan[i].Sizing.Mode > cfg.sizingMode > "manual"(默认)
	var targets []struct {
		plan    *planner.Plan
		mode    string
		profile string
	}
	for i := range plan {
		p := &plan[i]
		if p.Image == "" || p.CPU == "" || p.Memory == "" {
			continue // 跳过无资源定义的组件
		}
		// 解析配置优先级
		mode := cfg.sizingMode
		profile := cfg.sizingProfile
		if p.Sizing != nil {
			if p.Sizing.Mode != "" {
				mode = p.Sizing.Mode
			}
			if p.Sizing.Profile != "" {
				profile = p.Sizing.Profile
			}
		}
		if mode != "auto" {
			continue
		}
		targets = append(targets, struct {
			plan    *planner.Plan
			mode    string
			profile string
		}{plan: p, mode: mode, profile: profile})
	}
	if len(targets) == 0 {
		return // 无目标组件，静默返回
	}

	// 3. 执行优化 (串行遍历) + 收集建议
	updates := make(map[string]*sizing.Suggestion) // podName → suggestion
	for _, t := range targets {
		// 🔧 调用 computeSizingForPod (只计算，不写文件)
		// 参数: plan, promURL, namespace, kubeconfig, profile
		sug, err := computeSizingForPod(t.plan, cfg.prometheusURL, namespace, kubeconfig, sizing.Profile(t.profile))
		if err != nil {
			// 单 Pod 失败不中断整体
			// P.Warn("⚠", fmt.Sprintf("sizing failed for %s: %v", t.plan.Name, err))
			continue
		}
		if sug != nil {
			updates[t.plan.Name] = sug
		}
	}

	// 4. 批量原子写入 (避免多次读写竞争)
	if len(updates) > 0 {
		componentsPath := filepath.Join(projectRoot, cfg.components)
		if err := updateComponentsSizingBatch(componentsPath, updates); err != nil {
			P.Fail("✗ Failed to update components.yaml")
			fmt.Fprintf(os.Stderr, "  Details: %v\n", err)
			if !cfg.sizingForce {
				osExitFunc(1)
			}
			return
		}
		P.Info("✓", fmt.Sprintf("components.yaml updated for %d pods", len(updates)))
		fmt.Fprintf(os.Stderr, "\n📢 Review changes: git diff %s\n", cfg.components)
		if !cfg.sizingForce {
			fmt.Fprintf(os.Stderr, "💡 To apply: git add %s && git commit -m 'sizing: optimize %d pods'\n", cfg.components, len(updates))
			fmt.Fprintf(os.Stderr, "💡 Or bypass: kp deploy --sizing-force\n")
			osExitFunc(0) // 成功但阻断，等待用户确认
		}
	}
}

// computeSizingForPod 计算单个 Pod 的 sizing 建议 (不含文件写入)
// 返回: *sizing.Suggestion (nil = 无需优化) 或 error
func computeSizingForPod(plan *planner.Plan, promURL, namespace, kubeconfig string, profile sizing.Profile) (*sizing.Suggestion, error) {
	P.Start("📊", fmt.Sprintf("Optimizing %s/%s", namespace, plan.Name))

	// 1. 尝试 Prometheus 历史查询 (Level2 新增)
	var samples []*metrics.PodMetrics
	var err error

	if promURL != "" {
		client := metrics.NewPrometheusClient(promURL)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// CPU 查询: rate(container_cpu_usage_seconds_total{pod="xxx",namespace="yyy"}[5m])
		cpuQuery := fmt.Sprintf(`rate(container_cpu_usage_seconds_total{pod="%s",namespace="%s"}[5m])`, plan.Name, namespace)
		// Memory 查询: container_memory_working_set_bytes{pod="xxx",namespace="yyy"}
		memQuery := fmt.Sprintf(`container_memory_working_set_bytes{pod="%s",namespace="%s"}`, plan.Name, namespace)
		
		// QueryRange 返回 []*PodMetrics (对齐既有接口)
		// 时间范围: 最近 7 天，步长 15 分钟 (硬编码，Level3 可配置)
		start := time.Now().Add(-7 * 24 * time.Hour)
		end := time.Now()
		step := 15 * time.Minute
		
		promSamples, promErr := client.QueryRange(ctx, cpuQuery, memQuery, start, end, step)
		if promErr == nil && len(promSamples) > 0 {
			samples = promSamples // 直接用，单位已转换
		}
		// Prometheus 失败 → fallback 到瞬时采样 (下方逻辑)
	}

	// 2. Fallback: 瞬时指标采样 (复用既有逻辑)
	if len(samples) == 0 {
		client := metrics.NewKubectlMetricsClient(kubeconfig)
		samples, err = samplePodMetrics(context.Background(), client, namespace, plan.Name, 5, 2*time.Second)
		if err != nil {
			return nil, fmt.Errorf("fallback sampling failed: %w", err)
		}
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("no metrics data available for %s", plan.Name)
	}

	// 3. 执行 DP 计算
	sug, err := sizing.Compute(context.Background(), samples, profile)
	if err != nil {
		return nil, fmt.Errorf("sizing compute failed: %w", err)
	}

	// 4. 对比历史均值，判断是否显著差异 (>20%)
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
		P.Info("✓", fmt.Sprintf("%s already optimal (diff < 20%%)", plan.Name))
		return nil, nil // 无需优化
	}

	// 5. 返回建议 (由调用方决定是否写入)
	return sug, nil
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

// updateComponentsSizingBatch 批量更新 components.yaml (原子写入)
func updateComponentsSizingBatch(path string, updates map[string]*sizing.Suggestion) error {
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

	// 3. 批量修改
	for i := range raw.Components {
		if sug, ok := updates[raw.Components[i].Name]; ok {
			raw.Components[i].CPU = fmt.Sprintf("%dm", sug.RecommendedCPU)
			raw.Components[i].Memory = fmt.Sprintf("%dMi", sug.RecommendedMem>>20)
		}
	}

	// 4. 原子写入 (临时文件 + rename)
	newData, err := yaml.Marshal(&raw)
	if err != nil {
		return fmt.Errorf("marshal components: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, newData, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic rename: %w", err)
	}
	return nil
}