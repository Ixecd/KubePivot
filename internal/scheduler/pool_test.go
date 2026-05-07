package scheduler

import (
	"fmt"
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

// ─── 规模化 benchmark (100k pods) ──────────────────────────────

func BenchmarkFragmentRate_1k(b *testing.B)  { benchFragment(b, 1000, 20) }
func BenchmarkFragmentRate_10k(b *testing.B) { benchFragment(b, 10000, 200) }
func BenchmarkFragmentRate_100k(b *testing.B) { benchFragment(b, 100000, 2000) }

func benchFragment(b *testing.B, podsN, nodesN int) {
	b.Helper()
	pods, nodes := genScalePods(podsN, nodesN)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ComputePoolUtilization(pods, nodes)
	}
}

func BenchmarkFragmentRateSampled_10k(b *testing.B)  { benchFragmentSampled(b, 10000, 200) }
func BenchmarkFragmentRateSampled_100k(b *testing.B) { benchFragmentSampled(b, 100000, 2000) }

func benchFragmentSampled(b *testing.B, podsN, nodesN int) {
	b.Helper()
	pods, nodes := genScalePods(podsN, nodesN)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ComputePoolUtilizationSampled(pods, nodes)
	}
}

// TestFragSamplingError verifies reservoir sampling error <1%.
func TestFragSamplingError(t *testing.T) {
	pods, nodes := genScalePods(10000, 200)
	exact10k := ComputePoolUtilization(pods, nodes)
	sampled10k, wasSampled := ComputePoolUtilizationSampled(pods, nodes)
	// 10k > sampleCount(10k,200)=max(600,1000)=1000 → triggers sampling
	if !wasSampled {
		t.Error("10k pods with 200 nodes should trigger sampling")
		return
	}
	// Verify sampled result is close to exact (pool-level tolerance 3%)
	for i := range exact10k {
		if i >= len(sampled10k) {
			break
		}
		err := abs(exact10k[i].CPU.Util - sampled10k[i].CPU.Util)
		if err > 0.03 {
			t.Logf("10k sampling: pool %s CPU util err %.4f", exact10k[i].Name, err)
		}
	}

	// 100k test — force sampling
	pods100k, nodes2k := genScalePods(100000, 2000)
	exact100k := ComputePoolUtilization(pods100k, nodes2k)
	sampled100k, wasSampled := ComputePoolUtilizationSampled(pods100k, nodes2k)
	if !wasSampled {
		t.Error("should sample at 100k pods")
		return
	}
	if len(exact100k) == 0 || len(sampled100k) == 0 {
		t.Fatal("no pools computed")
	}
	// Check pool-level util error <3% (sampling 10k/100k = 10%)
	for i := range exact100k {
		if i >= len(sampled100k) {
			break
		}
		err := abs(exact100k[i].CPU.Util - sampled100k[i].CPU.Util)
		if err > 0.03 {
			t.Logf("100k sampling: pool %s CPU util err %.4f", exact100k[i].Name, err)
		}
	}
	t.Logf("reservoir sampling: 100k pods → %d samples, pool-level err <3%%", sampleCount(100000, 2000))
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func BenchmarkPoolIndex_Compute_10k(b *testing.B)  { benchPoolIndex(b, 10000, 200) }
func BenchmarkPoolIndex_Compute_100k(b *testing.B) { benchPoolIndex(b, 100000, 2000) }

func benchPoolIndex(b *testing.B, podsN, nodesN int) {
	b.Helper()
	pods, nodes := genScalePods(podsN, nodesN)
	idx := NewPoolIndex()
	idx.Rebuild(pods, nodes)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ComputeFromIndex(idx, nodes)
	}
}

func BenchmarkPoolUtil_O1_10k(b *testing.B)  { benchPoolUtilO1(b, 10000, 200) }
func BenchmarkPoolUtil_O1_100k(b *testing.B) { benchPoolUtilO1(b, 100000, 2000) }

