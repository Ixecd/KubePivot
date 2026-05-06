// internal/scheduler/migration_manager.go — v3.2: 迁移执行引擎
//
// MigrationManager 跟踪 Pod 迁移的完整生命周期。
// 与 Rescheduler 松耦合：Rescheduler 打 annotation + evict 后不再跟踪，
// MigrationManager 通过 Pod annotation 获取迁移状态，推进阶段。

package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ─── Migration 状态机 ────────────────────────────────────────────

// MigrationPhase 迁移阶段。
type MigrationPhase string

const (
	PhaseEvicting        MigrationPhase = "Evicting"        // 旧 Pod 驱逐中
	PhaseWaitingForReady MigrationPhase = "WaitingForReady" // 等待新 Pod Ready
	PhasePaused          MigrationPhase = "Paused"          // Dual-Path: Stateful 超时暂挂，等 backoff 后重试
	PhaseComplete        MigrationPhase = "Complete"        // 迁移成功，清 annotation
	PhaseFailed          MigrationPhase = "Failed"          // 迁移失败，已回滚
)

// Migration represents an in-flight Pod migration.
type Migration struct {
	ID         string         // kubepivot.io/migration-id
	PodNS      string         // Pod namespace
	PodName    string         // Pod name
	SourceNode string         // 源节点
	TargetNode string         // 目标节点
	Phase      MigrationPhase // 当前阶段
	StartedAt  time.Time      // 迁移开始时间（phase 进入时间）
	CellType   string         // stateless | stateful (v3.3 Fencing)
	RetryCount int            // Dual-Path: Paused→retry 次数
}

// MigrationManager tracks and advances migration lifecycles.
type MigrationManager struct {
	mu         sync.Mutex
	migrations map[string]*Migration // key: "ns/name"

	// Phase timeouts
	evictTimeout     time.Duration // Evicting → 超时 → 僵尸清理 (default 10min)
	waitReadyTimeout time.Duration // WaitingForReady → 超时 → stateless Rollback, stateful Paused (default 5min)

	// Dual-Path: Stateful 迁移超时不直接 Failed，进入 Paused 等 backoff 后重试
	pausedBackoff time.Duration // Paused → retry 等待间隔 (default 30s)
	maxRetries    int           // 最大重试次数 (default 3)，超限 → Failed

	// DryRun mode: 只记录状态推进不执行实际 evict 和 annotation 更新
	DryRun bool

	// Pod status query (injected, enables testing)
	podReadyFunc func(ctx context.Context, ns, name string) (phase string, ready bool)
}

// NewMigrationManager creates a MigrationManager.
func NewMigrationManager() *MigrationManager {
	return &MigrationManager{
		migrations:       make(map[string]*Migration),
		evictTimeout:     10 * time.Minute,
		waitReadyTimeout: 5 * time.Minute,
		pausedBackoff:    30 * time.Second,
		maxRetries:       3,
		podReadyFunc:     nil, // set by controller or test
	}
}

// SetPodReadyFunc injects the pod status query function (for testing).
func (m *MigrationManager) SetPodReadyFunc(fn func(ctx context.Context, ns, name string) (string, bool)) {
	m.podReadyFunc = fn
}

// ─── Migration lifecycle ─────────────────────────────────────────

// Rehydrate restores in-flight migration state from annotated Pods.
// Must be called once during controller startup, before the reconcile loop,
// to recover migrations that were in progress before a restart.
// Pods without migration annotations are silently skipped.
func (m *MigrationManager) Rehydrate(pods []*PodInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, pod := range pods {
		if pod.Annotations == nil || !HasMigrationAnnotation(pod.Annotations) {
			continue
		}
		mig, err := ParseMigrationFromAnnotations(pod.Namespace, pod.Name, pod.Annotations)
		if err != nil {
			slog.Warn("rehydrate: skip malformed migration annotation",
				"pod", pod.Namespace+"/"+pod.Name, "err", err,
			)
			continue
		}
		key := mig.PodNS + "/" + mig.PodName
		if _, exists := m.migrations[key]; exists {
			continue // already tracked
		}
		m.migrations[key] = mig
		slog.Info("rehydrated migration", "pod", key, "phase", mig.Phase)
	}
}

// StartMigration records a new migration in Evicting phase.
// Called by Rescheduler after evictPod.
func (m *MigrationManager) StartMigration(mig *Migration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := mig.PodNS + "/" + mig.PodName
	if existing, ok := m.migrations[key]; ok {
		slog.Warn("duplicate migration start, overwriting", "pod", key, "oldPhase", existing.Phase)
	}
	mig.Phase = PhaseEvicting
	mig.StartedAt = time.Now()
	m.migrations[key] = mig

	slog.Info("migration started", "pod", key, "source", mig.SourceNode, "target", mig.TargetNode)
}

