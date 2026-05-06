// internal/scheduler/bin_pack.go
package scheduler

import (
	"sort"
)

// cpudStep 和 memoryStep 是资源离散化步长。
const (
	cpuStep    = 50 // 毫核
	memoryStep = 64 // MiB → 在代码中转换为字节处理
)

// dpNode 对单个节点运行 0-1 背包 DP，并通过二维 keep 表回溯获得选中 Pod 子集。
// 内部会将节点的 AllocatableCPU / AllocatableMemory 离散化；memoryStep 采用 MiB 以保证步长合理。
func dpNode(node *NodeInfo, availablePods []*PodInfo) []*PodInfo {
	if len(availablePods) == 0 {
		return nil
	}

	// v3.1: GPU 约束 — 预过滤 GPU Pod（按型号匹配健康 GPU）
	hasGPU := false
	// 按 GPU product 统计健康 GPU 数（同一节点可能混合 A100/H100）
	healthyByProduct := make(map[string]int64)
	for _, g := range node.GPU {
		if g.Health == "Healthy" {
			healthyByProduct[g.Product]++
			healthyByProduct[""]++ // "" 匹配所有 GPU（Pod 未指定型号时用）
		}
	}

	var gpuFiltered []*PodInfo
	for _, p := range availablePods {
		if p.Requests.GPU > 0 {
			hasGPU = true
			// 毫卡转换：4000 = 4 整卡
			gpuCards := p.Requests.GPU / MilliGPUUnit
			// 按 Pod 指定的 GPU 型号匹配（从 label 读取）
			product := ""
			if p.Labels != nil {
				product = p.Labels["nvidia.com/gpu.product"]
			}
			if healthyByProduct[product] < gpuCards {
				continue // 该型号健康 GPU 不足
			}
		}
		gpuFiltered = append(gpuFiltered, p)
	}

	if hasGPU && len(healthyByProduct) == 0 {
		return nil // GPU Pod 存在但节点无健康 GPU
	}

	// 离散化
	cpuSlots := int(node.AllocatableCPU / cpuStep)
	memSlots := int(node.AllocatableMemory / (memoryStep * 1024 * 1024))
	if cpuSlots <= 0 || memSlots <= 0 {
		return nil
	}

	// 将 Pod 转为 DP 单元，并过滤掉无法放入单个维度的 Pod
	type podReq struct {
		origIndex int   // availablePods 中的原始下标
		cpu       int   // 离散化 CPU 槽位
		mem       int64 // 内存需求（字节）
	}
	reqs := make([]podReq, 0, len(gpuFiltered))
	for i, p := range gpuFiltered {
		cpu := int(p.Requests.CPU / cpuStep)
		if cpu == 0 {
			cpu = 1 // 至少占 1 个槽位
		}
		if cpu > cpuSlots {
			continue // 单 Pod CPU 需求超过节点总 CPU 槽位，跳过
		}
		if p.Requests.Memory > node.AllocatableMemory {
			continue // 单 Pod 内存需求超过节点总内存，跳过
		}
		reqs = append(reqs, podReq{
			origIndex: i,
			cpu:       cpu,
			mem:       p.Requests.Memory,
		})
	}

	if len(reqs) == 0 {
		return nil
	}

	// 一维 DP 数组存放最大内存价值
	dp := make([]int64, cpuSlots+1)
	// keep[i][c] 记录：在处理第 i 个 Pod（reqs 下标）时，容量 c 是否被该 Pod 贡献（即是否选中该 Pod）
	keep := make([][]bool, len(reqs))
	for i := range keep {
		keep[i] = make([]bool, cpuSlots+1)
	}

	// 0-1 背包逆序更新
	for i, r := range reqs {
		for c := cpuSlots; c >= r.cpu; c-- {
			newMem := dp[c-r.cpu] + r.mem
			if newMem > dp[c] && newMem <= node.AllocatableMemory {
				dp[c] = newMem
				keep[i][c] = true
			}
		}
	}

	// 寻找最大价值状态
	bestC := 0
	var bestMem int64
	for c := 0; c <= cpuSlots; c++ {
		if dp[c] > bestMem {
			bestMem = dp[c]
			bestC = c
		}
	}
	if bestMem == 0 {
		return nil
	}

	// 回溯：从最后一个 Pod 开始向前，还原选中的 Pod
	selected := make([]*PodInfo, 0)
	currC := bestC
	for i := len(reqs) - 1; i >= 0; i-- {
		if currC >= reqs[i].cpu && keep[i][currC] {
			p := gpuFiltered[reqs[i].origIndex]
			selected = append(selected, p)
			currC -= reqs[i].cpu
		}
	}

	return selected
}

