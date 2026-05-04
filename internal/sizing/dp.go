// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package sizing

import (
	"context"
	"fmt"
	"math"

	"github.com/Ixecd/kubepivot/internal/metrics"
)

// =============================================================================
// 1. Profile + 权重系统（Level5 扩展：导出 + 自定义权重）
// =============================================================================

// Profile 业务模板，驱动得分权重 (设计拍板 Q2=B)
type Profile string

const (
	ProfileWeb     Profile = "web"     // CPU 优先，低延迟
	ProfileBatch   Profile = "batch"   // Mem 优先，吞吐优先
	ProfileDB      Profile = "db"      // Mem 优先，缓存敏感
	ProfileDefault Profile = "default" // 均衡
)

// weights 按 Profile 返回 (cpuWeight, memWeight)
func weights(p Profile) (float64, float64) {
	switch p {
	case ProfileWeb:
		return 0.7, 0.3
	case ProfileBatch, ProfileDB:
		return 0.3, 0.7
	default:
		return 0.5, 0.5
	}
}

// GetWeights 导出函数: 按 Profile 返回 (cpuWeight, memWeight)
// Level5 扩展: 供外部调用 (如 deploy_sizing.go 的 weight_learning)
func GetWeights(p Profile) (float64, float64) {
	return weights(p)
}

// =============================================================================
// 4. 离散化配置（设计拍板 Q1=C，Level5 无变更）
// =============================================================================

// discreteLevels 混合离散化 (设计拍板 Q1=C)
// CPU: 线性 50m 步长 [50, 4000] → 80 级
// Mem: 倍数增长 [64Mi, 8Gi] → ~128 级 (256/512/1024... 槽位)
func discreteLevels() (cpuLevels []int64, memLevels []int64) {
	// CPU: 50m ~ 4000m, step=50m → 80 级 ✓
	for c := int64(50); c <= 4000; c += 50 {
		cpuLevels = append(cpuLevels, c)
	}
	// Mem: 64Mi ~ 8Gi, step=64Mi → 128 级 ✓
	// 计算: (8Gi - 64Mi) / 64Mi + 1 = (8192-64)/64 + 1 = 128
	step := int64(64 << 20) // 64 MiB in bytes
	for m := int64(64 << 20); m <= 8<<30; m += step {
		memLevels = append(memLevels, m)
	}
	return cpuLevels, memLevels
}

// =============================================================================
// 5. Suggestion 输出格式（Level5 无变更）
// =============================================================================

// Suggestion 输出格式 (设计拍板 Q4=B: patch/suggest, not auto-apply)
type Suggestion struct {
	CurrentCPU     int64   // millicores, 解析自 Plan.CPU (调用方传入)
	CurrentMem     int64   // bytes, 解析自 Plan.Memory
	RecommendedCPU int64   // millicores
	RecommendedMem int64   // bytes
	Confidence     float64 // 0.0-1.0, 基于采样数 + 离散度
	Profile        Profile
	SavingsCPU     float64 // 节省百分比 (负值=增加)
	SavingsMem     float64
	SampleCount    int // 参与计算的采样点数量
}

// =============================================================================
// 6. DP 核心算法（Level5 重构：提取 compute + 支持自定义权重）
// =============================================================================

// Compute 单 Pod 最优 sizing (维度 B: Pod × CPU × Memory)
//
// 核心逻辑:
//  1. 聚合采样点: 指数衰减加权 + 估算 P95 (avg + 1.5*std)
//  2. DP 状态空间: 混合离散化 (CPU 50m 步长, Mem 64Mi 步长)
//  3. 成本函数: 加权资源成本 + 浪费惩罚 (请求 > P95 的部分)
//  4. 约束: P95 <= request * (1 - headroom), headroom=20%
//  5. 输出: 最小成本状态 + 置信度 (基于样本数 + 离散度)
//
// 参数:
//   - ctx: 控制超时/取消
//   - samples: 多次瞬时 PodMetrics (模拟短期历史)
//   - profile: 业务模板 (web/batch/db/default), 驱动权重
//
// 返回:
//   - *Suggestion: 推荐值 + 置信度 + 节省率
//   - error: 仅当无采样/无可行解时返回
//
// 设计原则:
//   - 确定性: 同输入必同输出 (方便 CI 缓存 + 调试)
//   - 最小必要: 简化 P95 估算 + 线性离散化, 避免过度工程
//   - 可解释: 置信度 + 节省率帮助用户决策
//
// Level5 备注: 如需自定义权重，请使用 ComputeWithWeights
func Compute(ctx context.Context, samples []*metrics.PodMetrics, profile Profile) (*Suggestion, error) {
	cpuW, memW := GetWeights(profile)
	return compute(samples, profile, cpuW, memW)
}

