# KubePivot v2.7 流量层 Event Stream 设计草案

> 编写日期：2026-04-27
> 状态：📐 设计草案（待 Step 1 实施时 finalize）
> 关联文档：[ROADMAP.md v2.7 章节](../../ROADMAP.md) / [decision-stack.md](decision-stack.md)
> 实施周期：3 周（含 benchmark + 灰度上线）

---

## 摘要

v2.7 为 KubePivot 引入 **Event Stream Infrastructure** —— 自研 Informer + 高性能 Cache。
这是 v2.9 / v3.0 智能调度系统的**事件流基础设施**。

**核心论断**：

> Informer ≠ Cache
> KubePivot v2.7 的真正价值不是"重新发明一遍 client-go"，
> 是为 KubePivot 自己的 reconcile 模式做**专用 Cache**。

为什么自研：
- client-go 的 Informer 是通用方案，cache 实现保守
- KubePivot 是"读多写少 + 按 shard 分片 + 状态机驱动"的专用场景
- 专用方案的优化空间有 30-50%（理论上限）

为什么留 fallback：
- 第 1 周做 benchmark vs client-go（feature/client-go-comparison 分支）
- 数据真的差距大就承认，但**仍走自研路径**
- 自研是 KubePivot "不引入 client-go" 哲学的延续

---

## 一、整体架构

```
┌───────────────────────────────────────────────────────────────┐
│                      KubePivot Controller                     │
│                                                               │
│  ┌──────────────────────────────────────────────────────┐    │
│  │              Event Stream (v2.7 新增)                 │    │
│  │                                                        │    │
│  │  ┌───────────────┐    ┌────────────────────────────┐ │    │
│  │  │  Informer A   │    │   ShardSet (v2.5)          │ │    │
│  │  │  (Deployment) │───▶│                            │ │    │
│  │  └───────────────┘    │   Owns(ns) → bool          │ │    │
│  │  ┌───────────────┐    │   Subscribe() chan ShardChg│ │    │
│  │  │  Informer B   │    └────────────────────────────┘ │    │
│  │  │  (Service)    │                  ▲                 │    │
│  │  └───────────────┘                  │                 │    │
│  │  ┌───────────────┐    ┌──────────────────────────┐  │    │
│  │  │  Informer C   │    │   Dispatcher（shard 过滤）│  │    │
│  │  │  (Pod)        │───▶│   按 ShardSet.Owns 路由   │  │    │
│  │  └───────────────┘    └──────────────────────────┘  │    │
│  │                                  │                     │    │
│  │                                  ▼                     │    │
│  │  ┌───────────────────────────────────────────────┐    │    │
│  │  │   Cache (immutable snapshot, atomic.Value)     │    │    │
│  │  │                                                 │    │    │
│  │  │   ┌──────┐  ┌────────┐  ┌──────┐              │    │    │
│  │  │   │ Hot  │  │  Warm  │  │ Cold │              │    │    │
│  │  │   │      │  │        │  │      │              │    │    │
│  │  │   │skel+ │  │ skel + │  │ disk │              │    │    │
│  │  │   │ obj  │  │RawJSON │  │      │              │    │    │
│  │  │   └──────┘  └────────┘  └──────┘              │    │    │
│  │  └───────────────────────────────────────────────┘    │    │
│  │                                  │                     │    │
│  │                                  ▼                     │    │
│  │  ┌───────────────────────────────────────────────┐    │    │
│  │  │   Subscribers                                  │    │    │
│  │  │   - reconcile loop                             │    │    │
│  │  │   - drift sync loop                            │    │    │
│  │  │   - kp list (Step 2 验证用)                    │    │    │
│  │  │   - v2.9 sizing engine                         │    │    │
│  │  └───────────────────────────────────────────────┘    │    │
│  │                                                        │    │
│  └──────────────────────────────────────────────────────┘    │
│                                                               │
│  ┌──────────────────────────────────────────────────────┐    │
│  │              Metrics (v2.7 新增)                      │    │
│  │   MetricsClient                                       │    │
│  │   ├── KubectlTopClient    (兜底)                      │    │
│  │   └── PrometheusClient    (有 Prometheus 时)          │    │
│  │                  │                                    │    │
│  │                  ▼                                    │    │
│  │   v2.9 sizing engine (持续监控)                       │    │
│  │   v3.0 scheduler (运行时重调度)                       │    │
│  └──────────────────────────────────────────────────────┘    │
└───────────────────────────────────────────────────────────────┘
```

关键路径：
- **Informer → Dispatcher → Cache**：单向数据流
- **Cache → Subscribers**：lock-free 读
- **ShardSet → Dispatcher**：动态过滤（v2.5 sharding 联动）

---

## 二、Provider 接口

### 2.1 Informer 接口

