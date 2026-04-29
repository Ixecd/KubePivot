// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package sizing

import (
	"fmt"
	"math"

	"github.com/Ixecd/kubepivot/internal/metrics"
)

// CalculateAdaptiveWeights 基于历史利用率变异系数 (CV) 动态调整权重.
func CalculateAdaptiveWeights(points []*metrics.PodMetrics, baseCPU, baseMem float64) (cpuW, memW, cvCPU, cvMem float64) {
	if len(points) == 0 {
		return baseCPU, baseMem, 0, 0
	}

	cvCPU = calculateCV(extractCPUValues(points))
	cvMem = calculateCV(extractMemoryValues(points))

	// 核心逻辑: 波动越大，权重越高 (更保守)
	// 偏移量计算: 基于 CV 差异调整，最大偏移 0.2
	diff := (cvCPU - cvMem) * 0.5
	shift := math.Max(-0.2, math.Min(0.2, diff))

	cpuW = baseCPU + shift
	memW = baseMem - shift

	// 钳位保护 [0.2, 0.8]
	cpuW = math.Max(0.2, math.Min(0.8, cpuW))
	memW = math.Max(0.2, math.Min(0.8, memW))

	return
}

// RecommendProfile 自动识别业务类型并推荐 Profile.
func RecommendProfile(points []*metrics.PodMetrics) (Profile, float64, string) {
	if len(points) == 0 {
		return ProfileDefault, 0, "no metrics data"
	}

	// 1. 计算均值
	cpuVals := extractCPUValues(points)
	memVals := extractMemoryValues(points)

	var sumCPU, sumMem int64
	for _, v := range cpuVals {
		sumCPU += v
	}
	for _, v := range memVals {
		sumMem += v
	}

	avgCPU := float64(sumCPU) / float64(len(points))
	avgMem := float64(sumMem) / float64(len(points))

	// 2. 量纲归一化 (Unit Normalization)
	// CPU: milli-cores -> Cores
	// Mem: Bytes -> GiB
	cpuCore := avgCPU / 1000.0
	memGiB := avgMem / (1024 * 1024 * 1024)

	// 3. 比例特征分析
	var ratio float64
	if memGiB > 0.001 { // 避免除零，且忽略极小内存占用
		ratio = cpuCore / memGiB
	} else {
		ratio = 10.0 // 极高比例，视为计算密集型
	}

	profile := ProfileDefault
	reason := "balanced resource usage"
	var typeConfidence float64 = 0.7

	if ratio > 2.0 {
		profile = ProfileWeb
		typeConfidence = 0.7 + math.Min(0.3, (ratio-2.0)/5.0)
		reason = fmt.Sprintf("compute-intensive (%.1f Cores/GiB)", ratio)
	} else if ratio < 0.5 {
		if avgMem > 1024*1024*1024 { // > 1GiB
			profile = ProfileDB
			reason = fmt.Sprintf("memory-intensive + large footprint (%.1f GiB)", memGiB)
		} else {
			profile = ProfileBatch
			reason = fmt.Sprintf("memory-intensive (%.1f Cores/GiB)", ratio)
		}
		typeConfidence = 0.7 + math.Min(0.3, (0.5-ratio)/0.5)
	}

	// 4. 数据置信度计算
	// 基础置信度: 样本数/5.0 (3个样本=0.6, 5个及以上=1.0)
	baseConf := math.Min(1.0, float64(len(points))/5.0)

	// 波动惩罚: 变异系数越大，数据越不可信
	cvCPU := calculateCV(cpuVals)
	cvMem := calculateCV(memVals)
	penalty := 1.0 - 0.3*math.Max(cvCPU, cvMem)
	penalty = math.Max(0.0, penalty) // 确保惩罚不为负

	// 最终置信度 = 类型确信度 * 数据质量
	finalConfidence := typeConfidence * baseConf * penalty
	finalConfidence = math.Max(0.0, math.Min(1.0, finalConfidence))

	// 5. 灰度保护: 若置信度不足 0.6，强制回退到 Default Profile 以求稳
	if finalConfidence < 0.6 {
		reason = fmt.Sprintf("low confidence (%.2f), fallback to default. Original: %s", finalConfidence, reason)
		profile = ProfileDefault
	}

	return profile, finalConfidence, reason
}

// --- 辅助函数 (Helper Functions) ---

// calculateCV 计算变异系数 (Coefficient of Variation)
func calculateCV(vals []int64) float64 {
	n := len(vals)
	if n < 2 {
		return 0
	}

	var sum int64
	for _, v := range vals {
		sum += v
	}
	mean := float64(sum) / float64(n)
	if mean == 0 {
		return 0
	}

	var varianceSum float64
	for _, v := range vals {
		diff := float64(v) - mean
		varianceSum += diff * diff
	}

	stdDev := math.Sqrt(varianceSum / float64(n))
	return stdDev / mean
}

func extractCPUValues(points []*metrics.PodMetrics) []int64 {
	res := make([]int64, len(points))
	for i, p := range points {
		res[i] = p.TotalCPU.Value
	}
	return res
}

func extractMemoryValues(points []*metrics.PodMetrics) []int64 {
	res := make([]int64, len(points))
	for i, p := range points {
		res[i] = p.TotalMemory.Value
	}
	return res
}
