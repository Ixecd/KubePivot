// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package sizing

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Ixecd/kubepivot/internal/metrics"
)

// --- 1. Quantity 转换辅助（对齐真实 metrics/quantity.go）---

// toMillicores CPU Quantity → millicores (int64)
// 因 Quantity.Value 已是标准化 millicores，直接返回
func toMillicores(q metrics.Quantity) int64 {
	return q.Value
}

// toBytes Memory Quantity → bytes (int64)
// 因 Quantity.Value 已是标准化 bytes，直接返回
func toBytes(q metrics.Quantity) int64 {
	return q.Value
}

// --- 2. 采样层：模拟"历史"数据（Level1 方案）---

// samplePodMetrics 采集 N 次瞬时指标，模拟短期历史
// 间隔建议: 2-5s (避免太近噪声 / 太远漂移)
// 返回: 按时间排序的 []*metrics.PodMetrics
func samplePodMetrics(ctx context.Context, client metrics.MetricsClient, namespace, name string, count int, interval time.Duration) ([]*metrics.PodMetrics, error) {
	var samples []*metrics.PodMetrics
	for i := 0; i < count; i++ {
		m, err := client.GetPodMetrics(ctx, namespace, name)
		if err != nil {
			// 单次失败不中断，容忍瞬时抖动
			// 若首个样本就失败，直接返回错误
			if len(samples) == 0 {
				return nil, fmt.Errorf("initial sample failed: %w", err)
			}
			// 已有样本则记录警告 + 继续
			// P.Warn("⚠", fmt.Sprintf("sample %d/%d failed: %v", i+1, count, err))
			break
		}
		samples = append(samples, m)
		if i < count-1 {
			select {
			case <-time.After(interval):
			case <-ctx.Done():
				return samples, ctx.Err()
			}
		}
	}
	// 按时间排序确保计算顺序一致
	sort.Slice(samples, func(i, j int) bool {
		return samples[i].Timestamp.Before(samples[j].Timestamp)
	})
	return samples, nil
}

// --- 3. DP 核心：输入 []*metrics.PodMetrics ---

// Profile 业务模板，驱动得分权重 (设计拍板 Q2=B)
type Profile string

