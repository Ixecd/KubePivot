// internal/scheduler/gpu_parse_test.go
package scheduler

import (
	"testing"
)

// ── parseGPUCount ──────────────────────────────────────────────

func TestParseGPUCount_Integer(t *testing.T) {
	n, err := parseGPUCount("4")
	if err != nil {
		t.Fatal(err)
	}
	if n != 4000 {
		t.Errorf("parseGPUCount(\"4\") = %d, want 4000 (4 × MilliGPUUnit)", n)
	}
}

func TestParseGPUCount_MilliSuffix(t *testing.T) {
	n, err := parseGPUCount("4000m")
	if err != nil {
		t.Fatal(err)
	}
	if n != 4000 {
		t.Errorf("parseGPUCount(\"4000m\") = %d, want 4000", n)
	}
}

func TestParseGPUCount_Float(t *testing.T) {
	n, err := parseGPUCount("0.2")
	if err != nil {
		t.Fatal(err)
	}
	if n != 200 {
		t.Errorf("parseGPUCount(\"0.2\") = %d, want 200 (0.2 × MilliGPUUnit)", n)
	}
}

func TestParseGPUCount_Empty(t *testing.T) {
	n, err := parseGPUCount("")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("parseGPUCount(\"\") = %d, want 0", n)
	}
}

func TestParseGPUCount_One(t *testing.T) {
	n, err := parseGPUCount("1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1000 {
		t.Errorf("parseGPUCount(\"1\") = %d, want 1000", n)
	}
}

// ── parseGPUNodeLabels ─────────────────────────────────────────

func TestParseGPUNodeLabels_A100(t *testing.T) {
	labels := map[string]string{
		"nvidia.com/gpu.product":  "NVIDIA-A100-SXM4-40GB",
		"nvidia.com/gpu.memory":   "40960",
		"nvidia.com/gpu.nvswitch": "true",
	}
	gpus := parseGPUNodeLabels(labels, 8)
	if len(gpus) != 8 {
		t.Fatalf("expected 8 GPUs, got %d", len(gpus))
	}
	if gpus[0].Product != "NVIDIA-A100-SXM4-40GB" {
		t.Errorf("product = %s", gpus[0].Product)
	}
	// 40960 MiB = 40 GiB → bytes
	wantMem := int64(40960 * 1024 * 1024)
	if gpus[0].MemTotal != wantMem {
		t.Errorf("MemTotal = %d, want %d", gpus[0].MemTotal, wantMem)
	}
	// A100 with NVSwitch: 8 GPUs → 2 domains (0-3, 4-7)
	if gpus[0].NVLinkDomain != 0 {
		t.Errorf("GPU-0 domain = %d, want 0", gpus[0].NVLinkDomain)
	}
	if gpus[4].NVLinkDomain != 1 {
		t.Errorf("GPU-4 domain = %d, want 1", gpus[4].NVLinkDomain)
	}
}

func TestParseGPUNodeLabels_NoNVSwitch(t *testing.T) {
	labels := map[string]string{
		"nvidia.com/gpu.product": "NVIDIA-V100-32GB",
		"nvidia.com/gpu.memory":  "32768",
	}
	gpus := parseGPUNodeLabels(labels, 4)
	if len(gpus) != 4 {
		t.Fatalf("expected 4 GPUs, got %d", len(gpus))
	}
	// V100 has no NVSwitch → all domain 0
	for i, g := range gpus {
		if g.NVLinkDomain != 0 {
			t.Errorf("GPU-%d: domain = %d, want 0 (no NVSwitch)", i, g.NVLinkDomain)
		}
	}
}

func TestParseGPUNodeLabels_ZeroCount(t *testing.T) {
	gpus := parseGPUNodeLabels(nil, 0)
	if gpus != nil {
		t.Errorf("expected nil, got %d GPUs", len(gpus))
	}
}

func TestParseGPUNodeLabels_NoLabels(t *testing.T) {
	gpus := parseGPUNodeLabels(map[string]string{}, 1)
	if len(gpus) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(gpus))
	}
	if gpus[0].Product != "" {
		t.Errorf("expected empty product, got %s", gpus[0].Product)
	}
	if gpus[0].Health != "Healthy" {
		t.Errorf("expected Healthy, got %s", gpus[0].Health)
	}
}

// ── MilliGPUUnit constants ──────────────────────────────────────

func TestMilliGPUUnit(t *testing.T) {
	if MilliGPUUnit != 1000 {
		t.Errorf("MilliGPUUnit = %d, want 1000", MilliGPUUnit)
	}
	if WholeGPU != 1000 {
		t.Errorf("WholeGPU = %d, want 1000", WholeGPU)
	}
}
