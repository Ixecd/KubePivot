package ai

import (
	"testing"
)

func TestRunSizingCheck_ColdStart(t *testing.T) {
	plan := &AIPlan{Components: []AIComponent{
		{Name: "api", CPU: "auto", Memory: "auto"},
	}}
	ctx := &RepoContext{}

	checks := RunSizingCheck(plan, ctx)
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if !checks[0].ColdStart {
		t.Error("empty context should be cold start")
	}
	if checks[0].Source != "llm-estimated" {
		t.Errorf("cold start source = %s, want llm-estimated", checks[0].Source)
	}
}

func TestRunSizingCheck_HotUpdate(t *testing.T) {
	plan := &AIPlan{Components: []AIComponent{
		{Name: "api", CPU: "auto", Memory: "auto"},
	}}
	ctx := &RepoContext{ExistingPlan: "components:\n  - name: api\n    cpu: 500m"}

	checks := RunSizingCheck(plan, ctx)
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if checks[0].ColdStart {
		t.Error("existing plan should be hot update")
	}
	if len(checks[0].Warnings) == 0 {
		t.Error("hot update with no Prometheus should have warning")
	}
}

func TestRunSizingCheck_MultipleComponents(t *testing.T) {
	plan := &AIPlan{Components: []AIComponent{
		{Name: "api", CPU: "auto", Memory: "auto"},
		{Name: "worker", CPU: "auto", Memory: "auto"},
	}}
	ctx := &RepoContext{}

	checks := RunSizingCheck(plan, ctx)
	if len(checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(checks))
	}
}
