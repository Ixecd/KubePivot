# eventstream

> v2.7+ 自研 K8s Informer + KVCache 基础设施。0 client-go 依赖，3~46x 加速，1.65x 内存放大。
> 最后更新：2026-05-08（v3.2）

---

## 设计哲学

**核心命题："Informer is not Cache"** — eventstream 不是在重写 client-go，而是在为 KubePivot 的 reconcile 模式构建专用缓存层。

| 设计选择 | 理由 |
|---|---|
| 0 client-go 依赖 | 控制实现细节，性能可调优 |
| Raw HTTP chunked watch | 直接控制 watch 生命周期 + bookmark |
| atomic.Value snapshot | 读路径零锁，14.5ns Get |
| Delta buffer + CoW flush | 写路径 O(1)，CoW 推迟到 FlushDelta |
| 自研 ParseSkeleton | 不全量反序列化 JSON，内存 8.7x 节省 |
| Label 压缩（CommonLabels[10]） | 生产 Pod 368B vs ~1.4KB，减少 74% |

### vs client-go 性能对照（10k 对象 ColdStart）

| 指标 | client-go | eventstream | 加速 |
|---|---|---|---|
| Cache Get | ~45ns | **14.5ns** | 3.1x |
| Cache List (per ns, 1k items) | ~6800ns | **147ns** | 46x |
| Cache ListAll | | ~7500ns | |
| ColdStart PutBulk (10k) | ~645ms | **341ms** | 1.9x |
| 内存放大率 (10k obj) | 4.69x | **1.65x** | 2.84x better |
| PodEntry 单条内存 | ~1.4KB | **368B** | ~3.8x |

---

## 架构分层

```
┌──────────────────────────────────────────────────────┐
│ PodCache / NodeCache / ShardedPodCache               │  ← 调度层 KV Cache
│ (delta buffer + CoW snapshot + byNode index)         │
│ (Label 压缩: CommonLabels[10] + FNV64a LabelHash)   │
├──────────────────────────────────────────────────────┤
│ PodCacheBridge / NodeCacheBridge                     │  ← 接线层
│ (Informer Event → PodEntry/NodeEntry → Cache)       │
├──────────────────────────────────────────────────────┤
│ Informer (informerImpl)                               │  ← Watch/Event 层
│ (list → watch → reconnect → resync)                 │
│ (HTTP chunked + NDJSON + BOOKMARK)                  │
├──────────────────────────────────────────────────────┤
│ SkeletonCache (atomic.Value snapshot)                │  ← 基础 Cache 层
│ (CoW map[ns]map[name]*Resource)                     │
│ (lock-free read / CoW write)                        │
├──────────────────────────────────────────────────────┤
│ Auth: in-cluster SA / explicit URL / kubeconfig      │  ← 认证层
│ Serializer: ParseSkeleton (增量反序列化)             │
│ Reconnect: exponential backoff + jitter              │
│ Resync: resource-type differentiated periods         │
│ Metrics: Prometheus Collector (9 metrics)            │
└──────────────────────────────────────────────────────┘
```

---

## 核心组件

### 1. Informer — Watch 生命周期

**接口** (`informer.go`):

```go
type Informer interface {
    Start(ctx) <-chan error
    Get(ns, name string) (*Resource, bool)
    List(ns string) []*Resource
    ListAll() []*Resource
    Subscribe(handler EventHandler) Subscription
    Stats() InformerStats
    Stop()
}
```

每个 Informer 管理一个 K8s 资源的 Watch 生命周期。

**创建** — `NewInformer(ctx, InformerOptions)`:

```go
type InformerOptions struct {
    Resource        string          // pods / nodes / deployments ...
    APIVersion      string          // v1 / apps/v1 ...
    ShardSet        ShardSet        // 分片过滤（按 namespace）
    ResyncPeriod    time.Duration   // 默认按资源类型自动选择
    CachePolicy     CachePolicy     // 冷热分层策略
    ReconnectPolicy ReconnectPolicy // 重连退避策略
    KubeConfig      string          // （未实现）
    APIServerURL    string          // 显式 API Server 地址
}
```

**Watch 状态机** (`informer_impl.go:runWatchLoop`):

