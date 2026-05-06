// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package sizing

import (
	"context"
	"testing"
	"time"

	"github.com/Ixecd/kubepivot/internal/metrics"
)

func TestCompute_WebProfile(t *testing.T) {
	// ✅ 真实结构：[]*metrics.PodMetrics，Quantity.Value 已标准化
	samples := []*metrics.PodMetrics{
		{
			Timestamp:   time.Now().Add(-4 * time.Second),
			TotalCPU:    metrics.Quantity{Value: 200, Raw: "200m"},    // 200 millicores
			TotalMemory: metrics.Quantity{Value: 256 << 20, Raw: "256Mi"}, // 256 MiB
		},
		{
			Timestamp:   time.Now().Add(-3 * time.Second),
			TotalCPU:    metrics.Quantity{Value: 210, Raw: "210m"},
			TotalMemory: metrics.Quantity{Value: 260 << 20, Raw: "260Mi"},
		},
		{
			Timestamp:   time.Now().Add(-2 * time.Second),
			TotalCPU:    metrics.Quantity{Value: 220, Raw: "220m"},
			TotalMemory: metrics.Quantity{Value: 270 << 20, Raw: "270Mi"},
		},
		{
			Timestamp:   time.Now().Add(-1 * time.Second),
			TotalCPU:    metrics.Quantity{Value: 230, Raw: "230m"},
			TotalMemory: metrics.Quantity{Value: 280 << 20, Raw: "280Mi"},
		},
		{
			Timestamp:   time.Now(),
			TotalCPU:    metrics.Quantity{Value: 240, Raw: "240m"},
			TotalMemory: metrics.Quantity{Value: 290 << 20, Raw: "290Mi"},
		},
	}

	sug, err := Compute(context.Background(), samples, ProfileWeb)
	if err != nil {
		t.Fatal(err)
	}
	// Web profile: CPU 权重高，推荐值应 >= P95 估算 (~240m)
	if sug.RecommendedCPU < 240 {
		t.Errorf("expected CPU >= 240 (P95 estimate), got %d", sug.RecommendedCPU)
	}
	if sug.Confidence < 0.5 {
		t.Errorf("expected confidence >= 0.5 (5 samples), got %.2f", sug.Confidence)
	}
}

func TestCompute_InsufficientSamples(t *testing.T) {
	// 只有 1 个样本 → Confidence 应较低
	samples := []*metrics.PodMetrics{
		{
			Timestamp:   time.Now(),
			TotalCPU:    metrics.Quantity{Value: 300, Raw: "300m"},
			TotalMemory: metrics.Quantity{Value: 512 << 20, Raw: "512Mi"},
		},
	}
	sug, err := Compute(context.Background(), samples, ProfileDefault)
	if err != nil {
		t.Fatal(err)
	}
	if sug.Confidence > 0.3 {
		t.Errorf("expected low confidence for 1 sample, got %.2f", sug.Confidence)
	}
}

func TestDiscreteLevels(t *testing.T) {
	cpu, mem := discreteLevels()
	if len(cpu) != 80 {
		t.Errorf("expected 80 CPU levels (50-4000m/50), got %d", len(cpu))
	}
	// Mem: 64Mi,128Mi,...,1Gi,2Gi,...,8Gi → ~128 级
	if len(mem) < 100 || len(mem) > 150 {
		t.Errorf("expected ~128 Mem levels, got %d", len(mem))
	}
}

func TestMeanStd(t *testing.T) {
	vals := []int64{100, 200, 300, 400, 500}
	avg, std := meanStd(vals)
	if avg != 300 {
		t.Errorf("expected avg=300, got %d", avg)
	}
	// std = sqrt(((−200)^2+(−100)^2+0+100^2+200^2)/5) = sqrt(20000) ≈ 141
	if std < 140 || std > 142 {
		t.Errorf("expected std≈141, got %d", std)
	}
}

// ── v3.1 GPU weights ───────────────────────────────────────────

func TestGetGPUWeights(t *testing.T) {
	tests := []struct {
		profile      Profile
		expectedCPUW float64
		expectedMemW float64
		expectedGPUW float64
	}{
		{ProfileWeb, 0.7, 0.3, 0.0},
		{ProfileBatch, 0.3, 0.7, 0.0},
		{ProfileDB, 0.3, 0.7, 0.0},
		{ProfileGPU, 0.1, 0.2, 0.7},
		{ProfileDefault, 0.5, 0.5, 0.0},
		{"unknown", 0.5, 0.5, 0.0},
	}
	for _, tt := range tests {
		t.Run(string(tt.profile), func(t *testing.T) {
			cpuW, memW, gpuW := GetGPUWeights(tt.profile)
			if cpuW != tt.expectedCPUW || memW != tt.expectedMemW || gpuW != tt.expectedGPUW {
				t.Errorf("GetGPUWeights(%s) = (%.1f, %.1f, %.1f), want (%.1f, %.1f, %.1f)",
					tt.profile, cpuW, memW, gpuW, tt.expectedCPUW, tt.expectedMemW, tt.expectedGPUW)
			}
		})
	}
}

