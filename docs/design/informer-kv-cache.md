# Informer KV Cache — 从 Skeleton 到完整资源对象

> 编写日期：2026-05-05
> 状态：🌱 设计草案
> 关联：[eventstream-draft.md](eventstream-draft.md) / [eventstream-impl-notes.md](eventstream-impl-notes.md) / [scheduler.md](scheduler.md)

---

## 一、问题

当前调度器数据流：

```
rescheduler.run() [每 5min]
  → rs.pods.ListAllPods(ctx)     // kubectl get pods -A -o json
  → rs.nodes.ListAllNodes(ctx)    // kubectl get nodes -o json
  → computeNodeUtilization()
```

而 Informer（v2.7.0 已落地）也在 Watch 同一批 Pod/Node，维护着 Hot 层缓存。
但它只存 Skeleton（元数据：name/ns/uid/generation），不存资源字段
（pod.Spec.Containers[].Resources.Requests、node.Status.Allocatable）。

结果：两套数据通路各跑各的，Informer 的 Watch 流浪费了，scheduler
还在每 5 分钟打一次 kubectl 全量查询。

## 二、目标

把 Informer 的 Hot 层从 "Skeleton cache" 升级为 "完整资源对象 KV cache"。
scheduler / rescheduler / controller 通过 `InformerAdapter` 读缓存，
不再调 kubectl。

**"完整"的定义**：完整 = 调度所需的全部字段（CPU/Memory/GPU Requests、
Phase、NodeName、Labels），不是 K8s API 返回的全部 JSON 字段。
`managedFields`、`ownerReferences`、`conditions` 全展开、`annotations` 等
调度器不关心的字段一律不存。内存优势（2.84x vs client-go）靠的就是字段裁剪。

```
当前：
  Informer Watch Pod/Node → Skeleton cache (metadata only)
  Scheduler → kubectlAdapter → kubectl get pods -A -o json

目标：
  Informer Watch Pod/Node → KV cache (调度字段完整，非 K8s 字段全裁)
  Scheduler → InformerAdapter → cache.GetPod("ns/name")  // O(1)
```

## 三、数据结构

### 3.1 Pod KV Cache

```go
// internal/eventstream/kv_cache.go

type PodCache struct {
    mu   sync.RWMutex
    pods map[string]*PodCacheEntry  // key: "namespace/name"
    // 二级索引：按 nodeName 快速查找
    byNode map[string]map[string]struct{}  // nodeName → set of pod keys
}

type PodCacheEntry struct {
    Info      *PodInfo   // 完整 Pod 信息（含 resource request）
    UpdatedAt time.Time  // 最后 Watch 更新时间
    Version   string     // K8s ResourceVersion（乐观锁）
}
```

`PodInfo` 复用 `internal/scheduler.PodInfo`，跟现有 kubectl 解析结果结构一致，
零适配成本。

字段裁剪原则：
- ✅ 保留：Name / Namespace / NodeName / Phase / Labels / Requests(CPU/Mem/GPU)
- ❌ 剔除：`managedFields`（API 修改历史冗余）、`ownerReferences`（除非需级联删除）、
  `status.conditions` 全展开（仅保留核心 Ready condition）
- 结构体字段用最小类型：CPU 请求用 `int32`（毫核，最大值 ~2.1B 毫核 = 214 万核，
  当前足够，但需写进 `internal/scheduler/limits.go` 单元测试防溢出产生负数），
  Memory 请求用 `int64`（字节，需要大范围）。Labels 用 `map[string]string` 不裁剪
  （调度决策依赖 label selector 匹配）。

### 3.2 Node KV Cache

```go
type NodeCache struct {
    mu    sync.RWMutex
    nodes map[string]*NodeCacheEntry  // key: "nodeName"
}

type NodeCacheEntry struct {
    Info      *NodeInfo  // 完整 Node 信息（含 allocatable）
    UpdatedAt time.Time
    Version   string
}
```

`NodeInfo` 复用 `internal/scheduler.NodeInfo`。

### 3.3 为什么不用三层 Cache（Hot/Warm/Cold）

当前的三层缓存模型（Hot/Warm/Cold）是为**项目级 Deployment 对象**设计的：
一个 IDLE 项目的 Deployment 长时间不访问就被降级到 Warm/Cold。