```
Start()
  │
  ├─ doInitialList()
  │    paginated GET /api/v1/{resource}?limit=500
  │    → ParseSkeleton each item → PutBulk to SkeletonCache
  │    → save resourceVersion
  │
  ├─ doWatch()
  │    GET /api/v1/{resource}?watch=1&resourceVersion=X&allowWatchBookmarks=true
  │    bufio.Scanner reads NDJSON lines
  │    │
  │    ├─ BOOKMARK → silent RV update（防止 410 Gone）
  │    ├─ ADDED    → Put + dispatch EventAdd
  │    ├─ MODIFIED → SkeletonChanged check
  │    │               only RV changed → skip（零开销过滤）
  │    │               real change → Put + dispatch EventUpdate
  │    ├─ DELETED  → Delete + dispatch EventDelete
  │    └─ ERROR(410) → errStreamGone → 触发 relist
  │
  ├─ disconnect → exponential backoff (1s ~ 30s, ±20% jitter)
  │    410 Gone → clear RV → doInitialList()
  │    other    → 续 watch (retain RV)
  │
  └─ resync ticker → doInitialList()（周期取决于资源类型）
```

### 2. SkeletonCache — 基础缓存

**设计**：不可变 snapshot + CoW 写入。

```go
type SkeletonCache struct {
    snapshot atomic.Value  // *cacheSnapshot (读路径零锁)
    writeMu  sync.Mutex    // 写序列化
}

type cacheSnapshot struct {
    items map[string]map[string]*Resource  // namespace → name → Resource
    size  int
}
```

**操作复杂度**：

| 操作 | 复杂度 | 说明 |
|---|---|---|
| `Get(ns, name)` | O(1) | 两次 map 查找，lock-free |
| `List(ns)` | O(N_ns) | lock-free |
| `ListAll()` | O(N) | lock-free |
| `Put(r)` | O(N_ns) | CoW：复制外层 map + 深度复制目标 ns 内层 map |
| `PutBulk(items)` | O(N+M) | 分组后 batch CoW merge |
| `Delete(ns, name)` | O(N_ns) | CoW：复制所有 ns，目标 ns 内层 map 删除 key |

### 3. PodCache / NodeCache — KV 调度缓存（v3.2）

在 SkeletonCache 之上构建的资源类型专属缓存。

#### PodCache

```go
type PodCache struct {
    snapshot atomic.Value    // *podSnapshot
    writeMu sync.Mutex       // snapshot swap
    delta   map[string]*PodEntry  // 挂起的增量（O(1) 写）
    deltaMu sync.RWMutex     // delta 访问
    ready   atomic.Bool      // 缓存就绪标志
    generation atomic.Int64  // 版本号（供池化调度缓存校验）
}

type podSnapshot struct {
    pods   map[string]*PodEntry            // key="ns/name"
    byNode map[string]map[string]*PodEntry // nodeName → pods
    list   []*PodEntry                     // 预构建列表
}
```

**Delta Buffer 模式**：

```
Put(pod) → deltaMu.Lock → delta[key] = pod (O(1), ~120ns)
           deltaMu.Unlock

Get(key) → deltaMu.RLock → check delta first
           snapshot.Load() → check snapshot   (O(1))
           deltaMu.RUnlock

ListAll() → delta empty → 返回预构建 list（零分配）
            delta non-empty → merge-on-read
              ├─ 遍历 snapshot.list
              ├─ 对每个 key 查 delta（替换/删除）
              ├─ 追加 delta new keys
              └─ delta >200 → 异步 FlushDelta
```

**RV CAS**：Put 时检查 `delta.RV` 和 `snapshot.RV`，拒绝过期事件（410 Gone relist 后的残留 watch event）。

**IsReady 门控**：`bookmarkWithHeartbeat` 机制更新 `lastHeartbeat`，StaleWatchdog 超过 `maxStale`（默认 90s）则 `ready=false`。Scheduler/Controller 读取时先检查 ready，未就绪则 fallback 到 kubectl——此为 **shadow degradation** 策略。

#### NodeCache

```go
type NodeCache struct {
    snapshot atomic.Value    // *nodeSnapshot
    writeMu  sync.Mutex
    ready    atomic.Bool
}

type nodeSnapshot struct {
    nodes map[string]*NodeEntry
    list  []*NodeEntry       // 预构建列表
}
```