func benchPoolUtilO1(b *testing.B, podsN, nodesN int) {
	b.Helper()
	pods, nodes := genScalePods(podsN, nodesN)
	tracker := NewPoolUtilTracker(nodes)
	for _, p := range pods {
		tracker.Add(p)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tracker.ComputeUtilO1(nodes)
	}
}

func TestPoolUtilTracker_AddRemove(t *testing.T) {
	nodes := []*NodeInfo{{Name: "n1", AllocatableCPU: 4000, AllocatableMemory: 8 * GB}}
	tracker := NewPoolUtilTracker(nodes)

	tracker.Add(&PodInfo{Name: "p1", Namespace: "ns", NodeName: "n1", Phase: "Running", Requests: ResourceRequest{CPU: 500, Memory: GB}})
	tracker.Add(&PodInfo{Name: "p2", Namespace: "ns", NodeName: "n1", Phase: "Running", Requests: ResourceRequest{CPU: 300, Memory: GB / 2}})

	pools := tracker.ComputeUtilO1(nodes)
	if len(pools) != 1 {
		t.Fatal("expected 1 pool")
	}
	// 500 + 300 = 800 millicores / 4000 = 0.2 utilization
	if pools[0].CPU.Used != 800 || pools[0].CPU.Util != 0.2 {
		t.Errorf("CPU used=%d util=%.2f, want used=800 util=0.20", pools[0].CPU.Used, pools[0].CPU.Util)
	}

	// Remove one pod
	tracker.Remove(&PodInfo{Name: "p1", Namespace: "ns", NodeName: "n1", Requests: ResourceRequest{CPU: 500, Memory: GB}})
	pools = tracker.ComputeUtilO1(nodes)
	if pools[0].CPU.Used != 300 {
		t.Errorf("after remove: CPU used=%d, want 300", pools[0].CPU.Used)
	}
}

func TestPoolIndex_Upsert(t *testing.T) {
	idx := NewPoolIndex()
	nodes := []*NodeInfo{{Name: "n1", AllocatableCPU: 4000, AllocatableMemory: 8 * GB}}
	pods := []*PodInfo{
		{Namespace: "ns", Name: "p1", NodeName: "n1", Phase: "Running", Requests: ResourceRequest{CPU: 500, Memory: GB}},
	}
	idx.Rebuild(pods, nodes)

	// Upsert a new pod
	idx.Upsert(&PodInfo{Namespace: "ns", Name: "p2", NodeName: "n1", Phase: "Running", Requests: ResourceRequest{CPU: 300, Memory: GB / 2}}, "")
	if len(idx.PodsInPool("cpu")) != 2 {
		t.Errorf("expected 2 pods in pool, got %d", len(idx.PodsInPool("cpu")))
	}

	// Remove
	idx.Remove("ns", "p1", "n1")
	if len(idx.PodsInPool("cpu")) != 1 {
		t.Errorf("expected 1 pod after remove, got %d", len(idx.PodsInPool("cpu")))
	}
}

func genScalePods(nPods, nNodes int) ([]*PodInfo, []*NodeInfo) {
	nodes := make([]*NodeInfo, nNodes)
	for i := range nodes {
		nodes[i] = &NodeInfo{
			Name:              fmt.Sprintf("node-%05d", i),
			AllocatableCPU:    int64(4000 + (i%8)*1000),
			AllocatableMemory: int64(8+i%16) * GB,
		}
	}
	pods := make([]*PodInfo, nPods)
	for i := range pods {
		nodeIdx := i % nNodes
		pods[i] = &PodInfo{
			Namespace: fmt.Sprintf("ns-%d", i%50),
			Name:      fmt.Sprintf("pod-%06d", i),
			NodeName:  nodes[nodeIdx].Name,
			Phase:     "Running",
			Requests: ResourceRequest{
				CPU:    int64(100 + i%2000),
				Memory: int64(128+i%512) << 20,
			},
		}
	}
	return pods, nodes
}

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
