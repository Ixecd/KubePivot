// internal/scheduler/pool.go — v3.2: 池化调度层
//
// 将调度视角从"逐节点贪心"升级为"全局容量治理"。
// 池是第一道过滤 + 翻译层——Sizing 对着池容量决策，Placement 在池内优化。

package scheduler

import (
	"sort"
	"sync"
	"time"
)

// ─── PoolInfo ────────────────────────────────────────────────────

// PoolInfo 节点池的资源聚合视图。
type PoolInfo struct {
	Name      string            // 池名
	Labels    map[string]string // 匹配该池的 node labels
	UpdatedAt time.Time         // 最后计算时间（从 node label 或 GPU product 推导）
	Nodes     []*NodeInfo       // 池内节点列表
	CPU       PoolResource      // CPU 聚合
	Memory    PoolResource      // Memory 聚合
	GPU       PoolResource      // GPU 聚合（整卡计数）
	Score     float64           // 池健康度 (0-1)，调度权重基准
}

// PoolResource 池级资源聚合。
type PoolResource struct {
	Total        int64   // 总可分配资源
	Used         int64   // 已使用
	Util         float64 // 利用率 0-1
	FragmentRate float64 // 碎片率 0-1（1=完美连续，0=全碎）
	Effective    int64   // 有效容量（扣除碎片）
}

// ─── 池聚合计算 ──────────────────────────────────────────────────

// ComputePoolUtilization 按池聚合节点利用率。
// 池定义：从 NodeInfo.GPU product 推导。无 GPU 节点归入 "cpu" 池。
func ComputePoolUtilization(pods []*PodInfo, nodes []*NodeInfo) []*PoolInfo {
	// 按池名分组节点
	pools := make(map[string][]*NodeInfo)
	for _, n := range nodes {
		poolName := poolNameForNode(n)
		pools[poolName] = append(pools[poolName], n)
	}

	// 统计每个节点上的已分配资源
	usageMap := make(map[string]nodeUsage)
	for _, p := range pods {
		if p.Phase == "Running" && p.NodeName != "" {
			u := usageMap[p.NodeName]
			u.CPU += p.Requests.CPU
			u.Memory += p.Requests.Memory
			u.GPU += p.Requests.GPU
			usageMap[p.NodeName] = u
		}
	}

	// 按池聚合
	result := make([]*PoolInfo, 0, len(pools))
	for name, poolNodes := range pools {
		pi := &PoolInfo{Name: name, Nodes: poolNodes, Labels: poolNodes[0].Labels}

		for _, n := range poolNodes {
			u := usageMap[n.Name]

			// CPU
			pi.CPU.Total += n.AllocatableCPU
			pi.CPU.Used += u.CPU

			// Memory
			pi.Memory.Total += n.AllocatableMemory
			pi.Memory.Used += u.Memory

			// GPU
			gpuCount := int64(len(n.GPU))
			pi.GPU.Total += gpuCount * MilliGPUUnit
			pi.GPU.Used += u.GPU
		}

		// 利用率
		if pi.CPU.Total > 0 {
			pi.CPU.Util = float64(pi.CPU.Used) / float64(pi.CPU.Total)
		}
		if pi.Memory.Total > 0 {
			pi.Memory.Util = float64(pi.Memory.Used) / float64(pi.Memory.Total)
		}
		if pi.GPU.Total > 0 {
			pi.GPU.Util = float64(pi.GPU.Used) / float64(pi.GPU.Total)
		}

		// 碎片率 & 有效容量（复用 usageMap，不重复遍历 Pod）
		pi.CPU.FragmentRate, pi.CPU.Effective = poolFragmentRate(poolNodes, usageMap, "cpu")
		pi.Memory.FragmentRate, pi.Memory.Effective = poolFragmentRate(poolNodes, usageMap, "memory")
		if pi.GPU.Total > 0 {
			pi.GPU.FragmentRate, pi.GPU.Effective = poolGPUFragmentRate(poolNodes, pods)
		}

		// PoolScore: 加权(1-最大利用率, 1-碎片率)
		maxUtil := max(pi.CPU.Util, pi.Memory.Util, pi.GPU.Util)
		avgFrag := pi.CPU.FragmentRate
		if pi.GPU.Total > 0 {
			avgFrag = (pi.CPU.FragmentRate + pi.GPU.FragmentRate) / 2
		}
		pi.UpdatedAt = time.Now()
		pi.Score = (1-maxUtil)*0.5 + avgFrag*0.5
		if pi.Score < 0 {
			pi.Score = 0
		}

		result = append(result, pi)
	}

	// 按 Score 升序（低分池优先处理）
	sort.Slice(result, func(i, j int) bool {
		return result[i].Score < result[j].Score
	})

	return result
}