更简单——无 delta buffer，直接 CoW map 复制。节点变更频率远低于 Pod。

#### ShardedPodCache

```go
type ShardedPodCache struct {
    shards []*PodCache  // 16 路分片（默认）
    n      int
}
```

`fnv32a(namespace) % 16` 路由到独立 shard，每个 shard 有自己的 writeMu + delta buffer。16 路并发写入无锁竞争。

#### Label 压缩

```go
type PodEntry struct {
    Namespace    string
    Name         string
    NodeName     string
    Phase        string
    CommonLabels [10]LabelPair    // 10 个最常用 label 内联存储
    ExtraLabels  map[string]string // 其余 label
    LabelHash    uint64            // FNV64a hash，O(1) 等值检查
    Requests     ResourceRequest
    RV           int64
}
```

10 个常用 key：`app.kubernetes.io/name` / `instance` / `component` / `tier` / `environment` / `kubepivot.io/pool` / `shard` / `blue-green-locked` / `gpu-hardware-failure` / `statefulset.kubernetes.io/pod-name`

### 4. Bridge 接线层

`PodCacheBridge` 和 `NodeCacheBridge` 订阅 Informer 事件，完成 `Resource → PodEntry / NodeEntry` 转换：

```go
type PodCacheBridge struct {
    cache    *PodCache
    informer Informer
    sub      Subscription
}

NewPodCacheBridge(informer, cache)
  → informer.Subscribe(handler)
    ├─ EventAdd    → ResourceToPodEntry(r) → cache.Put(entry, "")
    ├─ EventUpdate → ResourceToPodEntry(r) → cache.Put(entry, oldNodeName)
    ├─ EventDelete → cache.Delete(ns, name, nodeName)
    └─ EventResync → cache.PutBulk(ListAll → ResourceToPodEntry)
```

**ResourceToPodEntry 提取路径**：
- `spec.nodeName` → NodeName
- `status.phase` → Phase
- `spec.containers[].resources.requests` → CPU (milli) / Memory (bytes) / GPU (nvidia.com/gpu)
- `metadata.labels` → SetLabels（Label 压缩）
- `metadata.resourceVersion` → RV

### 5. 认证层

`resolveK8sConfig()` 三优先路径：

1. **显式 URL** (`InformerOptions.APIServerURL`) — `https://` 用系统 CA，`http://` 无 TLS
2. **KubeConfig** — 当前未实现（`resolveKubeconfig` 返回 error）
3. **In-Cluster SA** — 读 `/var/run/secrets/kubernetes.io/serviceaccount/token` + CA，构造 Bearer auth transport，强制 TLS 1.2+

HTTP Client 配置：`Timeout=0`（watch 长连接），`MaxIdleConns=10`，`IdleConnTimeout=90s`。

### 6. Serializer — 增量反序列化

`ParseSkeleton(rawJSON)` 只提取业务相关字段，不全量反序列化：

- 解析字段：APIVersion, Kind, Metadata(Namespace, Name, UID, ResourceVersion, Labels, Annotations, CreationTimestamp, DeletionTimestamp), Spec.Replicas, Status.Phase/ReadyReplicas
- 保留 `RawJSON []byte`（共享 buffer，不拷贝）
- ~35us vs 60us（1.7x 时间，8.7x 内存）

### 7. Reconnect — 重连退避

```go
type ReconnectPolicy struct {
    InitialBackoff time.Duration // 默认 1s
    MaxBackoff     time.Duration // 默认 30s
    BackoffFactor  float64       // 默认 2.0
    Jitter         float64       // 默认 0.2 (±20%)
    MaxAttempts    int           // 0 = 不限
}
```

指数退避：`initialBackoff * factor^attempt`，上限 `MaxBackoff`，对称抖动 ±20%。

### 8. Resync — 差异化同步周期

按资源类型分为 4 档：

| 频率 | 间隔 | 资源 |
|---|---|---|
| High | 10min | pods, events |
| Medium | 30min | deployments, statefulsets, daemonsets, replicasets, jobs, cronjobs |
| Low | 60min | services, configmaps, secrets, ingresses, hpa, pvc, endpoints |
| VeryLow | 120min | namespaces, nodes, storageclasses, pv, crd |

