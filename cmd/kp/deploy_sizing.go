// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Ixecd/kubepivot/internal/metrics"
	"github.com/Ixecd/kubepivot/internal/planner"
	"github.com/Ixecd/kubepivot/internal/sizing"
	"gopkg.in/yaml.v3"
)

// targetSpec 内部类型: 待优化目标 + 配置
type targetSpec struct {
	plan    *planner.Plan
	profile sizing.Profile
}

// runSizingHook 资源优化 sizing 挂钩 (B-Level3: 并发 + 灰度 + 动态配置)
func runSizingHook(cfg *deployConfig, plan []planner.Plan, projectRoot, kubeconfig, namespace string) {
	if cfg.sizingMode != "auto" {
		return
	}

	// 1. 收集目标 (全量遍历 + 配置解析)
	var targets []targetSpec
	for i := range plan {
		p := &plan[i]
		if p.Image == "" || p.CPU == "" || p.Memory == "" {
			continue
		}
		mode, profile := resolveSizingConfig(cfg, p)
		if mode != "auto" {
			continue
		}
		targets = append(targets, targetSpec{plan: p, profile: profile})
	}
	if len(targets) == 0 {
		return
	}

	P.Start("📊", fmt.Sprintf("Running sizing optimization for %d pods (concurrency=10)", len(targets)))

	// 2. 并发执行 + 错误收集 (B-Level3 核心)
	//    限流: 10 并发，保护 Prometheus + 避免集群过载
	//    策略: 软失败降级，硬失败跳过 + 记录
	var (
		wg       sync.WaitGroup
		sem      = make(chan struct{}, 10) // 信号量限流
		updates  = make(map[string]*sizing.Suggestion)
		mu       sync.Mutex // 保护 updates 并发写
		softErrs []string   // 软失败: 记录但不停止
		hardErrs []string   // 硬失败: 记录 + 可能阻断
	)

	// 解析动态参数 (YAML > flag > 默认)
	// 注意: 这里用硬编码默认值，实际应调用 planner.resolveSizingParams
	defaultWindow := 7 * 24 * time.Hour
	defaultStep := 15 * time.Minute
	defaultThreshold := 0.7
	window := cfg.prometheusWindow
	if window <= 0 {
		window = defaultWindow
	}
	step := cfg.prometheusStep
	if step <= 0 {
		step = defaultStep
	}
	threshold := cfg.sizingThreshold
	if threshold <= 0.0 {
		threshold = defaultThreshold
	}

	for _, t := range targets {
		wg.Add(1)
		go func(t targetSpec) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// 执行计算 (含 Prometheus + fallback)
			sug, err := computeSizingForPod(t.plan, cfg.prometheusURL, namespace, kubeconfig, t.profile, window, step)
			if err != nil {
				// 区分软/硬失败
				if isSoftFailure(err) {
					softErrs = append(softErrs, fmt.Sprintf("%s: %v", t.plan.Name, err))
					return
				}
				hardErrs = append(hardErrs, fmt.Sprintf("%s: %v", t.plan.Name, err))
				return
			}
			if sug != nil {
				// Confidence 灰度拦截 (Q-B.26)
				if sug.Confidence < threshold {
					softErrs = append(softErrs, fmt.Sprintf("%s: low confidence (%.2f < %.2f), skipped auto-apply", t.plan.Name, sug.Confidence, threshold))
					return
				}
				mu.Lock()
				updates[t.plan.Name] = sug
				mu.Unlock()
			}
		}(t)
	}
	wg.Wait()

	// 3. 批量原子写入 (避免多次读写竞争)
	//    注意: 即使部分失败，已成功的更新仍应写入 (软失败降级原则)
	//    原子性由 updateComponentsSizingBatch 保障 (临时文件 + rename)
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
		P.Info("✓", fmt.Sprintf("components.yaml updated for %d/%d pods", len(updates), len(targets)))
	}

	// 4. 结果汇总 + 分级报告
	//    对齐既有 logger 风格: P.Info/P.Warn/P.Fail + fmt.Fprintf(os.Stderr)
	reportSizingResults(len(targets), len(updates), softErrs, hardErrs, cfg.sizingForce)
}

