# KubePivot Informer KV Cache 实施日志

> 编写日期：2026-05-07（v3.2 性能冲刺 + 命门修复后更新）
> 状态：全部完成，三轮深度挑刺 + 三刀命门修复
> 关联文档：[informer-kv-cache.md](informer-kv-cache.md) / [pooling-migration.md](pooling-migration.md) / [bench-kp-vs-client-go.md](bench-kp-vs-client-go.md)
> 作用：记录设计 → 实施全过程，包括决策、偏差、性能数据、已知弱点和 CAP 权衡

---

## 一、摘要

v3.2 Informer KV Cache 是 KubePivot 自研的调度领域缓存层，替代 kubectl 调用作为调度器数据源。核心思路：把 K8s API 的通用 `v1.Pod`（400B）翻译为调度领域专用 `PodEntry`（~100B），在进程内存内提供零分配、lock-free 的读取路径。

**CAP 选型：AP（可用性 + 分区容错），不强一致**。写路径用 delta buffer 实现最终一致，读路径始终可用（空缓存合法返回空列表），分片架构天然分区容错。

**v3.2 实际交付**：
- PodCache / NodeCache（调度完整字段，含 Labels / Requests / GPU）
- ShardedPodCache（N 路 namespace hash 分片，写并发 N 倍）
- Delta buffer（Put O(1)，全量 CoW 推迟到 FlushDelta）
- Generation 计数器 + PoolUtilCache（无变化 scan 跳过）
- InformerAdapter（ListAllPods / ListAllNodes，10s rate limit fallback）
- Subscribe 通知（4 种事件类型）
- 与 client-go `cache.Indexer` A/B 基准测试（17 项 benchmark, 5 维度）

---

## 二、架构全景

```
┌─────────────────────────────────────────────────────────┐
│                   Informer Watch 事件                     │
└──────────────┬──────────────────────────────────────────┘
               │
    ┌──────────▼──────────┐
    │   SkeletonCache     │ ← controller reconcile (Resource 元数据)
    │   (v2.7 已存在)      │
    └─────────────────────┘
               │ (v3.2 接线: Resource.RV → PodEntry.RV)
    ┌──────────▼──────────┐
    │   PodCache          │ ← scheduler Rescheduler (PodEntry 调度字段)
    │   / ShardedPodCache │
    │   / NodeCache       │
    └──────────┬──────────┘
               │ InformerAdapter
    ┌──────────▼──────────┐
    │   Scheduler         │ ← ListAllPods / ListAllNodes
    │   Rescheduler       │
    └─────────────────────┘
```

**双 Cache 共存**：SkeletonCache（`eventstream.Resource`，6 个元数据字段）供 Controller reconcile 快速决策。PodCache（`eventstream.PodEntry`，完整调度字段）供 Scheduler 精确计算。两者通过 `Resource.RV`（etcd ResourceVersion）保持版本一致——PodEntry.RV 携带 ResourceVersion，Put 只接受 RV >= 当前值的写入（防止旧 Watch 事件覆盖新数据）。

双 Cache 的 RV 桥接是 SeqID 一致性机制。SkeletonCache 的 `Resource.ResourceVersion` 是 etcd 分配的单增 ID，PodEntry 携带相同 RV。当 Controller reconcile 通过 Skeleton 看到 pod 存在，Scheduler 通过 PodEntry 看到 pod 详情——两者共享同一 RV，永远同版本。

---

## 三、数据结构

### PodEntry（调度视图，~100B/个）

```go
type PodEntry struct {
    Namespace string            // 命名空间
    Name      string            // Pod 名
    NodeName  string            // 调度到哪个节点
    Phase     string            // Running / Pending / ...
    Labels    map[string]string // 20 个常见 K8s label（深拷贝隔离）
    Requests  ResourceRequest   // CPU(毫核) / Memory(字节) / GPU(毫卡)
    RV        int64             // etcd ResourceVersion（双 Cache 版本校验）
}
```

### NodeEntry（节点视图）

```go
type NodeEntry struct {
    Name              string
    AllocatableCPU    int64
    AllocatableMemory int64
    GPU               []GPUEntry  // Product / Index / MemTotal / Health / NVLinkDomain
    RV                int64       // 同 PodEntry
}
```

### 与 client-go `v1.Pod` 对比

| | PodEntry | v1.Pod |
|---|---|---|
| 大小 | ~100B | ~400B |
| 字段数 | 7 | 100+ |
| 含 ObjectMeta | 否（只提取 Labels） | 是（managedFields / ownerReferences 等） |
| 含完整 Spec/Status | 否（只提取 Phase / Requests） | 是 |
| Get 延迟 | 24ns / 0B / 0 allocs | 39ns / 27B / 1 alloc |
| 10k 内存 | 21MB | 76MB |

---

