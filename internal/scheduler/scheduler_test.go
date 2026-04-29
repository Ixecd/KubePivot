// internal/scheduler/scheduler_test.go
package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Ixecd/kubepivot/internal/metrics"
	"github.com/Ixecd/kubepivot/internal/sizing"
)

// ─── Mock 适配器 ────────────────────────────────────────

type mockPodLister struct {
	pods []*PodInfo
	err  error
}

func (m *mockPodLister) ListAllPods(ctx context.Context) ([]*PodInfo, error) {
	return m.pods, m.err
}

type mockNodeLister struct {
	nodes []*NodeInfo
	err   error
}

func (m *mockNodeLister) ListAllNodes(ctx context.Context) ([]*NodeInfo, error) {
	return m.nodes, m.err
}

type mockMetricsProvider struct {
	metrics map[string]*metrics.PodMetrics // key: "ns/name"
	err     error
}

func (m *mockMetricsProvider) GetPodMetrics(ctx context.Context, namespace, pod string) (*metrics.PodMetrics, error) {
	if m.err != nil {
		return nil, m.err
	}
	key := namespace + "/" + pod
	return m.metrics[key], nil
}

func (m *mockMetricsProvider) QueryRange(ctx context.Context, cpuQuery, memQuery string, start time.Time, step time.Duration) ([]*metrics.PodMetrics, error) {
	return nil, nil
}

type mockSizingProvider struct {
	suggestions map[string]*sizing.Suggestion
	err         error
}

func (m *mockSizingProvider) Compute(ctx context.Context, samples []*metrics.PodMetrics, profile sizing.Profile) (*sizing.Suggestion, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &sizing.Suggestion{
		RecommendedCPU: 100,               // 100m
		RecommendedMem: 128 * 1024 * 1024, // 128 MiB
		Confidence:     1.0,
		Profile:        profile,
	}, nil
}

type mockPlanWriter struct {
	assignments map[string]string
	err         error
}

func (m *mockPlanWriter) WriteAssignments(path string, assignments map[string]string) error {
	if m.err != nil {
		return m.err
	}
	m.assignments = assignments
	return nil
}

// ─── 测试用例 ──────────────────────────────────────────

func TestSchedule_NaiveFirstNode(t *testing.T) {
	// 准备假数据：2 节点 + 3 Pod（其中 1 个非 Running）
	nodes := []*NodeInfo{
		{Name: "node1", AllocatableCPU: 4000, AllocatableMemory: 8 * 1024 * 1024 * 1024},
		{Name: "node2", AllocatableCPU: 4000, AllocatableMemory: 8 * 1024 * 1024 * 1024},
	}
	pods := []*PodInfo{
		{Namespace: "ns1", Name: "pod-a", NodeName: "node1", Phase: "Running",
			Requests: ResourceRequest{CPU: 500, Memory: 256 * 1024 * 1024}},
		{Namespace: "ns1", Name: "pod-b", NodeName: "node2", Phase: "Running",
			Requests: ResourceRequest{CPU: 300, Memory: 128 * 1024 * 1024}},
		{Namespace: "ns2", Name: "pod-c", NodeName: "node1", Phase: "Pending"},
	}

	s := NewScheduler(
		&mockPodLister{pods: pods},
		&mockNodeLister{nodes: nodes},
		&mockMetricsProvider{},
		&mockSizingProvider{},
		&mockPlanWriter{},
	)

	plan, err := s.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() failed: %v", err)
	}
	if !plan.Converged {
		t.Error("expected Converged=true for naive stub")
	}
	if len(plan.PodAssignments) != 2 {
		t.Fatalf("expected 2 Running pods, got %d", len(plan.PodAssignments))
	}
	// 当前桩逻辑：所有 Pod 都分配到 node1
	if plan.PodAssignments["ns1/pod-a"] != "node1" {
		t.Errorf("pod-a expected node1, got %s", plan.PodAssignments["ns1/pod-a"])
	}
	if plan.PodAssignments["ns1/pod-b"] != "node1" {
		t.Errorf("pod-b expected node1 in naive stub, got %s", plan.PodAssignments["ns1/pod-b"])
	}
	if _, exists := plan.PodAssignments["ns2/pod-c"]; exists {
		t.Error("pod-c is Pending, should not be assigned")
	}
}

func TestSchedule_EmptyCluster(t *testing.T) {
	s := NewScheduler(
		&mockPodLister{}, // 空集群
		&mockNodeLister{},
		&mockMetricsProvider{},
		&mockSizingProvider{},
		&mockPlanWriter{},
	)
	plan, err := s.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() failed on empty cluster: %v", err)
	}
	if len(plan.PodAssignments) != 0 {
		t.Error("expected 0 pods on empty cluster")
	}
	if !plan.Converged {
		t.Error("expected Converged=true on empty cluster")
	}
}

func TestSchedule_NoNodes(t *testing.T) {
	s := NewScheduler(
		&mockPodLister{pods: []*PodInfo{{Name: "p", Phase: "Running"}}},
		&mockNodeLister{}, // 无节点
		&mockMetricsProvider{},
		&mockSizingProvider{},
		&mockPlanWriter{},
	)
	_, err := s.Schedule(context.Background())
	if err == nil {
		t.Fatal("expected error for no nodes, got nil")
	}
}

func TestSchedule_PodListError(t *testing.T) {
	s := NewScheduler(
		&mockPodLister{err: errors.New("kubectl failed")},
		&mockNodeLister{},
		&mockMetricsProvider{},
		&mockSizingProvider{},
		&mockPlanWriter{},
	)
	_, err := s.Schedule(context.Background())
	if err == nil {
		t.Fatal("expected error from pod lister, got nil")
	}
}
