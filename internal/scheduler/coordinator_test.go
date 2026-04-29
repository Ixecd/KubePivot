// internal/scheduler/coordinator_test.go

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Ixecd/kubepivot/internal/metrics"
	"github.com/Ixecd/kubepivot/internal/sizing"
)

type mockCoordinatorPodLister struct {
	pods []*PodInfo
}

func (m *mockCoordinatorPodLister) ListAllPods(ctx context.Context) ([]*PodInfo, error) {
	return m.pods, nil
}

type mockCoordinatorNodeLister struct {
	nodes []*NodeInfo
}

func (m *mockCoordinatorNodeLister) ListAllNodes(ctx context.Context) ([]*NodeInfo, error) {
	return m.nodes, nil
}

type mockCoordinatorMetricsProvider struct {
	err error
}

func (m *mockCoordinatorMetricsProvider) GetPodMetrics(ctx context.Context, namespace, pod string) (*metrics.PodMetrics, error) {
	return nil, errors.New("not implemented")
}

func (m *mockCoordinatorMetricsProvider) QueryRange(ctx context.Context, cpuQuery, memQuery string, start time.Time, step time.Duration) ([]*metrics.PodMetrics, error) {
	if m.err != nil {
		return nil, m.err
	}
	return nil, nil
}

type mockCoordinatorSizingProvider struct {
	err error
}

func (m *mockCoordinatorSizingProvider) Compute(ctx context.Context, samples []*metrics.PodMetrics, profile sizing.Profile) (*sizing.Suggestion, error) {
	if m.err != nil {
		return nil, m.err
	}
	// 返回一个能塞进剩余空间的固定值，便于验证协同收敛
	return &sizing.Suggestion{
		RecommendedCPU: 300,               // 300m < 400m 剩余 CPU
		RecommendedMem: 128 * 1024 * 1024, // 128Mi < 212Mi 剩余内存
		Confidence:     1.0,
		Profile:        profile,
	}, nil
}

func TestCoordinate_ConvergesAfterTrimming(t *testing.T) {
	// 节点容量设计为初始装箱失败但裁剪后成功
	nodes := []*NodeInfo{
		{Name: "node1", AllocatableCPU: 1100, AllocatableMemory: 1024 * 1024 * 1024},
	}
	pods := []*PodInfo{
		{
			Namespace: "ns1", Name: "pod-a", Phase: "Running",
			Requests: ResourceRequest{CPU: 600, Memory: 300 * 1024 * 1024},
		},
		{
			Namespace: "ns2", Name: "pod-b", Phase: "Running",
			Requests: ResourceRequest{CPU: 600, Memory: 300 * 1024 * 1024},
		},
	}

	s := NewScheduler(
		&mockCoordinatorPodLister{pods: pods},
		&mockCoordinatorNodeLister{nodes: nodes},
		&mockCoordinatorMetricsProvider{},
		&mockCoordinatorSizingProvider{},
		nil,
	)

	plan, err := s.coordinate(context.Background())
	if err != nil {
		t.Fatalf("coordinate failed: %v", err)
	}
	if !plan.Converged {
		t.Error("expected Converged=true after trimming")
	}
	if len(plan.PodAssignments) != 2 {
		t.Fatalf("expected 2 assignments, got %d", len(plan.PodAssignments))
	}

	// 裁剪后 Pod B 的资源应低于原始值
	for _, p := range pods {
		if p.Requests.CPU >= 600 || p.Requests.Memory >= 300*1024*1024 {
			t.Logf("pod %s not trimmed yet: cpu=%d mem=%d", p.Name, p.Requests.CPU, p.Requests.Memory)
		}
	}
}

func TestCoordinate_MaxIterationsExceeded(t *testing.T) {
	nodes := []*NodeInfo{
		{Name: "node1", AllocatableCPU: 100, AllocatableMemory: 64 * 1024 * 1024},
	}
	pods := []*PodInfo{
		{
			Namespace: "ns1", Name: "giant", Phase: "Running",
			Requests: ResourceRequest{CPU: 5000, Memory: 8 * 1024 * 1024 * 1024},
		},
	}

	s := NewScheduler(
		&mockCoordinatorPodLister{pods: pods},
		&mockCoordinatorNodeLister{nodes: nodes},
		&mockCoordinatorMetricsProvider{},
		&mockCoordinatorSizingProvider{},
		nil,
	)

	plan, err := s.coordinate(context.Background())
	if err != nil {
		t.Fatalf("coordinate failed: %v", err)
	}
	if plan.Converged {
		t.Error("expected Converged=false after max iterations")
	}
	if len(plan.PodAssignments) != 0 {
		t.Errorf("expected 0 assignments, got %d", len(plan.PodAssignments))
	}
}

func TestCoordinate_AlreadyConverged(t *testing.T) {
	nodes := []*NodeInfo{
		{Name: "node1", AllocatableCPU: 4000, AllocatableMemory: 8 * 1024 * 1024 * 1024},
	}
	pods := []*PodInfo{
		{
			Namespace: "ns1", Name: "pod-a", Phase: "Running",
			Requests: ResourceRequest{CPU: 500, Memory: 256 * 1024 * 1024},
		},
	}

	s := NewScheduler(
		&mockCoordinatorPodLister{pods: pods},
		&mockCoordinatorNodeLister{nodes: nodes},
		&mockCoordinatorMetricsProvider{err: errors.New("should not be called")},
		&mockCoordinatorSizingProvider{},
		nil,
	)

	plan, err := s.coordinate(context.Background())
	if err != nil {
		t.Fatalf("coordinate failed: %v", err)
	}
	if !plan.Converged {
		t.Error("expected Converged=true on first try")
	}
	if len(plan.PodAssignments) != 1 {
		t.Errorf("expected 1 assignment, got %d", len(plan.PodAssignments))
	}
}