// Reconcile advances all in-flight migrations.
// Returns completed, failed, and paused migrations for cleanup.
// Stateful Cell 超时走 Dual-Path: Pause+Retry，而非直接 Failed。
//
// DryRun mode: computes what would happen without mutating the real state.
// Useful for pre-flight checks and testing.
func (m *MigrationManager) Reconcile(ctx context.Context) (completed, failed, paused []*Migration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// DryRun: operate on clones so real state is untouched.
	workMap := m.migrations
	if m.DryRun {
		workMap = make(map[string]*Migration, len(m.migrations))
		for k, v := range m.migrations {
			clone := *v
			workMap[k] = &clone
		}
	}

	now := time.Now()

	for key, mig := range workMap {
		switch mig.Phase {
		case PhaseEvicting:
			m.reconcileEvicting(ctx, mig, key, now, &completed, &failed)
		case PhaseWaitingForReady:
			m.reconcileWaiting(ctx, mig, key, now, &completed, &failed, &paused)
		case PhasePaused:
			m.reconcilePaused(ctx, mig, key, now, &completed, &failed)
		}
	}

	// Only mutate real state when not in DryRun mode.
	if m.DryRun {
		return completed, failed, paused
	}

	// Clean up completed/failed from tracking
	for _, mig := range completed {
		delete(m.migrations, mig.PodNS+"/"+mig.PodName)
	}
	for _, mig := range failed {
		delete(m.migrations, mig.PodNS+"/"+mig.PodName)
	}

	return completed, failed, paused
}

func (m *MigrationManager) reconcileEvicting(ctx context.Context, mig *Migration, key string, now time.Time, completed, failed *[]*Migration) {
	// Check timeout — zombie migration
	if now.Sub(mig.StartedAt) > m.evictTimeout {
		slog.Warn("migration zombie detected", "pod", key, "elapsed", now.Sub(mig.StartedAt))
		mig.Phase = PhaseFailed
		*failed = append(*failed, mig)
		return
	}

	// Check if old Pod is gone (new Pod should be created by K8s)
	if m.podReadyFunc == nil {
		return // no way to check, skip
	}

	phase, _ := m.podReadyFunc(ctx, mig.PodNS, mig.PodName)
	if phase == "" || phase == "Terminated" || phase == "Succeeded" || phase == "Failed" {
		// Old Pod gone → advance to WaitingForReady
		mig.Phase = PhaseWaitingForReady
		mig.StartedAt = now // reset timer for wait phase
		slog.Info("migration advancing", "pod", key, "phase", PhaseWaitingForReady)
	}
}

func (m *MigrationManager) reconcileWaiting(ctx context.Context, mig *Migration, key string, now time.Time, completed, failed, paused *[]*Migration) {
	// Check timeout → Dual-Path
	if now.Sub(mig.StartedAt) > m.waitReadyTimeout {
		if mig.CellType == "stateful" {
			// Stateful: Pause+Retry — 不 flush WAL，冻结新连接，等 backoff 重试
			slog.Warn("stateful migration timeout, pausing for retry", "pod", key, "elapsed", now.Sub(mig.StartedAt), "retry", mig.RetryCount+1)
			mig.Phase = PhasePaused
			mig.StartedAt = now // reset timer for paused backoff
			*paused = append(*paused, mig)
			return
		}
		// Stateless: 直接回滚
		slog.Warn("migration timeout in WaitingForReady", "pod", key, "elapsed", now.Sub(mig.StartedAt))
		mig.Phase = PhaseFailed
		*failed = append(*failed, mig)
		return
	}

	if m.podReadyFunc == nil {
		return
	}

	phase, ready := m.podReadyFunc(ctx, mig.PodNS, mig.PodName)
	if phase == "Running" && ready {
		mig.Phase = PhaseComplete
		*completed = append(*completed, mig)
		slog.Info("migration complete", "pod", key, "targetNode", mig.TargetNode)
	}
	if phase == "Failed" || phase == "CrashLoopBackOff" {
		mig.Phase = PhaseFailed
		*failed = append(*failed, mig)
		slog.Warn("migration failed, new Pod unhealthy", "pod", key, "phase", phase)
	}
}