```go
// internal/eventstream/informer.go

// Informer 抽象 K8s 资源的 watch + 事件分发
//
// 每个被监管的资源类型（Deployment / Service / Pod / Ingress）一个全局 Informer 实例。
// 跨 shard 复用 watch 连接，shard 过滤在 Dispatcher 层完成。
//
// 关键约束（Q9=A）：
//   一个 K8s 集群中，每种资源类型只有一个 Informer
//   不为"逻辑隔离"浪费 watch connection
type Informer interface {
    // Start 启动 watch 循环
    // 返回 watch goroutine 的 errChan，用于上层监控异常
    Start(ctx context.Context) <-chan error
    
    // Get 从 cache 读取（lock-free 路径）
    // 返回 nil + false 表示不存在
    Get(ns, name string) (Resource, bool)
    
    // List 按 namespace 过滤（基于 ShardSet 的 Owns 判定）
    // 返回的 slice 是 cache 快照副本，调用方可安全修改
    List(ns string) []Resource
    
    // ListAll 跨所有 namespace（仅 leader 用，shard 安全）
    ListAll() []Resource
    
    // Subscribe 订阅事件流
    // handler 在独立 goroutine 中调用，不阻塞 watch loop
    Subscribe(handler EventHandler) Subscription
    
    // Stats 监控指标
    Stats() InformerStats
    
    // Stop 停止 informer，关闭所有订阅
    Stop()
}

// EventType 事件类型
type EventType int

const (
    EventAdd EventType = iota
    EventUpdate
    EventDelete
    EventResync   // 全量 resync 时合成的事件
)

// Event 单个事件
type Event struct {
    Type      EventType
    Namespace string
    Name      string
    Old       Resource  // EventUpdate / EventDelete 时有值
    New       Resource  // EventAdd / EventUpdate / EventResync 时有值
}

type EventHandler func(e Event)

type Subscription interface {
    Unsubscribe()
    Stats() SubscriberStats
}

// InformerStats 监控指标
type InformerStats struct {
    Resource         string         // 资源类型
    CacheSize        int            // cache 中对象数
    HotCount         int            // hot 层对象数
    WarmCount        int            // warm 层对象数
    ColdCount        int            // cold 层对象数
    EventsTotal      uint64         // 总事件数
    EventsByType     map[EventType]uint64
    WatchReconnects  uint64         // watch 重连次数
    LastResyncTime   time.Time
    CacheHitRate     float64        // cache 命中率（kp list 验证）
    GoroutineCount   int            // 当前 goroutine 数
    MemoryBytes      uint64         // 估算内存占用
}

// 工厂函数
func NewInformer(ctx context.Context, opts InformerOptions) (Informer, error)

type InformerOptions struct {
    // Resource 资源类型，如 "deployments" / "services" / "pods"
    Resource string
    
    // APIVersion API 组/版本，如 "apps/v1" / "v1"
    APIVersion string
    
    // ShardSet 引用（v2.5 sharding）
    // Dispatcher 用 ShardSet.Owns(ns) 过滤事件
    // ShardSet 变化时通过 channel 通知 Informer 重订阅（Q5=C）
    ShardSet ShardSet
    
    // ResyncPeriod 全量重同步周期
    // 默认 30min，可按资源类型差异化（Q8=C）：
    //   Deployment/Service: 60min（变更少）
    //   Pod/Event:           10min（变更频繁）
    ResyncPeriod time.Duration
    
    // CachePolicy cache 分层策略
    CachePolicy CachePolicy
    
    // ReconnectPolicy watch 重连策略
    ReconnectPolicy ReconnectPolicy
}
```

### 2.2 Resource 类型（Skeleton 字段定义）

按 Q3=B+ 拍板，Skeleton 字段如下：

```go
// internal/eventstream/resource.go

// Resource cache 中的"骨架对象"
// 包含 reconcile / drift detection / metrics 关心的核心字段
// 全量数据保留在 RawJSON
//
// 关键设计（Q3=B+）：
//   增量序列化基础——只比对 skeleton 字段判断"是否真的变了"
//   比对 deep equal 整个对象快 70%+
type Resource struct {
    // ─── 不变字段（identity） ─────────────────────
    APIVersion string
    Kind       string
    Namespace  string
    Name       string
    UID        string
    
    // ─── 版本字段（critical for K8s）────────────────
    Generation      int64    // K8s spec 变更版本
    ResourceVersion string   // K8s 乐观锁字段（核心）
    
    // ─── 标签 / 注解 ──────────────────────────────
    Labels      map[string]string
    Annotations map[string]string
    
    // ─── 关键 spec 字段 ──────────────────────────
    Replicas       *int32   // Deployment / StatefulSet
    
    // ─── 关键 status 字段 ────────────────────────
    Phase          string   // Pod 状态（Running / Pending / Failed）
    ReadyReplicas  *int32   // Deployment.status
    
    // ─── 时间戳 ──────────────────────────────────
    CreationTimestamp time.Time
    DeletionTimestamp *time.Time
    
    // ─── 完整原始数据 ────────────────────────────
    // 业务通常不直接读，按需反序列化
    RawJSON []byte
    
    // ─── Cache 元数据（v2.7 内部使用）──────────────
    cacheLayer    CacheLayer  // hot / warm / cold
    lastAccessed  time.Time   // 用于 LRU 升降级判断
    accessCount   uint32      // 用于"频率驱动"分层判定
}

// SkeletonChanged 比对两个对象的 skeleton 字段是否变化
// 用于增量序列化：watch 收到 Update 事件后判断"真的变了吗"
// 如果 skeleton 没变（只是 ResourceVersion 变了），跳过 EventUpdate
func SkeletonChanged(old, new *Resource) bool {
    // ResourceVersion 不同但其他都同 = K8s 内部 housekeeping
    if old.Generation != new.Generation { return true }
    if old.Phase != new.Phase { return true }
    if !equalReplicas(old.Replicas, new.Replicas) { return true }
    if !equalReplicas(old.ReadyReplicas, new.ReadyReplicas) { return true }
    if !equalMaps(old.Labels, new.Labels) { return true }
    if !equalMaps(old.Annotations, new.Annotations) { return true }
    return false
}
```

### 2.3 ShardSet 接口（v2.5 联动）

```go
// internal/eventstream/sharding.go

// ShardSet 抽象 v2.5 的 sharding 机制
// Informer 启动时传入，dispatcher 用它过滤事件
type ShardSet interface {
    // Owns 判断本 pod 是否负责该 namespace
    // 必须 lock-free 实现（O(1)，每个事件都要调）
    Owns(ns string) bool
    
    // Subscribe 订阅 shard 变化通知
    // 返回的 channel 在 shard 重平衡时收到通知
    // Informer 收到通知后：
    //   1. 重新评估 cache 中所有对象的归属
    //   2. 不归属的对象触发 EventDelete（仍保留 cold 层备份）
    //   3. 新归属的对象触发 EventResync（重新拉取）
    Subscribe() <-chan ShardChange
}

type ShardChange struct {
    Added   []string  // 新增的 namespace
    Removed []string  // 移除的 namespace
}

// Adapter 把 v2.5 sharding.ShardSet 适配到本接口
// 避免 v2.7 直接 import internal/sharding（防 import cycle）
func NewShardSetAdapter(s sharding.ShardSet) ShardSet {
    return &shardSetAdapter{inner: s}
}
```