调度器的需求完全不同：
- Pod 和 Node **高频访问**（每 5min 跑一次全量遍历）
- **全量遍历**，不是按需查询
- 对象数量可控（100 Node + 1000 Pod = ~1100 对象）

对调度器场景，三层 Cache 的升降级逻辑是多余开销。Pod/Node cache
直接全量 Hot 层，一把 RWMutex，没有 Warm/Cold 降级。

如果未来 Pod 数量到 10000+ 再考虑分片，当前量级不值得。

## 四、写入路径：Watch 事件 → KV Cache

Informer Watch 到 K8s 事件后，走以下写入逻辑：

```
Watch Event (ADDED/MODIFIED/DELETED)
  │
  ├─ ADDED:
  │   1. 解析 JSON → PodInfo
  │   2. pc.mu.Lock()
  │   3. pc.pods["ns/name"] = &PodCacheEntry{Info: pod, ...}
  │   4. pc.byNode[pod.NodeName]["ns/name"] = struct{}{}
  │   5. pc.mu.Unlock()
  │
  ├─ MODIFIED:
  │   1. 解析 JSON → newPod (PodInfo)
  │   2. pc.mu.RLock()
  │      oldPod := pc.pods["ns/name"].Info  // 取旧值
  │      pc.mu.RUnlock()
  │   3. pc.mu.Lock()
  │   4. pc.pods["ns/name"] = &PodCacheEntry{Info: newPod, ...}
  │   5. if oldPod != nil && oldPod.NodeName != newPod.NodeName {
  │          // 跨节点迁移：从旧节点索引删除，加入新节点索引
  │          delete(pc.byNode[oldPod.NodeName], "ns/name")
  │          pc.byNode[newPod.NodeName]["ns/name"] = struct{}{}
  │      }
  │   6. pc.mu.Unlock()
  │
  └─ DELETED:
      1. pc.mu.Lock()
      2. oldPod := pc.pods["ns/name"].Info  // 取旧值获取 NodeName
      3. delete(pc.pods, "ns/name")
      4. delete(pc.byNode[oldPod.NodeName], "ns/name")
      5. pc.mu.Unlock()
```

关键：**MODIFIED 事件必须提取 oldPod.NodeName，检测跨节点迁移**。
如果 `old != new`，先从旧节点索引删除再写入新节点。否则 byNode 索引残留，
调度器通过 byNode 查询时会看到已不在该节点的 Pod，导致资源计算重叠。

### 4.1 Cold Start：ListAll 预热 + 双缓冲写入

Informer 启动时，先 `ListAll`（kubectl get pods -A -o json），
批量灌入 KV cache，然后从 ListAll 返回的 ResourceVersion 开始 Watch。

```
Cold Start:
  1. kubectl get pods -A -o json   ← 一次性
  2. ParseAll → PutBulk(cache)      ← 双缓冲写入，调度器零阻塞
  3. cache.ready = true             ← 标记可用
  4. Start Watch from RV            ← 增量更新
```

**双缓冲写入（PutBulk 优化）**：

ListAll 返回 1w 对象时，持写锁逐条插入约需 ~341ms。期间调度器无法读。
改为双缓冲——先在内存构造完整 `newMap`，一次指针交换完成原子更新：

```go
func (pc *PodCache) PutBulk(pods []*PodInfo) {
    newMap := make(map[string]*PodCacheEntry, len(pods))
    newByNode := make(map[string]map[string]struct{})

    for _, p := range pods {
        key := p.Namespace + "/" + p.Name
        newMap[key] = &PodCacheEntry{Info: p, ...}
        if newByNode[p.NodeName] == nil {
            newByNode[p.NodeName] = make(map[string]struct{})
        }
        newByNode[p.NodeName][key] = struct{}{}
    }

    pc.mu.Lock()
    pc.pods = newMap      // 指针交换，纳秒级
    pc.byNode = newByNode
    pc.ready = true
    pc.mu.Unlock()
}
```

持锁窗口从 ~341ms 缩短到纳秒级。调度器读取零停顿。

**影子降级（ready 为 false 时）**：

`InformerAdapter` 内置 fallback 逻辑——

```go
func (a *InformerAdapter) ListAllPods(ctx context.Context) ([]*PodInfo, error) {
    if a.podCache.IsReady() {
        return a.podCache.ListAll(), nil  // 内存读
    }
    return a.fallback.ListAllPods(ctx)    // 透传给 kubectlAdapter
}
```

