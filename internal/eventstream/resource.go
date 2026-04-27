package eventstream

import (
	"time"
)

// CacheLayer 缓存分层标识。
//
// 三层分层架构（详见 docs/design/eventstream-draft.md 第 3 节）：
//   - LayerHot:  Skeleton + RawJSON 全在内存（lock-free 读）
//   - LayerWarm: Skeleton + RawJSON 在内存（RWMutex 保护）
//   - LayerCold: 仅 disk 存储（mmap-backed，按需加载）
type CacheLayer int

const (
	LayerHot CacheLayer = iota
	LayerWarm
	LayerCold
)

// String 返回 CacheLayer 的可读名称。
func (l CacheLayer) String() string {
	switch l {
	case LayerHot:
		return "hot"
	case LayerWarm:
		return "warm"
	case LayerCold:
		return "cold"
	default:
		return "unknown"
	}
}

// Resource 是 K8s 资源在 KubePivot Cache 中的表达。
//
// 设计要点：
//
//  1. Skeleton 字段是业务 reconcile / drift detection / sizing 关心的核心字段
//     ParseSkeleton 只反序列化这些字段（不反序列化整个 K8s 对象）
//
//  2. RawJSON 保留完整原始数据
//     业务代码通常只读 Skeleton 字段
//     Cold 层降级时只保留 RawJSON
//     Hot 层完整持有
//
//  3. ResourceVersion 是关键字段（K8s 乐观锁机制）：
//     - watch 重连：用 listResourceVersion=X 续传
//     - 增量序列化：判断"对象真的变了吗"
//     - 并发安全：update 时带 RV 实现并发控制
//
//  4. cacheLayer / lastAccessed / accessCount 是 Cache 内部使用字段
//     业务代码不应直接读取，由 Cache 自身管理
type Resource struct {
	// ─── Identity（不变字段） ─────────────────────
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
	UID        string

	// ─── Version（K8s 关键字段） ──────────────────
	Generation      int64  // K8s spec 变更版本（spec 变化时递增）
	ResourceVersion string // K8s 乐观锁字段（任何变化都递增）

	// ─── Labels / Annotations ─────────────────────
	Labels      map[string]string
	Annotations map[string]string

	// ─── 关键 spec 字段 ────────────────────────────
	Replicas *int32 // Deployment / StatefulSet

	// ─── 关键 status 字段 ─────────────────────────
	Phase         string // Pod 状态（Running / Pending / Failed）
	ReadyReplicas *int32 // Deployment.status.readyReplicas

	// ─── 时间戳 ───────────────────────────────────
	CreationTimestamp time.Time
	DeletionTimestamp *time.Time

	// ─── 完整原始数据 ─────────────────────────────
	// 业务代码通常不直接读，按需通过 Unmarshal(RawJSON, &fullObj) 反序列化
	// Cold 层降级时只保留此字段
	RawJSON []byte

	// ─── Cache 内部元数据（业务代码不应读取） ──────
	cacheLayer   CacheLayer
	lastAccessed time.Time
	accessCount  uint32
}

// Layer 返回当前 Resource 所属的 Cache 层。
//
// 业务代码一般无需关心，仅在 debug / metrics 场景使用。
func (r *Resource) Layer() CacheLayer {
	return r.cacheLayer
}

// LastAccessed 返回该 Resource 上次被访问的时间戳。
//
// 用于 Cache 分层降级判定（30min 未访问降级到 Cold）。
func (r *Resource) LastAccessed() time.Time {
	return r.lastAccessed
}

// AccessCount 返回访问计数。
//
// 用于 Cache 分层升级判定（5min 内 ≥ 3 次升级到 Hot）。
func (r *Resource) AccessCount() uint32 {
	return r.accessCount
}

// SkeletonChanged 比对两个 Resource 的 Skeleton 字段是否发生有业务意义的变化。
//
// 增量序列化的核心：
//   - watch 收到 Update 事件后，先比对 Skeleton
//   - Skeleton 没变（如只有 ResourceVersion 变 = K8s housekeeping）→ 跳过 EventUpdate
//   - Skeleton 变了 → 发 EventUpdate 给 subscribers
//
// 比对范围：
//   - Generation（spec 变更）
//   - Phase / ReadyReplicas（status 变更）
//   - Replicas（关键 spec 字段）
//   - Labels / Annotations（业务标签）
//
// 不比对：
//   - ResourceVersion（K8s 内部，业务不关心）
//   - lastAccessed / accessCount（Cache 内部）
//   - RawJSON（包含所有变化但比对开销大）
func SkeletonChanged(old, new *Resource) bool {
	if old == nil || new == nil {
		return old != new
	}

	// 1. Identity 必须一致（理论上不应变化）
	if old.UID != new.UID {
		return true
	}

	// 2. spec 版本变化
	if old.Generation != new.Generation {
		return true
	}

	// 3. status 字段变化
	if old.Phase != new.Phase {
		return true
	}
	if !int32PtrEqual(old.ReadyReplicas, new.ReadyReplicas) {
		return true
	}

	// 4. spec 关键字段
	if !int32PtrEqual(old.Replicas, new.Replicas) {
		return true
	}

	// 5. Labels / Annotations
	if !stringMapEqual(old.Labels, new.Labels) {
		return true
	}
	if !stringMapEqual(old.Annotations, new.Annotations) {
		return true
	}

	// 6. DeletionTimestamp（标记删除）
	if (old.DeletionTimestamp == nil) != (new.DeletionTimestamp == nil) {
		return true
	}

	return false
}

// ─── 辅助函数 ─────────────────────────────────────────────────

// int32PtrEqual 比较两个 *int32 是否相等（处理 nil 情况）。
func int32PtrEqual(a, b *int32) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// stringMapEqual 比较两个 string map 是否相等。
//
// 注意：nil map 和空 map 视为相等。
func stringMapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// Key 返回 Resource 在 Cache 中的标准 key（"namespace/name"）。
//
// 注意：当前 Cache 实现使用 ns 二级索引，不直接使用此 key。
// 此函数主要用于 logging / debug。
func (r *Resource) Key() string {
	return r.Namespace + "/" + r.Name
}
