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