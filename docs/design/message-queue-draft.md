# KubePivot 消息队列设计

> 编写日期：2026-05-09
> 状态：📐 设计文档
> 关联：eventstream-draft.md / controller.md / heal.go

---

## 摘要

KubePivot 内部存在 **三种截然不同的消息队列** + **两条隐式缓冲路径**，五者分层解耦，服务于不同抽象层级。

核心原则：

> 上层不卡底层。洪峰缓冲、高吞吐池化、幂等阻塞——各自护城河，不混。

---

## 一、全景架构

```
K8s API Server
  │
  │  HTTP Watch (NDJSON 长连接)
  ▼
┌─────────────────────────────────────────────────────────┐
│  eventstream Informer                                   │
│                                                         │
│  runWatchLoop() [单 goroutine]                           │
│    │                                                    │
│    │  dispatchToSubscribers() — ShardSet 过滤            │
│    │  non-blocking send                                 │
│    ▼                                                    │
│  ┌─────────────────────────────────────────────────┐   │
│  │  Subscriber inflight chan [cap=1024]             │   │
│  │  × N 个订阅者，各独立 goroutine 消费              │   │
│  │  慢 → drop （EventsDropped 计数器）              │   │
│  └─────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
         │                          │
         │ PodCacheBridge           │ Scheduler (InformerAdapter)
         ▼                          ▼
┌────────────────────┐    ┌──────────────────────┐
│  PodCache (delta)  │    │  Scheduler.AssignPod │
│  delta > 200 flush │    │  cache 优先 / kubectl│
└────────────────────┘    └──────────────────────┘


┌─────────────────────────────────────────────────────────┐
│  Controller (Global Mode)                               │
│                                                         │
│  Watcher 层 (3 条入 task 路径)                           │
│    ├─ watchNamespaces()  → ns add/modify                │
│    ├─ watchConfigMaps()  → cm change (SHA256 去重)      │
│    └─ reconcileLoop()    → periodic tick                │
│         │                                               │
│         │  enqueueProjectResources() — 分片过滤          │
│         │  WorkerPool.Enqueue()   non-blocking send     │
│         ▼                                               │
│  ┌─────────────────────────────────────────────────┐   │
│  │  WorkerPool tasks chan [cap=20×4=80]             │   │
│  │  20 workers × for range，各独立 90s timeout      │   │
│  │  满 → drop + Warn 日志                           │   │
│  └─────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘


┌─────────────────────────────────────────────────────────┐
│  Controller (Standalone Mode — v2.x 遗留)               │
│                                                         │
│  etcd Watch                                             │
│    │                                                    │
│    │  q.Add("reconcile")                                │
│    ▼                                                    │
│  ┌─────────────────────────────────────────────────┐   │
│  │  ReconcileQueue (三集合 WorkQueue)                │   │
│  │  sync.Cond + []string FIFO                       │   │
│  │  dirty set → 去重，processing set → 不丢事件      │   │
│  │  阻塞 get，单 goroutine 消费                      │   │
│  └─────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘


┌─────────────────────────────────────────────────────────┐
│  隐式缓冲路径                                            │
│                                                         │
│  KVCache delta buffer  — 写累积到 delta map             │
│                         delta > 200 → 异步 flush 快照    │
│                                                         │
│  InformerAdapter 限流  — kubectl fallback 受 10s 保护   │
│                         超出 → ErrCacheNotReady          │
└─────────────────────────────────────────────────────────┘
```

---

## 二、三层队列逐一定义

### 2.1 Subscriber 事件队列 — 洪峰缓冲层

**位置**：`internal/eventstream/informer_impl.go`

```go
// subscriberImpl — 每个订阅者
type subscriberImpl struct {
    inflight chan Event   // make(chan Event, 1024)
    // ...
}
```

**数据流**：

```
informerImpl.runWatchLoop()                    [单 goroutine]
  │
  ├─ doInitialList()                           [分页 list, 500 条/页]
  ├─ doWatch()                                 [bufio.Scanner 逐行 NDJSON]
  │     └─ dispatchToSubscribers(ev)
  │           ├─ ShardSet.Owns(ns) 过滤
  │           └─ subscriberImpl.dispatch(ev)
  │                 select { case inflight <- ev: ... default: drop }
  │
  └─ resyncTicker                              [差异化周期: pods=10min, nodes=120min]
```

