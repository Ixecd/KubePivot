// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

// Package sizing 提供资源优化 sizing 能力.
// B-Level4 扩展: 智能权重学习 + 业务模板自动推荐 (纯 Go 实现，零外部依赖).

package sizing

import (
	"fmt"
	"math"

	"github.com/Ixecd/kubepivot/internal/metrics"
)

// CalculateAdaptiveWeights 基于历史利用率变异系数 (CV) 动态调整权重.
//
// 核心逻辑:
//   - CV = σ / μ (标准差 / 均值)，衡量数据波动性
//   - CV_CPU > CV_MEM → CPU 负载更突发 → 上调 cpuWeight (更保守)
//   - CV_MEM > CV_CPU → Memory 负载更突发 → 上调 memWeight
//   - 结果限制在 [0.2, 0.8] 区间，避免过度偏移
//
// 参数:
//   - points: 历史利用率采样点 (UsagePoint)
//   - baseCPU, baseMEM: 基础权重 (来自 Profile 默认值)
//
// 返回:
//   - (cpuWeight, memWeight): 自适应调整后的权重
//   - (cvCPU, cvMEM): 计算出的变异系数 (用于日志/可解释性)
//
// 设计原则:
//   - 可解释性: 返回 CV 值，调用方可记录 "Reason: High CPU volatility (CV: 0.85)"
//   - 确定性: 同输入必同输出，方便调试 + CI 缓存
//   - 零依赖: 纯 math 包实现，不引入 gonum 等重型库
func CalculateAdaptiveWeights(points []*metrics.PodMetrics, baseCPU, baseMem float64) (cpuW, memW, cvCPU, cvMem float64) {
	if len(points) < 3 {
		// 样本不足，返回基础权重
		return baseCPU, baseMem, 0, 0
	}

	// 1. 提取 CPU / Memory 时间序列
	var cpus, mems []float64
	for _, p := range points {
		// Quantity.Value 已是标准化值: CPU=millicores, Memory=bytes
		cpus = append(cpus, float64(p.TotalCPU.Value))
		mems = append(mems, float64(p.TotalMemory.Value))
	}

	// 2. 计算 CV (变异系数 = std / mean)
	cvCPU = calculateCV(cpus)
	cvMem = calculateCV(mems)

	// 3. 基于 CV 调整权重
	//    原理: 波动越大 → 权重越高 → 分配更多安全余量
	//    公式: weight = base + (cv / (cvCPU + cvMem)) * adjustment
	totalCV := cvCPU + cvMem
	if totalCV < 1e-6 {
		// 无波动，返回基础权重
		return baseCPU, baseMem, cvCPU, cvMem
	}

	// 调整幅度: 最大 ±0.3 (从 0.5→0.8 或 0.5→0.2)
	adjustment := 0.3
	cpuW = baseCPU + (cvCPU/totalCV)*adjustment - (cvMem/totalCV)*adjustment
	memW = baseMem + (cvMem/totalCV)*adjustment - (cvCPU/totalCV)*adjustment

	// 4. 限制边界 [0.2, 0.8]，避免极端权重
	cpuW = clamp(cpuW, 0.2, 0.8)
	memW = clamp(memW, 0.2, 0.8)

	// 5. 归一化: 确保 cpuW + memW = 1.0 (权重和为 1)
	sum := cpuW + memW
	cpuW /= sum
	memW /= sum

	return cpuW, memW, cvCPU, cvMem
}

// calculateCV 计算切片数据的变异系数 (CV = std / mean).
// 返回 0 如果均值接近 0 (避免除零).
func calculateCV(vals []float64) float64 {
	n := len(vals)
	if n < 2 {
		return 0
	}

	// 计算均值
	var sum float64
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(n)
	if math.Abs(mean) < 1e-6 {
		return 0
	}

	// 计算标准差 (样本标准差，除以 n-1)
	var sumSq float64
	for _, v := range vals {
		diff := v - mean
		sumSq += diff * diff
	}
	std := math.Sqrt(sumSq / float64(n-1))

	return std / math.Abs(mean)
}

// clamp 限制值在 [min, max] 区间.
func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}