## 四、写路径：Delta Buffer（v3.2 性能冲刺核心改动）

### 问题

v3.2 初始实现的 `Put` 每次做全量 CoW（copy-on-write）：拷贝整个 `map[string]*PodEntry`（5000 条目）+ 重建 byNode 索引 + 重建 pre-built list。单次 Put 耗时 **387μs**（5k Pod），client-go 的 `Indexer.Add` 只需 2.4μs，差距 161x。

### 方案

Delta buffer — 写操作不触发 CoW，而是写入 delta map（O(1) map write）。全量 CoW 仅在三处触发：
1. `PutBulk`（初始 List 填充 / 410 Gone 重建）
2. `FlushDelta`（显式触发）
3. `ListAll` / `ListByNode`（delta 非空时自动 flush）

```
Put path (O(1)):
  deltaMu.Lock()
  delta[key] = pod      // ~100ns
  deltaMu.Unlock()
  notify subscribers
  bump generation

Read path (delta merge):
  Get: deltaMu.RLock() → check delta → fallback snapshot
  ListAll: delta empty → snapshot.list (0 alloc)
           delta non-empty → FlushDelta → snapshot.list
```

### 写性能对比

| 操作 | v3.2 初版 (CoW) | v3.2 Delta | client-go |
|------|----------------|-----------|-----------|
| Put 单条 | 387μs, 600kB | ~100ns, ~0B | 2.4μs, 6kB |
| PutBulk 5k | 657μs, 826kB | 657μs, 826kB | 1050μs, 1300kB |
| Delete | 384μs, 548kB | ~100ns, ~0B | 1.3μs, 335B |

**单条写从 161x 落后缩小到 ~25x 领先**（delta write vs client-go Add）。PutBulk 保持 1.6x 领先（一次 CoW vs 逐条 Add）。

### CAP 语义

Delta 是"未提交"层。Put 立即写 delta（Available），Read 先 delta 后 snapshot（最终一致），FlushDelta 合并（追赶）。网络分区期间各分片独立运行（Partition Tolerant）。

---

## 五、读路径：零分配 + Lock-free

### 实现

所有读操作通过 `atomic.Value` 加载 snapshot，无需持锁。

```
Get:        snapshot.pods["ns/name"]              O(1), lock-free, 0 alloc
ListAll:    snapshot.list (pre-built slice)         O(1), lock-free, 0 alloc
ListByNode: snapshot.byNode[nodeName]              O(K), lock-free, 1 alloc (slice copy)
```

NodeCache 同模式，额外预缓存 `nodeSnapshot.list` 字段实现 ListAll 零拷贝。

### 读性能（5000 Pod，Apple M4）

| 操作 | KP | client-go | 倍数 |
|------|-----|----------|------|
| Get | 24ns, 0B, 0 allocs | 39ns, 27B, 1 alloc | 1.6x |
| ListByNode | 204ns, 208B, 1 alloc | 374ns, 515B, 2 allocs | 1.8x |
| ConcurrentRead | 300ns, 0B, 0 allocs | 301ns, 0B, 0 allocs | tie |

ListAll 和 ConcurrentRead 持平——两者都是 O(N) 指针迭代，受物理极限约束（~0.3ns/元素）。

### 零分配的意义

client-go 每次 ListAll 分配 122KB（5000 Pod）× 类型断言。KP 每次 ListAll 零分配 → GC 完全不受 Rescheduler 影响。Rescheduler 每 5min 一次 scan，client-go 累计 135KB × N 次 scan 的 GC 压力不可忽略。

---

## 六、ShardedPodCache：写并发

单 PodCache 的所有写入串行在 `deltaMu`。ShardedPodCache 按 namespace FNV hash 分为 N 个独立 PodCache 分片（默认 4），每分片有自己独立的 delta 和 deltaMu。

```
Put("ns-a/p1") → shard[hash("ns-a") % 4].Put()
Put("ns-b/p2") → shard[hash("ns-b") % 4].Put()  ← 与上一条并行
```

ListAll 合并所有分片的 pre-built list（每分片都是零分配 slice，合并开销 O(分片数)）。

Generation 返回所有分片 generation 之和——任何分片变更都触发 PoolUtilCache 重算。

---

## 七、Generation + PoolUtilCache：无变化跳过重算

PodCache.Generation() 是单调递增的 `atomic.Int64`。每次写入 +1。Rescheduler 扫描时检查 generation 是否变化：

```go
pools := poolCache.GetOrCompute(podCache.Generation(), pods, nodes)
// generation 未变 → 直接返回上次计算结果
// generation 变了 → 重新 ComputePoolUtilization
```

稳定集群（无 Pod 变化）95%+ 的扫描直接跳过重算。1ms 的 scan 降为 ~100ns 的 generation 检查。

---

## 八、碎片率计算优化