// BinPack 维度 A 顶层入口。
// 先对 Pod 进行 FFD 排序，然后逐节点运行 dpNode，返回分配计划。
func BinPack(nodes []*NodeInfo, pods []*PodInfo) (*SchedulingPlan, error) {
	if len(nodes) == 0 {
		return nil, errNoNodes
	}
	if len(pods) == 0 {
		return &SchedulingPlan{
			PodAssignments: make(map[string]string),
			Converged:      true,
		}, nil
	}

	// v3.1: GPU Pod 存在时，预过滤候选节点
	hasGPU := false
	for _, p := range pods {
		if HasGPURequest(p) {
			hasGPU = true
			break
		}
	}
	if hasGPU {
		nodes = FilterGPUNode(nodes, "", 1) // 至少 1 张健康 GPU
		if len(nodes) == 0 {
			return nil, errNoNodes
		}
	}

	// 只处理 Running 状态的 Pod
	runningPods := make([]*PodInfo, 0, len(pods))
	for _, p := range pods {
		if p.Phase == "Running" {
			if p.Requests.CPU > 0 || p.Requests.Memory > 0 || p.Requests.GPU > 0 {
				runningPods = append(runningPods, p)
			}
		}
	}

	if len(runningPods) == 0 {
		return &SchedulingPlan{
			PodAssignments: make(map[string]string),
			Converged:      true,
		}, nil
	}

	// FFD 排序（默认权重 0.5/0.5）
	sorted := sortPods(runningPods, 0.5, 0.5, nodes)

	assignments := make(map[string]string)
	remaining := make([]*PodInfo, len(sorted))
	copy(remaining, sorted)

	for _, node := range nodes {
		if len(remaining) == 0 {
			break
		}

		selected := dpNode(node, remaining)
		if len(selected) == 0 {
			continue // 该节点一个都装不下，继续下一个节点
		}

		// 标记已分配的 Pod
		assignedIdx := make(map[string]bool)
		for _, p := range selected {
			key := p.Namespace + "/" + p.Name
			assignments[key] = node.Name
			assignedIdx[key] = true
		}

		// 过滤剩余 Pod
		newRemaining := make([]*PodInfo, 0, len(remaining)-len(selected))
		for _, p := range remaining {
			key := p.Namespace + "/" + p.Name
			if !assignedIdx[key] {
				newRemaining = append(newRemaining, p)
			}
		}
		remaining = newRemaining
	}

	plan := &SchedulingPlan{
		PodAssignments: assignments,
		Converged:      len(remaining) == 0,
	}

	return plan, nil
}

// sortPods 使用归一化资源得分对 Pod 进行降序排序（FFD 策略）。
// alpha 和 beta 是 CPU 和 Memory 的权重，默认均为 0.5。
// nodes 用于计算归一化基准——取所有节点中 CPU 和 Memory 的最大值，
// 确保“连最大节点都装得吃力”的大 Pod 被优先处理。
// 返回新分配的切片，原始 pods 不受影响。
func sortPods(pods []*PodInfo, alpha, beta float64, nodes []*NodeInfo) []*PodInfo {
	if len(pods) == 0 {
		return nil
	}

	// 计算归一化基准：所有节点中最大 CPU 和最大 Memory
	var maxCPU, maxMem int64
	for _, n := range nodes {
		if n.AllocatableCPU > maxCPU {
			maxCPU = n.AllocatableCPU
		}
		if n.AllocatableMemory > maxMem {
			maxMem = n.AllocatableMemory
		}
	}

	// 防御：如果集群无节点或总量为 0，退回简单求和排序
	if maxCPU == 0 || maxMem == 0 {
		sorted := make([]*PodInfo, len(pods))
		copy(sorted, pods)
		sort.Slice(sorted, func(i, j int) bool {
			return podSizeSimple(sorted[i]) > podSizeSimple(sorted[j])
		})
		return sorted
	}

	sorted := make([]*PodInfo, len(pods))
	copy(sorted, pods)

	sort.Slice(sorted, func(i, j int) bool {
		si := normalizedSize(sorted[i], alpha, beta, maxCPU, maxMem)
		sj := normalizedSize(sorted[j], alpha, beta, maxCPU, maxMem)
		return si > sj
	})

	return sorted
}