// ComputeWithWeights 支持自定义权重的 sizing 计算 (Level5 新增)
//
// 用途: weight_learning 场景，基于历史利用率变异系数 (CV) 动态调整权重
//
// 参数:
//   - ctx, samples, profile: 同 Compute
//   - cpuW, memW: 自定义权重 (若 <=0 则自动 fallback 到 GetWeights(profile))
//
// 返回: 同 Compute
//
// 示例:
//
//	// 基于 CV 计算动态权重
//	adaptiveCPU, adaptiveMem, _, _ := CalculateAdaptiveWeights(samples, 0.5, 0.5)
//	sug, err := ComputeWithWeights(ctx, samples, profile, adaptiveCPU, adaptiveMem)
func ComputeWithWeights(ctx context.Context, samples []*metrics.PodMetrics, profile Profile, cpuW, memW float64) (*Suggestion, error) {
	// 容错 & fallback: 若权重非法，自动回退到默认 Profile 权重
	if cpuW <= 0 || memW <= 0 {
		cpuW, memW = GetWeights(profile)
	}
	// 归一化: 确保权重和为 1.0 (避免成本函数量纲错误)
	sum := cpuW + memW
	if sum > 1e-6 {
		cpuW /= sum
		memW /= sum
	}
	return compute(samples, profile, cpuW, memW)
}