---

## 三、Cache 分层架构

### 3.1 三层结构（Q2=C，状态机 + 访问频率双驱动）

```
┌───────────────────────────────────────────────────────┐
│                    Cache (per Informer)               │
│                                                       │
│  ┌──────────────────────────────────────────────┐    │
│  │  Hot Layer                                    │    │
│  │  ─────────                                    │    │
│  │  存储：Skeleton + 反序列化对象                 │    │
│  │  访问：lock-free（atomic.Value snapshot）     │    │
│  │  适用：项目状态 = RUNNING / VALIDATING       │    │
│  │  适用：最近 5min 访问 ≥ 3 次                  │    │
│  │  容量：典型 ~30-100 个对象                    │    │
│  │  内存：典型 ~10-50 MB                          │    │
│  └──────────────────────────────────────────────┘    │
│                       │                               │
│                       ▼ 访问 < 3 次/5min 或 状态降级 │
│  ┌──────────────────────────────────────────────┐    │
│  │  Warm Layer                                   │    │
│  │  ────────                                     │    │
│  │  存储：Skeleton（不反序列化对象）+ RawJSON    │    │
│  │  访问：RWMutex 保护                           │    │
│  │  适用：项目状态 = IDLE / TERMINATED          │    │
│  │  适用：最近 30min 没访问                       │    │
│  │  容量：典型 ~100-500 个对象                   │    │
│  │  内存：典型 ~50-200 MB（RawJSON 占大头）      │    │
│  └──────────────────────────────────────────────┘    │
│                       │                               │
│                       ▼ Warm 层超过 size 阈值        │
│  ┌──────────────────────────────────────────────┐    │
│  │  Cold Layer                                   │    │
│  │  ────────                                     │    │
│  │  存储：仅 disk（compressed RawJSON）          │    │
│  │  访问：异步加载（mmap-backed）                │    │
│  │  适用：历史快照（drift 检测的"上一版本"）      │    │
│  │  容量：受 disk 大小限制                       │    │
│  │  内存：~0（数据在 disk 上）                    │    │
│  └──────────────────────────────────────────────┘    │
└───────────────────────────────────────────────────────┘
```

### 3.2 分层判定规则（Q2=C 双驱动并集）

```go
// internal/eventstream/cache_policy.go

type CachePolicy struct {
    // 状态机驱动（v2.5 状态信息）
    StateBasedPromotion bool  // 默认 true
    
    // 访问频率驱动
    AccessBasedPromotion bool  // 默认 true
    AccessWindow         time.Duration  // 5min
    AccessThreshold      int            // ≥ 3 次 → 升 Hot
    IdleWindow           time.Duration  // 30min
    
    // Warm → Cold 阈值
    WarmSizeThreshold int  // 默认 1000 对象
}

// promoteOrDemote 决定对象进哪一层
// 双驱动并集：任一条件成立则升级
func (p *CachePolicy) promoteOrDemote(r *Resource, projectState string) CacheLayer {
    // 状态机驱动
    if p.StateBasedPromotion {
        switch projectState {
        case "RUNNING", "VALIDATING":
            return LayerHot
        case "IDLE", "TERMINATED":
            return LayerWarm
        }
    }
    
    // 访问频率驱动
    if p.AccessBasedPromotion {
        recent := r.lastAccessed.After(time.Now().Add(-p.AccessWindow))
        if recent && r.accessCount >= uint32(p.AccessThreshold) {
            return LayerHot
        }
        idle := r.lastAccessed.Before(time.Now().Add(-p.IdleWindow))
        if idle {
            return LayerCold
        }
    }
    
    // 默认 Warm
    return LayerWarm
}
```

### 3.3 lock-free Hot 层实现（Q4=C）

immutable snapshot + atomic.Value：

```go
// internal/eventstream/cache_hot.go

// HotCache 是 lock-free 读路径的核心
// 写时构造新快照，原子替换 root pointer
// 读者持有当前快照引用，无锁
type HotCache struct {
    // snapshot 存储当前 cache 快照
    // 读：snapshot.Load() 拿到 immutable map
    // 写：构造新 map → snapshot.Store(newMap)
    snapshot atomic.Value  // *hotSnapshot
    
    // writeMu 写者之间的互斥（避免并发写丢失）
    // 读者完全 lock-free，不持有此锁
    writeMu sync.Mutex
}

type hotSnapshot struct {
    items map[string]*Resource  // key = "ns/name"
}

// Get lock-free 读
// 性能：~50ns（无锁）
func (h *HotCache) Get(ns, name string) (*Resource, bool) {
    snap := h.snapshot.Load().(*hotSnapshot)
    if snap == nil {
        return nil, false
    }
    r, ok := snap.items[ns+"/"+name]
    return r, ok
}

// Put 写时构造新快照
// 性能：~5-10μs（构造 map 副本 + atomic 替换）
// 适用场景：写少（reconcile ~30s 一次）
func (h *HotCache) Put(r *Resource) {
    h.writeMu.Lock()
    defer h.writeMu.Unlock()
    
    // 1. 加载当前快照
    oldSnap := h.snapshot.Load().(*hotSnapshot)
    
    // 2. 构造新快照（copy-on-write）
    newItems := make(map[string]*Resource, len(oldSnap.items)+1)
    for k, v := range oldSnap.items {
        newItems[k] = v
    }
    newItems[r.Namespace+"/"+r.Name] = r
    
    // 3. 原子替换
    h.snapshot.Store(&hotSnapshot{items: newItems})
    
    // 4. 旧快照被 GC 回收（持有引用的读者继续看到旧版本）
}

// List 全量列出（lock-free）
// 性能：取决于 cache 大小，但都是 O(N) 单纯拷贝
func (h *HotCache) List() []*Resource {
    snap := h.snapshot.Load().(*hotSnapshot)
    if snap == nil {
        return nil
    }
    result := make([]*Resource, 0, len(snap.items))
    for _, r := range snap.items {
        result = append(result, r)
    }
    return result
}

// 性能预测（基于 Go 1.25 + Apple M1）：
//   Get:  ~50ns
//   Put:  ~5-10μs（cache size 100 时）
//   List: ~5μs（拷贝 100 个 pointer）
//   
//   vs RWMutex 实现：
//   Get:  ~200-500ns（锁竞争）
//   提升: 5-10x
```