// normalizedSize 计算 Pod 的归一化资源吞噬程度。
// v3.1: GPU Pod 额外乘以 GPU 因子（以 maxGPUs 为归一化基准）。
func normalizedSize(p *PodInfo, alpha, beta float64, maxCPU, maxMem int64) float64 {
	score := alpha*float64(p.Requests.CPU)/float64(maxCPU) +
		beta*float64(p.Requests.Memory)/float64(maxMem)

	// v3.1: GPU Pod 按 GPU 请求加权（FFD 大 Pod 优先）
	if p.Requests.GPU > 0 && maxGPUsForSort > 0 {
		gpuScore := float64(p.Requests.GPU) / float64(maxGPUsForSort*MilliGPUUnit)
		// GPU 权重取 alpha+beta 的均值（1/3 各维度）
		score += (alpha + beta) / 2 * gpuScore
	}
	return score
}

// maxGPUsForSort 全局变量：sortPods 前的 GPU 归一化基准。
// 在 sortPods 中由调用方设置。
var maxGPUsForSort int64

// podSizeSimple 简单求和排序（归一化不可用时的 fallback）。
func podSizeSimple(p *PodInfo) int64 {
	return p.Requests.CPU + p.Requests.Memory
}

// ─── v3.1 GPU ──────────────────────────────────────────────────

// FilterGPUNode 过滤出满足 GPU 需求的节点。
// 调用 BinPack 前，如果任何 Pod 有 GPU 请求，先用本函数缩小候选节点集。
// product 为空时不过滤型号。
func FilterGPUNode(nodes []*NodeInfo, product string, minGPUs int64) []*NodeInfo {
	filtered := make([]*NodeInfo, 0)
	for _, n := range nodes {
		if len(n.GPU) == 0 {
			continue
		}
		if product != "" && !hasGPUProduct(n, product) {
			continue
		}
		if int64(len(n.GPU)) < minGPUs {
			continue
		}
		filtered = append(filtered, n)
	}
	return filtered
}

func hasGPUProduct(n *NodeInfo, product string) bool {
	for _, g := range n.GPU {
		if g.Product == product {
			return true
		}
	}
	return false
}

// ScoreGPUNode 为 GPU 节点计算拓扑友好度得分。
// 设计文档 §5.2：同 NVSwitch domain 内空闲 GPU ≥ 需求数 → 3x 得分权重。
// 返回值 > 1.0 = 拓扑友好，≤ 1.0 = 碎片化降级。
func ScoreGPUNode(node *NodeInfo, requiredGPUs int64) float64 {
	if requiredGPUs <= 0 || len(node.GPU) == 0 {
		return 1.0
	}

	// 统计每个 NVLink domain 的健康 GPU 数
	domainFree := make(map[int]int)
	totalHealthy := 0
	for _, g := range node.GPU {
		if g.Health == "Healthy" {
			domainFree[g.NVLinkDomain]++
			totalHealthy++
		}
	}

	if int64(totalHealthy) < requiredGPUs {
		return 0 // 健康 GPU 总数不够，不可调度
	}

	// 基础分：健康 GPU 比例
	base := float64(totalHealthy) / float64(requiredGPUs)

	// 找最大的同 domain 连续空闲数
	bestDomain := 0
	for _, free := range domainFree {
		if free > bestDomain {
			bestDomain = free
		}
	}

	if bestDomain >= int(requiredGPUs) {
		return base * 3.0 // 同一 NVSwitch domain 装得下 → 3x 优先
	}
	if bestDomain >= int(requiredGPUs)/2 {
		return base * 1.5 // 跨 2 个 domain → 轻微加分
	}
	return base // 全碎了 → 基础分
}

// HasGPURequest 判断 Pod 是否请求了 GPU。
func HasGPURequest(pod *PodInfo) bool {
	return pod.Requests.GPU > 0
}
