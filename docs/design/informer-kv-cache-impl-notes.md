# KubePivot Informer KV Cache 实施日志

> 编写日期：2026-05-06
> 状态：✅ Phase 1 + Phase 3 + Subscribe 全部完成
> 关联文档：[informer-kv-cache.md](informer-kv-cache.md)（设计文档）
> 作用：记录设计 → 实施过程中的实际交付、决策调整、边界取舍

---

## 摘要

设计文档定义四个 Phase + Subscribe 回调。本次交付：Phase 1（PodCache + NodeCache）+ Phase 3（InformerAdapter）+ Subscribe 通知机制。
经三轮深度挑刺（CoW 优化、atomic 并发安全、Labels 深拷贝、Rate Limiter、预缓存切片），代码已接近生产级。
Phase 2（Informer Watch 接线）和 Phase 4（默认启用）留后续。

---

## 实际交付 vs 设计文档

### Phase 1: PodCache + NodeCache ✅

| 设计 | 实施 | 偏差 |
|------|------|------|
| `map[string]*PodInfo` + byNode 索引 | `map[string]*PodEntry` + byNode 索引 | 独立类型避免循环依赖 |
| atomic.Value + RWMutex | atomic.Value + sync.Mutex writeMu | 与 SkeletonCache 模式一致 |
| 双缓冲 PutBulk | ✅ 先构 newMap → 指针交换 | O(1) 持锁 |
| MODIFIED Fast Pre-check | ⏳ 设计已定，代码未写 | 等 Watch 接线时一起做 |
| atomicUpdate() 封装 | ✅ 内部读 oldNode，不信任调用方 | 加固 |
| 410 Gone 重建 | ✅ PutBulk 原子替换 | 无空窗期 |
| Map 内存压缩 | ⏳ 接口预留，压缩逻辑未触发 | fragmentation_ratio 指标待补 |
| CPU int32 | ❌ int64 | 安全优先，40KB 不值得 |

#### 关键设计调整

**独立 PodEntry/NodeEntry 类型**：设计文档建议复用 `scheduler.PodInfo`/`scheduler.NodeInfo`。实施时发现 `eventstream` import `scheduler` 会导致循环依赖（`scheduler` 也需要 import `eventstream` 用 InformerAdapter）。改为在 `eventstream` 定义独立 `PodEntry`/`NodeEntry` 类型，`InformerAdapter` 做转换。

**CoW 优化**：设计文档的 `copyByNode` 全量拷贝在每次 `Put` 时重建整个集群索引树。实施时改为只拷贝受影响节点的子 map，其余节点指针复用。O(N_nodes) → O(N_pods_on_node)。

**atomic.Bool 防 data race**：`ready` 和 `lastHeartbeat` 字段在多个 goroutine 间并发访问。改为 `atomic.Bool` / `atomic.Int64`（UnixNano），lock-free 读写。

**Stale Watchdog**：后台 goroutine 每 30s 检查 Heartbeat，超时则 `ready=false`，强制 InformerAdapter 降级到 kubectl。

**byNode 索引泄漏**：`Delete` 时若节点 Pod 数为 0，清理 byNode 顶层 key，防 Spot 实例频繁扩缩容导致 map 膨胀。

### Subscribe 回调 ✅

| 设计 | 实施 | 偏差 |
|------|------|------|
| CacheSubscriber 接口 | ✅ OnChange(CacheChangeEvent) | 一致 |
| 4 种事件类型 | ✅ PodAdded/PodModified/PodDeleted/BulkResync | 一致 |
| PutBulk → BulkResync | ✅ Keys 为空 | 一致 |
| Put → Added/Modified | ✅ 根据 Pod 是否存在判断 | 一致 |
| Delete → Deleted | ✅ | 一致 |
| debounce 逻辑 | ⏳ Rescheduler 接线留 v3.2 | 当前 ticker 模式够用 |

### Phase 3: InformerAdapter ✅

| 设计 | 实施 | 偏差 |
|------|------|------|
| 实现 PodLister/NodeLister | ✅ | 一致 |
| 影子降级（ready=false → kubectl） | ✅ + Rate Limiter (10s) | 额外加固 |
| cache 不可用时返回错误 | ✅ ErrCacheNotReady | 设计未明确，实际需区分"空"vs"不可用" |

---

## 优化轮次

经过三轮深度挑刺：

**Round 1 — 基本正确性**：
- int64 CPU（安全 > 40KB）
- atomic.Bool ready + atomic.Int64 lastHeartbeat（无锁防 data race）
- Heartbeat 无锁读写（不与 Put 抢 writeMu）
- Stale Watchdog 后台 goroutine，超时 ready=false 触发降级

**Round 2 — CoW 性能**：
- Put/Delete 只拷贝受影响节点的子 map（O(N_node_pods) vs 原 O(N_nodes)）
- ensureTopCopy() 懒拷贝顶层 map，同次 Put 只拷贝一次
- copyByNodeTop 浅拷贝（指针复用）
- byNode key 泄漏修复（节点空时清理顶层 key）

**Round 3 — 生产加固**：
- Labels 深拷贝（防调度器修改渗透回 cache）
- Rate Limiter 10s 间隔（防 stale→false 瞬间数千 kubectl 打爆 API）
- ErrCacheNotReady 语义区分（调用方能区分"集群空"vs"不可用"）
- Watchdog ticker = maxStale/2（动态精度）
- podSnapshot.list 预缓存切片（ListAll 零分配）
- 容量计算精确（len(src) 基础上仅在新增时 +1）

## 性能特征

```
PutBulk:    持锁 < 1μs（指针交换）
Put:        持锁 O(N_pods_on_node)（只拷贝受影响节点）
Delete:     持锁 O(N_pods_on_node)（同上）
ListAll:    O(N_pods) 内存分配（转换开销）
Get:        O(1) lock-free
ListByNode: O(N_pods_on_node) lock-free
```

---

## 编辑记录

```
2026-05-06  v1 创建
            - Phase 1: PodCache + NodeCache (atomic.Value + CoW)
            - Phase 3: InformerAdapter (shadow fallback)

2026-05-06  v2 三轮挑刺加固
            R1: atomic.Bool/Int64 防 data race + Stale Watchdog
            R2: true CoW (O(1) per Put/Delete) + byNode 索引泄漏修复
            R3: Labels 深拷贝 + Rate Limiter + ErrCacheNotReady + Watchdog 动态精度
                + podSnapshot.list 预缓存 (ListAll 零分配)
            Subscribe: CacheSubscriber 接口 + 4 种事件类型
```
