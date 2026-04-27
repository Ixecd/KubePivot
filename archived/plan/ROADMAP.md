# KubePivot v2.7 → v3.0 Roadmap

> 编写日期：2026-04-26
> 编写人：qc（v2.6.0 release 当日下午）
> 状态：📐 远期规划（持续演化）
> 范围：v2.7.0 / v2.8.0 / v2.8.1 / v2.9.0 / v3.0.0 + v3.x+ 远期备忘
> 预计周期：2026 Q2 末 → Q3 末（约 4-5 个月）

---

## 摘要

v2.6.0 release 后，KubePivot 进入**第二个完整工程章**。

第一章（v1.0 → v2.6）的叙事是：**工具 → 工程**
- v1.x 时代（dev-toolkit）：可用的命令行工具
- v2.0-v2.4：把命令式部署变成"声明式 + 状态机"
- v2.5：HA + 分片，工程级别可用
- v2.6：流量层抽象 + 声明式蓝绿，GitOps 范式完整

第二章（v2.7 → v3.0）的叙事是：**工程 → 智能调度系统**
- v2.7：自研 Informer + Cache —— 事件流基础设施
- v2.8：企业治理（SSO / RBAC / 加密 / 供应链）
- v2.8.1：v2.x 收尾（web3-blitz 升级 + 数据保护）
- v2.9：资源 Sizing 引擎（Pod × CPU × Memory 二维 DP）
- v3.0：智能调度系统（节点 × Pod × 资源类型 + 维度 B 协同）

**核心拐点**：v2.9 → v3.0 之间，KubePivot 从"工具"变成"决策系统"。
用户不再写 `replicas: 3 / cpu: 200m`，由 KubePivot 算出最优值。

---

## 路线图概览

```
v2.6.0 ✅ (2026-04-26 release, commit 1f780c2)
   │
   ├─ v2.7.0   Event Stream Infrastructure        ~3 周
   │            自研 Informer + Cache 优化
   │            metrics 接入 + 事件流抽象
   │            为 v2.9 智能调度打地基
   │
   ├─ v2.8.0   Enterprise Governance              ~3 周
   │            A. SSO / OAuth 集成
   │            B. RBAC 多团队隔离
   │            G. 加密 at rest（etcd / Secret）
   │            H. 镜像签名 + 供应链安全
   │
   ├─ v2.8.1   v2.x 收尾（持续改进，不打 tag）    ~1 周
   │            web3-blitz 升级到 v2.6 蓝绿
   │            数据保护（resources.yaml protect: true）
   │
   ├─ v2.9.0   Resource Sizing Engine（维度 B）   ~4 周
   │            Pod × (CPU, Memory) 二维 DP
   │            部署前最优 sizing
   │            目标：单 Pod 利用率 70%+
   │
   ├─ v3.0.0   Intelligent Scheduling System      ~6 周
   │            维度 A：节点 × Pod × 资源类型
   │            维度 B：Pod × CPU × Memory（继承 v2.9）
   │            双 DP 协同 + 周期性重调度
   │            目标：节点 CPU 85%+, Memory 70%+
   │            "KubePivot 智能调度系统"完整叙事
   │
   └─ v3.x+    多租户 / 商业化探索（待方向明确）
                organization 隔离 + SaaS 前置
                依赖 v3.0 智能调度的成熟
```

预计 **v3.0 release：2026 年 8-9 月**

---

## v2.7 — Event Stream Infrastructure

### 1. 目标

```
为 KubePivot 后续高级功能（智能调度、canary、流量观测）
打造**事件流 + 高性能 Cache**基础设施。

核心论断：
  Informer ≠ Cache
  Informer = "watch + cache + 事件分发"的复合
  
  KubePivot v2.7 的真正价值不是"自己实现一遍 client-go 的 informer"
  是为 KubePivot 自己的 reconcile 模式做**专用 Cache**：
  - 与 v2.5 sharding 深度集成（每 shard 一个 cache 实例）
  - 按业务关心程度分层（hot/warm/cold）
  - lock-free 读路径
  - 增量序列化 + 内存对齐
  
client-go 的 informer 是通用方案。
KubePivot 的 informer 是专用方案。
专用方案有可能比通用方案性能高 30-50%（理论上限）。
```

