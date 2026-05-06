package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestClassifyWorkload_Stateless(t *testing.T) {
	c := ClassifyWorkload([]string{"Deployment", "Service", "ConfigMap"}, false)
	if c != CellStateless {
		t.Errorf("Deployment+Service → want stateless, got %s", c)
	}
}

func TestClassifyWorkload_StatefulSet(t *testing.T) {
	c := ClassifyWorkload([]string{"StatefulSet", "Service"}, false)
	if c != CellStateful {
		t.Errorf("StatefulSet → want stateful, got %s", c)
	}
}

func TestClassifyWorkload_PVC(t *testing.T) {
	c := ClassifyWorkload([]string{"Deployment"}, true)
	if c != CellStateful {
		t.Errorf("hasPVC → want stateful, got %s", c)
	}
}

func TestFencingPhase_IsPointOfNoReturn(t *testing.T) {
	f := &FencingState{Phase: FencingNone}
	if f.IsPointOfNoReturn() {
		t.Error("none should not be point of no return")
	}

	f.Phase = FencingDraining
	if !f.IsPointOfNoReturn() {
		t.Error("draining should be point of no return")
	}

	f.Phase = FencingComplete
	if !f.IsPointOfNoReturn() {
		t.Error("complete should be point of no return")
	}
}

func TestFencingAnnotationBundle(t *testing.T) {
	f := &FencingState{PodNS: "ns", PodName: "db-0", Phase: FencingReady}
	ann := FencingAnnotationBundle(f)
	if ann["kubepivot.io/fencing-ready"] != "true" {
		t.Error("fencing-ready should be true when phase is ready")
	}

	f.Phase = FencingRequested
	ann = FencingAnnotationBundle(f)
	if ann["kubepivot.io/fencing-ready"] != "false" {
		t.Error("fencing-ready should be false when not ready")
	}
}

func TestMigrationManager_DryRun(t *testing.T) {
	m := NewMigrationManager()

	// Advance to WaitingForReady with real Reconcile first
	m.SetPodReadyFunc(func(ctx context.Context, ns, name string) (string, bool) {
		return "", false // old pod gone
	})
	mig := &Migration{ID: "dry-1", PodNS: "ns", PodName: "p", SourceNode: "n1", TargetNode: "n2"}
	m.StartMigration(mig)
	m.Reconcile(context.Background()) // real: Evicting → WaitingForReady
	if m.GetMigration("ns", "p").Phase != PhaseWaitingForReady {
		t.Fatal("expected WaitingForReady before dry-run test")
	}

	// Now enable DryRun with new pod Ready
	m.DryRun = true
	m.SetPodReadyFunc(func(ctx context.Context, ns, name string) (string, bool) {
		return "Running", true // new pod Ready
	})

	completed, _, _ := m.Reconcile(context.Background())

	// DryRun should compute completion
	if len(completed) != 1 {
		t.Fatalf("dry-run should compute completion, got %d completed", len(completed))
	}
	// But real state must be preserved (clones were used)
	got := m.GetMigration("ns", "p")
	if got.Phase != PhaseWaitingForReady {
		t.Errorf("DryRun must preserve real state, got phase=%s", got.Phase)
	}
}

func TestMapCellsToPods(t *testing.T) {
	cells := []*Cell{
		{Name: "cell-a", Class: CellStateless},
		{Name: "cell-b", Class: CellStateful},
		{Name: "cell-c", Class: CellStateless},
	}
	pods := []string{"controller-0", "controller-1", "controller-2"}

	mappings := MapCellsToPods(cells, pods)
	if len(mappings) != 3 {
		t.Fatalf("expected 3 mappings, got %d", len(mappings))
	}
	for _, m := range mappings {
		if !m.IsPrimary {
			t.Errorf("cell %s should be primary mapped", m.CellName)
		}
		if m.PodName == "" {
			t.Errorf("cell %s has empty pod name", m.CellName)
		}
	}
}

func TestMapCellsToPods_Empty(t *testing.T) {
	if MapCellsToPods(nil, nil) != nil {
		t.Error("empty input should return nil")
	}
}

func TestShouldTakeover_Stateless(t *testing.T) {
	cell := &Cell{Name: "web", Class: CellStateless}
	health := &CellHealth{OwnerPod: "controller-0", Healthy: false}
	if !ShouldTakeover(cell, health, "controller-1", []string{"controller-0", "controller-1"}) {
		t.Error("stateless cell should be taken over when unhealthy")
	}
}

func TestShouldTakeover_Stateful(t *testing.T) {
	cell := &Cell{Name: "db", Class: CellStateful}
	health := &CellHealth{OwnerPod: "controller-0", Healthy: false}
	if ShouldTakeover(cell, health, "controller-1", nil) {
		t.Error("stateful cell should NOT auto-takeover (needs fencing first)")
	}
}

func TestShouldTakeover_AlreadyOwner(t *testing.T) {
	cell := &Cell{Name: "web", Class: CellStateless}
	health := &CellHealth{OwnerPod: "controller-0", Healthy: false}
	if ShouldTakeover(cell, health, "controller-0", nil) {
		t.Error("should not take over own cell")
	}
}

