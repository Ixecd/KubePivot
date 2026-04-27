package eventstream

import (
	"context"
	"sync/atomic"
	"time"
)

// EventType 表示一个 Informer 事件的类型。
type EventType int

const (
	// EventAdd 新对象出现（first watch 或 watch 中新增）。
	EventAdd EventType = iota

	// EventUpdate 对象 Skeleton 字段发生变化。
	// 注意：仅 ResourceVersion 变化（K8s housekeeping）不触发此事件。
	EventUpdate

	// EventDelete 对象被删除。
	EventDelete

	// EventResync 全量 resync 周期合成的事件。
	// 用于周期性 reconcile 兜底（即使 watch 没漏事件）。
	EventResync
)

// String 返回 EventType 的可读名。
func (e EventType) String() string {
	switch e {
	case EventAdd:
		return "ADD"
	case EventUpdate:
		return "UPDATE"
	case EventDelete:
		return "DELETE"
	case EventResync:
		return "RESYNC"
	default:
		return "UNKNOWN"
	}
}

// Event 单个事件。
//
// 字段含义：
//   - Type: 事件类型
//   - Namespace / Name: 资源标识
//   - Old: 变更前对象（仅 EventUpdate / EventDelete 提供）
//   - New: 变更后对象（仅 EventAdd / EventUpdate / EventResync 提供）
type Event struct {
	Type      EventType
	Namespace string
	Name      string
	Old       *Resource
	New       *Resource
}

// EventHandler 事件处理函数。
//
// 在独立 goroutine 中调用（不阻塞 watch loop / dispatcher）。
// 实现方应避免 panic（dispatcher 会 recover 但仍记 metric）。
type EventHandler func(e Event)

// Subscription 一个订阅句柄。
//
// 通过 Informer.Subscribe(handler) 返回。
// 不再需要事件时调用 Unsubscribe()。
type Subscription interface {
	// Unsubscribe 取消订阅，handler 不再被调用。
	// 已经派发的 in-flight 事件可能仍会被处理。
	Unsubscribe()

	// Stats 返回该订阅的监控指标。
	Stats() SubscriberStats
}

// SubscriberStats 订阅监控指标。
type SubscriberStats struct {
	// EventsDelivered 已成功派发的事件数。
	EventsDelivered atomic.Uint64

	// EventsDropped 因 worker 池满被 drop 的事件数。
	// 应保持为 0；非零表示下游 handler 太慢需调优。
	EventsDropped atomic.Uint64

	// Panics handler panic 次数（被 dispatcher recover）。
	Panics atomic.Uint64
}

// Informer 抽象 K8s 资源的 watch + 事件分发。
//
// 一个 Informer 实例对应一种资源类型（如 deployments / pods / services）。
// 跨 shard 复用 watch 连接（Q9=A），shard 过滤在 Dispatcher 层完成。
//
// 生命周期：
//
//	NewInformer(ctx, opts)
//	    ↓
//	informer.Start(ctx)         启动 watch loop
//	    ↓
//	informer.Subscribe(handler) 订阅事件
//	    ↓
//	informer.Get / List         lock-free 读 cache
//	    ↓
//	informer.Stop()             清理资源（也可 ctx 取消触发）
type Informer interface {
	// Start 启动 watch loop。
	//
	// 返回一个 errChan，watch goroutine 异常退出时通过此 chan 通知。
	// 正常退出（ctx canceled）时 chan 关闭但不发送。
	Start(ctx context.Context) <-chan error

	// Get 从 cache 读取单个对象（lock-free 路径）。
	Get(ns, name string) (*Resource, bool)

	// List 按 namespace 列出所有 cache 对象。
	// 返回的 slice 是新分配的，调用方可安全修改。
	List(ns string) []*Resource

	// ListAll 跨 namespace 全量列出。
	ListAll() []*Resource

	// Subscribe 订阅事件。handler 在独立 goroutine 中调用。
	Subscribe(handler EventHandler) Subscription

	// Stats 返回监控指标。
	Stats() InformerStats

	// Stop 停止 informer。
	// 关闭所有订阅、停止 watch loop、清理 cache。
	Stop()
}