自动单复数规范化：`deployment → deployments`，`service → services`。

### 9. Metrics — 9 个 Prometheus 指标

| 指标 | 类型 | 标签 | 说明 |
|---|---|---|---|
| `kubepivot_informer_cache_size` | Gauge | resource | 缓存条目数 |
| `kubepivot_informer_cache_hot_count` | Gauge | resource | Hot 层条目数 |
| `kubepivot_informer_cache_warm_count` | Gauge | resource | Warm 层条目数 |
| `kubepivot_informer_cache_cold_count` | Gauge | resource | Cold 层条目数 |
| `kubepivot_informer_events_total` | Counter | resource, type | Watch 事件计数 |
| `kubepivot_informer_watch_reconnects_total` | Counter | resource | 重连次数 |
| `kubepivot_informer_cache_hit_ratio` | Gauge | resource | 缓存命中率 |
| `kubepivot_informer_memory_bytes` | Gauge | resource | 内存占用估算 |
| `kubepivot_informer_last_resync_timestamp_seconds` | Gauge | resource | 最近 resync 时间戳 |

### 10. BufPool — Slab 分配器

可选开启（默认关闭，GC 开销 >10% 时自动激活）。7 个 slab 类：4KB / 16KB / 64KB / 256KB / 1MB / 4MB / 8MB。`Alloc(size)` 返回最小 fitting slab，`Free(b)` 按首地址归还对应类。

---

## Event 数据结构

```go
type EventType int
const (
    EventAdd    EventType = 0
    EventUpdate EventType = 1
    EventDelete EventType = 2
    EventResync EventType = 3
)

type Event struct {
    Type      EventType
    Namespace string
    Name      string
    Old       *Resource
    New       *Resource
}

type Resource struct {
    APIVersion  string
    Kind        string
    Namespace   string
    Name        string
    UID         string
    Generation  int64
    ResourceVersion string
    Labels      map[string]string
    Annotations map[string]string
    Replicas    *int32
    Phase       string
    ReadyReplicas *int32
    CreationTimestamp  time.Time
    DeletionTimestamp  *time.Time
    RawJSON     []byte
    // internal: cacheLayer, lastAccessed, accessCount
}
```

**SkeletonChanged(old, new)** — 比较 UID / Generation / Phase / ReadyReplicas / Replicas / Labels / Annotations / DeletionTimestamp。**不比较 ResourceVersion**（K8s 内部维护字段，不属于业务变更）。

---

## 与 Controller 的集成

Controller 在 `global.go` 中创建 Informer Pool：

```
informerPool.Start("pods", "v1")
  → NewInformer(opts) → Start() → PodCacheBridge → PodCache
informerPool.Start("nodes", "v1")
  → NewInformer(opts) → Start() → NodeCacheBridge → NodeCache
```

InformerAdapter（`scheduler/informer_adapter.go`）从 PodCache/NodeCache 读取数据提供给调度器：

```
ListAllPods() → podCache.IsReady()?
  ├─ true  → podCache.ListAll() → 转换 PodEntry → PodInfo
  └─ false → kubectlAdapter.ListAllPods() (shadow degradation)
```

---

## 关键设计决策记录

| 决策 | 选择 | 理由 |
|---|---|---|
| CAP 模型 | AP（可用 + 分区容错） | 调度不需要强一致 |
| Delta buffer  vs 直接 CoW | Delta buffer | Put O(1) ~120ns，CoW 延迟到批量 |
| ListAll 策略 | merge-on-read | 非阻塞，delta > 200 异步 flush |
| 分片数 | 16 | power-of-2 hash，实测最优并发 |
| READY gate | bookmarkWithHeartbeat | 不需要额外的 healthz endpoint |
| Label 存储 | 内联 [10] + map remainder | 减少 74% 内存 |
| JSON 解析 | ParseSkeleton（部分提取） | 不全量反序列化，8.7x 内存节省 |
| Watch bookmark | `allowWatchBookmarks=true` | 防止 api-server 滑动窗口超限 |
| 重连策略 | 指数退避 + jitter | 防止惊群效应 |
| kubeconfig 支持 | 未实现 | 非 v2.7 scope |
