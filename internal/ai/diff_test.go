package ai

import (
	"strings"
	"testing"
)

func TestDiffComponents_NewComponent(t *testing.T) {
	old := &AIPlan{}
	new := &AIPlan{Components: []AIComponent{{Name: "api", CPU: "auto", Memory: "auto", Source: "llm-estimated"}}}
	diffs := DiffComponents(old, new)
	if len(diffs) != 1 || diffs[0].OldValue != "-" {
		t.Errorf("new component should show as added, got %+v", diffs)
	}
}

func TestDiffComponents_ChangedCPU(t *testing.T) {
	old := &AIPlan{Components: []AIComponent{{Name: "api", CPU: "500m", Memory: "256Mi"}}}
	new := &AIPlan{Components: []AIComponent{{Name: "api", CPU: "200m", Memory: "256Mi", Source: "sizing-refined"}}}
	diffs := DiffComponents(old, new)
	if len(diffs) != 1 || diffs[0].Field != "cpu" {
		t.Errorf("cpu change should be detected, got %+v", diffs)
	}
}

func TestDiffComponents_NoChange(t *testing.T) {
	old := &AIPlan{Components: []AIComponent{{Name: "api", CPU: "auto", Memory: "auto"}}}
	new := &AIPlan{Components: []AIComponent{{Name: "api", CPU: "auto", Memory: "auto"}}}
	diffs := DiffComponents(old, new)
	if len(diffs) != 0 {
		t.Errorf("no changes should produce empty diff, got %d", len(diffs))
	}
}

func TestFormatDiff(t *testing.T) {
	diffs := []DiffResult{
		{Component: "api", Field: "cpu", OldValue: "500m", NewValue: "200m", Source: "sizing-refined"},
		{Component: "redis", Field: "component", OldValue: "-", NewValue: "new", Source: ""},
	}
	out := FormatDiff(diffs)
	if !strings.Contains(out, "cpu") || !strings.Contains(out, "new component") {
		t.Errorf("diff output incomplete: %s", out)
	}
}

func TestPopulateSources(t *testing.T) {
	plan := &AIPlan{Components: []AIComponent{{Name: "api", Profile: "web"}, {Name: "train", Profile: "gpu"}}}
	checks := []SizingCheck{
		{ComponentName: "api", Source: "sizing-refined"},
		{ComponentName: "train", Source: "llm-estimated"},
	}
	gpu := &GPUDetection{NeedsGPU: true}

	PopulateSources(plan, checks, gpu)
	if plan.Components[0].Source != "sizing-refined" {
		t.Errorf("api source = %s, want sizing-refined", plan.Components[0].Source)
	}
	if plan.Components[1].Source != "gpu-detected" {
		t.Errorf("train source = %s, want gpu-detected", plan.Components[1].Source)
	}
}
