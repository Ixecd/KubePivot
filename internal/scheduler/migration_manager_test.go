package scheduler

import (
	"context"
	"testing"
	"time"
)

func TestMigrationManager_StartAndActiveCount(t *testing.T) {
	m := NewMigrationManager()
	mig := &Migration{ID: "test-1", PodNS: "default", PodName: "pod-a", SourceNode: "n1", TargetNode: "n2"}
	m.StartMigration(mig)

	if m.ActiveCount() != 1 {
		t.Errorf("ActiveCount = %d, want 1", m.ActiveCount())
	}

	got := m.GetMigration("default", "pod-a")
	if got == nil || got.Phase != PhaseEvicting {
		t.Errorf("phase = %s, want Evicting", got.Phase)
	}
}

func TestMigrationManager_EvictingToWaiting(t *testing.T) {
	m := NewMigrationManager()
	m.SetPodReadyFunc(func(ctx context.Context, ns, name string) (string, bool) {
		// Old pod is gone → should advance
		return "", false
	})

	mig := &Migration{ID: "test-1", PodNS: "ns", PodName: "p", SourceNode: "n1", TargetNode: "n2"}
	m.StartMigration(mig)

	completed, failed, _ := m.Reconcile(context.Background())
	if len(completed)+len(failed) != 0 {
		t.Error("should not complete or fail on first evicting→waiting transition")
	}

	got := m.GetMigration("ns", "p")
	if got.Phase != PhaseWaitingForReady {
		t.Errorf("phase = %s, want WaitingForReady", got.Phase)
	}
}

func TestMigrationManager_WaitingToComplete(t *testing.T) {
	m := NewMigrationManager()
	callCount := 0
	m.SetPodReadyFunc(func(ctx context.Context, ns, name string) (string, bool) {
		callCount++
		if callCount == 1 {
			return "", false // first call: old Pod is gone
		}
		return "Running", true // subsequent: new Pod is Ready
	})

	mig := &Migration{ID: "test-1", PodNS: "ns", PodName: "p", SourceNode: "n1", TargetNode: "n2"}
	m.StartMigration(mig)

	// First reconcile: Evicting → WaitingForReady (old pod gone)
	m.Reconcile(context.Background())
	if m.GetMigration("ns", "p").Phase != PhaseWaitingForReady {
		t.Fatal("should advance to WaitingForReady")
	}
	// Second reconcile: WaitingForReady → Complete (new pod ready)
	completed, _, _ := m.Reconcile(context.Background())

	if len(completed) != 1 {
		t.Fatalf("expected 1 completed, got %d", len(completed))
	}
	if m.ActiveCount() != 0 {
		t.Error("migration should be cleaned from tracking")
	}
}

func TestMigrationManager_ZombieEvicting(t *testing.T) {
	m := NewMigrationManager()
	m.evictTimeout = 1 * time.Millisecond // force immediate timeout

	mig := &Migration{ID: "test-1", PodNS: "ns", PodName: "p", SourceNode: "n1", TargetNode: "n2"}
	m.StartMigration(mig)

	time.Sleep(2 * time.Millisecond) // wait for timeout

	_, failed, _ := m.Reconcile(context.Background())
	if len(failed) != 1 {
		t.Fatalf("expected 1 failed (zombie), got %d", len(failed))
	}
	if failed[0].Phase != PhaseFailed {
		t.Errorf("zombie phase = %s, want Failed", failed[0].Phase)
	}
}

func TestMigrationManager_WaitingTimeout(t *testing.T) {
	m := NewMigrationManager()
	m.waitReadyTimeout = 1 * time.Millisecond

	m.SetPodReadyFunc(func(ctx context.Context, ns, name string) (string, bool) {
		return "", false // old pod gone
	})

	mig := &Migration{ID: "test-1", PodNS: "ns", PodName: "p", SourceNode: "n1", TargetNode: "n2"}
	m.StartMigration(mig)

	// Advance to WaitingForReady
	m.Reconcile(context.Background())

	time.Sleep(2 * time.Millisecond) // wait for timeout

	_, failed, _ := m.Reconcile(context.Background())
	if len(failed) != 1 {
		t.Fatalf("expected 1 failed (timeout), got %d", len(failed))
	}
}

func TestMigrationAnnotationBundle(t *testing.T) {
	mig := &Migration{
		ID: "mig-001", PodNS: "ns", PodName: "p",
		SourceNode: "n1", TargetNode: "n2",
		Phase: PhaseEvicting, StartedAt: time.Now(),
		CellType: "stateless",
	}
	ann := MigrationAnnotationBundle(mig)
	if ann["kubepivot.io/migration-id"] != "mig-001" {
		t.Error("migration-id annotation mismatch")
	}
	if ann["kubepivot.io/migration-cell-type"] != "stateless" {
		t.Error("cell-type annotation mismatch")
	}
}

