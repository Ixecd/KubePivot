// internal/scheduler/rescheduler.go
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
)

// PodAssigner 定义了为单个 Pod 实时分配节点的能力。
// 这是 Rescheduler 对调度器的唯一依赖，便于注入 mock 进行测试。
type PodAssigner interface {
	AssignPod(ctx context.Context, pod *PodInfo) (string, error)
}

// nodeUsage 节点当前已分配资源总量
type nodeUsage struct {
	CPU    int64 // 毫核
	Memory int64 // 字节
	GPU    int64 // v3.2: 毫卡
}

// nodeUtilInfo 节点利用率信息
type nodeUtilInfo struct {
	Node       *NodeInfo
	Usage      nodeUsage
	CPUUtil    float64 // 0.0 - 1.0
	MemoryUtil float64
	GPUUtil    float64 // v3.2: GPU 利用率
}

// imbalancePair 不平衡节点对：从高负载节点迁移 Pod 到低负载节点
type imbalancePair struct {
	High *nodeUtilInfo
	Low  *nodeUtilInfo
	// 建议迁移的资源量（毫核/字节），取高负载节点超出平均的部分与低负载节点剩余容量的较小值
	SuggestedCPU    int64
	SuggestedMemory int64
}

// ReschedulerConfig 重调度器配置
type ReschedulerConfig struct {
	Interval         time.Duration // 周期性扫描间隔（默认 5min）
	MaxMigrations    int           // 单次最多迁移数（0 使用默认 5%）
	JitterWindow     time.Duration // 抖动检测窗口（默认 5min）
	JitterThreshold  float64       // CPU 利用率阈值（默认 0.95）
	JitterSpikeCount int           // 窗口内允许的最大 spike 次数（默认 3）
}

// Rescheduler 负责运行时重调度。
// 它周期性扫描集群利用率，检测不平衡，并在满足约束时迁移 Pod。
type Rescheduler struct {
	assigner PodAssigner // 依赖接口而非具体类型
	pods     PodLister
	nodes    NodeLister

	// 配置
	interval      time.Duration // 周期性扫描间隔（默认 5min）
	maxMigrations int           // 单次重调度最多迁移的 Pod 数
	degradedLevel int           // 当前降级层级 (0-3)

	// 抖动检测与降级
	jitterWindow     time.Duration // 抖动检测窗口（默认 5min）
	jitterThreshold  float64       // 触发抖动的 CPU 利用率阈值（默认 0.95）
	jitterSpikeCount int           // 窗口内允许的最大 spike 次数（默认 3）
	recentSpikes     []time.Time   // 最近 spike 时间戳
	lastOOM          time.Time     // 最近 OOM 时间
	degradedUntil    time.Time     // 降级结束时间，之后自动恢复正常级别

	// 测试注入点（函数变量模式，与 KubePivot 工程惯例一致）
	evictPodFunc func(ctx context.Context, pod *PodInfo) error

	mu sync.Mutex
}

// NewRescheduler 创建重调度器
func NewRescheduler(assigner PodAssigner, pods PodLister, nodes NodeLister, cfg ReschedulerConfig) *Rescheduler {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.MaxMigrations <= 0 {
		cfg.MaxMigrations = 5 // 安全默认值
	}
	if cfg.JitterWindow <= 0 {
		cfg.JitterWindow = 5 * time.Minute
	}
	if cfg.JitterThreshold <= 0 {
		cfg.JitterThreshold = 0.95
	}
	if cfg.JitterSpikeCount <= 0 {
		cfg.JitterSpikeCount = 3
	}
	return &Rescheduler{
		assigner:         assigner,
		pods:             pods,
		nodes:            nodes,
		interval:         cfg.Interval,
		maxMigrations:    cfg.MaxMigrations,
		jitterWindow:     cfg.JitterWindow,
		jitterThreshold:  cfg.JitterThreshold,
		jitterSpikeCount: cfg.JitterSpikeCount,
		evictPodFunc:     defaultEvictPod,
	}
}

// Start 启动周期性重调度循环。在独立 goroutine 中运行。
func (rs *Rescheduler) Start(ctx context.Context) {
	slog.Info("乾枢重调度器已启动", "interval", rs.interval)
	ticker := time.NewTicker(rs.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("重调度器收到退出信号")
			return
		case <-ticker.C:
			rs.run(ctx)
		}
	}
}

// RunOnce 执行单次重调度扫描（供 CLI 手动触发）。
func (rs *Rescheduler) RunOnce(ctx context.Context) {
	rs.run(ctx)
}