// GetWeights 向后兼容：非 GPU profile 返回 2D 权重不变
func TestGetWeights_BackwardCompatible(t *testing.T) {
	cpuW, memW := GetWeights(ProfileWeb)
	if cpuW != 0.7 || memW != 0.3 {
		t.Errorf("GetWeights(web) = (%.1f, %.1f), want (0.7, 0.3)", cpuW, memW)
	}

	cpuW, memW = GetWeights(ProfileGPU)
	if cpuW != 0.1 || memW != 0.2 {
		t.Errorf("GetWeights(gpu) = (%.1f, %.1f), want (0.1, 0.2)", cpuW, memW)
	}
}

// ── v3.1 Suggestion GPU fields ─────────────────────────────────

func TestSuggestion_GPUFields_Nil(t *testing.T) {
	s := &Suggestion{Profile: ProfileWeb}
	if s.CurrentGPUCount != 0 || s.RecommendedGPUCount != 0 {
		t.Error("web profile should have zero GPU fields by default")
	}
}

func TestSuggestion_GPUFields_Populated(t *testing.T) {
	s := &Suggestion{
		Profile:             ProfileGPU,
		CurrentGPUCount:     4,
		CurrentGPUMem:       40 * 1024 * 1024 * 1024,
		RecommendedGPUCount: 4,
		RecommendedGPUMem:   40 * 1024 * 1024 * 1024,
		SavingsGPU:          0,
	}
	if s.RecommendedGPUCount != 4 {
		t.Errorf("RecommendedGPUCount = %d, want 4", s.RecommendedGPUCount)
	}
	if s.CurrentGPUMem != 40*1024*1024*1024 {
		t.Errorf("CurrentGPUMem incorrect")
	}
}

// ── v3.1 ComputeGPU ────────────────────────────────────────────

func TestComputeGPU_ConservativeMode(t *testing.T) {
	samples := []*metrics.PodMetrics{
		{
			Timestamp:   time.Now(),
			TotalCPU:    metrics.Quantity{Value: 500, Raw: "500m"},
			TotalMemory: metrics.Quantity{Value: 512 << 20, Raw: "512Mi"},
		},
	}

	sug, err := ComputeGPU(context.Background(), samples, ProfileGPU, 4, 40*1024*1024*1024)
	if err != nil {
		t.Fatalf("ComputeGPU: %v", err)
	}
	// CPU/Mem 应由 2D DP 给出非零推荐
	if sug.RecommendedCPU == 0 || sug.RecommendedMem == 0 {
		t.Error("ComputeGPU should produce CPU/Mem recommendations via 2D DP")
	}
	// GPU 保守模式：推荐值 = 当前值
	if sug.RecommendedGPUCount != 4 {
		t.Errorf("RecommendedGPUCount = %d, want 4 (conservative)", sug.RecommendedGPUCount)
	}
	if sug.RecommendedGPUMem != 40*1024*1024*1024 {
		t.Errorf("RecommendedGPUMem incorrect in conservative mode")
	}
	if sug.SavingsGPU != 0 {
		t.Errorf("SavingsGPU = %.2f, want 0 (conservative, no downsize)", sug.SavingsGPU)
	}
}

func TestComputeGPU_WebProfileIgnoresGPU(t *testing.T) {
	samples := []*metrics.PodMetrics{
		{
			Timestamp:   time.Now(),
			TotalCPU:    metrics.Quantity{Value: 200, Raw: "200m"},
			TotalMemory: metrics.Quantity{Value: 256 << 20, Raw: "256Mi"},
		},
	}
	// Web profile 即使传了 GPU 当前值，推荐也应忽略
	sug, err := ComputeGPU(context.Background(), samples, ProfileWeb, 0, 0)
	if err != nil {
		t.Fatalf("ComputeGPU(web): %v", err)
	}
	if sug.CurrentGPUCount != 0 || sug.RecommendedGPUCount != 0 {
		t.Error("web profile should have zero GPU fields")
	}
}