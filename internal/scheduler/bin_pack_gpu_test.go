// internal/scheduler/bin_pack_gpu_test.go
package scheduler

import (
	"testing"
)

// ── FilterGPUNode ──────────────────────────────────────────────

func TestFilterGPUNode_NoGPUNodes(t *testing.T) {
	nodes := []*NodeInfo{
		{Name: "cpu-only", AllocatableCPU: 4000, AllocatableMemory: 8 * 1024 * 1024 * 1024},
	}
	result := FilterGPUNode(nodes, "", 1)
	if len(result) != 0 {
		t.Errorf("expected 0 GPU nodes, got %d", len(result))
	}
}

func TestFilterGPUNode_ByProduct(t *testing.T) {
	nodes := []*NodeInfo{
		{Name: "a100-node", GPU: []GPUInfo{{Product: "NVIDIA-A100-SXM4-40GB", Health: "Healthy"}}},
		{Name: "v100-node", GPU: []GPUInfo{{Product: "NVIDIA-V100-32GB", Health: "Healthy"}}},
	}
	result := FilterGPUNode(nodes, "NVIDIA-A100-SXM4-40GB", 1)
	if len(result) != 1 || result[0].Name != "a100-node" {
		t.Errorf("expected a100-node only, got %v", result)
	}
}

func TestFilterGPUNode_MinGPUs(t *testing.T) {
	nodes := []*NodeInfo{
		{Name: "4gpu", GPU: make([]GPUInfo, 4)},
		{Name: "8gpu", GPU: make([]GPUInfo, 8)},
	}
	result := FilterGPUNode(nodes, "", 8)
	if len(result) != 1 || result[0].Name != "8gpu" {
		t.Errorf("expected 8gpu only, got %v", result)
	}
}

// ── ScoreGPUNode ───────────────────────────────────────────────

func TestScoreGPUNode_NoGPU(t *testing.T) {
	node := &NodeInfo{Name: "cpu-only"}
	score := ScoreGPUNode(node, 4)
	if score != 1.0 {
		t.Errorf("score = %.2f, want 1.0", score)
	}
}

func TestScoreGPUNode_InsufficientHealthy(t *testing.T) {
	gpus := make([]GPUInfo, 4)
	gpus[0].Health = "Failed"
	gpus[1].Health = "Degraded"
	node := &NodeInfo{Name: "broken", GPU: gpus}
	score := ScoreGPUNode(node, Integer(4))
	if score != 0 {
		t.Errorf("score = %.2f, want 0 (insufficient healthy)", score)
	}
}

func TestScoreGPUNode_SameNVSwitchDomain(t *testing.T) {
	gpus := make([]GPUInfo, 8)
	for i := range gpus {
		gpus[i] = GPUInfo{Health: "Healthy", NVLinkDomain: i / 4}
	}
	node := &NodeInfo{Name: "a100-8gpu", GPU: gpus}
	score := ScoreGPUNode(node, Integer(4))
	// 4 GPUs in same NVSwitch domain → base=2.0, x3=6.0
	if score < 3.0 {
		t.Errorf("score = %.2f, want >= 3.0 (same NVSwitch domain bonus)", score)
	}
}

func TestScoreGPUNode_Fragmented(t *testing.T) {
	gpus := make([]GPUInfo, 8)
	for i := range gpus {
		gpus[i] = GPUInfo{Health: "Healthy", NVLinkDomain: i % 4} // 分散在 4 个 domain
	}
	node := &NodeInfo{Name: "fragmented", GPU: gpus}
	score := ScoreGPUNode(node, Integer(4))
	// 每个 domain 最多 2 个，base=2.0, no bonus
	if score > 3.0 {
		t.Errorf("score = %.2f, want <= 3.0 (fragmented, no bonus)", score)
	}
}

// ── HasGPURequest ──────────────────────────────────────────────

func TestHasGPURequest(t *testing.T) {
	if HasGPURequest(&PodInfo{}) {
		t.Error("empty pod should not have GPU")
	}
	if !HasGPURequest(&PodInfo{Requests: ResourceRequest{GPU: 4000}}) {
		t.Error("pod with GPU=4000 should have GPU")
	}
}

// helper for int64 literal
func Integer(v int) int64 { return int64(v) }