// computeSizingForPod 计算单个 Pod 的 sizing 建议 (B-Level3: 支持动态参数)
// 参数: window/step 用于 Prometheus 查询，fallback 瞬时采样参数硬编码
func computeSizingForPod(plan *planner.Plan, promURL, namespace, kubeconfig string, profile sizing.Profile, window, step time.Duration) (*sizing.Suggestion, error) {
	var samples []*metrics.PodMetrics
	var err error

	// 1. 尝试 Prometheus 历史查询
	if promURL != "" {
		client := metrics.NewPrometheusClient(promURL)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// CPU 查询: rate(container_cpu_usage_seconds_total{pod="xxx",namespace="yyy"}[5m])
		cpuQuery := fmt.Sprintf(`rate(container_cpu_usage_seconds_total{pod="%s",namespace="%s"}[5m])`, plan.Name, namespace)
		// Memory 查询: container_memory_working_set_bytes{pod="xxx",namespace="yyy"}
		memQuery := fmt.Sprintf(`container_memory_working_set_bytes{pod="%s",namespace="%s"}`, plan.Name, namespace)

		// QueryRange 返回 []*PodMetrics (对齐既有接口)
		// 时间范围: 最近 window，步长 step (动态参数)
		start := time.Now().Add(-window)
		end := time.Now()

		promSamples, promErr := client.QueryRange(ctx, cpuQuery, memQuery, start, end, step)
		if promErr == nil && len(promSamples) > 0 {
			samples = promSamples // 直接用，单位已转换
		}
		// Prometheus 失败 → fallback 到瞬时采样 (下方逻辑)
	}

	// 2. Fallback: 瞬时指标采样 (复用既有逻辑)
	//    参数硬编码: 5 次采样，间隔 2s (Level1 约定)
	//    Level4 可扩展: 从 SizingConfig 读取 samples/interval
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
	//    注意: sizing.Compute 内部已计算 Confidence，这里只透传
	sug, err := sizing.Compute(context.Background(), samples, profile)
	if err != nil {
		return nil, fmt.Errorf("sizing compute failed: %w", err)
	}

	// 4. 对比历史均值，判断是否显著差异 (>20%)
	//    注意: 这里用历史均值作为基准，实际可扩展: 对比用户当前配置
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
		// 无显著差异，返回 nil 表示无需优化
		// 调用方 (runSizingHook) 会跳过写入
		return nil, nil
	}

	// 5. 返回建议 (由调用方决定是否写入)
	//    注意: Confidence 已在 sizing.Compute 内计算，这里直接透传
	return sug, nil
}

// isSoftFailure 判断是否为"可降级"的软失败
// 软失败: 无历史数据/查询超时/空结果 → fallback 瞬时采样或跳过
// 硬失败: 认证错误/权限拒绝/解析错误 → 必须记录 + 可能阻断
func isSoftFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// 软失败关键词 (按需扩展)
	softKeywords := []string{
		"no data returned",
		"query timeout",
		"fallback sampling",
		"no metrics data",
		"initial sample failed",
	}
	for _, kw := range softKeywords {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

// reportSizingResults 分级报告结果 (对齐 logger 风格)
func reportSizingResults(total, success int, softErrs, hardErrs []string, force bool) {
	// 1. 成功统计
	if success > 0 {
		P.Info("✓", fmt.Sprintf("Sizing applied: %d/%d pods optimized", success, total))
	}

	// 2. 软失败警告 (warn 级别，不影响退出码)
	//    输出格式: "⚠ 3 pods had low-confidence suggestions (review manually)"
	if len(softErrs) > 0 {
		P.Info("⚠", fmt.Sprintf("%d pods had soft failures (review logs)", len(softErrs)))
		// 详细日志: 每行一个，便于 grep (用 slog.Debug 避免刷屏)
		for _, e := range softErrs {
			slog.Debug("sizing soft failure", "detail", e)
		}
	}

	// 3. 硬失败错误 (error 级别，可能阻断)
	//    输出格式: "✗ 2 pods failed sizing (auth/parse error)"
	if len(hardErrs) > 0 {
		P.Fail(fmt.Sprintf("✗ %d pods had hard failures", len(hardErrs)))
		for _, e := range hardErrs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		// 非 force 模式: 阻断部署，等待用户确认
		if !force {
			fmt.Fprintf(os.Stderr, "💡 Use --sizing-force to bypass hard failures (not recommended)\n")
			osExitFunc(1)
		}
	}
}

// resolveSizingConfig 解析单个 Pod 的 sizing 配置 (优先级: YAML > flag > default)
func resolveSizingConfig(cfg *deployConfig, plan *planner.Plan) (mode string, profile sizing.Profile) {
	mode = cfg.sizingMode
	profile = sizing.Profile(cfg.sizingProfile)
	if plan.Sizing != nil {
		if plan.Sizing.Mode != "" {
			mode = plan.Sizing.Mode
		}
		if plan.Sizing.Profile != "" {
			profile = sizing.Profile(plan.Sizing.Profile)
		}
	}
	return
}

// samplePodMetrics 封装采样逻辑 (复用既有，B-Level3 无变更)
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

// updateComponentsSizingBatch 批量更新 components.yaml (原子写入，B-Level3 无变更)
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