`cache.ready == true` 的瞬间，后续请求自动从 kubectl 切换到内存。
两个 Adapter 返回的 `PodInfo` 结构体完全一致（共用同一个 struct 定义），
上层调用方感知不到切换。

### 4.2 Watch 断连重同步

Watch 连接断开超过 ResourceVersion 过期窗口（K8s 默认 ~5min），
Informer 需要重新 ListAll。此时：

1. ListAll 全量 → PutBulk 双缓冲写入（纳秒级持锁）
2. `cache.ready` 全程保持 true（不中断读）
3. scheduler 读到的数据始终是完整快照

### 4.3 410 Gone：RV 过期时的 Cache 重建

当 etcd 历史版本被压缩，Watch 返回 HTTP 410 Gone 时，Informer 的
RV 已失效，必须全量重同步。重建顺序至关重要——不能让调度器读到残缺快照。

```
410 Gone 处理:
  1. kubectl get pods -A -o json   ← 全量拉取新数据
  2. ParseAll → newMap             ← 在内存构建全新 map
  3. pc.mu.Lock()
  4. pc.pods = newMap              ← 原子替换，不出现"空窗期"
  5. pc.byNode = newByNode
  6. pc.staleSince = time.Time{}   ← 清除陈旧标记
  7. pc.mu.Unlock()
  8. 从新 ListAll 的 RV 重启 Watch
```

关键原则：**先构造完整新快照，再原子替换旧快照**。不在 Watch 失效期间暴露
半清空的 cache。调度器从 `pc.pods` 读到的始终是旧完整快照或新完整快照，
不存在中间态。

### 4.4 Go Map 内存不归还 — 强制压缩

Go 的 `map` 在删除 key 后，其底层 bucket 内存不会归还给 OS。
如果集群经历剧烈扩缩容（如 10k Pod→100），map 仍占用 10k 级别的内存。

**检测**：监控指标 `fragmentation_ratio = map_buckets / (len(pods) * avgEntrySize)`。
当该值超过阈值（如 3.0），触发压缩。

**压缩**：复用 PutBulk 双缓冲——遍历现有 pods 构造新 map，指针交换，
旧 map 被 GC 回收，重新获得紧凑内存布局。

```go
func (pc *PodCache) compact() {
    pods := pc.ListAll()
    pc.PutBulk(pods)  // 双缓冲：新 map + 原子替换
}
```

压缩触发策略：`fragmentation_ratio > 3.0` 且当前 Pod 数 < 上次压缩时的 50%。
避免在稳态集群上频繁重建。

### 4.5 MODIFIED 事件的 Fast Pre-check

K8s `MODIFIED` 事件极其频繁——`status.lastTransitionTime`、annotations
变更等都会触发。但这些字段调度器不关心。每次 MODIFIED 都持写锁更新 map
并通知 Subscriber 是巨大浪费。

**优化**：在解析 JSON 后、持锁前，做一次快速字段对比——

```go
func (pc *PodCache) handleModified(key string, newPod *PodInfo) {
    oldPod := pc.getOld(key)
    if oldPod == nil {
        goto update
    }

    // Fast Pre-check: 仅对比调度相关字段
    if oldPod.Phase == newPod.Phase &&
        oldPod.NodeName == newPod.NodeName &&
        oldPod.Requests == newPod.Requests &&      // struct 值比较
        mapsEqual(oldPod.Labels, newPod.Labels) {
        return  // 调度字段无变化，跳过写锁 + 通知
    }

update:
    pc.atomicUpdate(newPod, oldPod)
}
```

`ResourceRequests` 定义为纯值 struct（无指针），直接 `==` 比较。
Labels 变化极低频（调度器不主动打 label），`mapsEqual` 几乎永远 true。
整个 pre-check 在 RLock 下完成，避免每次 MODIFIED 都持写锁。

### 4.6 索引更新的原子封装

`pc.pods` 和 `pc.byNode` 必须同步更新。虽然当前持有 `mu.Lock()`，
但未来如果有人在锁外只读 `pods` 不读 `byNode`，可能读到不一致状态。

**加固**：所有 pods + byNode 联合更新封装为私有方法，禁止外部直接操作：