func TestParseMigrationFromAnnotations(t *testing.T) {
	ann := map[string]string{
		"kubepivot.io/migration-id":              "mig-001",
		"kubepivot.io/migration-source-node":     "n1",
		"kubepivot.io/migration-target-node":     "n2",
		"kubepivot.io/migration-phase":           "Evicting",
		"kubepivot.io/migration-start-timestamp": time.Now().Format(time.RFC3339),
		"kubepivot.io/migration-cell-type":       "stateless",
	}
	mig, err := ParseMigrationFromAnnotations("ns", "p", ann)
	if err != nil {
		t.Fatal(err)
	}
	if mig.ID != "mig-001" || mig.SourceNode != "n1" {
		t.Errorf("parsed migration mismatch: %+v", mig)
	}
}

func TestParseMigrationFromAnnotations_MissingID(t *testing.T) {
	_, err := ParseMigrationFromAnnotations("ns", "p", map[string]string{})
	if err == nil {
		t.Error("expected error for missing migration-id")
	}
}

func TestMigrationManager_StatefulTimeoutPaused(t *testing.T) {
	m := NewMigrationManager()
	m.waitReadyTimeout = 1 * time.Millisecond
	m.pausedBackoff = 1 * time.Millisecond

	m.SetPodReadyFunc(func(ctx context.Context, ns, name string) (string, bool) {
		return "", false // old pod gone
	})

	mig := &Migration{ID: "test-1", PodNS: "ns", PodName: "p", SourceNode: "n1", TargetNode: "n2", CellType: "stateful"}
	m.StartMigration(mig)

	// Advance to WaitingForReady
	m.Reconcile(context.Background())
	if m.GetMigration("ns", "p").Phase != PhaseWaitingForReady {
		t.Fatal("should advance to WaitingForReady")
	}

	time.Sleep(2 * time.Millisecond) // wait for timeout

	_, failed, paused := m.Reconcile(context.Background())
	if len(failed) != 0 {
		t.Fatalf("stateful timeout should NOT fail immediately, got %d failed", len(failed))
	}
	if len(paused) != 1 {
		t.Fatalf("stateful timeout should pause, got %d paused", len(paused))
	}
	if paused[0].Phase != PhasePaused {
		t.Errorf("paused phase = %s, want Paused", paused[0].Phase)
	}
	if paused[0].RetryCount != 0 {
		t.Errorf("retry count should still be 0 before first retry, got %d", paused[0].RetryCount)
	}

	// After backoff: should retry → WaitingForReady
	time.Sleep(2 * time.Millisecond)
	m.Reconcile(context.Background())
	migAfter := m.GetMigration("ns", "p")
	if migAfter.Phase != PhaseWaitingForReady {
		t.Errorf("after backoff should retry to WaitingForReady, got %s", migAfter.Phase)
	}
	if migAfter.RetryCount != 1 {
		t.Errorf("retry count = %d, want 1", migAfter.RetryCount)
	}
}

func TestMigrationManager_StatefulRetriesExhausted(t *testing.T) {
	m := NewMigrationManager()
	m.waitReadyTimeout = 1 * time.Millisecond
	m.pausedBackoff = 1 * time.Millisecond
	m.maxRetries = 1

	m.SetPodReadyFunc(func(ctx context.Context, ns, name string) (string, bool) {
		return "", false // old pod gone → advance Evicting→Waiting
	})

	mig := &Migration{ID: "test-1", PodNS: "ns", PodName: "p", SourceNode: "n1", TargetNode: "n2", CellType: "stateful"}
	m.StartMigration(mig)

	// Evicting → WaitingForReady
	m.Reconcile(context.Background())

	// Timeout → Paused
	time.Sleep(2 * time.Millisecond)
	_, _, _ = m.Reconcile(context.Background())

	// Backoff elapses → Retry to WaitingForReady (retryCount=1, maxRetries=1 → still ok)
	time.Sleep(2 * time.Millisecond)
	m.Reconcile(context.Background())

	// Wait timeout again → Paused
	time.Sleep(2 * time.Millisecond)
	_, _, paused := m.Reconcile(context.Background())
	if len(paused) != 1 {
		t.Fatalf("expected 1 paused (second timeout), got %d paused, %d failed", len(paused), 0)
	}

	// Backoff elapses → RetryCount=2 > maxRetries=1 → Failed
	time.Sleep(2 * time.Millisecond)
	_, failed, _ := m.Reconcile(context.Background())

	if len(failed) != 1 {
		t.Fatalf("expected 1 failed after retries exhausted, got %d", len(failed))
	}
	if m.ActiveCount() != 0 {
		t.Error("exhausted migration should be cleaned from tracking")
	}
}

func TestMigrationLabels(t *testing.T) {
	labels := MigrationLabels()
	if labels[MigrationLabelKey] != "true" {
		t.Errorf("MigrationLabels missing %s key", MigrationLabelKey)
	}
}

func TestMigrationLabelSelector(t *testing.T) {
	sel := MigrationLabelSelector()
	if sel != MigrationLabelKey {
		t.Errorf("MigrationLabelSelector = %s, want %s", sel, MigrationLabelKey)
	}
}