// run 执行一次重调度扫描
func (rs *Rescheduler) run(ctx context.Context) {
	rs.mu.Lock()
	level := rs.degradedLevel
	// 如果处于 Level 3（用户暂停），直接返回
	if level >= 3 {
		rs.mu.Unlock()
		slog.Warn("重调度已暂停：当前降级层级为 Level 3，等待用户介入")
		return
	}
	rs.mu.Unlock()

	pods, err := rs.pods.ListAllPods(ctx)
	if err != nil {
		slog.Error("重调度：获取 Pod 列表失败", "err", err)
		return
	}
	nodes, err := rs.nodes.ListAllNodes(ctx)
	if err != nil {
		slog.Error("重调度：获取 Node 列表失败", "err", err)
		return
	}

	if len(nodes) == 0 || len(pods) == 0 {
		return
	}

	// 计算节点利用率
	nodeUtil := rs.computeNodeUtilization(pods, nodes)

	// 在 run 方法中，在 computeNodeUtilization 之后，添加：
	for _, u := range nodeUtil {
		GetSchedulerMetrics().SetNodeUtilCPU(u.Node.Name, u.CPUUtil)
		GetSchedulerMetrics().SetNodeUtilMem(u.Node.Name, u.MemoryUtil)
	}

	// 先进行抖动检测，可能会自动调整降级层级
	rs.detectJitter(nodeUtil)

	// 根据当前降级层级调整重调度策略
	rs.mu.Lock()
	currentLevel := rs.degradedLevel
	rs.mu.Unlock()

	switch currentLevel {
	case 0:
		// 正常模式，执行完整重调度
	case 1:
		// 观察模式：降低迁移数量
		rs.maxMigrations = rs.maxMigrations / 2
		if rs.maxMigrations < 1 {
			rs.maxMigrations = 1
		}
		slog.Info("重调度进入观察模式，降低迁移量", "maxMigrations", rs.maxMigrations)
	case 2:
		// 紧急降级：停止迁移，只记录日志
		slog.Warn("重调度进入紧急降级模式，停止所有迁移")
		return
	}

	// 检测不平衡并迁移
	imbalanced := rs.detectImbalance(nodeUtil)
	if len(imbalanced) == 0 {
		slog.Debug("重调度：集群利用率均衡，无需迁移")
		return
	}
	rs.migratePods(ctx, imbalanced, pods)
}

// computeNodeUtilization 计算每个节点的当前资源利用率
func (rs *Rescheduler) computeNodeUtilization(pods []*PodInfo, nodes []*NodeInfo) []*nodeUtilInfo {
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

	// 计算利用率
	utils := make([]*nodeUtilInfo, 0, len(nodes))
	for _, n := range nodes {
		u := usageMap[n.Name]
		gpuTotal := int64(len(n.GPU)) * MilliGPUUnit
		gpuUtil := 0.0
		if gpuTotal > 0 {
			gpuUtil = float64(u.GPU) / float64(gpuTotal)
		}
		info := &nodeUtilInfo{
			Node:       n,
			Usage:      u,
			CPUUtil:    float64(u.CPU) / float64(n.AllocatableCPU),
			MemoryUtil: float64(u.Memory) / float64(n.AllocatableMemory),
			GPUUtil:    gpuUtil,
		}
		utils = append(utils, info)
	}
	return utils
}

