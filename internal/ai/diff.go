// internal/ai/diff.go — v2.0 Phase 5: 生成 diff + Source 填充
package ai

import (
	"fmt"
	"strings"
)

// ─── Diff 展示 ────────────────────────────────────────────────────

// DiffResult 表示一个组件的变更。
type DiffResult struct {
	Component string
	Field     string
	OldValue  string
	NewValue  string
	Source    string // 变更来源
}

// DiffComponents 对比新旧 Plan，生成 diff 列表。
func DiffComponents(oldPlan, newPlan *AIPlan) []DiffResult {
	var diffs []DiffResult

	oldMap := make(map[string]*AIComponent)
	if oldPlan != nil {
		for i := range oldPlan.Components {
			c := &oldPlan.Components[i]
			oldMap[c.Name] = c
		}
	}

	for _, newC := range newPlan.Components {
		oldC, exists := oldMap[newC.Name]

		if !exists {
			diffs = append(diffs, DiffResult{
				Component: newC.Name,
				Field:     "component",
				OldValue:  "-",
				NewValue:  "new",
				Source:    newC.Source,
			})
			continue
		}

		// 逐字段对比
		addDiff := func(field, oldV, newV string) {
			if oldV != newV {
				diffs = append(diffs, DiffResult{
					Component: newC.Name, Field: field,
					OldValue: oldV, NewValue: newV, Source: newC.Source,
				})
			}
		}
		addDiff("cpu", oldC.CPU, newC.CPU)
		addDiff("memory", oldC.Memory, newC.Memory)
		addDiff("replicas", fmt.Sprintf("%d", oldC.Replicas), fmt.Sprintf("%d", newC.Replicas))
		addDiff("profile", oldC.Profile, newC.Profile)
		addDiff("storage", oldC.Storage, newC.Storage)
	}

	// 删除的组件
	if oldPlan != nil {
		newMap := make(map[string]bool)
		for _, c := range newPlan.Components {
			newMap[c.Name] = true
		}
		for _, oldC := range oldPlan.Components {
			if !newMap[oldC.Name] {
				diffs = append(diffs, DiffResult{
					Component: oldC.Name,
					Field:     "component",
					OldValue:  "exists",
					NewValue:  "-",
					Source:    "removed",
				})
			}
		}
	}

	return diffs
}

// FormatDiff 格式化 diff 列表为可读输出。
func FormatDiff(diffs []DiffResult) string {
	if len(diffs) == 0 {
		return "  （无变更）"
	}

	var sb strings.Builder
	for _, d := range diffs {
		switch {
		case d.Field == "component" && d.OldValue == "-":
			sb.WriteString(fmt.Sprintf("  + %-20s (new component)\n", d.Component))
		case d.Field == "component" && d.NewValue == "-":
			sb.WriteString(fmt.Sprintf("  - %-20s (removed)\n", d.Component))
		default:
			source := ""
			if d.Source != "" && d.Source != "llm-estimated" {
				source = fmt.Sprintf(" [%s]", d.Source)
			}
			sb.WriteString(fmt.Sprintf("  ~ %-20s %-8s %s → %s%s\n",
				d.Component, d.Field, d.OldValue, d.NewValue, source))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// PopulateSources 为 Plan 组件填充 Source 标记。
// 在 sizing check + GPU detect 之后调用。
func PopulateSources(plan *AIPlan, checks []SizingCheck, gpu *GPUDetection) {
	checkMap := make(map[string]*SizingCheck)
	for i := range checks {
		checkMap[checks[i].ComponentName] = &checks[i]
	}

	for i := range plan.Components {
		c := &plan.Components[i]

		if chk, ok := checkMap[c.Name]; ok {
			if chk.Source != "" {
				c.Source = chk.Source
			}
		}

		// GPU 标记仅对 profile=gpu 的组件生效
		if gpu != nil && gpu.NeedsGPU && c.Profile == "gpu" {
			c.Source = "gpu-detected"
		}
	}
}