### 2. 关键设计

#### 2.1 Informer 整体架构

```go
// internal/eventstream/informer.go (草图)

// Informer 抽象 K8s 资源的 watch + cache。
// 每个被监管的资源类型（Deployment / Service / Ingress / ...）一个 Informer 实例。
type Informer interface {
    // Start 启动 watch 循环（非阻塞，返回 watch goroutine 的 errChan）
    Start(ctx context.Context) <-chan error
    
    // Get 从 cache 读取（lock-free 路径）
    Get(ns, name string) (Resource, bool)
    
    // List 按 namespace 过滤（v2.5 sharding 接入点）
    List(ns string) []Resource
    
    // Subscribe 订阅事件流
    // Subscriber 收到 Add/Update/Delete 事件
    Subscribe(handler EventHandler) Subscription
    
    // Stats 监控指标（cache 命中率 / watch 重连次数 / 内存占用）
    Stats() InformerStats
}

type EventType int
const (
    EventAdd EventType = iota
    EventUpdate
    EventDelete
)

type Event struct {
    Type      EventType
    Namespace string
    Name      string
    Old       Resource  // 仅 Update / Delete 有值
    New       Resource  // 仅 Add / Update 有值
}

type EventHandler func(e Event)

type Resource struct {
    APIVersion string
    Kind       string
    Namespace  string
    Name       string
    UID        string
    Generation int64       // 用于增量序列化
    Labels     map[string]string
    
    // RawJSON 原始数据
    // 业务通常不直接读，而是按需反序列化
    RawJSON    []byte
}

// 工厂函数
func NewInformer(ctx context.Context, opts InformerOptions) (Informer, error)

type InformerOptions struct {
    Resource     string  // "deployments" / "services" / ...
    Namespaces   []string  // 空 = 全集群（按 shard 过滤）
    ResyncPeriod time.Duration  // 全量重同步周期，默认 30min
    
    // ShardSet 指向 v2.5 的 sharding.ShardSet
    // Informer 内部按 shard 过滤事件
    ShardSet     interface {
        Owns(ns string) bool
    }
}
```

#### 2.2 Cache 优化策略（v2.7 真心脏）

```
分层 Cache：
  hot cache   高频访问的资源（RUNNING 状态项目的 Deployment / Service）
              内存 + 反序列化后的对象
              读路径：sync.Map 或 atomic.Value，lock-free
              
  warm cache  低频访问（IDLE 项目）
              内存，但只存 RawJSON，按需反序列化
              读路径：RWMutex，写少读多场景下接近 lock-free
              
  cold cache  历史快照（用于 drift 检测的"上一版本"对比）
              磁盘 / etcd（不长期占内存）
              读路径：异步加载

分层判定：
  根据状态机：
    项目状态 = RUNNING / VALIDATING → hot cache
    项目状态 = IDLE / TERMINATED → warm cache
    项目状态 = 历史快照 → cold cache
    
  根据访问频率：
    最近 5min 被访问 ≥3 次 → 升级到 hot
    最近 30min 没访问 → 降级到 warm
```

```
增量序列化：
  问题：每次 watch 收到 update 事件，K8s 把整个对象的 JSON 发回来
        即使只变了一个 label，也是几 KB 的 RawJSON
        反序列化整个对象 + 比对 → CPU 浪费
  
  解法：
    1. 维护"对象骨架"（namespace / name / kind / labels / annotations / spec.replicas）
       这些是 KubePivot reconcile 关心的核心字段
    2. RawJSON 仍保留（用于 cold cache 和 debug）
    3. 比对时只比 skeleton（避免 deep equal 整个对象）
    4. 只有 skeleton 变化才触发 EventUpdate

  效果：
    估算：events 总流量降低 40-60%
    CPU 反序列化降低 70%+
    
  验证：v2.7 实施时跑 benchmark 对比有/无增量序列化的 CPU 占用
```