// detectImbalance 检测不平衡的节点对
// 策略：CPU 或 Memory 利用率超过平均值的 1.2 倍为“高”，低于平均值的 0.8 倍为“低”
func (rs *Rescheduler) detectImbalance(utils []*nodeUtilInfo) []*imbalancePair {
	if len(utils) < 2 {
		return nil
	}

	// 计算平均利用率
	var totalCPU, totalMem float64
	for _, u := range utils {
		totalCPU += u.CPUUtil
		totalMem += u.MemoryUtil
	}
	avgCPU := totalCPU / float64(len(utils))
	avgMem := totalMem / float64(len(utils))

	// 分类节点
	var highs, lows []*nodeUtilInfo
	for _, u := range utils {
		// 高负载：任一维度超过平均值的 1.2 倍
		if u.CPUUtil > avgCPU*1.2 || u.MemoryUtil > avgMem*1.2 {
			highs = append(highs, u)
		}
		// 低负载：两个维度都低于平均值的 0.8 倍
		if u.CPUUtil < avgCPU*0.8 && u.MemoryUtil < avgMem*0.8 {
			lows = append(lows, u)
		}
	}

	if len(highs) == 0 || len(lows) == 0 {
		return nil
	}

	// 配对：为每个高负载节点找一个低负载节点
	pairs := make([]*imbalancePair, 0)
	for _, high := range highs {
		// 跳过没有低负载节点的情况
		if len(lows) == 0 {
			break
		}
		low := lows[0]
		// 计算建议迁移量：高负载节点超出的资源与低负载节点剩余容量的较小值
		excessCPU := int64(float64(high.Node.AllocatableCPU) * (high.CPUUtil - avgCPU))
		excessMem := int64(float64(high.Node.AllocatableMemory) * (high.MemoryUtil - avgMem))
		availCPU := low.Node.AllocatableCPU - low.Usage.CPU
		availMem := low.Node.AllocatableMemory - low.Usage.Memory

		sugCPU := excessCPU
		if sugCPU > availCPU {
			sugCPU = availCPU
		}
		sugMem := excessMem
		if sugMem > availMem {
			sugMem = availMem
		}

		if sugCPU > 0 || sugMem > 0 {
			pairs = append(pairs, &imbalancePair{
				High:            high,
				Low:             low,
				SuggestedCPU:    sugCPU,
				SuggestedMemory: sugMem,
			})
		}
	}
	return pairs
}

// isMigratable 判断 Pod 是否允许迁移
// 不可迁移的情况：StatefulSet 管理的 Pod、蓝绿部署期间的 Pod
func isMigratable(pod *PodInfo) bool {
	if pod.Labels == nil {
		return true
	}
	// StatefulSet 管理的 Pod 通常有 controller-revision-hash 标签，但不唯一；
	// 更准确是通过 ownerReferences 判断，但我们目前只有 Labels，使用 statefulset.kubernetes.io/pod-name 标签判断
	if _, ok := pod.Labels["statefulset.kubernetes.io/pod-name"]; ok {
		return false
	}
	// 蓝绿部署：KubePivot 可能会添加特定标签，这里假设标签 "kubepivot.io/blue-green-locked" = "true"
	if v, ok := pod.Labels["kubepivot.io/blue-green-locked"]; ok && v == "true" {
		return false
	}

	// v3.1: GPU Pod 粘滞策略 — 默认不迁移
	// GPU 训练任务迁移意味着 preemption → checkpoint → restore，
	// 对大部分训练框架是不可恢复的中断。
	if pod.Requests.GPU > 0 {
		// 唯一例外：GPU 硬件故障（DCGM Xid 48/61/94 等）
		// 死在坏卡上比继续跑更糟 → 强制驱逐
		if pod.Labels["kubepivot.io/gpu-hardware-failure"] == "true" {
			return true
		}
		return false
	}

	// v3.2 候选：支持 checkpoint-aware GPU 迁移

	return true
}

// migratePods 执行 Pod 迁移，并返回成功迁移的 Pod 数量
func (rs *Rescheduler) migratePods(ctx context.Context, pairs []*imbalancePair, allPods []*PodInfo) int {
	maxMigrate := rs.maxMigrations
	if maxMigrate <= 0 {
		totalPods := len(allPods)
		maxMigrate = totalPods * 5 / 100
		if maxMigrate < 1 {
			maxMigrate = 1
		}
	}

	migrated := 0
	for _, pair := range pairs {
		if migrated >= maxMigrate {
			slog.Info("已达到单次迁移上限", "migrated", migrated, "max", maxMigrate)
			break
		}

		for _, p := range allPods {
			// 只考虑位于高负载节点上的、可迁移的 Running Pod
			if p.Phase != "Running" || p.NodeName != pair.High.Node.Name || !isMigratable(p) {
				continue
			}
			// 确保 Pod 资源需求在建议迁移量范围内
			if p.Requests.CPU > pair.SuggestedCPU || p.Requests.Memory > pair.SuggestedMemory {
				continue
			}

			// 1. 调用调度器尝试将 Pod 分配到低负载节点
			node, err := rs.assigner.AssignPod(ctx, p)
			if err != nil {
				slog.Warn("迁移失败，无法分配 Pod 到目标节点", "pod", p.Namespace+"/"+p.Name, "targetNode", pair.Low.Node.Name, "err", err)
				continue
			}

			// 1.5. 注入迁移目标节点 hint — webhook 收到重建 Pod 时直接路由，防止回弹到源节点
			SetMigrationTargetHint(p.Namespace, p.Name, node)

			// 2. 驱逐 Pod（K8s 重建 + Webhook 注入目标节点）
			oldNode := p.NodeName
			if err := rs.evictPodFunc(ctx, p); err != nil {
				slog.Warn("驱逐 Pod 失败，清理目标 hint", "pod", p.Namespace+"/"+p.Name, "err", err)
				PopMigrationTargetHint(p.Namespace, p.Name) // 驱逐失败，清理无用的 hint
				continue
			}

			slog.Info("重调度：驱逐 Pod",
				"pod", p.Namespace+"/"+p.Name,
				"from", oldNode,
				"targetNode", node,
				"reason", "imbalance",
			)
			migrated++
			break // 每个高负载节点对只迁移一个 Pod，控制粒度
		}
	}

	if migrated > 0 {
		slog.Info("重调度完成", "migratedPods", migrated)
	}
	return migrated
}