// compute 核心 DP 逻辑 (内部复用，不导出)
// 参数 cpuW/memW 为已归一化的权重，直接用于成本计算
func compute(samples []*metrics.PodMetrics, profile Profile, cpuW, memW float64) (*Suggestion, error) {
	// --- 前置校验 ---
	if len(samples) == 0 {
		return nil, fmt.Errorf("no metrics samples: cannot compute sizing without data")
	}

	// --- 1. 聚合采样点: 提取指标 + 估算统计量 ---

	// 提取所有采样点的 CPU / Memory (Quantity.Value 已是标准化值: CPU=millicores, Mem=bytes)
	var cpus, mems []int64
	for _, s := range samples {
		cpus = append(cpus, s.TotalCPU.Value)    // 零转换: Value 已是 millicores
		mems = append(mems, s.TotalMemory.Value) // 零转换: Value 已是 bytes
	}
	if len(cpus) == 0 {
		return nil, fmt.Errorf("no valid metrics after extraction: check Quantity parsing")
	}

	// 计算均值 + 标准差 (用于估算 P95)
	avgCPU, stdCPU := meanStd(cpus)
	avgMem, stdMem := meanStd(mems)

	// 估算 P95: 简化公式 (实际可用分位数算法, Level2 升级)
	// 原理: 正态分布下, P95 ≈ avg + 1.645*std, 取 1.5 略保守
	p95CPU := avgCPU + int64(1.5*float64(stdCPU))
	p95Mem := avgMem + int64(1.5*float64(stdMem))

	// --- 2. DP 状态空间: 混合离散化 (设计拍板 Q1=C) ---

	// cpuLevels: 50m ~ 4000m, step=50m → 80 级
	// memLevels: 64Mi ~ 8Gi, step=64Mi → 128 级
	cpuLevels, memLevels := discreteLevels()

	// 状态定义: (cpu_millicores, mem_bytes)
	type state struct {
		cpu int64 // CPU request (millicores)
		mem int64 // Memory request (bytes)
	}
	// DP 值: 成本分数 + 可行性标记
	type result struct {
		score    float64 // 成本分数 (越小越好)
		feasible bool    // 是否满足稳定性约束
	}

	// DP 表: map[state]result (稀疏存储, 只存可行状态)
	dp := make(map[state]result)

	// 约束参数: 20% 缓冲 (headroom)
	headroom := 0.2

	// --- 3. 填充 DP: 遍历状态空间, 计算最小成本 ---

	// 外层: CPU 离散级别
	for _, cpuReq := range cpuLevels {
		// 内层: Memory 离散级别
		for _, memReq := range memLevels {
			// [约束检查] 稳定性: P95 <= request * (1 - headroom)
			// 违反则跳过 (等价于 +∞ 惩罚)
			cpuFeasible := float64(cpuReq) >= float64(p95CPU)/(1-headroom)
			memFeasible := float64(memReq) >= float64(p95Mem)/(1-headroom)
			if !cpuFeasible || !memFeasible {
				continue
			}

			// [成本计算] 第一项: 加权资源成本
			// 原理: 资源本身有成本 (云厂商计费), 权重反映业务偏好
			// Level5: 使用传入的 cpuW/memW (支持 weight_learning)
			resourceCost := float64(cpuReq)*cpuW + float64(memReq)*memW

			// [成本计算] 第二项: 浪费惩罚
			// 原理: 请求 > P95 的部分是"过度配置", 应惩罚
			// 注意: Mem 浪费权重减半 (内存更弹性, CPU 更敏感)
			// Level5: Mem 浪费惩罚也使用传入的 memW (保持一致性)
			wasteCPU := math.Max(0, float64(cpuReq)-float64(p95CPU))
			wasteMem := math.Max(0, float64(memReq)-float64(p95Mem))
			wastePenalty := wasteCPU*cpuW + wasteMem*memW*0.5

			// 总成本 = 资源成本 + 浪费惩罚
			totalCost := resourceCost + wastePenalty

			// [状态更新] 取最小成本 (稀疏 map + ok 判断避免零值陷阱)
			currState := state{cpu: cpuReq, mem: memReq}
			if prev, ok := dp[currState]; !ok || totalCost < prev.score {
				dp[currState] = result{score: totalCost, feasible: true}
			}
		}
	}

	// --- 4. 找最优解: 遍历 DP 表, 取最小成本状态 ---

	var bestState state
	bestScore := math.MaxFloat64
	for s, r := range dp {
		// 只考虑可行状态 (理论上都可行, 但防御性检查)
		if r.feasible && r.score < bestScore {
			bestState = s
			bestScore = r.score
		}
	}
	// 无可行解: 所有状态都违反约束 (如 P95 > 最大离散级别)
	if bestScore == math.MaxFloat64 {
		return nil, fmt.Errorf("no feasible sizing found: P95(%dm, %dMi) exceeds max levels(4000m, 8Gi)",
			p95CPU, p95Mem>>20)
	}

	// --- 5. 置信度计算: 基于样本数 + 离散度 (变异系数) ---

	// 基础置信度: 样本数越多越可信 (5 个样本=1.0)
	confidence := math.Min(1.0, float64(len(samples))/5.0)

	// 离散度惩罚: 变异系数 (std/avg) 越大, 数据越不稳定, 置信度越低
	// 防除零: avg 可能为 0 (新服务无流量), 用 Max(1, ...) 保底
	coeffVarCPU := float64(stdCPU) / math.Max(1, float64(avgCPU))
	coeffVarMem := float64(stdMem) / math.Max(1, float64(avgMem))
	// 最多扣 30% 置信度 (保留基础可信度)
	confidence *= (1.0 - 0.3*math.Max(coeffVarCPU, coeffVarMem))
	// 钳位到 [0, 1]
	confidence = math.Max(0.0, math.Min(1.0, confidence))

	// --- 6. 节省率计算 (对比历史平均值, 非用户当前配置) ---
	// 注意: 用户当前配置需调用方传入, 在 Suggestion 外部计算
	// 这里用历史 avg 作为参考基准 (简化实现)

	// 防除零: avg 可能为 0
	avgCPUSafe := math.Max(1, float64(avgCPU))
	avgMemSafe := math.Max(1, float64(avgMem))

	savCPU := (avgCPUSafe - float64(bestState.cpu)) / avgCPUSafe * 100
	savMem := (avgMemSafe - float64(bestState.mem)) / avgMemSafe * 100

	// --- 7. 构建并返回 Suggestion ---
	// 原理: 即使调用方未设置 threshold，算法自身也应保证建议质量
	// 策略: Confidence < 0.3 时标记为"低置信度"，调用方应谨慎应用
	// 注意: 不直接返回错误，保持"建议可审计 + 人类最终确认"原则
	// if confidence < 0.3 {
	//     P.Warn("⚠", fmt.Sprintf("Low confidence (%.2f) for %s, review manually", confidence, planName))
	// }
	return &Suggestion{
		// 推荐值 (核心输出)
		RecommendedCPU: bestState.cpu, // millicores
		RecommendedMem: bestState.mem, // bytes

		// 置信度 (帮助用户决策: <0.7 建议人工审查)
		Confidence: confidence,

		// 业务模板 (追溯权重来源)
		Profile: profile,

		// 节省率 (正=节省, 负=增加; 基于历史 avg 基准)
		SavingsCPU:  savCPU,
		SavingsMem:  savMem,
		SampleCount: len(samples),
	}, nil
}