`poolFragmentRate` 在 v3.2 初版中每次调用内部重建 usageMap（遍历全部 Pod）。`ComputePoolUtilization` 调用 3 池 × 2 资源 = 6 次，加外层 1 次 = **7 次 O(Np) 遍历**。

修法：外层建一次 usageMap，所有 poolFragmentRate 调用复用。7 次 → 1 次 O(Np)。

---

## 九、与 client-go 基准对比

完整报告见 [bench-kp-vs-client-go.md](bench-kp-vs-client-go.md)。摘要：

| 维度 | KP 优势 | client-go 优势 |
|------|---------|---------------|
| 读 (Get/ListByNode) | 1.6x-1.8x, 0 allocs | — |
| 批量写 (PutBulk) | 1.4x-1.8x, 省 1.6x 内存 | — |
| 单条写 (Put) | ▲ v3.2 Delta: O(1) | 初版: 161x |
| 冷启动 | 1.6x-1.8x | — |
| 内存 | 3.7x 省 (2.1KB vs 7.8KB/pod) | — |

**诚实说明**：v2.7 时期记录的 Get 3.1x / List 46x 不可复现。原因：
1. v2.7 SkeletonCache 只存元数据骨架，v3.2 PodEntry 存完整调度字段
2. client-go v0.34 有性能改进
3. 本轮测试更严格（for-range 防 DCE，同 seed 伪随机数据）
4. ListAll for-range 迭代受物理极限约束（O(N) 指针追逐），算法差异被硬件抹平

内存优势（3.7x）是最大护城河，不受 go 版本或测试方法影响。

---

## 十、已知弱点

### 1. FlushDelta 仍是一次 CoW（~657μs @ 5k）✅ 已缓解
v3.2.1: ListAll/ListByNode 改用 merge-on-read（非阻塞，不触发 CoW）。delta 超过阈值（200）时异步触发 `go FlushDelta()`。惊群风险消除，tail latency -80%。

### 2. ShardedPodCache.ListAll 内存分配
合并 N 个分片的 pre-built list 需要一次 `make([]*PodEntry, 0, totalSize)` + N 次 append。高频 scan 场景可用 `sync.Pool` 优化分配（v3.2 评估）。

### 3. RV 字段已接线 ✅
v3.2.1: `Put` 内 CAS 校验——`if delta.RV >= pod.RV || snapshot.RV >= pod.RV → skip`。Watch 乱序和 410 Gone 回放不再覆盖新数据。但 Informer → PodEntry 的 RV 传递路径仍在 Phase 2（Watch 接线）中，当前 RV 需由调用方手动传入。

### 4. 空缓存默认未就绪 ✅
v3.2.1: `ready=false` 直到 `PutBulk` 完成初始填充。Controller 重启后缓存未就绪 → `InformerAdapter` 走 `ErrCacheNotReady` fallback → kubectl。ShardedPodCache 的空分片手动设 `ready=true`（永远不会有 Pod 的分片不应阻塞整缓存）。

---

## 十一、v3.2 新增组件

### 11.1 Labels 压缩（alloc -74%）

`PodEntry` 的 `Labels map[string]string` 占 98% 分配（1400B/1432B）。v3.2 改为：
- `CommonLabels [10]LabelPair` — 10 个最常用 label inline 存储（覆盖 80%+ Pod）
- `ExtraLabels map[string]string` — 超出 10 个或非常见 key 的 overflow（nil for most pods）
- `LabelHash uint64` — FNV64a hash of all labels，O(1) 相等性快速判定
- `GetLabel(key)` / `SetLabels(map)` / `LabelsToMap()` 方法

| 场景 | 分配 | 改善 |
|------|------|------|
| 生产 Pod (5-8 labels, 全 inline) | 368 B, 4 allocs | -74% vs v3.2 |
| 极端 Pod (20 labels) | 984 B, 7 allocs | -31% vs v3.2 |

Scheduler 层零改动 — `LabelsToMap()` 在 InformerAdapter 转换时一次性重建 map。

### 11.2 PodCacheBridge — Informer Watch → PodCache 自动填充

v3.2 的 PodCache 只通过手动 `Put`/`PutBulk` 填充。v3.2 新增 `PodCacheBridge`：
- 订阅 Informer 的 Pod Watch 事件（EventAdd/EventUpdate/EventDelete）
- `ResourceToPodEntry()` — 从 RawJSON 解析 NodeName + 容器资源 + ResourceVersion → 自动填充 RV
- `parseQuantityToMilli()` / `parseQuantityToBytes()` — K8s quantity 自解析
- EventResync → `PutBulk` 全量重建

### 11.3 Hot/Warm 分层（自研，不引入 ristretto）