func TestFencingConfig_FallbackChain(t *testing.T) {
	tests := []struct {
		protocol SignalProtocol
		want     []SignalProtocol
	}{
		{SignalAnnotationWatch, []SignalProtocol{SignalAnnotationWatch, SignalWebhook, SignalSIGTERM}},
		{SignalWebhook, []SignalProtocol{SignalWebhook, SignalSIGTERM}},
		{SignalSIGTERM, []SignalProtocol{SignalSIGTERM}},
	}
	for _, tt := range tests {
		fc := &FencingConfig{Protocol: tt.protocol}
		chain := fc.FallbackChain()
		if len(chain) != len(tt.want) {
			t.Errorf("%s FallbackChain len = %d, want %d", tt.protocol, len(chain), len(tt.want))
			continue
		}
		for i, p := range chain {
			if p != tt.want[i] {
				t.Errorf("%s FallbackChain[%d] = %s, want %s", tt.protocol, i, p, tt.want[i])
			}
		}
	}
}

func TestFencingConfig_NextProtocol(t *testing.T) {
	fc := &FencingConfig{Protocol: SignalAnnotationWatch}

	// annotation-watch → webhook
	next, ok := fc.NextProtocol(SignalAnnotationWatch)
	if !ok || next != SignalWebhook {
		t.Errorf("NextProtocol(annotation-watch) = (%s, %v), want (webhook, true)", next, ok)
	}

	// webhook → sigterm
	next, ok = fc.NextProtocol(SignalWebhook)
	if !ok || next != SignalSIGTERM {
		t.Errorf("NextProtocol(webhook) = (%s, %v), want (sigterm, true)", next, ok)
	}

	// sigterm is last resort
	next, ok = fc.NextProtocol(SignalSIGTERM)
	if ok {
		t.Errorf("NextProtocol(sigterm) should be last resort, got (%s, true)", next)
	}
}

func TestHashRing_Deterministic(t *testing.T) {
	pods := []string{"controller-0", "controller-1", "controller-2"}
	r1 := NewHashRing(pods, 40)
	r2 := NewHashRing(pods, 40)

	// Same input → same node count
	if len(r1.nodes) != len(r2.nodes) {
		t.Errorf("node count mismatch: %d vs %d", len(r1.nodes), len(r2.nodes))
	}
	// Same key maps to same pod
	for _, key := range []string{"a", "b", "c"} {
		if r1.GetPod(key) != r2.GetPod(key) {
			t.Errorf("not deterministic for key %s: %s vs %s", key, r1.GetPod(key), r2.GetPod(key))
		}
	}
}

func TestHashRing_GetPod(t *testing.T) {
	pods := []string{"controller-0", "controller-1", "controller-2"}
	ring := NewHashRing(pods, 40)

	// Same key always maps to same pod
	p1 := ring.GetPod("cell-a")
	p2 := ring.GetPod("cell-a")
	if p1 != p2 {
		t.Errorf("GetPod not deterministic: %s vs %s", p1, p2)
	}
	if p1 == "" {
		t.Error("GetPod should return non-empty for non-empty ring")
	}
}

func TestHashRing_Empty(t *testing.T) {
	ring := NewHashRing(nil, 40)
	if ring.GetPod("cell-a") != "" {
		t.Error("empty ring should return empty pod")
	}
}

func TestHashRing_DiffCells_MinimalReshuffle(t *testing.T) {
	pods := []string{"p0", "p1", "p2", "p3"}
	ring := NewHashRing(pods, 40)

	// Create 200 cells with diverse names for better hash distribution
	cells := make([]*Cell, 200)
	for i := range cells {
		cells[i] = &Cell{Name: fmt.Sprintf("shard-%d-cell-%s", i/10, string(rune('a'+i%26)))}
	}

	// Remove one pod (simulate node failure): 4 → 3
	moved := ring.DiffCells(cells, []string{"p0", "p1", "p2"})

	// True consistent hashing: only ~1/N cells should move (25% with 4 pods)
	reshuffleRatio := float64(len(moved)) / float64(len(cells))
	if reshuffleRatio > 0.40 {
		t.Errorf("reshuffle ratio %.2f too high; consistent hashing should limit to ~1/N. moved %d/%d",
			reshuffleRatio, len(moved), len(cells))
	}
	t.Logf("removed 1/4 pods → %d/%d cells moved (%.1f%%)", len(moved), len(cells), reshuffleRatio*100)
}

func TestShouldForceRelease_Draining(t *testing.T) {
	cfg := &FencingConfig{HardTimeout: 1 * time.Millisecond}

	f := &FencingState{Phase: FencingDraining, StartedAt: time.Now().Add(-2 * time.Millisecond).Format(time.RFC3339)}
	if !f.ShouldForceRelease(cfg) {
		t.Error("Draining past HardTimeout should force release")
	}
}

func TestShouldForceRelease_NotEligible(t *testing.T) {
	cfg := &FencingConfig{HardTimeout: 1 * time.Hour}

	f := &FencingState{Phase: FencingDraining, StartedAt: time.Now().Format(time.RFC3339)}
	if f.ShouldForceRelease(cfg) {
		t.Error("Draining within HardTimeout should NOT force release")
	}

	f.Phase = FencingComplete // only Draining/Paused eligible
	if f.ShouldForceRelease(cfg) {
		t.Error("Complete should not trigger force release")
	}
}

func TestShouldForceRelease_CorruptTimestamp(t *testing.T) {
	cfg := &FencingConfig{HardTimeout: 1 * time.Hour}

	f := &FencingState{Phase: FencingDraining, StartedAt: "not-a-timestamp"}
	if !f.ShouldForceRelease(cfg) {
		t.Error("corrupt timestamp should trigger force release (conservative)")
	}
}