```
内存对齐 + GC 压力：
  问题：cache 里大量 small struct + map，GC 压力大
        v2.5 实测：controller pod memory 70% 时 GC 占 CPU 5%+
  
  解法：
    1. Resource struct 字段重排（hot fields 在前，按缓存行 64-byte 对齐）
    2. RawJSON 用 sync.Pool 复用 []byte 缓冲（避免频繁分配）
    3. 大对象（RawJSON > 4KB）用 mmap-backed file（不进 heap）
    4. cache eviction 时显式 nil 化引用，帮助 GC
    
  效果：
    估算：GC 占 CPU 5%+ → 1.5%
    内存占用降低 20-30%
```

```
lock-free 读路径：
  问题：传统 cache 用 RWMutex，高并发读时锁竞争
  
  解法（hot cache 专用）：
    1. atomic.Pointer[Resource] 单条记录原子读
    2. sync.Map 整体（Go 标准库自带 lock-free 实现，但慢）
    3. immutable cache snapshot：每次写产生一个新快照（指针交换）
       - 读者持有当前快照指针，无锁
       - 写者构造新快照（包含修改），原子替换指针
       - 旧快照被 GC 回收（写少读多场景适用）
       
  适用场景：
    KubePivot reconcile 是"读多写少"
    （reconcile 频率 ~30s 一次，事件流速率取决于集群活跃度）
    
  性能预测：
    单条读：< 50ns（lock-free）
    vs RWMutex: ~200-500ns
    提升 5-10x
```

#### 2.3 与 v2.5 sharding 的集成

```
v2.5 现状：
  controller 多副本，每 pod 持有部分 namespace shard
  reconcile 只处理本 pod 的 shard
  
v2.7 informer 接入：
  每 shard 一个 informer 实例
  watch 时通过 fieldSelector 只 watch 本 shard 的 namespace
  减少 K8s API server 压力（vs 每个 pod watch 全集群）
  
代码组织：
  internal/eventstream/
    ├── informer.go             核心接口
    ├── informer_impl.go        默认实现
    ├── cache.go                分层 cache
    ├── cache_hot.go            hot cache (lock-free)
    ├── cache_warm.go           warm cache (RWMutex)
    ├── cache_cold.go           cold cache (etcd / disk)
    ├── skeleton.go             增量序列化的 skeleton
    ├── stats.go                监控指标
    └── *_test.go               单测
    
ShardSet 接口注入：
  Informer 启动时传入 v2.5 sharding.ShardSet
  watch 收到事件时调 ShardSet.Owns(ns) 过滤
  shard 变化时（ShardSet.Subscribe 通知）informer 重新订阅
```

#### 2.4 metrics-server / Prometheus 接入

```
v2.9 智能调度需要"实时资源使用率"数据：
  - Pod 当前 CPU / Memory usage
  - Node 当前 CPU / Memory allocatable / used
  - 历史趋势（5min / 1h / 24h）
  
v2.7 阶段引入 metrics 接入层：
  internal/eventstream/metrics.go
    
    // MetricsClient 抽象 metrics 来源
    type MetricsClient interface {
        // GetPodMetrics 拉取 Pod 当前 CPU/Memory usage
        GetPodMetrics(ctx context.Context, ns, name string) (PodMetrics, error)
        
        // GetNodeMetrics 拉取 Node 当前 CPU/Memory allocatable & used
        GetNodeMetrics(ctx context.Context, name string) (NodeMetrics, error)
        
        // GetHistorical 历史趋势（如有 Prometheus）
        GetHistorical(ctx context.Context, q HistoricalQuery) ([]TimePoint, error)
    }
    
  实现：
    KubectlMetricsClient   通过 kubectl top（兜底，最低要求）
    PrometheusClient       通过 Prometheus API（可选，更高质量数据）
    
  metrics 不进 cache（数据时间敏感，用完即丢）
  v2.9 调度算法直接调 MetricsClient.GetXxx
```

### 3. 任务清单