**dispatch 实现**（`informer_impl.go:371`）：

```go
func (s *subscriberImpl) dispatch(ev Event) {
    select {
    case s.inflight <- ev:
        // 入队成功
    case <-s.stopCh:
        // 已停止
    default:
        s.stats.EventsDropped.Add(1)  // 队列满，静默 drop
    }
}
```

**消费**：独立 goroutine `sub.run()`，`for { select { case ev := <-inflight: dispatchOne(ev) } }`，panic recover 包裹。

| 属性 | 值 |
|------|-----|
| 通道类型 | buffered chan `Event` |
| 容量 | 1024 (`subscriberQueueSize`) |
| 写策略 | non-blocking（select + default） |
| 慢消费者行为 | 静默 drop，计入 `EventsDropped` |
| 背压 | **无**（v3.4 计划 ack 机制） |
| 去重 | 无（ShardSet 仅过滤不属本 Pod 的事件） |
| 重同步兜底 | resync ticker 周期性全量 relist |
| 重连策略 | 指数退避 1s→30s + 20% 抖动 |

### 2.2 WorkerPool — 高吞吐任务池

**位置**：`internal/controller/worker_pool.go`

```go
type WorkerPool struct {
    size        int                // goroutine 数，默认 20
    tasks       chan ReconcileTask // make(chan, size*4)
    handler     TaskHandler
    taskTimeout time.Duration      // 90s
}
```

**数据流**：

```
Watcher 层 (3 条入 task 路径)
  │
  ├─ watchNamespaces()    → ns add/modify
  ├─ watchConfigMaps()    → cm change（SHA256 去重在 GlobalState 层）
  └─ reconcileLoop()      → periodic tick
        │
        │  enqueueProjectResources() — 分片过滤
        │  WorkerPool.Enqueue(task)  — non-blocking send
        ▼
  tasks chan ReconcileTask [cap=poolSize×4]
        │
        ▼
  size 个 worker goroutine (for task := range tasks)
        ├─ taskCtx, cancel := context.WithTimeout(90s)
        ├─ safeHandle → handler(taskCtx, task)
        └─ 单任务 panic 不影响其他 worker
```

**Enqueue 实现**（`worker_pool.go:109`）：

```go
func (p *WorkerPool) Enqueue(task ReconcileTask) bool {
    if IsProtectedNamespace(task.Namespace) {
        return false  // 护栏：Protected namespace 不入队
    }
    select {
    case p.tasks <- task:
        return true
    default:
        slog.Warn("Worker Pool channel 已满，丢弃任务", ...)
        return false
    }
}
```

**ReconcileTask 结构**：

```go
type ReconcileTask struct {
    Project    string    // 项目名（通常 = namespace）
    Namespace  string
    Kind       string    // Deployment / StatefulSet / ConfigMap / ...
    Name       string
    Reason     string    // "configmap-changed", "periodic-tick", "resource-missing"
    Key        string    // 可选唯一标识，用于去重
    EnqueuedAt time.Time // 入队时间，用于延迟观测
}
```

| 属性 | 值 |
|------|-----|
| 通道类型 | buffered chan `ReconcileTask` |
| 容量公式 | `poolSize × 4`（默认 80） |
| 写策略 | non-blocking（select + default） |
| 慢消费者行为 | drop + Warn 日志 |
| 背压 | **无**（v3.4 计划 token bucket） |
| 去重 | 无（幂等性由 handler 保证） |
| 优雅关闭 | ctx.Done() → close(tasks) → wg.Wait() |
| Task 隔离 | 独立 90s timeout context + panic recover |
| 护栏 | Protected namespace 在入队前拒绝 |

### 2.3 ReconcileQueue — 幂等阻塞队列

**位置**：`internal/controller/workqueue.go`

```go
type ReconcileQueue struct {
    mu         sync.Mutex
    queue      []string              // FIFO 待处理
    dirty      map[string]struct{}   // 待处理 / 处理中又来新事件
    processing map[string]struct{}   // 当前正在处理
    cond       *sync.Cond            // 阻塞等待
}
```

**三集合语义**：