---

## 四、增量序列化（Skeleton 比对）

### 4.1 问题与方案

```
问题：
  K8s watch event 收到 Update 时，server 把整个对象 JSON 推过来
  即使只变了一个 label（几个字节）
  client 收到的 RawJSON 通常 ~5-50KB
  反序列化 + deep equal 整个对象 → CPU 浪费
  
解法：
  1. 反序列化时只填 Skeleton 字段（轻量）
  2. RawJSON 保留（用于 cold cache 和 debug）
  3. Update 事件先比对 Skeleton：
     - Skeleton 没变 → 跳过 EventUpdate（K8s housekeeping，例如 ResourceVersion）
     - Skeleton 变了 → 发 EventUpdate（业务关心的变更）
  
预期收益：
  events 总流量降低 40-60%
  CPU 反序列化降低 70%+
```

### 4.2 实现路径

```go
// internal/eventstream/serializer.go

// ParseSkeleton 从 RawJSON 解出 Skeleton 字段
// 不反序列化整个对象
func ParseSkeleton(rawJSON []byte) (*Resource, error) {
    var partial struct {
        APIVersion string `json:"apiVersion"`
        Kind       string `json:"kind"`
        Metadata struct {
            Namespace         string            `json:"namespace"`
            Name              string            `json:"name"`
            UID               string            `json:"uid"`
            Generation        int64             `json:"generation"`
            ResourceVersion   string            `json:"resourceVersion"`
            Labels            map[string]string `json:"labels"`
            Annotations       map[string]string `json:"annotations"`
            CreationTimestamp string            `json:"creationTimestamp"`
            DeletionTimestamp *string           `json:"deletionTimestamp"`
        } `json:"metadata"`
        Spec struct {
            Replicas *int32 `json:"replicas"`
        } `json:"spec"`
        Status struct {
            Phase         string `json:"phase"`
            ReadyReplicas *int32 `json:"readyReplicas"`
        } `json:"status"`
    }
    
    if err := json.Unmarshal(rawJSON, &partial); err != nil {
        return nil, err
    }
    
    return &Resource{
        APIVersion:        partial.APIVersion,
        Kind:              partial.Kind,
        Namespace:         partial.Metadata.Namespace,
        Name:              partial.Metadata.Name,
        UID:               partial.Metadata.UID,
        Generation:        partial.Metadata.Generation,
        ResourceVersion:   partial.Metadata.ResourceVersion,
        Labels:            partial.Metadata.Labels,
        Annotations:       partial.Metadata.Annotations,
        Replicas:          partial.Spec.Replicas,
        Phase:             partial.Status.Phase,
        ReadyReplicas:     partial.Status.ReadyReplicas,
        CreationTimestamp: parseTime(partial.Metadata.CreationTimestamp),
        DeletionTimestamp: parseTimePtr(partial.Metadata.DeletionTimestamp),
        RawJSON:           rawJSON,
    }, nil
}
```

### 4.3 ResourceVersion 的关键作用

```
ResourceVersion 是 K8s 乐观锁字段，v2.7 中有 3 个用途：

1. Watch 重连续传
   watch 断线后用 listResourceVersion=X 续传
   只拉取 RV > X 的事件
   不重新跑全量
   
2. 增量序列化判定
   如果 RV 变了但 skeleton 没变 → K8s 内部 housekeeping
   不向 subscribers 发 EventUpdate（节省下游开销）
   
3. 并发安全（reconcile 写场景）
   Update K8s 资源时带 ResourceVersion
   server 端比较 RV：相等才 update
   不等返回 conflict（reconcile 重试）
```

---

## 五、Watch 重连机制

按 Q7=C 拍板：指数退避 + jitter=0.2

