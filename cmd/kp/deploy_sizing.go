// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package main

import (
	"context"
	"fmt"
	"log/slog"
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
		reasons  = make(map[string]string)
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
				reasons[t.plan.Name] = fmt.Sprintf("sizing optimized (profile=%s, confidence=%.2f)", sug.Profile, sug.Confidence)
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
		if err := updateComponentsSizingBatch(componentsPath, updates, reasons); err != nil {
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

	// 👇 Level5: 生成 VPA 建议 (只读模式，批量输出)
	// 策略: 仅当至少一个 Pod 优化成功时生成，避免空文件
	// 输出: <projectRoot>/configs/vpa-suggestion.yaml (覆盖模式，简化)
	// 注意: 不自动应用，用户需手动 `kubectl apply` 或忽略
	// -----------------------------------------------------------------
	if len(updates) > 0 {
		vpaPath := filepath.Join(projectRoot, "configs", "vpa-suggestion.yaml")
		// 简化: 只生成第一个成功 Pod 的 VPA 建议 (Level6 支持批量)
		var firstSug *sizing.Suggestion
		var firstName string
		for name, sug := range updates {
			firstSug = sug
			firstName = name
			break
		}
		if firstSug != nil {
			if err := sizing.WriteVPASuggestion(vpaPath, namespace, firstName, firstSug, "Off"); err != nil {
				slog.Warn("failed to write VPA suggestion", "err", err)
			} else {
				P.Info("📄", fmt.Sprintf("VPA suggestion written to %s (mode=Off, review before apply)", vpaPath))
			}
		}
	}
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

	// 3. B-Level4: 自动 Profile 推荐 (如果启用)
	//    注意: auto_profile 优先级低于显式 profile，避免覆盖用户意图
	//    即: 用户显式设 profile=web → 不用推荐；未设 + auto_profile=true → 推荐
	if plan.Sizing != nil && plan.Sizing.AutoProfile != nil && *plan.Sizing.AutoProfile {
		if plan.Sizing.Profile == "" {
			// 用户未显式设 profile，尝试自动推荐
			// sizing.RecommendProfile 是导出函数 (大写开头)，可跨包调用
			recProfile, conf, recReason := sizing.RecommendProfile(samples)
			if conf >= 0.6 {
				// 置信度足够高，采用推荐
				profile = recProfile
				// 记录日志 (可解释性)
				// P.Info("🤖", fmt.Sprintf("Auto-recommended profile for %s: %s (%s, confidence=%.2f)", plan.Name, recProfile, recReason, conf))
				_ = recReason // Level5: 注入注释到输出
			}
		}
	}

	// 4. 执行 DP 计算 (用最终确定的 profile)
	//    注意: sizing.Compute 内部用 weights(profile) 计算权重，我们无需干预
	//    Level5: 扩展 Compute 签名支持自定义权重 (weight_learning)
	sug, err := sizing.Compute(context.Background(), samples, profile)
	if err != nil {
		return nil, fmt.Errorf("sizing compute failed: %w", err)
	}

	// 5. 返回建议 (由调用方决定是否写入)
	//    注意: Confidence 已在 sizing.Compute 内计算，这里直接透传
	//    👇 Level5: 生成 VPA 建议 (只读模式，输出独立文件)
	//    注意: 不自动应用，保持 GitOps 原则 (建议可审计 + 人类最终确认)
	//    输出路径: <projectRoot>/configs/vpa-suggestion.yaml (可配置)
	//    简化: 先硬编码路径，Level6 支持命令行参数
	// vpaPath := filepath.Join(projectRoot, "configs", "vpa-suggestion.yaml")
	// if err := sizing.WriteVPASuggestion(vpaPath, namespace, plan.Name, sug, "Off"); err != nil {
	//     // 记录警告但不阻断: VPA 建议生成失败不影响 sizing 核心功能
	//     slog.Warn("failed to write VPA suggestion", "err", err, "pod", plan.Name)
	// }

	// 简化: 先不写入文件，只返回 sug，调用方在 runSizingHook 中统一处理
	// Level6: 支持 --vpa-output 参数 + 批量生成

	_ = namespace // 占位，后续集成时移除
	_ = plan.Name // 占位，后续集成时移除
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
	if profile == "" {
		profile = sizing.ProfileDefault // 👈 兜底: 空字符串 → ProfileDefault
	}
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

// updateComponentsSizingBatch 批量更新 components.yaml (原子写入 + v3 注释注入)
// Level5:
//   - 无需注释注入时走简单路径 (兼容 Level1-4 测试)
//   - 需要注释时用 yaml.Node API 精确控制
//   - 健壮导航: 能处理常见 YAML 结构变体
func updateComponentsSizingBatch(path string, updates map[string]*sizing.Suggestion, reasons map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read components: %w", err)
	}

	// Level5: 检测是否需要注释注入
	// 如果不需要，走简单路径 (兼容既有测试 + 性能更好)
	needsComment := false
	if reasons != nil {
		for name := range updates {
			if _, hasReason := reasons[name]; hasReason {
				needsComment = true
				break
			}
		}
	}

	if !needsComment {
		// 👇 简单路径: 解析 → 修改 → Marshal → 原子写入 (兼容 Level1-4)
		var raw struct {
			Components []planner.Component `yaml:"components"`
		}
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parse components: %w", err)
		}
		for i := range raw.Components {
			if sug, ok := updates[raw.Components[i].Name]; ok {
				raw.Components[i].CPU = fmt.Sprintf("%dm", sug.RecommendedCPU)
				raw.Components[i].Memory = fmt.Sprintf("%dMi", sug.RecommendedMem>>20)
			}
		}
		newData, err := yaml.Marshal(&raw)
		if err != nil {
			return fmt.Errorf("marshal components: %w", err)
		}
		// 原子写入
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

	// 👇 注释注入路径: 用 yaml.Node API 精确控制
	// 注意: 此路径对 YAML 结构要求较严格，仅当需要注释时使用
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse components: %w", err)
	}

	// 健壮导航: 尝试多种常见结构
	// 模式 1: root -> DocumentNode -> MappingNode -> "components" Key -> SequenceNode
	// 模式 2: root -> MappingNode (无显式 DocumentNode) -> "components" Key -> SequenceNode
	var compSeq *yaml.Node
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		// 变体 1: 有显式 DocumentNode
		compSeq = findComponentsSequence(root.Content[0])
	} else {
		// 变体 2: 无显式 DocumentNode，root 本身就是 MappingNode
		compSeq = findComponentsSequence(&root)
	}

	if compSeq == nil || compSeq.Kind != yaml.SequenceNode {
		// 兜底: 如果找不到，回退到简单路径 + 警告
		// 这是「为业务写测试」原则: 不因结构解析失败阻断核心功能
		// 注释注入失败 → 记录日志 + 继续用简单路径更新值
		// (实际生产应返回错误，但测试场景先保证核心功能)
		// 🔧 简化: 直接返回错误，让调用方决定如何处理
		return fmt.Errorf("components list not found: yaml structure may be non-standard")
	}

	// 遍历并修改/注入注释
	for _, compMap := range compSeq.Content {
		if compMap.Kind != yaml.MappingNode {
			continue
		}

		var nameNode, cpuNode, memNode *yaml.Node
		for i := 0; i < len(compMap.Content); i += 2 {
			key := compMap.Content[i]
			val := compMap.Content[i+1]
			switch key.Value {
			case "name":
				nameNode = val
			case "cpu":
				cpuNode = val
			case "memory":
				memNode = val
			}
		}

		if nameNode == nil {
			continue
		}
		sug, ok := updates[nameNode.Value]
		if !ok {
			continue
		}

		// 更新值
		if cpuNode != nil {
			cpuNode.Value = fmt.Sprintf("%dm", sug.RecommendedCPU)
		}
		if memNode != nil {
			memNode.Value = fmt.Sprintf("%dMi", sug.RecommendedMem>>20)
		}

		// 注入注释 (Level5: 只注入一次，避免重复追加)
		if reason, hasReason := reasons[nameNode.Value]; hasReason {
			comment := fmt.Sprintf("# Reason: %s", reason)
			if cpuNode != nil {
				cpuNode.LineComment = comment
			}
			if memNode != nil {
				memNode.LineComment = comment
			}
		}
	}

	// 序列化回写
	out, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("marshal components: %w", err)
	}

	// 原子写入
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic rename: %w", err)
	}
	return nil
}

// findComponentsSequence 辅助函数: 在 MappingNode 中查找 "components" 对应的 SequenceNode
// 健壮实现: 能处理不同 YAML 解析结果的结构变体
func findComponentsSequence(node *yaml.Node) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		val := node.Content[i+1]
		if key.Value == "components" && val.Kind == yaml.SequenceNode {
			return val
		}
	}
	return nil
}

// getWeights 内联权重计算 (临时方案，避免跨包调用未导出的 weights 函数)
// 逻辑必须与 internal/sizing/dp.go 的 weights 函数完全同步
// Level5 重构: 统一导出或提取公共包
func getWeights(p sizing.Profile) (float64, float64) {
	switch p {
	case sizing.ProfileWeb:
		return 0.7, 0.3
	case sizing.ProfileBatch, sizing.ProfileDB:
		return 0.3, 0.7
	default:
		return 0.5, 0.5
	}
}