// RecommendProfile 基于资源使用模式自动推荐业务模板.
//
// 核心逻辑 (简化规则匹配，纯 Go 实现):
//   1. 计算 CPU/Memory 均值比值: ratio = avgCPU / avgMem (归一化后)
//   2. 规则匹配:
//      - ratio > 2.0 → compute-intensive → ProfileWeb
//      - ratio < 0.5 → memory-intensive → ProfileBatch/DB
//      - 0.5 <= ratio <= 2.0 → balanced → ProfileDefault
//   3. 置信度调整: 样本越多越可信，低置信度降级为 default
//
// 参数:
//   - points: 历史采样点 []*metrics.PodMetrics
//   - hints: 可选提示 (如服务名/标签)，Level5 用于更精准推荐
//
// 返回:
//   - Profile: 推荐的业务模板
//   - confidence: 推荐置信度 (0.0-1.0)，基于数据量 + 规则匹配度
//   - reason: 可解释性说明 (用于日志/注释)
//
// 设计原则:
//   - 可解释性: 返回 reason 字符串，用户可知"为什么推荐 web"
//   - 保守优先: 置信度 < 0.6 时返回 ProfileDefault + 低 confidence
//   - 零依赖: 纯规则匹配，不引入聚类算法库
//   - 确定性: 同输入必同输出，方便调试 + CI 缓存
func RecommendProfile(points []*metrics.PodMetrics, hints map[string]string) (Profile, float64, string) {
	if len(points) < 3 {
		// 样本不足，返回默认 + 低置信度
		return ProfileDefault, 0.3, "insufficient samples (<3)"
	}

	// 1. 计算 CPU/Memory 均值
	var sumCPU, sumMem float64
	for _, p := range points {
		// Quantity.Value 已是标准化值: CPU=millicores, Memory=bytes
		sumCPU += float64(p.TotalCPU.Value)
		sumMem += float64(p.TotalMemory.Value)
	}
	avgCPU := sumCPU / float64(len(points))
	avgMem := sumMem / float64(len(points))

	// 2. 归一化比值 (避免单位影响)
	//    参考基准: 500m CPU / 512Mi Memory ≈ 1.0 (均衡服务)
	referenceCPU := 500.0              // millicores
	referenceMem := 512.0 * 1024 * 1024 // bytes (512Mi)
	normalizedCPU := avgCPU / referenceCPU
	normalizedMem := avgMem / referenceMem

	// 防除零
	if normalizedMem < 1e-6 {
		normalizedMem = 1e-6
	}
	ratio := normalizedCPU / normalizedMem

	// 3. 规则匹配 + 置信度计算
	profile := ProfileDefault
	confidence := 0.5
	reason := "balanced resource usage"

	if ratio > 2.0 {
		profile = ProfileWeb
		// 偏离越大越确信: (ratio-2.0)/2.0 最大 +0.3
		confidence = 0.7 + math.Min(0.3, (ratio-2.0)/2.0)
		reason = fmt.Sprintf("compute-intensive pattern (CPU/Mem ratio=%.2f > 2.0)", ratio)
	} else if ratio < 0.5 {
		// 进一步区分 batch vs db: 看 Memory 绝对值
		// DB 通常需要 >1Gi 缓存，batch 可能 <512Mi
		if avgMem > 1024*1024*1024 { // >1Gi
			profile = ProfileDB
			reason = fmt.Sprintf("memory-intensive + high memory footprint (%.0fMi > 1Gi), likely DB/cache", avgMem/(1024*1024))
		} else {
			profile = ProfileBatch
			reason = fmt.Sprintf("memory-intensive pattern (CPU/Mem ratio=%.2f < 0.5)", ratio)
		}
		// 偏离越大越确信: (0.5-ratio)/2.0 最大 +0.3
		confidence = 0.7 + math.Min(0.3, (0.5-ratio)/2.0)
	}

	// 4. 置信度调整: 样本越多越可信
	//    10+ 样本 +0.2, 5-9 样本 +0.1, <5 样本不变
	if len(points) >= 10 {
		confidence = math.Min(1.0, confidence+0.2)
	} else if len(points) >= 5 {
		confidence = math.Min(1.0, confidence+0.1)
	}

	// 5. 保守兜底: 置信度 < 0.6 → 降级为 default
	//    避免低质量推荐污染配置
	if confidence < 0.6 {
		profile = ProfileDefault
		reason = fmt.Sprintf("low confidence (%.2f < 0.6), fallback to default", confidence)
		confidence = 0.5
	}

	return profile, confidence, reason
}

// GenerateExplainComment 生成可解释性注释 (用于 sizing-patch.yaml).
// 输出格式: "# Reason: <reason> | Confidence=<conf>"
// Level5 扩展: 添加 CV/权重等详细信息
func GenerateExplainComment(reason string, confidence float64) string {
	return fmt.Sprintf("# Reason: %s | Confidence=%.2f", reason, confidence)
}