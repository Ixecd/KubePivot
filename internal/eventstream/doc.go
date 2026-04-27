// Package eventstream 提供 KubePivot v2.7 的事件流基础设施。
//
// # 设计目标
//
// 自研 Informer + 高性能 Cache，作为 v2.9 / v3.0 智能调度系统的事件流基础。
//
// 核心论断：Informer ≠ Cache。
// 本包不是"重新发明 client-go"，是为 KubePivot 自己的 reconcile 模式做专用 Cache。
//
// # 性能基线
//
// vs client-go cache.Indexer (Apple M4 / Go 1.25)：
//
//	Cache Get:           14.5ns vs 45ns        (3.1x 时间, 6x 内存)
//	Cache List (ns):     147ns vs 6800ns       (46x 时间, 100x 内存)
//	ColdStart 10000:     341ms vs 645ms        (1.9x 时间, 8.6x 内存)
//	内存放大率 (1w obj): 1.65x vs 4.69x        (2.84x 优势)
//
// 完整数据见 docs/design/eventstream-perf.md。
//
// # 架构
//
// 三层 Cache：
//
//	Hot Layer  - Skeleton + 反序列化对象 (lock-free, atomic.Value snapshot)
//	Warm Layer - Skeleton + RawJSON      (RWMutex 保护)
//	Cold Layer - 仅 disk                  (mmap-backed, 暂未实现)
//
// 关键组件：
//
//	Informer       每种资源类型一个全局实例 (Q9=A)
//	Cache          ns 二级索引 + immutable snapshot (Q4=C)
//	Dispatcher     shard 过滤 + 异步分发 (Q9: shard 过滤在此)
//	Subscriber     业务订阅事件流
//	MetricsClient  双实现 (kubectl + Prometheus)
//
// # 与 v2.5 sharding 的关系
//
// ShardSet 通过 adapter_sharding.go 适配 v2.5 sharding.ShardSet 接口
// (避免 import cycle)。Dispatcher 启动时拿到 ShardSet，运行时事件按
// ShardSet.Owns(ns) 过滤；ShardSet 变化时通过 channel 通知，触发
// EventResync (新增 ns) / EventDelete (移除 ns)。
//
// # 哲学约束
//
// 本包不引入 k8s.io/client-go。watch loop 自己实现（HTTP chunked + JSON）。
// 这是 KubePivot Master 分支的核心哲学之一，benchmark 数据已验证此路径
// 在性能上不仅可行，而且优于 client-go (见 docs/design/eventstream-perf.md)。
//
// # 设计 Q 拍板
//
// 完整 14 个 Q 拍板见 docs/design/eventstream-draft.md (本包实施基准)。
package eventstream
