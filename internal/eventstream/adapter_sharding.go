package eventstream

import (
	"github.com/Ixecd/kubepivot/internal/sharding"
)

// ─── v2.5 sharding 适配 ────────────────────────────────────────────
//
// 桥接 internal/sharding/ 包的 *sharding.ShardSet（v2.5）
// 到本包定义的 ShardSet 接口（eventstream）。
//
// 为什么需要适配层：
//
//   1. v2.5 sharding 是"无状态分片索引集合"
//      OwnsNamespace(ns, totalShards) 需要传入 totalShards
//
//   2. eventstream.ShardSet 期望"封装好的"接口
//      Owns(ns) 不需要传 totalShards
//
//   3. 避免 internal/eventstream/ 直接 import internal/sharding/
//      虽然 Go 允许内部包互相 import，但保持依赖单向更清晰
//      此 adapter 是唯一的桥梁文件
//
// 使用场景（典型 cmd/kp 主入口）：
//
//	mgr := sharding.NewMultiLeaseManager(sharding.MultiLeaseConfig{
//	    TotalShards: 10,
//	    Replicas:    3,
//	})
//	mgr.Start(ctx)
//
//	shardSet := eventstream.NewShardSetAdapter(mgr.Shards(), 10)
//	informer, _ := eventstream.NewInformer(ctx, eventstream.InformerOptions{
//	    Resource:     "deployments",
//	    APIVersion:   "apps/v1",
//	    ShardSet:     shardSet,
//	    APIServerURL: ...,
//	})

// shardSetAdapter 把 *sharding.ShardSet 适配为 eventstream.ShardSet 接口。
//
// 持有 totalShards 配置（启动时一次性传入，v2.5 实际语义即静态）。
// totalShards 改变时需重建 adapter。
type shardSetAdapter struct {
	inner       *sharding.ShardSet
	totalShards int
}

// NewShardSetAdapter 创建 v2.5 sharding 到 eventstream.ShardSet 的适配器。
//
// 参数：
//   - inner: v2.5 的 *sharding.ShardSet（通常来自 mgr.Shards()）
//   - totalShards: 总分片数（与 v2.5 MultiLeaseConfig.TotalShards 保持一致）
//
// 返回 nil 表示参数无效（inner=nil 或 totalShards <= 0）。
//
// 注意：
//   - inner 由调用方持有所有权，adapter 仅借用引用
//   - inner 的并发安全由 sharding.ShardSet 内部 RWMutex 保证
//   - 不会修改 inner 状态
//
// 关于 shard 变化的通知：
//   v2.5 通过 MultiLeaseConfig.OnShardChanged 回调通知 shard 变化
//   v2.7.0 当前实现：adapter 不订阅此 callback
//   shard 变化时漏事件由 resync ticker 周期性兜底（默认 30min）
//   v2.7.1+ 计划：在此 adapter 中订阅 OnShardChanged，触发 EventResync
func NewShardSetAdapter(inner *sharding.ShardSet, totalShards int) ShardSet {
	if inner == nil || totalShards <= 0 {
		return nil
	}
	return &shardSetAdapter{
		inner:       inner,
		totalShards: totalShards,
	}
}

// Owns 判定指定 namespace 是否归属本 pod 的 shard。
//
// 实现：直接调用 v2.5 的 OwnsNamespace。
// hash 算法（FNV-1a 32-bit）由 v2.5 提供，本适配器不重复计算。
//
// 性能：sharding.ShardSet.OwnsNamespace 持有 RWMutex 读锁
//       高频调用（每个事件一次）通常不是瓶颈
//       如未来成为热点，考虑在 adapter 中缓存 hash 结果
func (a *shardSetAdapter) Owns(ns string) bool {
	return a.inner.OwnsNamespace(ns, a.totalShards)
}

// staticShardSet 用于测试 / 简单场景的固定 shard 实现。
//
// 不依赖 v2.5 sharding，直接由调用方指定"哪些 ns 归属本 pod"。
// 主要用途：
//   - 单测：模拟 ShardSet 行为
//   - 单 pod 场景（不分片）：所有 ns 都归本 pod
type staticShardSet struct {
	allNamespaces bool
	owned         map[string]struct{}
}

// NewStaticShardSet 创建一个静态 shard 集合。
//
// 参数：
//   - namespaces: 归属本 pod 的 namespace 列表
//   - 传 nil 或空 slice → 所有 ns 都归本 pod（单 pod 场景）
//   - 传非空 slice → 仅这些 ns 归本 pod（明确指定）
func NewStaticShardSet(namespaces []string) ShardSet {
	if len(namespaces) == 0 {
		return &staticShardSet{allNamespaces: true}
	}
	owned := make(map[string]struct{}, len(namespaces))
	for _, ns := range namespaces {
		owned[ns] = struct{}{}
	}
	return &staticShardSet{owned: owned}
}

// Owns 检查 namespace 是否归属。
func (s *staticShardSet) Owns(ns string) bool {
	if s.allNamespaces {
		return true
	}
	_, ok := s.owned[ns]
	return ok
}