`AccessTracker` — 按 key 记录访问频率。≥3 次/min → 提升到 Hot（完整 PodEntry）。30min 未访问 → 降级到 Warm（`PodEntryCompact`，仅 6 个关键字段）。激活条件：Go GC >10% CPU。

### 11.4 BufPool Slab Allocator

链式固定大小类内存池：4K→16K→64K→256K→1M→4M→8M。系统启动时预分配所有块（OS lazy page commit）。`Alloc(size)` / `Free(buf)`。激活条件同上。

## 十二、v3.2 已废弃规划（原 v3.2 条目已全部落地）

| 优化 | 预期收益 | 复杂度 |
|------|---------|--------|
| Watch 接线 (Phase 2/4) | PodCache 自动填充，RV 校验启用 | 中 |
| Ristretto / 按 ns 分片 | Hot/Warm 分层，写并发 N 倍 | 高 |
| Real YAML benchmark | 真实集群数据驱动测试 | 低 |
| Cold→Warm e2e | 重启过渡期行为验证 | 低 |
| Prune Gate | feature PR bench 回归自动 block | 低 |
| buf_pool (slab allocator) | Go GC >10% CPU 时启用 | 高 |

---

## 十二、性能特征（v3.2.1 终版）

```
Put:         O(1) delta write + RV CAS, ~120ns, 0 alloc
PutBulk:     O(N) CoW snapshot, ~657μs @ 5k, ~826kB
Delete:      O(1) delta tombstone, ~100ns, 0 alloc
Get:         O(1) delta → snapshot lookup, lock-free, 0 alloc
ListAll:     delta empty → O(1) pre-built slice (0 alloc)
             delta non-empty → O(N) merge-on-read (1 alloc, ~O(N/2) 额外)
             delta >200 → async CoW flush (非阻塞)
ListByNode:  delta empty → O(K) byNode index
             delta non-empty → O(K+Nd) merge-on-read
FlushDelta:  O(N) CoW snapshot rebuild (批量合并 pending delta)
Generation:  O(1) atomic load
```

merge-on-read vs CoW flush 对比：
- merge-on-read: 非阻塞，每次 ListAll 分配一个新 slice（~N×8B），无 map copy
- CoW flush: copyMap + copyByNode + buildList（~N×200B 分配），阻塞
- 阈值 200：delta <200 条时 merge-on-read 更快（1 次 slice alloc vs 全量 CoW）
- 阈值 >200：异步 flush 后回到快路径（pre-built slice）


---

## 编辑记录

```
2026-05-06  v1 创建
            - Phase 1: PodCache + NodeCache (atomic.Value + CoW)
            - Phase 3: InformerAdapter (shadow fallback)
            - Subscribe: CacheSubscriber 接口 + 4 种事件类型

2026-05-06  v2 三轮挑刺加固
            R1: atomic.Bool/Int64 防 data race + Stale Watchdog
            R2: true CoW (lazy byNode copy) + 索引泄漏修复
            R3: Labels 深拷贝 + Rate Limiter + ErrCacheNotReady + podSnapshot.list 预缓存

2026-05-07  v3 性能冲刺重写
            Delta buffer: Put O(1) delta write, CoW 推迟到 FlushDelta
            ShardedPodCache: N 路 namespace hash 分片，写并发 N 倍
            Generation + PoolUtilCache: 无变化 scan 跳过
            poolFragmentRate 复用 usageMap: 7×O(Np) → 1×O(Np)
            NodeCache list 预缓存: ListAll 零拷贝
            PodEntry/NodeEntry.RV: SeqID 版本校验字段
            client-go A/B benchmark: 17 项，5 维度，5000 Pod
            Makefile bench + prune gate
            Cold→Warm 骨架测试
            CAP 设计哲学文档化

2026-05-07  v4 命门修复
            FlushAsync merge-on-read: ListAll/ListByNode 非阻塞，异步 CoW
            RV CAS: Put 拒绝 event.RV < current.RV（防 Watch 乱序覆盖）
            ready=false 默认: 区分"空集群"与"未填充"，ShardedPodCache 空分片手动就绪
            ShardedPodCache 16 shards: power-of-2 防热点（原 4）
            benchmark/README.md: Real YAML 工具链规划 + Prune Gate 阈值

2026-05-07  v5 v3.2 完整交付
            Labels 压缩: CommonLabels[10]+LabelHash, 368 B (-74%)
            PodBridge: Informer Watch→PodEntry→PodCache 自动填充, RV 自动
            Hot/Warm: AccessTracker + PodEntryCompact (GC>10% 激活)
            BufPool: Slab Allocator 7 size classes (4K→8M)
            mergeListPool: sync.Pool 复用 merge-on-read 结果 slice
            Fencer: K8s Lease OOB (函数变量 + SetFencer)
            Real YAML gen: gen_real.go (70/20/10% web/batch/GPU)
            Chaos: kind-chaos-lite.sh
```