```go
// internal/eventstream/reconnect.go

type ReconnectPolicy struct {
    InitialBackoff time.Duration  // 默认 1s
    MaxBackoff     time.Duration  // 默认 30s
    BackoffFactor  float64        // 默认 2.0
    Jitter         float64        // 默认 0.2 (Q7=C)
    MaxAttempts    int            // 0 = 无限重试
}

// nextBackoff 计算下一次重连等待时间
// jitter 避免雷鸣群（thundering herd）：
//   多个 Informer 同时断线时，加 ±20% 抖动错开重连
func (p *ReconnectPolicy) nextBackoff(attempt int) time.Duration {
    base := time.Duration(float64(p.InitialBackoff) * 
                          math.Pow(p.BackoffFactor, float64(attempt)))
    if base > p.MaxBackoff {
        base = p.MaxBackoff
    }
    
    // jitter: ± 20% 随机
    jitterRange := float64(base) * p.Jitter
    jitterAmount := time.Duration((rand.Float64()*2 - 1) * jitterRange)
    
    return base + jitterAmount
}

// 重连退避序列示例（jitter=0.2）：
//   尝试 1: 1s   ± 0.2s  → [0.8s, 1.2s]
//   尝试 2: 2s   ± 0.4s  → [1.6s, 2.4s]
//   尝试 3: 4s   ± 0.8s  → [3.2s, 4.8s]
//   尝试 4: 8s   ± 1.6s  → [6.4s, 9.6s]
//   尝试 5: 16s  ± 3.2s  → [12.8s, 19.2s]
//   尝试 6+: 30s ± 6s    → [24s, 36s]（封顶 + jitter）

// startWatchLoop 启动 watch 循环（含重连）
func (i *informerImpl) startWatchLoop(ctx context.Context) {
    attempt := 0
    
    for {
        select {
        case <-ctx.Done():
            return
        default:
        }
        
        // 重连前等待
        if attempt > 0 {
            backoff := i.opts.ReconnectPolicy.nextBackoff(attempt - 1)
            slog.Warn("watch 重连等待",
                "resource", i.opts.Resource,
                "attempt", attempt,
                "backoff", backoff)
            select {
            case <-ctx.Done():
                return
            case <-time.After(backoff):
            }
        }
        
        // 启动 watch
        i.stats.WatchReconnects.Add(1)
        err := i.runWatchOnce(ctx)
        
        if err == nil {
            // 正常退出（ctx canceled）
            return
        }
        
        slog.Error("watch 失败，将重连",
            "resource", i.opts.Resource,
            "error", err,
            "attempt", attempt+1)
        attempt++
    }
}
```

---

## 六、按资源类型差异化 Resync

按 Q8=C 拍板：默认 30min，按资源类型差异化

```go
// internal/eventstream/resync.go

// DefaultResyncPeriod 按资源类型返回默认 resync 周期
// 设计依据：变更频率
//   高频资源（Pod / Event）→ 短周期，避免漏事件
//   低频资源（Service / ConfigMap）→ 长周期，节省 K8s API server 压力
func DefaultResyncPeriod(resource string) time.Duration {
    switch resource {
    // ─── 高频变更（10min）─────────────────────
    case "pods", "events":
        return 10 * time.Minute
        
    // ─── 中频变更（30min）─────────────────────
    case "deployments", "statefulsets", "daemonsets", "replicasets":
        return 30 * time.Minute
        
    // ─── 低频变更（60min）─────────────────────
    case "services", "configmaps", "secrets", "ingresses", "horizontalpodautoscalers":
        return 60 * time.Minute
        
    // ─── 极低频（120min）─────────────────────
    case "namespaces", "nodes", "storageclasses", "persistentvolumes":
        return 120 * time.Minute
        
    default:
        return 30 * time.Minute  // 兜底
    }
}

// 用户可覆盖：
//   InformerOptions{
//     Resource:     "deployments",
//     ResyncPeriod: 15 * time.Minute,  // 用户特定场景显式指定
//   }
```

---

## 七、Shard Dispatcher（事件过滤层）

按 Q9=A 拍板：每种资源一个全局 Informer，shard 过滤在 dispatcher 层。

```go
// internal/eventstream/dispatcher.go

// Dispatcher 把 Informer 收到的事件路由给 subscribers
// 关键职责：
//   1. 按 ShardSet.Owns(ns) 过滤（只发本 pod 关心的事件）
//   2. 异步分发给所有 subscribers
//   3. 慢 subscriber 不阻塞 watch loop
type Dispatcher struct {
    shardSet ShardSet
    subs     atomic.Value  // *subscriberSet
    
    // shardChangeCh 接收 ShardSet 变化通知
    shardChangeCh <-chan ShardChange
    
    // workerPool 异步分发用的 worker 池
    workerPool chan struct{}
}

func (d *Dispatcher) Dispatch(e Event) {
    // Step 1: shard 过滤（关键路径）
    if !d.shardSet.Owns(e.Namespace) {
        return
    }
    
    // Step 2: 异步发给所有 subscribers
    subs := d.subs.Load().(*subscriberSet)
    for _, sub := range subs.list {
        d.dispatchAsync(sub, e)
    }
}

func (d *Dispatcher) dispatchAsync(sub *subscriber, e Event) {
    select {
    case d.workerPool <- struct{}{}:
        // 拿到 worker slot
        go func() {
            defer func() { <-d.workerPool }()
            
            // panic 隔离（subscriber handler 不能影响 dispatcher）
            defer func() {
                if r := recover(); r != nil {
                    slog.Error("subscriber handler panic",
                        "subscriber", sub.id,
                        "event", e,
                        "panic", r)
                    sub.stats.Panics.Add(1)
                }
            }()
            
            sub.handler(e)
            sub.stats.EventsDelivered.Add(1)
        }()
    default:
        // worker 池满，drop event 并记录
        sub.stats.EventsDropped.Add(1)
        slog.Warn("dispatcher worker pool 满，drop event",
            "subscriber", sub.id,
            "event", e)
    }
}

// HandleShardChange 处理 shard 变化
// v2.5 sharding 重平衡时调用
func (d *Dispatcher) HandleShardChange(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        case change := <-d.shardChangeCh:
            // 1. Removed namespace：触发 EventDelete（subscribers 清理状态）
            for _, ns := range change.Removed {
                d.synthesizeRemovedEvents(ns)
            }
            // 2. Added namespace：触发 EventResync（subscribers 拉取新数据）
            for _, ns := range change.Added {
                d.synthesizeResyncEvents(ns)
            }
        }
    }
}
```

---

## 八、Metrics 接入

按 Q6=B 拍板：kubectl top + Prometheus 双 client

