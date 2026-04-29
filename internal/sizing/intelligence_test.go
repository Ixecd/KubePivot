// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package sizing

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Ixecd/kubepivot/internal/metrics"
)

// TestRecommendProfile_UnitNormalization 验证量纲归一化逻辑
// 修复点：增加采样点至 5 个，以通过置信度校验 (samples/5.0 >= 0.6)
func TestRecommendProfile_UnitNormalization(t *testing.T) {
	// 构造 5 个相同的采样点：2 Cores (2000m) vs 512MiB
	p := &metrics.PodMetrics{
		TotalCPU:    metrics.Quantity{Value: 2000},
		TotalMemory: metrics.Quantity{Value: 512 * 1024 * 1024},
	}
	points := []*metrics.PodMetrics{p, p, p, p, p}

	profile, conf, reason := RecommendProfile(points)

	if profile != ProfileWeb {
		t.Errorf("Expected ProfileWeb, got %s (Confidence: %.2f, Reason: %s)", profile, conf, reason)
	}
}

// TestRecommendProfile_ZeroMemoryPanic 验证除零保护
func TestRecommendProfile_ZeroMemoryPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RecommendProfile panicked on zero memory: %v", r)
		}
	}()

	points := []*metrics.PodMetrics{
		{
			TotalCPU:    metrics.Quantity{Value: 100},
			TotalMemory: metrics.Quantity{Value: 0},
		},
	}

	RecommendProfile(points)
}

// TestRecommendProfile_ExtremeVolatility 验证置信度下限保护
func TestRecommendProfile_ExtremeVolatility(t *testing.T) {
	// 构造波动极大的数据
	points := []*metrics.PodMetrics{
		{TotalCPU: metrics.Quantity{Value: 10}},
		{TotalCPU: metrics.Quantity{Value: 1000}},
		{TotalCPU: metrics.Quantity{Value: 10}},
		{TotalCPU: metrics.Quantity{Value: 1000}},
		{TotalCPU: metrics.Quantity{Value: 10}},
	}

	_, confidence, _ := RecommendProfile(points)

	if confidence < 0 {
		t.Errorf("Confidence should never be negative, got %.2f", confidence)
	}
}

// TestCalculateAdaptiveWeights_EmptyInput 验证空输入兜底
func TestCalculateAdaptiveWeights_EmptyInput(t *testing.T) {
	baseCPU, baseMem := 0.5, 0.5
	cpuW, memW, cvCPU, _ := CalculateAdaptiveWeights(nil, baseCPU, baseMem)

	if cpuW != baseCPU || memW != baseMem {
		t.Error("Empty input should return base weights")
	}
	if cvCPU != 0 {
		t.Error("Empty input should have 0 CV")
	}
}

// TestRecommendProfile_DBCategory 验证数据库类业务识别
func TestRecommendProfile_DBCategory(t *testing.T) {
	p := &metrics.PodMetrics{
		TotalCPU:    metrics.Quantity{Value: 200},
		TotalMemory: metrics.Quantity{Value: 4 * 1024 * 1024 * 1024}, // 4 GiB
	}
	points := []*metrics.PodMetrics{p, p, p, p, p}

	profile, conf, reason := RecommendProfile(points)

	if profile != ProfileDB {
		t.Errorf("Expected ProfileDB, got %s (Conf: %.2f)", profile, conf)
	}
	if !strings.Contains(reason, "memory-intensive") {
		t.Errorf("Reason should mention memory-intensive, got %q", reason)
	}
}

// TestCalculateCV_Stability 验证变异系数计算的稳定性
func TestCalculateCV_Stability(t *testing.T) {
	vals := []int64{100, 100, 100}
	cv := calculateCV(vals)
	if cv != 0 {
		t.Errorf("CV for identical values should be 0, got %.2f", cv)
	}

	if calculateCV(nil) != 0 {
		t.Error("CV for nil should be 0")
	}
}

// TestRecommendProfile_Balanced 验证均衡场景
func TestRecommendProfile_Balanced(t *testing.T) {
	p := &metrics.PodMetrics{
		TotalCPU:    metrics.Quantity{Value: 500},
		TotalMemory: metrics.Quantity{Value: 512 << 20},
	}
	points := []*metrics.PodMetrics{p, p, p, p, p}

	profile, _, _ := RecommendProfile(points)
	if profile != ProfileDefault {
		t.Errorf("Expected ProfileDefault for balanced usage, got %s", profile)
	}
}

// TestSizingFallback_Integration 验证从异常数据源 Fallback 后的链路可靠性
// 业务语义：当 Prometheus 采集失败并回退到基础样本（可能点数较少）时，
// 引擎必须输出一个“安全”的推荐值，确保部署流程不被中断。
func TestSizingFallback_Integration(t *testing.T) {
	// 模拟 Prometheus 失败后，从 kubectl top 拿到的少量保底数据
	// 场景：3 个点，略有波动，总内存 1Gi 左右
	samples := []*metrics.PodMetrics{
		{
			Timestamp:   time.Now().Add(-10 * time.Second),
			TotalCPU:    metrics.Quantity{Value: 200},        // 200m
			TotalMemory: metrics.Quantity{Value: 1024 << 20}, // 1Gi
		},
		{
			Timestamp:   time.Now().Add(-5 * time.Second),
			TotalCPU:    metrics.Quantity{Value: 220},
			TotalMemory: metrics.Quantity{Value: 1050 << 20},
		},
		{
			Timestamp:   time.Now(),
			TotalCPU:    metrics.Quantity{Value: 180},
			TotalMemory: metrics.Quantity{Value: 980 << 20},
		},
	}

	t.Run("Execution Safety: compute should never panic with valid samples", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// 验证链路畅通：Compute 内部会调用 RecommendProfile 和权重计算
		sug, err := Compute(ctx, samples, ProfileDefault)

		if err != nil {
			t.Fatalf("Compute failed during fallback scenario: %v", err)
		}

		if sug == nil {
			t.Fatal("Compute returned nil suggestion without error")
		}

		// 业务稳定性保证：推荐值必须大于 0，且不能离谱地低于均值
		// 均值约 200m, 1Gi。由于 P95 逻辑，推荐值应 >= 均值
		if sug.RecommendedCPU < 180 {
			t.Errorf("Recommended CPU (%d) too low, risk of OOM/Throttle", sug.RecommendedCPU)
		}
		if sug.RecommendedMem < (900 << 20) {
			t.Errorf("Recommended Memory (%d) too low", sug.RecommendedMem)
		}
	})

	t.Run("Business Continuity: low sample count should still yield Default profile", func(t *testing.T) {
		// 构造极少样本
		sparseSamples := []*metrics.PodMetrics{
			{
				TotalCPU:    metrics.Quantity{Value: 2000},              // 2 Cores
				TotalMemory: metrics.Quantity{Value: 512 * 1024 * 1024}, // 512Mi
			},
		}

		// 业务语义：即使数据看起来像 Web 型，但因为只有 1 个点，系统不应冒险修改配置
		profile, conf, _ := RecommendProfile(sparseSamples)

		// 关键修复：检查 RecommendProfile 的内部逻辑是否正确计算了低置信度
		if conf >= 0.6 {
			t.Errorf("System is too confident (%.2f) with only 1 sample, risk of misjudgment", conf)
		}

		if profile != ProfileDefault {
			t.Errorf("Expected fallback to default, but engine insisted on %s with low data quality", profile)
		}
	})
}
