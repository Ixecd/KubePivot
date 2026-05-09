// Package sharding 实现 v2.5.0 A.1 Controller 分片机制
//
// 设计要点（详见 docs/design/sharding.md，待补）：
//   - 基于 K8s Lease 的 N 个分片选举（复用 v2.4.0 lease 机制）
//   - 每个 pod 同时持有多个分片 lease（通过配额限制避免独吞）
//   - jump consistent hash 决定项目归属哪个 shard（v3.3: FNV % N → jump hash）
//   - O(1) 无外部依赖，分布均匀（连续短字符串如 kp-bench-001..050 也不偏）
//   - 分片切换时 task drop（依赖 reconcile 幂等）
//   - 每个 pod 自扫孤儿 machine（无需跨 pod 协调）
package sharding

import (
	"hash/fnv"
	"sort"
	"sync"
)

// ShardOf 计算 namespace 归属的分片索引。
//
// v3.3: Google jump consistent hash (2014)。
// FNV-64a 将 namespace 转换为 uint64 key，jumpHash 均匀映射到 [0, totalShards)。
// vs v3.2 FNV-32a % N: 连续短字符串（kp-bench-001..050）从 ±40% 不均 → ±5% 以内。
func ShardOf(namespace string, totalShards int) int {
	if totalShards <= 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(namespace))
	return jumpHash(h.Sum64(), totalShards)
}

// jumpHash Google jump consistent hash (2014).
//
// 论文: "A Fast, Minimal Memory, Consistent Hash Algorithm" (Lamping & Veach)
// O(1) 计算，零状态，结果在 [0, numBuckets) 均匀分布。
// 相比 FNV % N: 无论 key 分布如何，bucket 分布天然均匀。
func jumpHash(key uint64, numBuckets int) int {
	var b, j int64 = -1, 0
	for j < int64(numBuckets) {
		b = j
		key = key*2862933555777941757 + 1
		j = int64(float64(b+1) * (float64(1<<31) / float64((key>>33)+1)))
	}
	return int(b)
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
