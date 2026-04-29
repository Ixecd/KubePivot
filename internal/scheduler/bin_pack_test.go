// internal/scheduler/bin_pack_test.go
package scheduler

import (
	"testing"
)

func TestBinPack_TwoNodesThreePods(t *testing.T) {
	// 验证推演场景：2 节点，3 Pod，FFD + DP
	nodes := []*NodeInfo{
		{Name: "node1", AllocatableCPU: 4000, AllocatableMemory: 8 * 1024 * 1024 * 1024},
		{Name: "node2", AllocatableCPU: 2000, AllocatableMemory: 4 * 1024 * 1024 * 1024},
	}
	pods := []*PodInfo{
		{Namespace: "ns1", Name: "pod-a", Phase: "Running", Requests: ResourceRequest{CPU: 3000, Memory: 2 * 1024 * 1024 * 1024}},
		{Namespace: "ns1", Name: "pod-b", Phase: "Running", Requests: ResourceRequest{CPU: 1000, Memory: 6 * 1024 * 1024 * 1024}},
		{Namespace: "ns2", Name: "pod-c", Phase: "Running", Requests: ResourceRequest{CPU: 1000, Memory: 1 * 1024 * 1024 * 1024}},
	}

	plan, err := BinPack(nodes, pods)
	if err != nil {
		t.Fatalf("BinPack failed: %v", err)
	}
	if !plan.Converged {
		t.Error("expected Converged=true")
	}
	if len(plan.PodAssignments) != 3 {
		t.Fatalf("expected 3 assignments, got %d", len(plan.PodAssignments))
	}

	// 验证大 Pod (A, B) 被分配到 node1（因为 FFD 优先填满大节点）
	if plan.PodAssignments["ns1/pod-a"] != "node1" {
		t.Errorf("pod-a expected node1, got %s", plan.PodAssignments["ns1/pod-a"])
	}
	if plan.PodAssignments["ns1/pod-b"] != "node1" {
		t.Errorf("pod-b expected node1, got %s", plan.PodAssignments["ns1/pod-b"])
	}
	if plan.PodAssignments["ns2/pod-c"] != "node2" {
		t.Errorf("pod-c expected node2, got %s", plan.PodAssignments["ns2/pod-c"])
	}
}

func TestBinPack_Overflow(t *testing.T) {
	// 一个超大 Pod，任何节点都装不下
	nodes := []*NodeInfo{
		{Name: "node1", AllocatableCPU: 1000, AllocatableMemory: 1 * 1024 * 1024 * 1024},
	}
	pods := []*PodInfo{
		{Namespace: "ns1", Name: "giant", Phase: "Running", Requests: ResourceRequest{CPU: 2000, Memory: 2 * 1024 * 1024 * 1024}},
	}

	plan, err := BinPack(nodes, pods)
	if err != nil {
		t.Fatalf("BinPack returned error: %v", err)
	}
	// 所有 Pod 都装不下，但算法本身不应报错，只是收敛失败
	if plan.Converged {
		t.Error("expected Converged=false because pod cannot fit")
	}
	if len(plan.PodAssignments) != 0 {
		t.Errorf("expected 0 assignments, got %d", len(plan.PodAssignments))
	}
}

func TestBinPack_EmptyPods(t *testing.T) {
	nodes := []*NodeInfo{
		{Name: "node1", AllocatableCPU: 1000, AllocatableMemory: 1 * 1024 * 1024 * 1024},
	}
	plan, err := BinPack(nodes, nil)
	if err != nil {
		t.Fatalf("BinPack failed on nil pods: %v", err)
	}
	if !plan.Converged {
		t.Error("expected Converged=true for empty pods")
	}
	if len(plan.PodAssignments) != 0 {
		t.Error("expected 0 assignments for empty pods")
	}
}

func TestSortPods_Normalized(t *testing.T) {
	nodes := []*NodeInfo{
		{Name: "node1", AllocatableCPU: 4000, AllocatableMemory: 8 * 1024 * 1024 * 1024},
	}
	pods := []*PodInfo{
		{Name: "small", Requests: ResourceRequest{CPU: 100, Memory: 128 * 1024 * 1024}},
		{Name: "big", Requests: ResourceRequest{CPU: 2000, Memory: 4 * 1024 * 1024 * 1024}},
	}

	sorted := sortPods(pods, 0.5, 0.5, nodes)
	if sorted[0].Name != "big" {
		t.Errorf("expected 'big' first, got %s", sorted[0].Name)
	}
}