// ShardSet 抽象 v2.5 sharding 系统。
//
// Dispatcher 用 ShardSet.Owns(ns) 决定一个事件是否归属本 pod。
// 不归属的事件直接 drop，不分发给订阅者。
//
// 完整适配实现在 Day 4 的 adapter_sharding.go：
//
//	eventstream.ShardSet (本接口)
//	  ↑ adapter_sharding.go 适配
//	sharding.ShardSet (v2.5 实际实现)
//
// 这样避免 internal/eventstream/ 直接 import internal/sharding/
// 减少包间耦合，便于独立测试。
type ShardSet interface {
	// Owns 判定指定 namespace 是否归属本 pod 的 shard。
	Owns(namespace string) bool
}

// InformerOptions 创建 Informer 的参数。
type InformerOptions struct {
	// Resource 资源类型，K8s API 的 plural name。
	// 例：deployments / pods / services / ingresses
	Resource string

	// APIVersion K8s API 版本组。
	// 例：apps/v1 / v1 / networking.k8s.io/v1
	APIVersion string

	// ShardSet 引用 v2.5 sharding 系统。
	// Dispatcher 用 ShardSet.Owns(ns) 过滤事件。
	// 可为 nil（不分片，所有事件都派发）。
	ShardSet ShardSet

	// ResyncPeriod 全量重同步周期。
	// 默认按资源类型差异化（DefaultResyncPeriod）。
	// 用户特殊需求可显式覆盖。
	ResyncPeriod time.Duration

	// CachePolicy 缓存分层策略。
	// 默认 DefaultCachePolicy()。
	CachePolicy CachePolicy

	// ReconnectPolicy watch 重连策略。
	// 默认 DefaultReconnectPolicy()。
	ReconnectPolicy ReconnectPolicy

	// KubeConfig kubectl 配置路径（用于本地开发）。
	// 留空走集群内 ServiceAccount。
	KubeConfig string

	// APIServerURL K8s API server URL。
	// 留空从 KubeConfig 或集群内环境推断。
	APIServerURL string
}

// InformerStats Informer 监控指标。
//
// 用于 Prometheus 暴露：
//   - kubepivot_informer_cache_size{resource=...}
//   - kubepivot_informer_events_total{resource=..., type=ADD|UPDATE|...}
//   - kubepivot_informer_watch_reconnects_total{resource=...}
type InformerStats struct {
	// Resource 资源类型名（用于 metrics label）。
	Resource string

	// CacheSize 当前 cache 中对象数。
	CacheSize int

	// HotCount / WarmCount / ColdCount 各层对象数。
	HotCount  int
	WarmCount int
	ColdCount int

	// EventsTotal 累计事件数。
	EventsTotal uint64

	// EventsByType 按类型统计的累计事件数。
	EventsByType map[EventType]uint64

	// WatchReconnects watch 重连次数。
	// 持续递增的话说明 K8s API server 不稳定。
	WatchReconnects uint64

	// LastResyncTime 上次全量 resync 完成时间。
	LastResyncTime time.Time

	// CacheHitRate cache 命中率（kp list 验证用）。
	// 仅在 Step 2 实施 kp list 后有意义。
	CacheHitRate float64

	// GoroutineCount 当前 informer 持有的 goroutine 数。
	GoroutineCount int

	// MemoryBytes 估算内存占用。
	MemoryBytes uint64
}

// ─── 工厂函数 ──────────────────────────────────────────────────────
//
// NewInformer 创建并返回一个 Informer 实例（不自动启动）。
//
// 调用方负责调用 informer.Start(ctx) 启动 watch loop。
//
// 实施位于 informer_impl.go（包内同 package，无需 import）。