```go
// internal/eventstream/metrics.go

// MetricsClient 抽象 metrics 来源
// 不进 cache（数据时间敏感，用完即丢）
type MetricsClient interface {
    // GetPodMetrics 拉取 Pod 当前 CPU/Memory usage
    GetPodMetrics(ctx context.Context, ns, name string) (PodMetrics, error)
    
    // GetNodeMetrics 拉取 Node 当前 CPU/Memory allocatable & used
    GetNodeMetrics(ctx context.Context, name string) (NodeMetrics, error)
    
    // GetHistorical 历史趋势（仅 Prometheus 实现）
    GetHistorical(ctx context.Context, q HistoricalQuery) ([]TimePoint, error)
    
    // Capabilities 报告 client 能力
    Capabilities() ClientCapabilities
}

type ClientCapabilities struct {
    SupportsRealtime    bool  // 能拉当前点
    SupportsHistorical  bool  // 能拉历史趋势
    HistoricalRange     time.Duration  // 历史数据保留时长
}

// ─── Implementation 1: KubectlTopClient（兜底，最低要求） ─────
//
// 通过 kubectl top pod / kubectl top node 拉取 metrics
// 优点：无外部依赖（已用 internal/executor）
// 缺点：只能拉当前点，不能拉历史
type KubectlTopClient struct {
    executor executor.Executor
}

func (c *KubectlTopClient) GetPodMetrics(ctx context.Context, ns, name string) (PodMetrics, error) {
    out, err := c.executor.Kubectl(ctx, "", "top", "pod", name, "-n", ns, "--no-headers")
    if err != nil {
        return PodMetrics{}, fmt.Errorf("kubectl top pod failed: %w", err)
    }
    return parseTopPodOutput(string(out))
}

func (c *KubectlTopClient) GetHistorical(ctx context.Context, q HistoricalQuery) ([]TimePoint, error) {
    return nil, ErrHistoricalNotSupported
}

func (c *KubectlTopClient) Capabilities() ClientCapabilities {
    return ClientCapabilities{
        SupportsRealtime:   true,
        SupportsHistorical: false,
    }
}

// ─── Implementation 2: PrometheusClient（推荐生产） ──────────
//
// 通过 Prometheus HTTP API 拉取
// 优点：有历史趋势（v2.9 DP 必需）
// 缺点：依赖 Prometheus 部署
type PrometheusClient struct {
    endpoint string  // 如 http://prometheus.monitoring:9090
    client   *http.Client
}

func (c *PrometheusClient) GetHistorical(ctx context.Context, q HistoricalQuery) ([]TimePoint, error) {
    // 构造 PromQL 查询
    // 例：rate(container_cpu_usage_seconds_total{namespace="X",pod="Y"}[1m])
    promQL := buildPromQL(q)
    return c.queryRange(ctx, promQL, q.Start, q.End, q.Step)
}

func (c *PrometheusClient) Capabilities() ClientCapabilities {
    return ClientCapabilities{
        SupportsRealtime:   true,
        SupportsHistorical: true,
        HistoricalRange:    7 * 24 * time.Hour,  // Prometheus 默认保留 15 天，保守 7 天
    }
}

// ─── 工厂 ────────────────────────────────────────────────────
//
// 自动选择：
//   1. 检测 Prometheus 可达 → PrometheusClient
//   2. 不可达 → KubectlTopClient 兜底
func NewMetricsClient(ctx context.Context, opts MetricsOptions) (MetricsClient, error) {
    if opts.PrometheusEndpoint != "" {
        client := &PrometheusClient{endpoint: opts.PrometheusEndpoint}
        if err := client.Validate(ctx); err == nil {
            return client, nil
        }
        slog.Warn("Prometheus 不可达，降级到 KubectlTopClient",
            "endpoint", opts.PrometheusEndpoint)
    }
    return &KubectlTopClient{executor: executor.GetExecutor()}, nil
}
```

---

## 九、与 v2.5 sharding 集成（动态联动）

按 Q5=C 拍板：启动时 + 运行时变化都处理

```go
// internal/eventstream/integration_sharding.go

// shardSetAdapter 把 v2.5 sharding.ShardSet 适配到 eventstream.ShardSet
// 避免 import cycle
type shardSetAdapter struct {
    inner    sharding.ShardSet
    changeCh chan ShardChange
}

func NewShardSetAdapter(inner sharding.ShardSet) *shardSetAdapter {
    a := &shardSetAdapter{
        inner:    inner,
        changeCh: make(chan ShardChange, 16),
    }
    go a.watchLoop()
    return a
}

func (a *shardSetAdapter) Owns(ns string) bool {
    // 必须 lock-free（O(1)），每个事件都要调
    return a.inner.Owns(ns)
}

func (a *shardSetAdapter) Subscribe() <-chan ShardChange {
    return a.changeCh
}

// watchLoop 监听 v2.5 sharding 的 OnShardChanged 回调
// 转换成 eventstream.ShardChange 推到 channel
func (a *shardSetAdapter) watchLoop() {
    for shardChg := range a.inner.Changes() {
        select {
        case a.changeCh <- ShardChange{
            Added:   shardChg.AddedNamespaces,
            Removed: shardChg.RemovedNamespaces,
        }:
        default:
            // channel 满了，drop 并 warn
            slog.Warn("eventstream shard change channel 满，drop change")
        }
    }
}
```

---

## 十、灰度上线计划（Q10=C 三阶段）