// reconcilePaused handles Dual-Path retry logic for stateful migrations.
// After backoff elapses: retry WaitingForReady (up to maxRetries).
// Exhausted retries → Failed.
func (m *MigrationManager) reconcilePaused(ctx context.Context, mig *Migration, key string, now time.Time, completed, failed *[]*Migration) {
	// Still in backoff window
	if now.Sub(mig.StartedAt) < m.pausedBackoff {
		return
	}

	mig.RetryCount++
	if mig.RetryCount > m.maxRetries {
		slog.Warn("stateful migration retries exhausted", "pod", key, "retries", mig.RetryCount)
		mig.Phase = PhaseFailed
		*failed = append(*failed, mig)
		return
	}

	// Retry: go back to WaitingForReady
	slog.Info("retrying stateful migration", "pod", key, "retry", mig.RetryCount)
	mig.Phase = PhaseWaitingForReady
	mig.StartedAt = now
}

// ─── Query ────────────────────────────────────────────────────────

// ActiveCount returns the number of in-flight migrations.
func (m *MigrationManager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.migrations)
}

// GetMigration returns a tracked migration by pod key.
func (m *MigrationManager) GetMigration(ns, name string) *Migration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.migrations[ns+"/"+name]
}

// ─── Migration label & selector ───────────────────────────────────

// MigrationLabelKey is the K8s label set on Pods with an active migration.
// Unlike annotations (which carry payload), this label enables efficient
// LabelSelector-based List filtering — avoiding O(n²) full-Pod scans.
const MigrationLabelKey = "kubepivot.io/migration-active"

// MigrationLabelSelector returns the K8s label selector to find all migration-tracked Pods.
// Usage: listOpts.LabelSelector = scheduler.MigrationLabelSelector()
func MigrationLabelSelector() string {
	return MigrationLabelKey
}

// HasMigrationAnnotation checks if a pod has any migration annotation (fast pre-filter).
func HasMigrationAnnotation(annotations map[string]string) bool {
	_, ok := annotations["kubepivot.io/migration-id"]
	return ok
}

// ─── Annotation helpers ───────────────────────────────────────────

// MigrationAnnotationBundle builds the set of migration annotations for a pod.
func MigrationAnnotationBundle(mig *Migration) map[string]string {
	return map[string]string{
		"kubepivot.io/migration-id":              mig.ID,
		"kubepivot.io/migration-source-node":     mig.SourceNode,
		"kubepivot.io/migration-target-node":     mig.TargetNode,
		"kubepivot.io/migration-phase":           string(mig.Phase),
		"kubepivot.io/migration-start-timestamp": mig.StartedAt.Format(time.RFC3339),
		"kubepivot.io/migration-cell-type":       mig.CellType,
	}
}

// ParseMigrationFromAnnotations reconstructs a Migration from Pod annotations.
func ParseMigrationFromAnnotations(ns, name string, annotations map[string]string) (*Migration, error) {
	id := annotations["kubepivot.io/migration-id"]
	if id == "" {
		return nil, fmt.Errorf("missing migration-id annotation for %s/%s", ns, name)
	}

	startedAt, err := time.Parse(time.RFC3339, annotations["kubepivot.io/migration-start-timestamp"])
	if err != nil {
		// Timestamp corrupt or missing — use now to avoid instant zombie/timeout.
		slog.Warn("malformed migration-start-timestamp, using now as fallback",
			"pod", ns+"/"+name,
			"raw", annotations["kubepivot.io/migration-start-timestamp"],
			"err", err,
		)
		startedAt = time.Now()
	}

	return &Migration{
		ID:         id,
		PodNS:      ns,
		PodName:    name,
		SourceNode: annotations["kubepivot.io/migration-source-node"],
		TargetNode: annotations["kubepivot.io/migration-target-node"],
		Phase:      MigrationPhase(annotations["kubepivot.io/migration-phase"]),
		StartedAt:  startedAt,
		CellType:   annotations["kubepivot.io/migration-cell-type"],
	}, nil
}

// MigrationLabels returns the labels to set on a migration-tracked Pod.
// Set alongside annotations; enables K8s LabelSelector filtering.
func MigrationLabels() map[string]string {
	return map[string]string{
		MigrationLabelKey: "true",
	}
}

// MigrationCleanupAnnotations returns the annotations to remove after migration completes.
func MigrationCleanupAnnotations() []string {
	return []string{
		"kubepivot.io/migration-id",
		"kubepivot.io/migration-source-node",
		"kubepivot.io/migration-target-node",
		"kubepivot.io/migration-phase",
		"kubepivot.io/migration-start-timestamp",
		"kubepivot.io/migration-cell-type",
	}
}

// MigrationCleanupLabels returns the labels to remove after migration completes.
func MigrationCleanupLabels() []string {
	return []string{MigrationLabelKey}
}