```
[ ] internal/eventstream/ 包基础（~600 行 + 测试 ~400 行）
    - informer.go             接口定义
    - resource.go             Resource / Event / 序列化
    - errors.go               错误类型

[ ] Cache 实现（~800 行 + 测试 ~500 行）
    - cache_hot.go            lock-free hot cache
    - cache_warm.go           RWMutex warm cache
    - cache_cold.go           etcd-backed cold cache
    - skeleton.go             增量序列化
    - eviction.go             分层间迁移逻辑

[ ] Informer 实现（~500 行 + 测试 ~300 行）
    - informer_impl.go        watch loop + reconnect
    - subscription.go         事件分发 + handler 调度
    - shard_filter.go         与 sharding.ShardSet 集成

[ ] Metrics 接入（~300 行 + 测试 ~200 行）
    - metrics.go              MetricsClient 接口
    - kubectl_metrics.go      kubectl top 实现
    - prometheus_metrics.go   Prometheus 实现（可选）

[ ] 与 v2.5 sharding 集成（~150 行）
    - 改造 internal/controller/global.go
    - reconcile 改用 informer 而不是直接 kubectl get
    
[ ] benchmark：自研 Informer vs client-go 对比
    - fork feature/client-go-comparison 分支
    - 跑同样的 P=10 / P=50 矩阵
    - 数据驱动评估（见风险章节）

[ ] 文档（~600 行）
    - docs/design/eventstream.md
    - 含架构图、cache 分层原理、benchmark 数据

合计：
  代码：~2350 行 + 测试 ~1400 行 = ~3750 行
  文档：~600 行
  benchmark + 验证：~1 周
  
工作量预估：3 周（含测试 + benchmark + 文档）
```

### 4. 风险与对策

```
风险 1：自研 Cache 性能反而不如 client-go
  概率：中等
  影响：v2.7 价值打折，但仍是"为 v2.9 服务"的基础设施
  
  对策：
  - benchmark 优先级最高，第 1 周就跑数据
  - 如果差距 < 20%：保持自研（cache 优化空间还大）
  - 如果差距 > 50%：诚实承认，但仍用自研封装一层
                   （为 v2.9 智能调度提供事件流抽象）
                   性能不如 client-go 但与 KubePivot 哲学一致
  - 数据公开记录到 docs/design/eventstream.md

风险 2：增量序列化引入 bug
  概率：中等
  影响：reconcile 漏掉变更 → 漂移检测失败
  
  对策：
  - skeleton 字段定义保守（多包含字段，宁滥勿缺）
  - 加 "fallback to full RawJSON compare" 选项
    （高敏感场景退回到全量比对）
  - 集成测试覆盖：手动改 K8s 资源 → 验证 informer 检测到

风险 3：Cache 内存占用爆炸
  概率：低（warm cache 不存反序列化对象）
  影响：controller pod OOM
  
  对策：
  - cold cache 阈值（warm cache 超过 100MB → 降级到 cold）
  - prometheus 监控：informer.cache_size_bytes
  - cache eviction 策略：LRU + size 双重限制

风险 4：v2.5 sharding 与 informer 的死锁
  概率：低（设计时已避免）
  影响：shard 变化时 informer 阻塞
  
  对策：
  - ShardSet.Owns() 必须 lock-free（O(1)）
  - shard 变化通知用 channel 异步
  - informer 重订阅在独立 goroutine
```

### 5. 验收

```
功能验收：
  ✓ informer 启动 / 停止 / 重连测试通过
  ✓ Cache 命中率 ≥ 95%（reconcile 直接读 cache 不调 K8s API）
  ✓ Pod 维度的 CPU / Memory metrics 能拉到
  ✓ 与 v2.5 sharding 联动正确

性能验收：
  ✓ vs client-go 对比 benchmark（数据公开）
  ✓ 单 Pod 内存占用比 v2.5 降低 ≥ 20%
  ✓ Cache 读延迟（hot path）< 50ns
  ✓ 全量重同步（30min 一次）耗时 < 5s

集成验收：
  ✓ v2.5 既有 reconcile 路径切换到 informer
  ✓ make dev 全绿
  ✓ orbstack 集群 P=50 跑稳态 5min，行为与 v2.5 一致
```

### 6. 工作量预估

```
3 周专注 = 21 天
分阶段：
  Week 1: Cache 实现 + benchmark vs client-go
  Week 2: Informer 实现 + sharding 集成
  Week 3: Metrics 接入 + 文档 + 验收

关键里程碑：
  Day 7:  benchmark 数据公开（决定 v2.7 路径）
  Day 14: informer + sharding 集成跑通
  Day 21: v2.7.0 release（如未阻塞）
```

---