const (
	ProfileWeb    Profile = "web"     // CPU 优先，低延迟
	ProfileBatch  Profile = "batch"   // Mem 优先，吞吐优先
	ProfileDB     Profile = "db"      // Mem 优先，缓存敏感
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

// discreteLevels 混合离散化 (设计拍板 Q1=C)
// CPU: 线性 50m 步长 [50, 4000] → 80 级
// Mem: 倍数增长 [64Mi, 8Gi] → ~128 级 (256/512/1024... 槽位)
// internal/sizing/dp.go 修正 discreteLevels:

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

// Suggestion 输出格式 (设计拍板 Q4=B: patch/suggest, not auto-apply)
type Suggestion struct {
	CurrentCPU       int64   // millicores, 解析自 Plan.CPU (调用方传入)
	CurrentMem       int64   // bytes, 解析自 Plan.Memory
	RecommendedCPU   int64   // millicores
	RecommendedMem   int64   // bytes
	Confidence       float64 // 0.0-1.0, 基于采样数 + 离散度
	Profile          Profile
	SavingsCPU       float64 // 节省百分比 (负值=增加)
	SavingsMem       float64
}

// Compute 单 Pod 最优 sizing (维度 B)
// 输入: samples = 多次瞬时 PodMetrics (模拟历史) + profile
// 输出: Suggestion (patch 格式，用户确认后手动/自动并入 Git)
func Compute(ctx context.Context, samples []*metrics.PodMetrics, profile Profile) (*Suggestion, error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("no metrics samples")
	}

	// 1. 聚合采样点: 提取加权平均 + 估算 P95
	//    权重: 越新权重越高 (指数衰减)
	//    简化: 先取算术平均 + 标准差估算波动 (P95 ≈ avg + 1.5*std)
	
	var cpus, mems []int64
	for _, s := range samples {
		// Quantity.Value 已是标准化值，零转换
		cpus = append(cpus, toMillicores(s.TotalCPU))
		mems = append(mems, toBytes(s.TotalMemory))
	}
	if len(cpus) == 0 {
		return nil, fmt.Errorf("no valid metrics after extraction")
	}

	// 计算 avg + std
	avgCPU, stdCPU := meanStd(cpus)
	avgMem, stdMem := meanStd(mems)
	
	// 估算 P95 (简化: avg + 1.5*std，实际可用分位数算法)
	p95CPU := avgCPU + int64(1.5*float64(stdCPU))
	p95Mem := avgMem + int64(1.5*float64(stdMem))

	// 2. DP 状态空间 (混合离散化)
	cpuLevels, memLevels := discreteLevels()
	type state struct{ cpu, mem int64 }
	type result struct{ score float64; feasible bool }

	dp := make(map[state]result)
	cpuW, memW := weights(profile)
	headroom := 0.2 // 20% 缓冲

	// 3. 填充 DP: 找满足约束的最小成本
	// 约束: P95 <= request * (1 - headroom)
	for _, c := range cpuLevels {
		for _, m := range memLevels {
			feasible := float64(c) >= float64(p95CPU)/(1-headroom) &&
			            float64(m) >= float64(p95Mem)/(1-headroom)
			if !feasible {
				continue
			}
			// 成本: 加权资源 + 浪费惩罚 (浪费 = request - P95)
			wasteCPU := math.Max(0, float64(c)-float64(p95CPU))
			wasteMem := math.Max(0, float64(m)-float64(p95Mem))
			waste := wasteCPU*cpuW + wasteMem*memW*0.5 // Mem 浪费权重减半
			score := float64(c)*cpuW + float64(m)*memW + waste
			dp[state{c, m}] = result{score: score, feasible: true}
		}
	}

	// 4. 找最优解 (最小成本)
	var best state
	bestScore := math.MaxFloat64
	for s, r := range dp {
		if r.feasible && r.score < bestScore {
			best = s
			bestScore = r.score
		}
	}
	if bestScore == math.MaxFloat64 {
		return nil, fmt.Errorf("no feasible sizing found")
	}

	// 5. Confidence: 基于采样数 + 离散度 (变异系数)
	//    5 个样本=1.0, 离散度越大置信度越低
	confidence := math.Min(1.0, float64(len(samples))/5.0)
	coeffVarCPU := float64(stdCPU) / math.Max(1, float64(avgCPU)) // 防除零
	coeffVarMem := float64(stdMem) / math.Max(1, float64(avgMem))
	confidence *= (1.0 - 0.3*math.Max(coeffVarCPU, coeffVarMem)) // 最多扣 30%
	confidence = math.Max(0.0, math.Min(1.0, confidence)) // 钳位 [0,1]

	// 6. 节省计算 (对比当前配置，调用方传入)
	//    若 current=0 表示未配置，节省率=0
	savCPU := 0.0
	savMem := 0.0
	if samples[0].TotalCPU.Value > 0 { // 简化: 用首个样本的当前值
		// 实际: 调用方传入 Plan.CPU 解析后的 int64
		savCPU = float64(avgCPU-best.cpu) / float64(avgCPU) * 100
	}
	if samples[0].TotalMemory.Value > 0 {
		savMem = float64(avgMem-best.mem) / float64(avgMem) * 100
	}

	// 7. 构建 Suggestion
	return &Suggestion{
		RecommendedCPU: best.cpu,
		RecommendedMem: best.mem,
		Confidence:     confidence,
		Profile:        profile,
		SavingsCPU:     savCPU,
		SavingsMem:     savMem,
	}, nil
}

// meanStd 计算均值 + 标准差 (辅助函数)
func meanStd(vals []int64) (avg, std int64) {
	if len(vals) == 0 {
		return 0, 0
	}
	var sum int64
	for _, v := range vals {
		sum += v
	}
	avg = sum / int64(len(vals))
	
	var sumSq int64
	for _, v := range vals {
		diff := v - avg
		sumSq += diff * diff
	}
	std = int64(math.Sqrt(float64(sumSq / int64(len(vals)))))
	return
}

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