func TestMigrationCleanupLabels(t *testing.T) {
	keys := MigrationCleanupLabels()
	if len(keys) != 1 || keys[0] != MigrationLabelKey {
		t.Errorf("MigrationCleanupLabels = %v, want [%s]", keys, MigrationLabelKey)
	}
}

func TestRehydrate_RestoresMigrations(t *testing.T) {
	m := NewMigrationManager()
	pods := []*PodInfo{
		{
			Namespace: "ns", Name: "pod-a",
			Annotations: map[string]string{
				"kubepivot.io/migration-id":              "mig-1",
				"kubepivot.io/migration-source-node":     "n1",
				"kubepivot.io/migration-target-node":     "n2",
				"kubepivot.io/migration-phase":           "Evicting",
				"kubepivot.io/migration-start-timestamp": time.Now().Format(time.RFC3339),
				"kubepivot.io/migration-cell-type":       "stateless",
			},
		},
	}

	m.Rehydrate(pods)
	if m.ActiveCount() != 1 {
		t.Fatalf("Rehydrate should restore 1 migration, got %d", m.ActiveCount())
	}

	mig := m.GetMigration("ns", "pod-a")
	if mig == nil || mig.ID != "mig-1" {
		t.Errorf("rehydrated migration mismatch: %+v", mig)
	}
}

func TestRehydrate_SkipsNoAnnotation(t *testing.T) {
	m := NewMigrationManager()
	pods := []*PodInfo{
		{Namespace: "ns", Name: "pod-b"}, // no annotations
	}
	m.Rehydrate(pods)
	if m.ActiveCount() != 0 {
		t.Error("Rehydrate should skip pods without migration annotations")
	}
}

func TestRehydrate_Idempotent(t *testing.T) {
	m := NewMigrationManager()
	pods := []*PodInfo{
		{
			Namespace: "ns", Name: "pod-a",
			Annotations: map[string]string{
				"kubepivot.io/migration-id":              "mig-1",
				"kubepivot.io/migration-source-node":     "n1",
				"kubepivot.io/migration-target-node":     "n2",
				"kubepivot.io/migration-phase":           "Evicting",
				"kubepivot.io/migration-start-timestamp": time.Now().Format(time.RFC3339),
				"kubepivot.io/migration-cell-type":       "stateless",
			},
		},
	}
	m.Rehydrate(pods)
	m.Rehydrate(pods) // second call should not duplicate
	if m.ActiveCount() != 1 {
		t.Errorf("Rehydrate should be idempotent, got %d", m.ActiveCount())
	}
}

func TestDryRun_DoesNotMutate(t *testing.T) {
	m := NewMigrationManager()
	m.DryRun = true
	m.SetPodReadyFunc(func(ctx context.Context, ns, name string) (string, bool) {
		return "", false // old pod gone → would advance Evicting→WaitingForReady
	})

	mig := &Migration{ID: "dry-1", PodNS: "ns", PodName: "p", SourceNode: "n1", TargetNode: "n2", CellType: "stateless"}
	m.StartMigration(mig)

	// DryRun Reconcile: Evicting → old pod gone → computes WaitingForReady (on clone)
	m.Reconcile(context.Background())

	// Real state should still be Evicting (clones were used)
	got := m.GetMigration("ns", "p")
	if got.Phase != PhaseEvicting {
		t.Errorf("DryRun should NOT mutate real state, got phase=%s", got.Phase)
	}
}

func TestDryRun_ReturnsComputedResults(t *testing.T) {
	m := NewMigrationManager()

	// Advance to WaitingForReady without DryRun first (real state change)
	m.SetPodReadyFunc(func(ctx context.Context, ns, name string) (string, bool) {
		return "", false // old pod gone
	})
	mig := &Migration{ID: "dry-1", PodNS: "ns", PodName: "p", SourceNode: "n1", TargetNode: "n2", CellType: "stateless"}
	m.StartMigration(mig)
	m.Reconcile(context.Background()) // real: Evicting → WaitingForReady

	// Now enable DryRun and simulate new pod Ready
	m.DryRun = true
	m.SetPodReadyFunc(func(ctx context.Context, ns, name string) (string, bool) {
		return "Running", true
	})

	completed, _, _ := m.Reconcile(context.Background())
	if len(completed) != 1 {
		t.Fatalf("DryRun should compute completion when pod is Ready, got %d completed", len(completed))
	}
	// Real state still WaitingForReady
	got := m.GetMigration("ns", "p")
	if got.Phase != PhaseWaitingForReady {
		t.Errorf("DryRun should preserve real state, got phase=%s", got.Phase)
	}
}

func TestParseMigration_CorruptTimestamp(t *testing.T) {
	ann := map[string]string{
		"kubepivot.io/migration-id":              "mig-1",
		"kubepivot.io/migration-start-timestamp": "garbage",
	}
	mig, err := ParseMigrationFromAnnotations("ns", "p", ann)
	if err != nil {
		t.Fatal(err)
	}
	// Should use time.Now() as fallback, not Year 1 zero value
	if mig.StartedAt.Year() < 2020 {
		t.Errorf("corrupt timestamp should fallback to now, got %v (Year 1 = instant zombie)", mig.StartedAt)
	}
}