```
Add(key):
  key 已在 dirty      → 去重，跳过
  key 正在 processing  → 只标记 dirty，Done 后自动 re-queue
  首次入队             → queue.append + cond.Signal

get(ctx):
  queue 空            → cond.Wait 阻塞（goroutine 监听 ctx.Done 做唤醒）
  queue 非空           → queue[0] 出队 → 移入 processing → 删 dirty → 返回

done(key):
  从 processing 移除
  dirty 中还有         → 重新 append 到 queue（处理期间有新事件，不丢）
```

**关键保证**：

> 同一 key 永远不并发处理。处理期间到达的新事件通过 dirty set 实现"记忆"——Done 后自动重新入队。

| 属性 | 值 |
|------|-----|
| 数据结构 | `sync.Mutex` + `sync.Cond` + 三集合（queue / dirty / processing） |
| 去重 | 强制（同一 key 不并发） |
| 事件不丢 | dirty set 保证"处理中事件"Done 后 re-queue |
| 容量 | 无界（slice append） |
| 背压 | 阻塞式：get() 队列空时 cond.Wait |
| 适用场景 | 旧 v2.x standalone 模式，每 reconciler 一个 queue |

---

## 三、隐式缓冲路径

### 3.1 KVCache delta buffer

**位置**：`internal/eventstream/kv_cache.go`

写入不是直接修改缓存快照，而是累积到 `delta map`：

```
Put(key, entry):
  delta[key] = entry              // O(1) write, ~120ns

ListAll():
  if len(delta) == 0:
    return prebuilt snapshot       // 零分配
  else:
    merge(snapshot, delta)         // merge-on-read

  if len(delta) > 200:
    async flush → 新快照           // CoW
```

这不是传统消息队列，但起到了**写缓冲**的队列作用——写路径 O(1)，读路径在 delta 溢出前零分配。

### 3.2 InformerAdapter 降级限流

**位置**：`internal/scheduler/informer_adapter.go`

```
Scheduler.ListAllPods():
  if PodCache.IsReady() && delta < 200:
    return cache.ListAll()           // 快路径
  else:
    kubectlAdapter()                 // 降级
    if 距上次降级 < 10s:
      return ErrCacheNotReady        // 限流保护
```

速率限制（`minFallbackGap = 10s`）作为隐式队列——控制降级调用频率，防止 kubectl 风暴。

---

## 四、rollback 自循环保护

**位置**：`internal/controller/heal.go`

**问题**：`handleCrashLoop` 检测到 CrashLoopBackOff（restarts >= 5）→ 自动 rollback。但如果 rollback 后资源再次不稳 → reconcile 再次检测 → 再次 rollback → 死循环。

**方案**：package-level `rollbackTracker`，按 namespace 追踪连续 rollback 次数。

```go
type rollbackTracker struct {
    mu      sync.RWMutex
    entries map[string]rollbackEntry
}

type rollbackEntry struct {
    cnt    int
    lastAt time.Time
}
```

**退避策略**：

| count | backoff | 语义 |
|-------|---------|------|
| 0-2 | 0 | 正常自愈，不拦截 |
| 3 | 1 min | 连续 3 次 rollback 后进入冷却 |
| 4 | 2 min | |
| 5 | 4 min | |
| 6 | 8 min | |
| n | 2^(n-3) min | 指数发散 |

**并发模型**：

- `shouldBlock` → `RLock`（20 worker 并发读，零争用）
- `record` → `Lock`（写路径低频）
- GC 协程 → `Lock`（每小时扫一次，删 2h 无活动 entry）

---

## 五、三层队列对比

```
层级          队列类型          通道/结构         容量     去重   背压      用途
─────────────────────────────────────────────────────────────────────────────
Subscriber    buffered chan     chan Event        1024     ✗     drop     K8s 事件 → 订阅者
WorkerPool    buffered chan     chan ReconcileTask  80     ✗     drop     全局 reconcile 任务
ReconcileQueue sync.Cond+slice  []string           无界    ✓     阻塞      standalone reconcile
delta buffer  map               delta map          200*    ✗     异步flush KVCache 写缓冲
rate limiter  time.Duration     10s gap            -       ✗     拒绝     降级 kubectl 保护
```

\* delta > 200 触发异步 flush，非硬上限

---

## 六、设计决策记录

