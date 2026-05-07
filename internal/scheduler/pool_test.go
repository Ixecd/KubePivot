package scheduler

import (
	"testing"
)

func TestComputePoolUtilization_CPUOnly(t *testing.T) {
	nodes := []*NodeInfo{
		{Name: "cpu-1", AllocatableCPU: 4000, AllocatableMemory: 8 * 1024 * 1024 * 1024},
		{Name: "cpu-2", AllocatableCPU: 4000, AllocatableMemory: 8 * 1024 * 1024 * 1024},
	}
	pods := []*PodInfo{
		{Name: "pod-a", NodeName: "cpu-1", Phase: "Running", Requests: ResourceRequest{CPU: 1000, Memory: 2 * 1024 * 1024 * 1024}},
		{Name: "pod-b", NodeName: "cpu-2", Phase: "Running", Requests: ResourceRequest{CPU: 3000, Memory: 6 * 1024 * 1024 * 1024}},
	}

	pools := ComputePoolUtilization(pods, nodes)
	if len(pools) != 1 || pools[0].Name != "cpu" {
		t.Fatalf("expected 1 cpu pool, got %d", len(pools))
	}
	if pools[0].CPU.Util != 0.5 {
		t.Errorf("CPU util = %.2f, want 0.5", pools[0].CPU.Util)
	}
}

func TestComputePoolUtilization_GPUAndCPU(t *testing.T) {
	nodes := []*NodeInfo{
		{Name: "gpu-1", AllocatableCPU: 64000, AllocatableMemory: 256 * 1024 * 1024 * 1024,
			GPU: []GPUInfo{{Product: "NVIDIA-A100-40GB", Health: "Healthy", NVLinkDomain: 0}}},
		{Name: "cpu-1", AllocatableCPU: 4000, AllocatableMemory: 8 * 1024 * 1024 * 1024},
	}
	pods := []*PodInfo{
		{Name: "gpu-pod", NodeName: "gpu-1", Phase: "Running", Requests: ResourceRequest{CPU: 1000, Memory: 4 * 1024 * 1024 * 1024, GPU: 1000}},
		{Name: "cpu-pod", NodeName: "cpu-1", Phase: "Running", Requests: ResourceRequest{CPU: 500, Memory: 1 * 1024 * 1024 * 1024}},
	}

	pools := ComputePoolUtilization(pods, nodes)
	if len(pools) != 2 {
		t.Fatalf("expected 2 pools, got %d", len(pools))
	}

	for _, p := range pools {
		if p.Name == "gpu-NVIDIA-A100-40GB" {
			if p.GPU.Util != 1.0 {
				t.Errorf("GPU pool util = %.2f, want 1.0", p.GPU.Util)
			}
		}
	}
}

func TestComputePoolUtilization_Empty(t *testing.T) {
	pools := ComputePoolUtilization(nil, nil)
	if len(pools) != 0 {
		t.Errorf("expected 0 pools, got %d", len(pools))
	}
}

func TestDetectPoolImbalance(t *testing.T) {
	pools := []*PoolInfo{
		{Name: "high", CPU: PoolResource{Total: 4000, Used: 3500, Util: 0.875},
			Memory: PoolResource{Total: 8 * GB, Used: 7 * GB, Util: 0.875}},
		{Name: "low", CPU: PoolResource{Total: 4000, Used: 500, Util: 0.125},
			Memory: PoolResource{Total: 8 * GB, Used: 512 * 1024 * 1024, Util: 0.0625}},
	}

	pairs := DetectPoolImbalance(pools)
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	if pairs[0].High.Name != "high" || pairs[0].Low.Name != "low" {
		t.Errorf("wrong pair: high=%s low=%s", pairs[0].High.Name, pairs[0].Low.Name)
	}
}

func TestDetectPoolImbalance_NoImbalance(t *testing.T) {
	pools := []*PoolInfo{
		{Name: "a", CPU: PoolResource{Total: 4000, Used: 2000, Util: 0.5},
			Memory: PoolResource{Total: 8 * GB, Used: 4 * GB, Util: 0.5}},
		{Name: "b", CPU: PoolResource{Total: 4000, Used: 2000, Util: 0.5},
			Memory: PoolResource{Total: 8 * GB, Used: 4 * GB, Util: 0.5}},
	}

	pairs := DetectPoolImbalance(pools)
	if len(pairs) != 0 {
		t.Errorf("expected 0 pairs, got %d", len(pairs))
	}
}

func TestPoolScore(t *testing.T) {
	nodes := []*NodeInfo{
		{Name: "n1", AllocatableCPU: 4000, AllocatableMemory: 8 * GB},
	}
	pods := []*PodInfo{
		{Name: "p1", NodeName: "n1", Phase: "Running", Requests: ResourceRequest{CPU: 2000, Memory: 4 * GB}},
	}
	pools := ComputePoolUtilization(pods, nodes)
	if len(pools) != 1 {
		t.Fatal("expected 1 pool")
	}
	// 利用率 0.5, 碎片率接近 1.0 → Score ≈ (1-0.5)*0.5 + 1.0*0.5 = 0.75
	if pools[0].Score < 0.5 || pools[0].Score > 1.0 {
		t.Errorf("Score = %.2f, expected 0.5-1.0", pools[0].Score)
	}
}

func TestPoolNameForNode_LabelPriority(t *testing.T) {
	n := &NodeInfo{
		Name:   "node1",
		Labels: map[string]string{"kubepivot.io/pool": "prod-gpu"},
		GPU:    []GPUInfo{{Product: "A100"}},
	}
	name := poolNameForNode(n)
	if name != "prod-gpu" {
		t.Errorf("label pool should take priority, got %s", name)
	}
}

func TestPoolNameForNode_GPUProduct(t *testing.T) {
	n := &NodeInfo{
		Name: "node1",
		GPU:  []GPUInfo{{Product: "NVIDIA-A100-40GB"}},
	}
	name := poolNameForNode(n)
	if name != "gpu-NVIDIA-A100-40GB" {
		t.Errorf("GPU product pool = %s", name)
	}
}

func TestPoolNameForNode_CPUOnly(t *testing.T) {
	n := &NodeInfo{Name: "node1"}
	name := poolNameForNode(n)
	if name != "cpu" {
		t.Errorf("CPU-only node pool = %s", name)
	}
}

// GB helper for pool_test
const GB = 1024 * 1024 * 1024

func TestPoolUtilCache_Hit(t *testing.T) {
	cache := &PoolUtilCache{}
	nodes := []*NodeInfo{{Name: "n1", AllocatableCPU: 4000, AllocatableMemory: 8 * GB}}
	pods := []*PodInfo{{Name: "p1", Namespace: "ns", NodeName: "n1", Phase: "Running", Requests: ResourceRequest{CPU: 1000, Memory: 2 * GB}}}

	// First call: compute
	r1 := cache.GetOrCompute(1, pods, nodes)
	if len(r1) != 1 || r1[0].CPU.Util == 0 {
		t.Fatal("first call should compute")
	}

	// Same gen: cached
	r2 := cache.GetOrCompute(1, pods, nodes)
	if &r1[0] != &r2[0] {
		t.Error("same gen should return cached pointer")
	}

	// New gen: recompute
	r3 := cache.GetOrCompute(2, pods, nodes)
	if &r1[0] == &r3[0] {
		t.Error("new gen should recompute")
	}
}