// poolNameForNode 从节点推断所属池名。
// 优先用 kubepivot.io/pool label，无则用 GPU product，再无则 "cpu"。
func poolNameForNode(n *NodeInfo) string {
	if n.Labels != nil {
		if pool, ok := n.Labels["kubepivot.io/pool"]; ok && pool != "" {
			return pool
		}
	}
	if len(n.GPU) > 0 && n.GPU[0].Product != "" {
		return "gpu-" + n.GPU[0].Product
	}
	return "cpu"
}

// ─── 池级不平衡检测 ──────────────────────────────────────────────

// PoolImbalancePair 池不平衡对。
type PoolImbalancePair struct {
	High *PoolInfo // 高负载池
	Low  *PoolInfo // 低负载池
}

// DetectPoolImbalance 检测池间不平衡。
// 策略：CPU/Mem/GPU 任一利用率超过均值 1.2 倍为"高"，全部低于 0.8 倍为"低"。
func DetectPoolImbalance(pools []*PoolInfo) []*PoolImbalancePair {
	if len(pools) < 2 {
		return nil
	}

	var totalCPUUtil, totalMemUtil, totalGPUUtil float64
	gpuPools := 0
	for _, p := range pools {
		totalCPUUtil += p.CPU.Util
		totalMemUtil += p.Memory.Util
		if p.GPU.Total > 0 {
			totalGPUUtil += p.GPU.Util
			gpuPools++
		}
	}
	avgCPU := totalCPUUtil / float64(len(pools))
	avgMem := totalMemUtil / float64(len(pools))
	avgGPU := 0.0
	if gpuPools > 0 {
		avgGPU = totalGPUUtil / float64(gpuPools)
	}

	var highs, lows []*PoolInfo
	for _, p := range pools {
		high := p.CPU.Util > avgCPU*1.2 || p.Memory.Util > avgMem*1.2
		if gpuPools > 0 && p.GPU.Total > 0 && p.GPU.Util > avgGPU*1.2 {
			high = true
		}
		if high {
			highs = append(highs, p)
		}
		low := p.CPU.Util < avgCPU*0.8 && p.Memory.Util < avgMem*0.8
		if gpuPools > 0 && p.GPU.Total > 0 && p.GPU.Util >= avgGPU*0.8 {
			low = false
		}
		if low {
			lows = append(lows, p)
		}
	}

	if len(highs) == 0 || len(lows) == 0 {
		return nil
	}

	pairs := make([]*PoolImbalancePair, 0)
	for _, high := range highs {
		if len(lows) == 0 {
			break
		}
		low := lows[0]
		pairs = append(pairs, &PoolImbalancePair{High: high, Low: low})
	}

	return pairs
}

// ─── 碎片率计算 ──────────────────────────────────────────────────

// poolFragmentRate 计算池内 CPU/Memory 的碎片率。
// usageMap 由调用方预建（一次 O(Np)），本函数只走 O(Nn) 节点循环。
// 碎片率 = 1 - (最大节点剩余/池总剩余)，1=无碎片, 0=全碎。
// CAP 语义：最终一致——usageMap 是上次 scan 的快照，不实时。
func poolFragmentRate(nodes []*NodeInfo, usageMap map[string]nodeUsage, resource string) (float64, int64) {
	var totalRemain, maxRemain int64
	for _, n := range nodes {
		u := usageMap[n.Name]
		var remain int64
		if resource == "cpu" {
			remain = n.AllocatableCPU - u.CPU
		} else {
			remain = n.AllocatableMemory - u.Memory
		}
		if remain < 0 {
			remain = 0
		}
		totalRemain += remain
		if remain > maxRemain {
			maxRemain = remain
		}
	}

	if totalRemain == 0 {
		return 1.0, 0 // 全满 → 无碎片
	}

	ratio := 1.0 - float64(maxRemain)/float64(totalRemain)
	return 1.0 - ratio, maxRemain
}

// poolGPUFragmentRate 计算池内 GPU 碎片率（整卡粒度）。
func poolGPUFragmentRate(nodes []*NodeInfo, pods []*PodInfo) (float64, int64) {
	// 简化：统计各节点 GPU 占用比例
	var totalFree, maxContiguous int64
	for _, n := range nodes {
		if len(n.GPU) == 0 {
			continue
		}
		healthy := int64(0)
		for _, g := range n.GPU {
			if g.Health == "Healthy" {
				healthy++
			}
		}

		// 统计每个 NVLink domain 的连续空闲数
		domainFree := make(map[int]int64)
		for _, g := range n.GPU {
			if g.Health == "Healthy" {
				domainFree[g.NVLinkDomain]++
			}
		}
		for _, free := range domainFree {
			totalFree += free
			if free > maxContiguous {
				maxContiguous = free
			}
		}
		_ = healthy
	}

	if totalFree == 0 {
		return 1.0, 0
	}
	ratio := 1.0 - float64(maxContiguous)/float64(totalFree)
	return 1.0 - ratio, maxContiguous
}