### Q1: 为什么三层并存，不统一？

三层队列语义互斥：

- **Subscriber**：事件洪峰（K8s watch NDJSON 每秒可达数百 event），关键是不阻塞 watch loop。用 buffered chan + drop 是正确的——上游不能卡。
- **WorkerPool**：业务任务（reconcile），吞吐优先。同一 ns 同一资源可能被多个 task 同时处理，但 handler 是幂等的，无冲突。
- **ReconcileQueue**：严格幂等（同一 key 不并发），适合 etcd watch 这种"一个 key 多次变更只处理一次"的场景。

统一只会牺牲性能或正确性。

### Q2: 为什么 WorkerPool 不去重？

去重需要全局状态（dirty set），20 worker 并发查/改 dirty set 的锁开销 > 幂等执行的开销。ReconcileTask 的 handler（`healRecreate` / `healRollback`）本身就是幂等的——重复 rollback 到同一 revision 是 no-op。

### Q3: 为什么 subscriber dispatch 用 non-blocking 而不是 block？

watch loop 是单 goroutine。如果 dispatch 阻塞，整个 watch 暂停——丢失的不是一个 event 而是从阻塞到恢复期间的所有 event。drop 一个 event > 丢失一个 event 窗口。

### Q4: 为什么 rollbackTracker 用 namespace 粒度而不是 resource 粒度？

按 ns 聚合的退避更保守也更简单。同一 ns 多个 resource 同时 unstable 通常是同因（配置错误、依赖故障），按 resource 粒度会低估风险。

---

## 七、已知待办与路线

| # | 待办 | 优先级 | 版本 | 说明 |
|---|------|--------|------|------|
| 1 | ShardSet 回调 | ✅ 高 | v3.3 | Informer.ForceResync() + InformerPool.ForceResyncAll() + global.go 接线 |
| 2 | WorkerPool 背压 | 中 | v3.4 | token bucket（10/s），满时上游暂停入队 |
| 3 | Subscriber ack | 中 | v3.4 | inflight pop 后 upstream 确认，替换 drop |
| 4 | 混合队列 | 低 | v3.4+ | VIP ns 走 ReconcileQueue，散兵走 WorkerPool |
| 5 | prom metrics | 中 | - | queue_depth / dropped_total / dirty_size → Grafana |

---

## 八、测试策略

| 层 | 已有测试 | 待补 |
|----|---------|------|
| Subscriber dispatch | `TestInformer_Dispatch*` (informer_test.go) | 洪峰 drop 率 e2e |
| WorkerPool | `TestWorkerPool_BasicDispatch`, `TestWorkerPool_PanicRecovery` | channel 饱和 drop 率 |
| ReconcileQueue | `TestWorkQueue_Dedup`, `TestWorkQueue_ProcessingDedup`, `TestWorkQueue_DoneRequeue` | - |
| rollbackTracker | 间接：controller 测试全绿 | `TestRollbackTracker_Cooldown`, `TestRollbackTracker_Cleanup` |
| delta buffer | `TestPodCache_MergeOnRead`, `TestPodCache_DeltaFlush` | - |
| shard listener | controller + eventstream 测试全绿 (2026-05-09) | shard flip e2e |

---

## 九、编辑记录

```
2026-05-09  创建。覆盖三层队列 + 两条隐式路径 + rollbackTracker。
2026-05-09  shard listener 落地。Informer.ForceResync() + ForceResyncAll() +
            OnShardChanged 接线 + force_resync_total 指标 + 防御性 cancel。
2026-05-09  v3.3 P0 扫荡 (8/8):
            - #5 rollbackTracker 指数退避 ✅
            - #7 etcd config compact/defrag 接线 ✅
            - #19 jump consistent hash 替换 FNV ✅
            - #4 token bucket 入队限流 ✅
            - #3 Webhook TLS 自签证书 + secret volume mount ✅
            - #6 Prometheus /metrics HTTP server + 16 指标注册 ✅
            - #1 shard gap 事件盲区归零 (ForceResync), reconcile gap 待 lease handoff 🟡
            - #2 Quota hash 均匀化, lease rebalance 待 🟡
            P1 #13 label migration hint name+label 双路查找 ✅
            共同作者: qc + DeepSeek
```
