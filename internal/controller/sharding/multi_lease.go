package sharding

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Ixecd/kubepivot/internal/controller"
)

// MultiLeaseManager 管理 N 个分片 lease 的抢占 + 续约
//
// 工作流：
//   1. 启动后周期性扫描所有 N 个 shard lease
//   2. 对每个 lease 尝试抢占 / 续约
//   3. 配额限制：本 pod 持有的 lease 数 ≤ quota（避免独吞）
//   4. 已持有的 shard 维护在 ShardSet 中，供 reconcile 路径查询
//   5. 失去 lease 时（被别的 pod 抢走）从 ShardSet 移除
//
// 复用 v2.4.0 controller.TryAcquireOrRenew + GenerateIdentity（lease.go 导出）
type MultiLeaseManager struct {
	totalShards   int
	quota         int
	leasePrefix   string // "kubepivot-controller-shard-" 默认
	namespace     string // "kubepivot-system"
	ttl           time.Duration
	identity      string
	kubeconfig    string
	checkInterval time.Duration

	shards *ShardSet // 当前持有的 shard 集合（线程安全）

	// 通知 channel：分片增删时通知调用方
	// 调用方可以监听这个 channel 来触发"清理孤儿状态机"等动作
	mu              sync.Mutex
	onShardChanged  func(added, removed []int)
}

// MultiLeaseConfig 配置参数
type MultiLeaseConfig struct {
	TotalShards    int                              // KUBEPIVOT_SHARDS（默认 10）
	Replicas       int                              // controller deployment 副本数（默认 3）
	LeasePrefix    string                           // "kubepivot-controller-shard-"
	Namespace      string                           // "kubepivot-system"
	TTL            time.Duration                    // 15s（与 leader lease 一致）
	Kubeconfig     string                           // 通常空字符串（用 in-cluster config）
	OnShardChanged func(added, removed []int)       // 分片变化回调
}

// NewMultiLeaseManager 构造分片 lease 管理器
func NewMultiLeaseManager(cfg MultiLeaseConfig) *MultiLeaseManager {
	if cfg.TotalShards <= 0 {
		cfg.TotalShards = 10
	}
	if cfg.LeasePrefix == "" {
		cfg.LeasePrefix = "kubepivot-controller-shard-"
	}
	if cfg.Namespace == "" {
		cfg.Namespace = "kubepivot-system"
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 15 * time.Second
	}

	checkInterval := cfg.TTL / 3
	if checkInterval < 2*time.Second {
		checkInterval = 2 * time.Second
	}

	quota := QuotaPerPod(cfg.TotalShards, cfg.Replicas)

	return &MultiLeaseManager{
		totalShards:    cfg.TotalShards,
		quota:          quota,
		leasePrefix:    cfg.LeasePrefix,
		namespace:      cfg.Namespace,
		ttl:            cfg.TTL,
		identity:       controller.GenerateIdentity(),
		kubeconfig:     cfg.Kubeconfig,
		checkInterval:  checkInterval,
		shards:         NewShardSet(),
		onShardChanged: cfg.OnShardChanged,
	}
}

// Shards 返回当前持有的 shard 集合（用于 reconcile 路径判断归属）
func (m *MultiLeaseManager) Shards() *ShardSet {
	return m.shards
}

// Identity 返回本实例的 identity（用于日志展示）
func (m *MultiLeaseManager) Identity() string {
	return m.identity
}

// Run 启动 lease 管理主循环，阻塞直到 ctx 取消
//
// 每 checkInterval（约 ttl/3）扫一遍所有 shard：
//   - 已持有的 → 续约
//   - 未持有但有空 quota → 尝试抢占
//   - 已 quota 满 → 跳过未持有的
func (m *MultiLeaseManager) Run(ctx context.Context) {
	slog.Info("🧩 MultiLeaseManager 启动",
		"total_shards", m.totalShards,
		"quota_per_pod", m.quota,
		"identity", m.identity,
		"ttl", m.ttl,
		"check_interval", m.checkInterval,
	)

	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	// 立刻先跑一轮，避免首次抢占被 ttl/3 间隔拖慢
	m.reconcileShards(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("🧩 MultiLeaseManager 退出", "identity", m.identity)
			return
		case <-ticker.C:
			m.reconcileShards(ctx)
		}
	}
}

// reconcileShards 单轮扫描：续约已持有 + 抢占未持有
func (m *MultiLeaseManager) reconcileShards(ctx context.Context) {
	beforeShards := m.shards.List()
	beforeSet := make(map[int]struct{}, len(beforeShards))
	for _, s := range beforeShards {
		beforeSet[s] = struct{}{}
	}

	// Phase 1：续约已持有的 shard（优先级高，先做）
	for _, shardIdx := range beforeShards {
		leaseName := m.leaseName(shardIdx)
		held, err := controller.TryAcquireOrRenew(
			ctx, leaseName, m.namespace, m.identity, m.ttl, m.kubeconfig,
		)
		if err != nil {
			slog.Warn("续约 shard lease 失败", "shard", shardIdx, "err", err)
			// 续约失败保留状态，等下轮再试
			continue
		}
		if !held {
			// 失去了 lease（被别的 pod 抢走或自己 ttl 超时）
			slog.Warn("失去 shard lease", "shard", shardIdx)
			m.shards.Remove(shardIdx)
		}
	}

	// Phase 2：当前持有数 < quota → 尝试抢新的
	currentSize := m.shards.Size()
	for shardIdx := 0; shardIdx < m.totalShards && currentSize < m.quota; shardIdx++ {
		if m.shards.Contains(shardIdx) {
			continue // 已持有，跳过
		}

		leaseName := m.leaseName(shardIdx)
		acquired, err := controller.TryAcquireOrRenew(
			ctx, leaseName, m.namespace, m.identity, m.ttl, m.kubeconfig,
		)
		if err != nil {
			slog.Debug("抢占 shard lease 失败（可能被其他 pod 持有）",
				"shard", shardIdx, "err", err)
			continue
		}
		if acquired {
			slog.Info("🎯 已抢占 shard lease", "shard", shardIdx, "identity", m.identity)
			m.shards.Add(shardIdx)
			currentSize++
		}
	}

	// Phase 3：通知调用方 shard 变化
	afterShards := m.shards.List()
	afterSet := make(map[int]struct{}, len(afterShards))
	for _, s := range afterShards {
		afterSet[s] = struct{}{}
	}

	added := []int{}
	removed := []int{}
	for s := range afterSet {
		if _, ok := beforeSet[s]; !ok {
			added = append(added, s)
		}
	}
	for s := range beforeSet {
		if _, ok := afterSet[s]; !ok {
			removed = append(removed, s)
		}
	}

	if len(added) > 0 || len(removed) > 0 {
		slog.Info("🧩 shard 持有变化",
			"added", added,
			"removed", removed,
			"current", afterShards,
			"identity", m.identity,
		)
		m.mu.Lock()
		callback := m.onShardChanged
		m.mu.Unlock()
		if callback != nil {
			go callback(added, removed) // 异步通知避免阻塞主循环
		}
	}
}

// leaseName 生成第 idx 个 shard 的 lease 名
func (m *MultiLeaseManager) leaseName(idx int) string {
	return fmt.Sprintf("%s%d", m.leasePrefix, idx)
}