```
Step 1: 写完 internal/eventstream/ 包
  ────────────────────────────────────────
  内容：
    - 完整接口实现（Informer / Cache / Dispatcher / Metrics）
    - 单测覆盖 ≥ 200 个 case
    - 完整 godoc
    - benchmark vs client-go 对比数据公开

  集成点：
    暂不动 internal/controller
    新包独立可用，但不被生产代码引用
    
  验收：
    make dev 全绿
    新增 27+ 个 sub-cases 全 PASS（参考 v2.6 route 包密度）
    benchmark 数据公开到 docs/design/eventstream-perf.md
    
  工作量：第 1 周（5 工作日）

Step 2: 写 kp list 命令（验证用，关键里程碑）⭐
  ────────────────────────────────────────
  目的：
    把 internal/eventstream 暴露成"用户可见命令"
    通过真实使用场景验证 cache 命中率 + 延迟改善
    
  实现：
    kp list                  → 列所有 namespace 资源
    kp list deployments      → 列 Deployment
    kp list pods -n my-ns    → 列指定 ns
    kp list --all-resources  → 全量（用于 stress test）
    
  验证指标：
    1. Cache 命中率 ≥ 95%
       kp list 跑 100 次，95+ 次直接读 cache
    2. 延迟改善
       kp list vs kubectl get 对比
       期望 p50 改善 10x，p99 改善 5x
    3. 数据一致性
       kp list 和 kubectl get 返回结果 100% 一致
    4. Watch 完整性
       手动改 K8s 资源 → kp list 立即反映
       
  实测脚本：
    benchmark/scripts/eventstream-validate.sh
    自动跑 100+ 次 kp list / kubectl get 对比
    
  工作量：第 2 周（5 工作日）
  
Step 3: 全切（生产代码引用 eventstream）
  ────────────────────────────────────────
  改造路径：
    1. internal/controller/global.go
       reconcile 改用 informer.Get() / List()
       不再直接 kubectl get
       
    2. internal/controller/drift_sync.go
       drift 检测改用 cache 比对
       不再每轮拉全量
       
    3. internal/controller/resources.go
       DetectResourceExists 改用 cache
       
  灰度策略：
    feature flag: KUBEPIVOT_USE_EVENTSTREAM=true (默认 false)
    分批开启：
    Week 3 Day 1-2: 测试集群开启
    Week 3 Day 3-4: 一个生产 namespace 开启
    Week 3 Day 5: 全量开启
    
  回退路径：
    任何阶段发现问题
    KUBEPIVOT_USE_EVENTSTREAM=false 回退
    eventstream 包仍在但不被引用
    不影响 v2.6 既有行为
    
  工作量：第 3 周（5 工作日）
```

---

## 十一、Benchmark 计划（vs client-go）

第 1 周第 1 件事，决定 v2.7 路径。

```
fork 分支：
  feature/client-go-comparison

实现：
  internal/eventstream-clientgo/   （仅在该分支存在）
  把 client-go SharedInformerFactory 包一层
  暴露与 eventstream 相同的 Informer 接口
  Cache 直接用 client-go 的 cache.Store

测试矩阵：
  规模：P=10 / P=50 / P=100 项目
  操作：
    - 启动后 30s 稳态 watch（基线）
    - kp list（cache 读）100 次
    - 模拟 K8s API 抖动（关闭 watch 30s 再恢复）
  指标：
    - 内存占用（pod RSS）
    - CPU 占用（avg / peak）
    - cache 读延迟（p50 / p99）
    - watch 重连恢复时间

判定标准：
  自研 vs client-go 性能差距：
    
    ≤ 10%   → 自研路径（哲学 + 工程美学，差距可接受）
    10-30%  → 自研，但优化重点在 cache 写路径
    30-50%  → 自研，但记录差距 + v2.7.1 优化
    > 50%   → 严肃讨论，是否 client-go 包一层
              （但仍倾向自研，按 Q1=C "默认偏向自研"原则）

数据归档：
  docs/design/eventstream-perf.md
  完整数据 + 分析 + 决策记录
  公开（不藏数据）
```

---

## 十二、14 个设计 Q（拍板表）

| Q | 问题 | 选项 | 决定 | 备注 |
|---|------|------|------|------|
| Q1 | 自研 vs client-go 包一层 | A 完全自研 / B 包一层 / C benchmark 决定 | **C** | 默认偏向 A，benchmark 验证 |
| Q2 | Cache 分层判定 | A 状态机 / B 访问频率 / C 双驱动 | **C** | 双驱动并集 |
| Q3 | Skeleton 字段定义 | A 极简 / B 标准 / C 详细 | **B+** | 必须含 ResourceVersion |
| Q4 | Hot 层 lock-free 实现 | A atomic.Pointer / B sync.Map / C immutable snapshot | **C** | 用 atomic.Value 存根指针 |
| Q5 | ShardSet 集成方式 | A 启动时 / B 运行时 / C 双方 | **C** | 动态更新必须 |
| Q6 | Metrics client | A kubectl top / B 双 client / C 三 client | **B** | kubectl + Prometheus |
| Q7 | Watch 重连 | A 立即 / B 指数退避 / C B + jitter | **C** | jitter=0.2 |
| Q8 | 全量 resync 周期 | A 不做 / B 30min / C 按资源差异化 | **C** | 默认 30min，差异化覆盖 |
| Q9 | Informer 实例粒度 | A 全局 / B per shard / C 内部路由 | **A** | shard 在 dispatcher 过滤 |
| Q10 | 集成上线方式 | A 替换 / B 并行 / C 渐进 | **C** | 三阶段 + kp list 验证 |

未在拍板会上但实施时需要的隐含 Q（v2.7 实施时再细化）：

| Q | 问题 | 当前默认 |
|---|------|---------|
| Q11 | watch 用 HTTP / gRPC | HTTP（K8s API server 默认） |
| Q12 | RawJSON 大对象（>4KB）是否 mmap | v2.7.0 不做，v2.7.1 评估 |
| Q13 | Informer Restart 行为 | 重启不丢 cache（异常退出由 watch loop 重连） |
| Q14 | 测试集群规模 | orbstack 8GiB 足够（P=50 已验证） |

---

## 十三、风险与对策