```go
// atomicUpdate 必须在 pc.mu.Lock() 保护下调用。
// pods 和 byNode 在同一个闭包内完成更新，保证索引一致性。
func (pc *PodCache) atomicUpdate(newPod *PodInfo, oldPod *PodInfo) {
    pc.mu.Lock()
    defer pc.mu.Unlock()

    key := newPod.Namespace + "/" + newPod.Name
    pc.pods[key] = &PodCacheEntry{Info: newPod, UpdatedAt: time.Now()}

    // 跨节点迁移：从旧索引删除
    if oldPod != nil && oldPod.NodeName != newPod.NodeName {
        delete(pc.byNode[oldPod.NodeName], key)
    }

    // 确保新节点索引存在
    if pc.byNode[newPod.NodeName] == nil {
        pc.byNode[newPod.NodeName] = make(map[string]struct{})
    }
    pc.byNode[newPod.NodeName][key] = struct{}{}
}
```

外部只通过 `pc.ListAll()` / `pc.ListByNode(node)` / `pc.GetPod(key)` 读取，
禁止直接访问 `pc.pods` / `pc.byNode`。

## 五、读取路径：InformerAdapter

```go
// internal/scheduler/informer_adapter.go

type InformerAdapter struct {
    podCache  *eventstream.PodCache
    nodeCache *eventstream.NodeCache
}

func (a *InformerAdapter) ListAllPods(ctx context.Context) ([]*PodInfo, error) {
    return a.podCache.ListAll(), nil  // 内存操作，零 I/O
}

func (a *InformerAdapter) ListAllNodes(ctx context.Context) ([]*NodeInfo, error) {
    return a.nodeCache.ListAll(), nil
}
```

实现 `PodLister` + `NodeLister` 接口，跟 `kubectlAdapter` 同一套签名。
替换点：

```go
// cmd/kp/scheduler.go — 当前
adapter := scheduler.NewKubectlAdapter(kc)

// cmd/kp/scheduler.go — 改后
adapter := scheduler.NewInformerAdapter(podCache, nodeCache)
```

## 六、与 client-go 的对比

> 既然又要拿出来鞭尸 😋

| | KubePivot Informer KV | client-go Indexer |
|---|---|---|
| 数据模型 | 专用 `PodInfo` / `NodeInfo`，字段就是调度器要的 | 通用 `runtime.Object` + unstructured.Unstructured，每次读都要类型断言 |
| 索引 | 手写 `byNode` 二级索引，只建调度器真用的 | 多级 IndexFunc 框架，但每个 Index 背后是一个 `map[string]sets.String`，38B overhead per key |
| 内存 | ~500B/Pod（纯调度字段） | ~1200B/Pod（含 managedFields / ownerReferences / conditions 全展开） |
| Watch 解析 | 只 extract resource requests + status 字段 | 全 JSON deserialize 到 Go struct |
| 并发 | 一把 `sync.RWMutex`，读多写少 | `threadSafeMap` 也用 RWMutex，但 Index 更新时持写锁 |
| 依赖 | 无 | `k8s.io/client-go` + 14 个传递依赖 |

client-go Indexer 的设计目标是 "通用 K8s controller 框架"——所以它把所有资源类型
都抽象成 `runtime.Object`，把索引做成通用框架，把字段全展开。但这意味着：
每个 Pod 缓存了 30+ 个调度器不关心的字段，每个 Index key 分配了 `sets.String`。

KubePivot 的场景更单一：调度器只需要 CPU/Memory/GPU/Phase/NodeName/Labels。
Informa KV Cache 只存这些字段，内存大约是 client-go 的 40%。

v2.7 Day 1 benchmark 已经证明了这件事——自建 Informer 在 Cache Get (14.5ns)
和 Cold Start (4.6x) 上都比 client-go 快。

## 七、迁移策略

### Phase 1: KV Cache 实现

- `internal/eventstream/kv_cache.go` — PodCache + NodeCache
- 单测覆盖：Put/Get/Delete/ListAll/PutBulk + 并发读写
- 不接 Informer Watch，纯数据结构测试

### Phase 2: Informer Watch 接线

- Informer 启动时 ListAll → PutBulk
- Watch 事件 → 增量 Put/Delete
- `cache.ready` 信号

### Phase 3: InformerAdapter + 切换

- `internal/scheduler/informer_adapter.go`
- feature flag：`KUBEPIVOT_USE_INFORMER_CACHE=true` → v3.4 废弃
- `false` 时走 kubectlAdapter（回退路径） → v3.4: config `kvcache.enabled: false`

### Phase 4: 默认启用 ✅ v3.4