func max(vals ...float64) float64 {
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

// ─── PoolUtilCache ──────────────────────────────────────────────

// PoolUtilCache 基于 PodCache.Generation 的池利用率缓存。
// Generation 不变 → 直接返回上次计算结果（稳定集群 95%+ scan 跳过重算）。
// CAP 语义：最终一致——缓存可能稍旧于最新 delta，但 5min ticker 会追上。
type PoolUtilCache struct {
	mu  sync.RWMutex
	gen int64
	res []*PoolInfo
}

// GetOrCompute 如果 generation 未变，返回缓存结果；否则重新计算。
func (c *PoolUtilCache) GetOrCompute(gen int64, pods []*PodInfo, nodes []*NodeInfo) []*PoolInfo {
	c.mu.RLock()
	if gen == c.gen && c.res != nil {
		r := c.res
		c.mu.RUnlock()
		return r
	}
	c.mu.RUnlock()

	r := ComputePoolUtilization(pods, nodes)

	c.mu.Lock()
	c.gen = gen
	c.res = r
	c.mu.Unlock()
	return r
}

// ─── GPU 迁移可行性校验 ─────────────────────────────────────

// CanMigrateGPU checks if a GPU pod can safely migrate from source to target node.
// Returns false if driver or CUDA capability mismatch would cause CrashLoop.
// This is a pre-flight check — Rescheduler should call before eviction.
func CanMigrateGPU(source, target *NodeInfo) bool {
	if len(source.GPU) == 0 || len(target.GPU) == 0 {
		return false
	}
	src := source.GPU[0]
	tgt := target.GPU[0]

	// Same product → compatible (e.g. A100-SXM4-80GB ↔ A100-SXM4-80GB)
	if src.Product == tgt.Product {
		return true
	}
	// Cross-generation: H100 can run A100 workloads (CUDA backward compat)
	// A100 cannot run H100 workloads (missing compute capability 9.0)
	if tgt.Product == "H100" && src.Product == "A100" {
		return false // H100 code won't run on A100
	}
	// Same generation, different variant (e.g. A100-SXM vs A100-PCIe)
	if tgt.Product[:4] == src.Product[:4] {
		return true
	}
	return false
}

// ─── 池自动聚类 ──────────────────────────────────────────────

// AutoDiscoverPool assigns a node to a pool based on its resource profile.
// Falls back to explicit label (kubepivot.io/pool) if present.
// Otherwise clusters by GPU product → compute/memory ratio → "cpu" default.
func AutoDiscoverPool(n *NodeInfo) string {
	// 1. Explicit label wins (user override)
	if n.Labels != nil {
		if pool, ok := n.Labels["kubepivot.io/pool"]; ok && pool != "" {
			return pool
		}
	}
	// 2. GPU product → "gpu-<product>" pool
	if len(n.GPU) > 0 && n.GPU[0].Product != "" {
		return "gpu-" + n.GPU[0].Product
	}
	// 3. Resource profile clustering
	if n.AllocatableMemory > 0 {
		ratio := float64(n.AllocatableMemory) / float64(n.AllocatableCPU)
		if ratio > 12 { // >12 GB/core → memory-optimized
			return "memory"
		}
	}
	return "cpu"
}

// ─── Deployment Pod label 匹配 ──────────────────────────────

// MatchPodByLabels checks if two PodInfo share a common Deployment label.
// Used for MigrationTargetHint to match Deployment pods after rename.
// Returns true if they share app.kubernetes.io/name or app label.
func MatchPodByLabels(a, b *PodInfo) bool {
	if a.Labels == nil || b.Labels == nil {
		return false
	}
	if a.Namespace != b.Namespace {
		return false // cross-ns pods are never the same Deployment
	}
	// Match by app.kubernetes.io/name (preferred) or app label
	if name, ok := a.Labels["app.kubernetes.io/name"]; ok {
		if bName, ok2 := b.Labels["app.kubernetes.io/name"]; ok2 && name == bName {
			return true
		}
	}
	if name, ok := a.Labels["app"]; ok {
		if bName, ok2 := b.Labels["app"]; ok2 && name == bName {
			return true
		}
	}
	return false
}
