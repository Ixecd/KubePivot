// Package sharding 实现 v2.5.0 A.1 Controller 分片机制
//
// 设计要点（详见 docs/design/sharding.md，待补）：
//   - 基于 K8s Lease 的 N 个分片选举（复用 v2.4.0 lease 机制）
//   - 每个 pod 同时持有多个分片 lease（通过配额限制避免独吞）
//   - hash(namespace) % N 决定项目归属哪个 shard
//   - FNV-1a 32-bit hash（标准库 hash/fnv）
//   - 分片切换时 task drop（依赖 reconcile 幂等）
//   - 每个 pod 自扫孤儿 machine（无需跨 pod 协调）
package sharding

import (
	"hash/fnv"
	"sort"
	"sync"
)

// ShardOf 计算 namespace 归属的分片索引
//
// 用 FNV-1a 32-bit hash，稳定且分布良好。
// 结果在 [0, totalShards) 之间。
func ShardOf(namespace string, totalShards int) int {
	if totalShards <= 0 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(namespace))
	return int(h.Sum32() % uint32(totalShards))
}

// QuotaPerPod 每个 pod 至多持有的分片数量
//
//	totalShards = N（环境变量 KUBEPIVOT_SHARDS）
//	replicas = controller deployment 副本数
//	返回 ceil(N / replicas)，至少为 1
//
// 示例：
//
//	N=10, replicas=3 → quota = 4（因为 ceil(10/3)=4）
//	N=10, replicas=2 → quota = 5
//	N=3,  replicas=3 → quota = 1
func QuotaPerPod(totalShards, replicas int) int {
	if replicas <= 0 {
		return totalShards
	}
	q := (totalShards + replicas - 1) / replicas
	if q < 1 {
		return 1
	}
	return q
}

// ShardSet 一个 pod 当前持有的分片集合
//
// 线程安全。分片增删通过 Add / Remove；分片归属判断通过 Contains。
// 用于 reconcile 路径快速判断"这个 namespace 是不是我的工作"。
type ShardSet struct {
	mu     sync.RWMutex
	shards map[int]struct{}
}

// NewShardSet 构造空的 ShardSet
func NewShardSet() *ShardSet {
	return &ShardSet{shards: make(map[int]struct{})}
}

// Add 加入一个分片
func (s *ShardSet) Add(idx int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shards[idx] = struct{}{}
}

// Remove 移除一个分片
func (s *ShardSet) Remove(idx int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.shards, idx)
}

// Contains 检查分片是否在集合中
func (s *ShardSet) Contains(idx int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.shards[idx]
	return ok
}

// OwnsNamespace 是 reconcile 路径的便捷方法
// 等价于 ShardSet.Contains(ShardOf(ns, totalShards))
func (s *ShardSet) OwnsNamespace(namespace string, totalShards int) bool {
	return s.Contains(ShardOf(namespace, totalShards))
}

// Size 当前持有的分片数
func (s *ShardSet) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.shards)
}

// List 返回当前持有的分片索引列表（已排序，便于日志展示）
func (s *ShardSet) List() []int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]int, 0, len(s.shards))
	for k := range s.shards {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