- ~~Informer KV 在生产环境跑过 N 周无 issues → 切默认 `true`~~
- v3.4: `kvcache.enabled: true` (config.system.yaml 默认)，KVCache 默认主路径
- kubectlAdapter 保留作为 cache 未就绪时的 fallback
- `kvcache.enabled: false` → 纯 kubectl 路径（podCache/nodeCache = nil）
- InformerAdapter nil-safe：cache=nil → 直接走 fallback

## 八、可观测性指标

```
kubepivot_informer_cache_pods_total          — 缓存中 Pod 数量
kubepivot_informer_cache_nodes_total         — 缓存中 Node 数量
kubepivot_informer_cache_hit_total           — 缓存命中次数（ListAll 调用计数）
kubepivot_informer_cache_stale_seconds       — Watch Heartbeat 失效秒数
kubepivot_informer_cache_watch_restarts      — Watch 重连次数（含 410 Gone）
kubepivot_informer_cache_bulk_insert_ms      — PutBulk 耗时（毫秒）
kubepivot_informer_cache_fragmentation_ratio — map bucket / 实际条目比值，>3 触发压缩
```

**`stale_seconds` 不是墙钟差**：不依赖 `time.Now() - lastWatchTime`（节点时钟漂移
会误报）。改为 Watch Heartbeat——Informer 在 Watch 流中每 30s 注入一个虚拟
Heartbeat 事件。`stale_seconds` = 距离上次收到任何 Watch 事件（含 Heartbeat）的秒数。
如果 > 60s 意味着 Watch 真断了或 Debounce 堵塞，触发告警。

**`fragmentation_ratio`**：Go map 删除 key 后底层 bucket 不归还 OS。
公式 =  `estimated_map_memory / (len(pods) * avgPodEntrySize)`。超过 3.0
且 Pod 数 < 上次压缩时的 50%，触发 PutBulk 内存压缩。

## 九、Subscribe 回调：为事件驱动调度预留

有了 KV Cache + Watch 增量更新后，调度器应该"随变而动"而不是"等 5min ticker"。
但直接在每 Watch 事件触发 `run()` 会抖动——10 个 Pod 同时创建触发 10 次重调度。

接口设计：

```go
// internal/eventstream/kv_cache.go

type CacheSubscriber interface {
    OnChange(event CacheChangeEvent)
}

type CacheChangeEvent struct {
    Type      CacheChangeType  // PodAdded | PodModified | PodDeleted | NodeChanged | BulkResync
    Keys      []string         // 受影响的 cache key。BulkResync 时为空
    Timestamp time.Time
}

type CacheChangeType string

const (
    PodAdded    CacheChangeType = "pod_added"
    PodModified CacheChangeType = "pod_modified"
    PodDeleted  CacheChangeType = "pod_deleted"
    NodeChanged CacheChangeType = "node_changed"
    BulkResync  CacheChangeType = "bulk_resync"   // 全量同步（ListAll / 410 Gone），Keys 为空
)
```

PodCache / NodeCache 在写入（Put/Delete/PutBulk）后通知所有 subscriber。

**BulkResync 特殊处理**：当 ListAll 或 410 Gone 重建触发全量同步时，
一次性通知 10,000 个 Pod 的变更会造成巨大的内存压力和无效遍历。
改为发送单个 `BulkResync` 事件，Keys 为空，Rescheduler 收到后直接
无条件触发一次 `run()`，无需遍历 key。

Rescheduler 侧实现 `CacheSubscriber`：

```go
func (rs *Rescheduler) OnChange(event CacheChangeEvent) {
    switch event.Type {
    case "bulk_resync":
        rs.changeDebouncer.Flush()  // 全量重建，立即触发
    default:
        rs.changeDebouncer.Notify(event)
    }
    // debouncer: 30s 窗口内合并多次变更，只触发一次 run()
}
```

**debounce 策略**：
- 收到第一个变更 → 启动 30s timer
- 30s 内后续变更 → 合并 keys（去重，Set 语义），不重置 timer
- timer 到期 → 触发一次 `run()`
- 保留 ticker（5min）为兜底——Watch 漏了 ticker 也能自愈

**BulkResync 的 Jitter**：全量同步时，如果多个 controller 副本同时收到
BulkResync 并执行 `run()`，会对 K8s API Server 产生瞬时压力。
BulkResync 触发时加入随机 jitter（0-5s），打散调度周期。