```
风险 1：自研 Cache 性能不如 client-go
  概率：中等
  影响：v2.7 价值打折
  对策：
    - benchmark 第 1 周跑数据
    - 数据公开到 docs/design/eventstream-perf.md
    - 差距 < 10% → 走自研
    - 差距 > 50% → 严肃讨论但仍倾向自研
    - 不藏数据，工程诚实

风险 2：增量序列化引入 bug
  概率：中等
  影响：reconcile 漏检测变更 → 漂移
  对策：
    - skeleton 字段保守（多包含字段）
    - 加 fallback 选项（高敏感场景退回 deep equal）
    - 集成测试：手动改 K8s 资源 → 验证 informer 检测到
    - kp list 验证 step 实测一致性

风险 3：Cache 内存爆炸
  概率：低（warm 不存反序列化对象）
  影响：controller pod OOM
  对策：
    - WarmSizeThreshold 限制
    - prometheus 监控 informer.cache_size_bytes
    - LRU + size 双重 eviction

风险 4：v2.5 sharding 与 informer 死锁
  概率：低（设计已避免）
  影响：shard 变化时 informer 阻塞
  对策：
    - ShardSet.Owns() 必须 lock-free
    - shard 变化通知用 channel + buffer
    - informer 重订阅在独立 goroutine

风险 5：Watch 重连 jitter 配错
  概率：低
  影响：要么不分散（雷鸣群），要么过度分散（大集群启动慢）
  对策：
    - jitter=0.2 是行业经验值（client-go 也用此值）
    - benchmark 验证大规模启动场景

风险 6：Step 2 kp list 与 kubectl get 不一致
  概率：中等（critical bug）
  影响：用户对 cache 失去信任
  对策：
    - validate 脚本自动对比 100+ 次
    - 不一致立即 fix
    - Step 3 必须 100% 一致才能进入

风险 7：metrics client 拉取慢拖慢 reconcile
  概率：中等
  影响：reconcile 周期被拉长
  对策：
    - metrics 拉取异步（不阻塞 reconcile）
    - 缓存最近 N 秒 metrics（5s 内多次请求合并）
    - timeout 强制（kubectl top 5s 超时）
```

---

## 十四、验收标准

### 14.1 功能验收

```
✓ Informer 启动 / 停止 / 重连测试通过
✓ Cache 三层升降级正确
✓ skeleton 比对正确（增量序列化）
✓ ShardSet 动态变化时 cache 同步更新
✓ watch 重连退避 + jitter 行为正确
✓ kp list 输出与 kubectl get 100% 一致
✓ Pod / Node metrics 双 client 都能拉到数据
```

### 14.2 性能验收

```
✓ Hot cache 读延迟 < 50ns
✓ Cache 命中率 ≥ 95%（kp list 验证）
✓ kp list vs kubectl get：p50 改善 ≥ 10x
✓ 全量 resync 30min 触发耗时 < 5s
✓ vs client-go benchmark 数据公开
✓ 单 pod 内存占用比 v2.5 降低 ≥ 20%
```

### 14.3 集成验收

```
✓ v2.5 既有 reconcile 路径全切到 informer
✓ make dev 全绿
✓ orbstack 集群 P=50 跑稳态 5min
✓ feature flag 开关正确（可一键回退）
✓ Step 3 全切后行为与 v2.5 一致
```

---

## 十五、工作量预估

```
3 周专注 = 21 天

Week 1: Cache + Informer 主体 + benchmark
  Day 1: benchmark 框架 + client-go 对比 baseline
  Day 2-3: Resource / Skeleton / Cache 三层实现
  Day 4: Informer 接口 + watch 实现
  Day 5: 单测 + benchmark 数据收集
  里程碑: benchmark 数据公开 → 决定 v2.7 路径

Week 2: kp list + 集成验证
  Day 6-7: Dispatcher + Subscribers
  Day 8: ShardSet 集成 + adapter
  Day 9: kp list 命令 + 一致性 validate 脚本
  Day 10: metrics client 双实现 + 接入测试
  里程碑: kp list 命中率 ≥ 95% + 一致性 100%

Week 3: 全切 + 生产灰度
  Day 11-12: 改造 internal/controller 用 informer
  Day 13: feature flag + 回退路径
  Day 14: 测试集群灰度
  Day 15: 文档收尾 + v2.7.0 release
  里程碑: kp release v2.7.0
```

---

## 十六、与既有功能 / 文档的关系

```
依赖：
  v2.5 sharding（ShardSet 接口）
  v2.6 controller（reconcile 路径接入点）

被依赖：
  v2.9 Resource Sizing Engine（消费 metrics + cache）
  v3.0 Intelligent Scheduler（消费 cache + node 状态）
  v2.6.x kp pvc list（潜在受益，按需切换）

文档关系：
  ROADMAP.md v2.7 章节 ← 本草案是其细化
  decision-stack.md ← 本草案是 Layer 2/3 的"基础设施层"
  HANDOFF.md ← v2.7 release 后更新引用
```

---

## 编辑记录

```
2026-04-27  设计草案创建（v2.6.0 release 后第二天上午）
            
            背景：
            v2.6.0 release 后启动 v2.7 实施。
            10 个设计 Q 全部当场拍板。
            
            qc 关键扩展：
            - Q3+: ResourceVersion 必须进 skeleton
            - Q4 用 atomic.Value（不是 atomic.Pointer）
            - Q7 jitter=0.2 明确化
            - Q8 按资源类型差异化 resync 周期
            - Q9 shard 过滤在 dispatcher（不为隔离浪费 connection）
            - Q10 Step 2 写 kp list 验证 cache 命中率（关键里程碑）
            
            写作约定：
            - 高度技术（Q1=A 风格，含完整 Go 接口草图）
            - 工程伦理（含 benchmark + 灰度上线 + 回退路径）
            - 不藏数据（benchmark 数据公开）
            
            实施时 finalize 路径：
            - draft.md → eventstream.md（v2.7.0 release 时）
            - 内容：删 "待实施" 标记 + 加 "v2.7.0 已实施" 章节
            - 引用 internal/eventstream/ 实际代码 + benchmark 数据
```
