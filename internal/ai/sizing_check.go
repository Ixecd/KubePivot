// internal/ai/sizing_check.go — v2.0 Phase 3: Sizing 反馈环
package ai

import (
	"strings"
)

// ─── Sizing 检查结果 ─────────────────────────────────────────────

// SizingCheck 对单个组件的 sizing 检查结果。
type SizingCheck struct {
	ComponentName string
	ColdStart     bool   // 是否冷启动（无历史数据）
	LLMValue      string // LLM 推荐值（如 "500m" / "auto"）
	SizingValue   string // Prometheus 实测推荐值（如 "120m"）
	Decision      string // 最终采用的资源值
	Confidence    float64
	Warnings      []string
	Source        string // sizing-refined / llm-estimated / oom-protected
}

// RunSizingCheck 对 ai-plan 生成的 AIPlan 执行 sizing 验证。
// v3.2: 骨架实现——冷启动回退到 LLM 值，热更新留 Prometheus 接入点。
// 冲突解决策略见设计文档 §三：LLM偏保守→用实测值，LLM偏激进→warn+用实测值。
func RunSizingCheck(plan *AIPlan, ctx *RepoContext) []SizingCheck {
	var results []SizingCheck

	for _, c := range plan.Components {
		check := SizingCheck{
			ComponentName: c.Name,
			LLMValue:      formatResource(c.CPU, c.Memory),
		}

		if ctx.ExistingPlan != "" && strings.Contains(ctx.ExistingPlan, c.Name) {
			// 热更新：服务已在集群运行，有历史数据
			// v3.2 TODO: 调 metrics.PrometheusClient.QueryRange() 获取 P95/P99
			// v3.2 TODO: 实现冲突解决表（LLM值 vs Sizing值）
			check.ColdStart = false
			check.SizingValue = check.LLMValue
			check.Decision = check.LLMValue
			check.Source = "llm-estimated"
			check.Warnings = append(check.Warnings,
				"已检测到现有配置，但 Prometheus 数据源未接入（v3.2 待实现）")
		} else {
			// 冷启动：无历史数据，保持 LLM 框架
			check.ColdStart = true
			check.SizingValue = ""
			check.Decision = check.LLMValue
			check.Source = "llm-estimated"
		}

		results = append(results, check)
	}

	return results
}

func formatResource(cpu, mem string) string {
	if cpu == "auto" && mem == "auto" {
		return "auto"
	}
	return cpu + " / " + mem
}