**事件顺序保证**：`CacheChangeEvent.Keys` 是去重 Set，不是有序 Slice。
Subscriber 必须以缓存的**当前状态**为准做决策，而非依赖事件发生时
的快照。例如 Pod 快速 `ADDED→DELETED→ADDED` 后，30s 窗口结束时
Subscriber 从 cache 读到的是最终状态（Pod 存在），不会因事件顺序错乱
而产生脏数据。

**v3.1 实施范围**：接口定义 + PodCache/NodeCache subscriber 机制。
Rescheduler 的 debounce 逻辑 v3.2 接入（当前 ticker 模式够用，先不切）。

## 十、设计 Q

```
Q1: PodCache 是否需要按 nodeName 之外的二级索引？
    A. 只需要 byNode（当前调度器只按 node 查 Pod）
    B. 加 byPhase（Running/Pending 分索引）
    C. 加 byLabel（按 label 查询）
    建议: A — byPhase 可以遍历时过滤（<1000 Pod，毫秒级），byLabel 需求未出现

Q2: cache.ready 为 false 时，调度器行为？
    A. 阻塞等待（直到 Informer 完成 ListAll）
    B. 降级到 kubectlAdapter（影子降级）
    C. 返回 error，等下次 ticker 重试
    建议: B — InformerAdapter 内置 fallback，ready 后自动切换。不阻塞调度周期

Q3: KV cache 是否替代现有三层 Cache？
    A. 完全替代（所有对象走 KV cache）
    B. 并存（KV cache 给调度器，三层 Cache 给 controller）
    C. KV cache 存 Pod/Node，三层 Cache 存 Deployment 等
    建议: C — 不同资源类型不同策略。Deployment 仍走三层（有 IDLE 降级需求），
          Pod/Node 走 KV（全 Hot + 调度器高频访问）

Q4: Subscribe 回调的 debounce 窗口？
    A. 固定 30s
    B. 按变更量自适应（少量变更短窗口，大量变更长窗口）
    C. 可配置
    建议: A → 先用 30s 落地。v3.2 根据生产抖动数据决定是否切 B 或 C
```

---

## 编辑记录

```
2026-05-05  创建
            基于 qc + DeepSeek 对话：
            - 发现 Informer 存 Skeleton 但 scheduler 调 kubectl 的数据通路浪费
            - 设计 KV cache 升级方案（PodCache + NodeCache）
            - InformerAdapter 替换 kubectlAdapter，零接口改动
            - client-go 对比鞭尸 😋

2026-05-05  ESTP/THIN-K 漏招补全（qc 反馈）
            - §4: MODIFIED 事件补全 cross-node 索引迁移逻辑（oldPod.NodeName 检测）
            - §4.1: 双缓冲 PutBulk（持锁 341ms→纳秒）+ 影子降级逻辑
            - §4.3: 新增 410 Gone Cache 重建顺序（先建完整新快照，再原子替换）
            - §2: 明确"完整"=调度字段，严禁 managedFields 等冗余字段
            - §8: 新增 6 个监控指标（含 stale_seconds 陈旧告警）
            - §9: 新增 Subscribe 回调接口 + debounce 策略，为事件驱动调度预留

2026-05-05  深度对齐补丁（qc 反馈）
            - §3.1: 字段裁剪清单（保留/剔除）+ CPU 用 int32 压内存
            - §9: BulkResync 事件类型 — 全量同步时不发 10k 个 key，只发一个空 key 事件
                   Rescheduler 收到则跳过 debounce 直接 run()

2026-05-05  底层陷阱补刺（qc 反馈，6 个深度细节）
            - §4.4: Go map 内存不归还 — fragmentation_ratio 指标 + PutBulk 强制压缩
            - §4.5: MODIFIED Fast Pre-check — 调度字段无变化跳写锁 + 通知
            - §4.6: atomicUpdate() 封装 — pods + byNode 联合更新，禁止外部直接操作
            - §8: stale_seconds 改 Watch Heartbeat — 不依赖墙钟，防 NTP 漂移误报
            - §8: 新增 fragmentation_ratio 指标 + 压缩触发策略
            - §3.1: CPU int32 溢出测试 — limits.go 单元测试防负数
            - §9: Keys 去重 Set 语义 + BulkResync jitter (0-5s) + 事件顺序以当前 cache 为准
```