// =============================================================================
// 7. 辅助函数（Level5 无变更）
// =============================================================================

// meanStd 计算整数切片的均值 + 总体标准差
// 公式:
//
//	avg = sum(x) / n
//	std = sqrt( sum((x-avg)^2) / n )  ← 总体标准差 (非样本标准差)
//
// 注意:
//   - 小样本 (n<30) 时, 总体标准差略低估真实波动, 但简化实现可接受
//   - Level2 可升级为样本标准差 (除以 n-1) 或分位数算法
//
// 返回: (avg, std) 均为 int64 (向下取整, 保守估计)
func meanStd(vals []int64) (avg, std int64) {
	n := len(vals)
	if n == 0 {
		return 0, 0
	}

	// 计算均值
	var sum int64
	for _, v := range vals {
		sum += v
	}
	avg = sum / int64(n)

	// 计算标准差 (总体标准差)
	var sumSq int64
	for _, v := range vals {
		diff := v - avg
		sumSq += diff * diff
	}
	// sqrt 后向下取整 (保守: 略低估波动 → P95 估算略保守 → 推荐值略安全)
	std = int64(math.Sqrt(float64(sumSq / int64(n))))

	return
}

// =============================================================================
// 8. 输出格式化（Level5 无变更）
// =============================================================================

// ToPatch 生成 patch YAML (设计拍板 Q4=B)
// 输出可直接重定向到文件: kp sizing recommend ... > sizing-patch.yaml
func (s *Suggestion) ToPatch(namespace, name string) string {
	// limits = 2x requests (简化策略，实际可按 Profile 调整)
	limitsCPU := s.RecommendedCPU * 2
	limitsMem := s.RecommendedMem * 2

	return fmt.Sprintf(`# sizing-patch: %s/%s
# profile: %s, confidence: %.2f
# savings: CPU %.1f%%, Mem %.1f%%
# Generated by KubePivot v2.9 sizing engine
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  template:
    spec:
      containers:
      - name: main
        resources:
          requests:
            cpu: "%dm"
            memory: "%dMi"
          limits:
            cpu: "%dm"
            memory: "%dMi"
`, name, namespace, s.Profile, s.Confidence, s.SavingsCPU, s.SavingsMem,
		name, namespace,
		s.RecommendedCPU, s.RecommendedMem>>20, // bytes → MiB
		limitsCPU, limitsMem>>20)
}