// DegradedLevel 返回当前降级层级
func (rs *Rescheduler) DegradedLevel() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if time.Now().Before(rs.degradedUntil) {
		return rs.degradedLevel
	}
	return 0 // 降级时间窗口已过，自动恢复
}

// Pause 紧急暂停所有自动重调度（Level 3）
func (rs *Rescheduler) Pause() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.degradedLevel = 3
	rs.degradedUntil = time.Now().Add(30 * time.Minute) // 暂停至少30分钟，等用户手动介入
	slog.Warn("重调度已暂停（Level 3），等待用户介入")
}

// Resume 用户手动恢复重调度
func (rs *Rescheduler) Resume() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.degradedLevel = 0
	rs.degradedUntil = time.Time{}
	rs.recentSpikes = nil
	slog.Info("重调度已手动恢复")
}

// detectJitter 基于近期负载检测抖动，并自动调整降级层级
// 返回当前层级
func (rs *Rescheduler) detectJitter(utils []*nodeUtilInfo) int {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	now := time.Now()

	// 清理过期的 spike 记录
	cutoff := now.Add(-rs.jitterWindow)
	validSpikes := make([]time.Time, 0)
	for _, ts := range rs.recentSpikes {
		if ts.After(cutoff) {
			validSpikes = append(validSpikes, ts)
		}
	}
	rs.recentSpikes = validSpikes

	// 检测当前负载 spikes
	for _, u := range utils {
		if u.CPUUtil > rs.jitterThreshold || u.MemoryUtil > rs.jitterThreshold {
			rs.recentSpikes = append(rs.recentSpikes, now)
		}
	}

	// 判定降级
	spikeCount := len(rs.recentSpikes)
	oomRecently := now.Sub(rs.lastOOM) < rs.jitterWindow

	switch {
	case spikeCount >= rs.jitterSpikeCount*2 || oomRecently:
		// 严重抖动或 OOM
		rs.degradedLevel = int(math.Min(float64(rs.degradedLevel)+1, 3))
		rs.degradedUntil = now.Add(30 * time.Minute)
		slog.Warn("检测到严重抖动，提升降级层级", "level", rs.degradedLevel, "spikes", spikeCount, "oom", oomRecently)
	case spikeCount >= rs.jitterSpikeCount:
		// 轻微抖动，维持现有级别，避免频繁升级
		if rs.degradedLevel < 2 {
			rs.degradedLevel = 1
			rs.degradedUntil = now.Add(15 * time.Minute)
		}
		slog.Warn("检测到资源抖动，进入观察模式", "level", rs.degradedLevel, "spikes", spikeCount)
	default:
		// 窗口内无抖动，逐步降低降级层级
		if rs.degradedLevel > 0 && time.Now().After(rs.degradedUntil) {
			rs.degradedLevel--
			slog.Info("抖动消失，降低降级层级", "level", rs.degradedLevel)
		}
	}

	return rs.degradedLevel
}

// ReportOOM 通知重调度器发生了 OOM 事件
func (rs *Rescheduler) ReportOOM() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.lastOOM = time.Now()
}

// defaultEvictPod 通过 kubectl delete pod 驱逐 Pod，触发 K8s 重建 + Webhook 注目标节点。
var defaultEvictPod = func(ctx context.Context, pod *PodInfo) error {
	exec := executor.GetExecutor()
	_, err := exec.Kubectl(ctx, "",
		"delete", "pod", pod.Name,
		"-n", pod.Namespace,
		"--grace-period=30",
		"--wait=false",
	)
	if err != nil {
		return fmt.Errorf("kubectl delete pod %s/%s: %w", pod.Namespace, pod.Name, err)
	}
	return nil
}
