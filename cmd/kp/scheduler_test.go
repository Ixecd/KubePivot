package main

import (
	"testing"

	"github.com/Ixecd/kubepivot/internal/scheduler"
)

func TestComputeClusterSummary_Empty(t *testing.T) {
	s := computeClusterSummary(nil, nil)
	if s.NodeCount != 0 || s.PodCount != 0 || s.RunningCount != 0 || s.TotalCPU != 0 || s.TotalMemory != 0 {
		t.Errorf("空集群 summary 应为全零，got %+v", s)
	}
}

func TestComputeClusterSummary_Normal(t *testing.T) {
	nodes := []*scheduler.NodeInfo{
		{Name: "node1", AllocatableCPU: 4000, AllocatableMemory: 8 * 1024 * 1024 * 1024},
		{Name: "node2", AllocatableCPU: 2000, AllocatableMemory: 4 * 1024 * 1024 * 1024},
	}
	pods := []*scheduler.PodInfo{
		{Name: "pod-a", Phase: "Running"},
		{Name: "pod-b", Phase: "Running"},
		{Name: "pod-c", Phase: "Pending"},
		{Name: "pod-d", Phase: "Succeeded"},
	}

	s := computeClusterSummary(nodes, pods)
	if s.NodeCount != 2 {
		t.Errorf("NodeCount = %d, want 2", s.NodeCount)
	}
	if s.PodCount != 4 {
		t.Errorf("PodCount = %d, want 4", s.PodCount)
	}
	if s.RunningCount != 2 {
		t.Errorf("RunningCount = %d, want 2", s.RunningCount)
	}
	if s.TotalCPU != 6000 {
		t.Errorf("TotalCPU = %d (millicores), want 6000", s.TotalCPU)
	}
	if s.TotalMemory != 12*1024*1024*1024 {
		t.Errorf("TotalMemory = %d (bytes), want %d", s.TotalMemory, 12*1024*1024*1024)
	}
}

func TestComputeClusterSummary_OnlyNodes(t *testing.T) {
	nodes := []*scheduler.NodeInfo{
		{Name: "solo", AllocatableCPU: 1000, AllocatableMemory: 2 * 1024 * 1024 * 1024},
	}
	s := computeClusterSummary(nodes, nil)
	if s.NodeCount != 1 || s.PodCount != 0 || s.RunningCount != 0 {
		t.Errorf("只有 Node 时 pod 统计应全零，got %+v", s)
	}
	if s.TotalCPU != 1000 || s.TotalMemory != 2*1024*1024*1024 {
		t.Errorf("资源汇总不正确")
	}
}

func TestComputeClusterSummary_OnlyPods(t *testing.T) {
	pods := []*scheduler.PodInfo{
		{Name: "orphan", Phase: "Running"},
	}
	s := computeClusterSummary(nil, pods)
	if s.NodeCount != 0 || s.PodCount != 1 || s.RunningCount != 1 {
		t.Errorf("node count should be 0 with nil nodes, got %+v", s)
	}
}
