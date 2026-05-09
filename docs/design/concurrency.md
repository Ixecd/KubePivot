# KubePivot 并发设计

> 编写日期：2026-05-09
> 状态：📐 设计文档
> 关联：message-queue-draft.md / sharding.md / controller.md

---

## 摘要

KubePivot 内部 11 种并发构造，覆盖锁、channel、条件变量、信号量、原子操作五类。本文档记录每种构造的设计决策、数据流、已知漏洞。

核心原则：

> 锁粒度最小化。读路径不阻塞写路径。生产者不卡消费者。drop 优于阻塞。

---

## 一、并发构造全景

```
┌─────────────────────────────────────────────────────────────────┐
│                        Concurrency Map                          │
│                                                                 │
│  Lock-based         Channel-based       Atomic / CAS            │
│  ─────────         ─────────────       ────────────             │
│  RWMutex × 4       buffered chan × 3   sync.Map × 1            │
│  Mutex  × 3        chan(1) signal × 1  atomic.Value × 1        │
│  sync.Cond × 1                        atomic.Uint64/Int64 × N  │
│                                                                 │
│  Semaphore (token bucket)              sync.Once × 2            │
└─────────────────────────────────────────────────────────────────┘
```

| # | 构造 | 类型 | 位置 | 并发度 |
|---|------|------|------|--------|
| 1 | `migrationTargetHints` | `sync.Map` | webhook.go | lock-free, 双 goroutine |
| 2 | `rollbackTracker` | `RWMutex` + struct | heal.go | 20 reader / 1 writer |
| 3 | `tokenBucket` | `Mutex` + float | token_bucket.go | 信号量, 20 并发 |
| 4 | `WorkerPool.tasks` | buffered chan(80) | worker_pool.go | 1-N producer / N consumer |
| 5 | `ReconcileQueue` | `sync.Cond` + 3-set | workqueue.go | 1 producer / 1 consumer |
| 6 | `subscriber.inflight` | buffered chan(1024) | informer_impl.go | 1 producer / N consumer |
| 7 | `forceResync` | buffered chan(1) + cancel | informer_impl.go | 信号合并 + 中断 |
| 8 | `PodCache.delta` | `RWMutex` + `atomic.Value` | kv_cache.go | CoW + delta buffer |
| 9 | `GlobalState` | `RWMutex` + `Mutex` | global_state.go | 读多写少, 双锁粒度 |
| 10 | `ShardSet` | `RWMutex` + map | shard.go | 高频读 / 低频写 |
| 11 | `MultiLeaseManager` | `Mutex` + callback | multi_lease.go | lease 循环 + 异步通知 |

---

## 二、逐构造分析

### 2.1 tokenBucket — 计数信号量

**位置**：`internal/controller/token_bucket.go`

```go
type tokenBucket struct {
    rate     float64       // tokens/sec
    burst    float64       // max tokens (= rate)
    tokens   float64       // current count
    lastTime time.Time     // last replenish timestamp
    mu       sync.Mutex
}
```

**语义**：

```
allow():
  rate <= 0 → 永远 true（无限流）
  lock → tokens += elapsed * rate → clamp(burst) → tokens-- → unlock
```

**设计决策**：

| Q | 选 | 理由 |
|---|-----|------|
| 为什么 time.Now() 在锁内？ | 锁内 | elapsed 计入锁等待时间，反映真实间隔 |
| 为什么 burst = rate？ | 1s 突发 | 启动时满桶，允许初始 burst，之后稳定在 rate |
| 为什么不用 `rate.Limiter`？ | 0 外部依赖 | `golang.org/x/time/rate` 不在 go.mod |

**已知漏洞**：

- ~~**令牌浪费**~~ ✅ v3.4 — 后置代币（channel 先行，成功再耗 token），令牌不白花
- **burst 边界**：`rate=0` 时 `burst=1` 但 path 走 `rate<=0` 分支永不读取 burst。代码味非 bug

### 2.2 WorkerPool + tokenBucket — 两阶段门控 (v3.4: 后置代币)

**位置**：`internal/controller/worker_pool.go`

**v3.4 数据流**：

```
Enqueue(task):
  1. select { case tasks <- task }  ← channel 先行
     ↓ 满 → channelDropped++, return false
  2. tokenBucket.allow()            ← 后置代币（成功再耗）
  3. enqueued++, return true
```

**v3.3 数据流（旧）**：

```
Enqueue(task):
  1. tokenBucket.allow()            ← 前置限流
     ↓ 拒绝 → rateLimited++, return false
  2. select { case tasks <- task }  ← channel
     ↓ 满 → drop (令牌已浪费)
```

**v3.4 改进**：channel 满时不浪费令牌。token 统计 = 实际入队速率，非尝试速率。

**生产者-消费者模型**：

```
Watchers (3 goroutines)           Workers (20 goroutines)
  │                                  │
  ├─ watchNamespaces                 ├─ for task := range tasks
  ├─ watchConfigMaps                 │    taskCtx, cancel := WithTimeout(90s)
  └─ reconcileLoop                   │    safeHandle → handler(task)
       │                             │    cancel()
       │  Enqueue()                  │
       ▼                             ▼
  ┌─────────────────────────────────────────┐
  │  tokenBucket  →  chan(80)  →  handler   │
  └─────────────────────────────────────────┘
```

**设计决策**：

| Q | 选 | 理由 |
|---|-----|------|
| v3.3 为什么先 token 后 channel？ | 前置限流 | 令牌稀缺，先过稀缺资源 |
| v3.4 为什么改为先 channel 后 token？ | 后置代币 | channel 满不浪费令牌，统计准 |
| 为什么 channel 不用阻塞发送？ | non-blocking | 生产者是 watch loop，不能卡 |
| 为什么不去重？ | 幂等 handler | healRollback 到同 revision 是 no-op |

**v3.4 改进**：
- `channelDropped` 独立计数器：channel 满 drop vs 令牌拒绝，不再混淆
- 令牌统计 = 实际入队速率（非尝试速率）
- 自然背压：channel 满 = worker 消费不过来，令牌跟踪但不阻止

### 2.3 ReconcileQueue — 条件变量阻塞队列

**位置**：`internal/controller/workqueue.go`

**结构**：

```go
type ReconcileQueue struct {
    mu         sync.Mutex
    queue      []string              // FIFO
    dirty      map[string]struct{}   // 待处理 / 处理中又来新事件
    processing map[string]struct{}   // 正在处理
    cond       *sync.Cond
}
```

**三集合语义**：

```
Add(key):
  key ∈ dirty      → 去重, return
  key ∈ processing → dirty.upsert(key), return   // "记忆"
  首次             → queue.append(key), cond.Signal

get(ctx):
  queue 空 → spawn goroutine 监听 ctx.Done → cond.Wait
             Wait 返回 → close(done) → check ctx.Err()
  queue 非空 → queue[0] 出队 → processing.insert → dirty.delete

done(key):
  processing.delete(key)
  key ∈ dirty → queue.append(key), cond.Signal  // 处理期间有新事件, re-queue
```

**ctx 取消机制**：

```go
// 每个 Wait 周期 spawn 一个 goroutine
done := make(chan struct{})
go func() {
    select {
    case <-ctx.Done(): q.cond.Signal()  // 唤醒 Wait
    case <-done:                        // 正常返回
    }
}()
q.cond.Wait()
close(done)
```

**设计决策**：

| Q | 选 | 理由 |
|---|-----|------|
| 为什么不用 channel？ | 需要去重语义 | channel 不能查"key 是否已在队列中" |
| 为什么 spawn goroutine 监听 ctx？ | cond.Wait 不响应 ctx | Go 标准库 `sync.Cond` 无 `WaitContext` |
| 为什么 Signal 不是 Broadcast？ | 单消费者 | 只有一个 goroutine 调 `get()` |

**已知漏洞**：

- **`close(done)` 与 `<-ctx.Done()` 竞态**：`close(done)` 后 goroutine 可能先触发 `<-ctx.Done()` → `q.cond.Signal()`。Signal 对空等待者无害，但行为不确定
- **goroutine churn**：每个 Wait 周期 spawn 一个。standalone 模式下低频，非瓶颈

### 2.4 subscriber.inflight — 单生产者多消费者 Channel

**位置**：`internal/eventstream/informer_impl.go`

**结构**：

```go
type subscriberImpl struct {
    inflight chan Event  // make(chan Event, 1024)
    // ...
}
```

**数据流**：

```
informerImpl.runWatchLoop()           [单 goroutine — 生产者]
  │
  │  dispatchToSubscribers(ev)
  │    ShardSet.Owns(ns) 过滤
  │    for each subscriber:
  │      subscriber.dispatch(ev)      [non-blocking]
  │
  ▼
subscriber.inflight chan Event        [cap=1024]
  │
  ▼
subscriber.run() goroutines           [N 个消费者，各独立]
  │  for { select { case ev := <-inflight: dispatchOne(ev) } }
  │  panic recover 包裹
```

**dispatch 实现**：

```go
func (s *subscriberImpl) dispatch(ev Event) {
    select {
    case s.inflight <- ev:     // 入队成功
    case <-s.stopCh:           // 已停止
    default:                   // 队列满, drop
        s.stats.EventsDropped.Add(1)
    }
}
```

**设计决策**：

| Q | 选 | 理由 |
|---|-----|------|
| 为什么 non-blocking？ | 不卡 watch loop | watch loop 是单 goroutine, block = 丢失事件窗口 |
| 为什么 cap=1024？ | 经验值 | 足够吸收 K8s event burst（~100/s），超过即 drop |
| 为什么 drop 优于 block？ | AP 语义 | 丢一个 event < 阻塞丢失一个窗口的 events |

**已知漏洞**：

- **无背压**：慢消费者不知道 drop 发生，生产者不知道丢了什么。v3.4 subscriber ack 可解决
- **无优先级**：所有订阅者平等。PodCacheBridge（高频）和慢 handler 抢同一 inflight

### 2.5 forceResync — 信号合并 + 上下文取消

**位置**：`internal/eventstream/informer_impl.go`

**结构**：

```go
type informerImpl struct {
    forceResync chan struct{}         // cap=1, 合并重复触发
    forceResyncTotal atomic.Uint64    // 计数器
    watchCancel   context.CancelFunc  // 当前 watch 的取消函数
    watchCancelMu sync.Mutex
}
```

**ForceResync() 实现**：

```go
func (im *informerImpl) ForceResync() {
    im.forceResyncTotal.Add(1)
    select {
    case im.forceResync <- struct{}{}:  // 发送信号
    default:                             // 合并重复
    }
    im.watchCancelMu.Lock()
    if im.watchCancel != nil {
        im.watchCancel()                // 中断当前 watch
    }
    im.watchCancelMu.Unlock()
}
```

**watch loop 三处处理**：

```
1. top-of-loop:  case <-forceResync → 防御性 cancel → relist → continue
2. doWatch 后:   errors.Is(err, context.Canceled) → continue（跳过退避）
3. backoff 中:   case <-forceResync → relist → attempt=0
```

**竞态分析**：

```
T1: ForceResync → channel send ✓ → watchCancel()
T2: watch loop  ← context.Canceled ← continue
T3: watch loop  ← case <-forceResync ← relist

窄窗:
  T3a: watch loop 设置新的 watchCancel
  T3b: ForceResync_B → watchCancel(nil)  ← no-op（旧 cancel 已清）
  T3c: watch loop ← case <-forceResync ← 防御性 cancel（取消刚建立的 watch） ← 关键！
```

**设计决策**：

| Q | 选 | 理由 |
|---|-----|------|
| 为什么 cap=1？ | 合并重复触发 | 一次 relist 即可覆盖所有 missed events |
| 为什么先 signal 后 cancel？ | 保证信号先于中断 | 反序会导致 cancel 后 signal 丢失在 channel 满时 |
| 为什么 top-of-loop 防御性 cancel？ | 窄窗保护 | forceResync 触发到 relist 之间 watch 可能重建 |

**已知漏洞**：

- **cancel 后 channel send 失败**：ForceResync_B 在 ForceResync_A 之后触发，channel 满 → no-op。但 watchCancel 仍有值（新 watch），cancel 触发第二次中断。第一次 relist 已经完成，第二次中断浪费但无害
- **计数器 overcount**：`forceResyncTotal` 在入口处增加，即使 channel send 是 no-op。语义上是"请求次数"而非"实际 relist 次数"

### 2.6 PodCache — RWMutex + atomic.Value CoW

**位置**：`internal/eventstream/kv_cache.go`

**结构**：

```go
type PodCache struct {
    snapshot  atomic.Value         // *podSnapshot (immutable)
    writeMu   sync.Mutex           // guards snapshot swap (CoW)
    delta     map[string]*PodEntry // pending changes
    deltaMu   sync.RWMutex         // WLock for Put, RLock for Get/ListAll
}
```

**操作语义**：

```
Put(key, entry):                     Get(ns, name):
  deltaMu.Lock()                       deltaMu.RLock()
  delta[key] = entry                   if delta[key] exists → return delta val
  deltaMu.Unlock()                     deltaMu.RUnlock()
  generation++                         snapshot.Load() → lookup → return

ListAll():                           FlushDelta():
  deltaMu.RLock()                      writeMu.Lock()
  nd = len(delta)                      snapshot.Load() + delta → CoW → newSnapshot
  if nd == 0: return snapshot.list     deltaMu.Lock(); delta = new(map); deltaMu.Unlock()
  merge(snapshot, delta)               snapshot.Store(newSnapshot)
  deltaMu.RUnlock()                    writeMu.Unlock()
  if nd > 200: go FlushDelta()
```

**设计决策**：

| Q | 选 | 理由 |
|---|-----|------|
| 为什么 delta 用 RWMutex 而不是 Mutex？ | 读多写少 | Get RLock 零争用，Put WLock 独占但 O(1) |
| 为什么 writeMu 单独存在？ | CoW 互斥 | 防止并发 FlushDelta 同时 swap snapshot |
| 为什么 async flush？ | 非阻塞写路径 | Put 只写 delta（O(1) ~120ns），flush 推迟到后台 |

**已知漏洞**：

- **FlushDelta 无去重**：`go c.FlushDelta()` 可能被多次触发。writeMu 防止并发 swap，但第二次 flush 在空 delta 上做 CoW——浪费但不丢数据
- **merge-on-read 分配**：ListAll 在 delta 非空时做 merge，有分配。seenPool 缓解但未根治
- **delta Map 无限增长**：如果 FlushDelta 持续失败或延迟，delta 无限增长直到 OOM。无上限保护

### 2.7 GlobalState — 双锁粒度

**位置**：`internal/controller/global_state.go`

**结构**：

```go
type GlobalState struct {
    mu         sync.RWMutex                    // projects map
    projects   map[string]*projectState
    machinesMu sync.Mutex                      // machines map (独立锁)
    machines   map[string]*machineEntry
}
```

**设计决策**：

| Q | 选 | 理由 |
|---|-----|------|
| 为什么两把锁？ | 减小锁粒度 | projects（RW, 高频读）和 machines（Mutex, 低频写）不互斥 |
| 为什么 machines 用 Mutex 而非 RWMutex？ | 写主导 | 创建/销毁 machine 是低频操作，RWMutex 开销更大 |

**已知漏洞**：

- **无**。双锁粒度设计合理，machine 创建在锁外做 I/O（etcd 连接），不阻塞 projects 读

### 2.8 rollbackTracker — 高并发读保护

**位置**：`internal/controller/heal.go`

**结构**：

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

**并发模型**：

```
shouldBlock(ns):                    record(ns):
  mu.RLock()                          mu.Lock()
  e := entries[ns]                    e := entries[ns]
  mu.RUnlock()                        e.cnt++; e.lastAt = now
  if e.cnt < 3 → false               entries[ns] = e
  backoff := 2^(cnt-3) min           mu.Unlock()
  return since < backoff

cleanup (hourly):
  mu.Lock()
  delete entries with lastAt > 2h ago
  mu.Unlock()
```

**设计决策**：

| Q | 选 | 理由 |
|---|-----|------|
| 为什么 RWMutex？ | 20 worker 并发读 | shouldBlock 在每次 reconcile 调用，Rlock 零争用 |
| 为什么 value 语义？ | 避免指针共享 | `e := entries[ns]` 拷贝，修改后 `entries[ns] = e` 写回 |

**已知漏洞**：

- **cleanup vs record 竞态**：shouldBlock RUnlock → cleanup Lock(delete) → record Lock(create new)。退避计数归零。需 entry 在 2h 无活动且恰好此时触发 rollback——极边缘
- **entry 被 cleanup 删除期间，backoff 窗口遗漏**：同 ns 第 4 次 rollback 在 2h 后 → 退避重置为 1min 而非 2min。生产意义微

### 2.9 migrationTargetHints — sync.Map

**位置**：`internal/scheduler/webhook.go`

**结构**：

```go
var migrationTargetHints sync.Map

// v3.3: 双 key 存储
SetMigrationTargetHintWithLabel(ns, name, appLabel, node):
  Store("ns/name", node)            // name-based (StatefulSet)
  if appLabel != "":
    Store("ns/label:appLabel", node) // label-based (Deployment)

PopMigrationTargetHint(ns, name):
  LoadAndDelete("ns/name")          // primary

PopMigrationTargetHintByLabel(ns, appLabel):
  LoadAndDelete("ns/label:appLabel") // fallback
```

**设计决策**：

| Q | 选 | 理由 |
|---|-----|------|
| 为什么 sync.Map 不是 Mutex+map？ | lock-free | rescheduler 写 + webhook 读，无竞争热点 |
| 为什么双 key？ | Deployment 改名 | name-based 匹配 StatefulSet, label-based 回退 Deployment |

**已知漏洞**：

- **脏 hint**：驱逐失败时只 Pop name key，label key 可能残留。webhook 找到过期 hint → Pod 路由到错误节点。应加 hint TTL 或驱逐失败时 Pop 双 key
- **无 TTL**：sync.Map 无自动过期。hint 泄漏不会导致 OOM（驱逐成功会 Pop），但脏数据存留风险存在

### 2.10 ShardSet — 高频读优化

**位置**：`internal/sharding/shard.go`

```go
type ShardSet struct {
    mu     sync.RWMutex
    shards map[int]struct{}
}
```

**并发模型**：

```
OwnsNamespace(ns, N):             Add/Remove:
  RLock                             Lock
  return Contains(ShardOf(ns,N))    shards[idx] = {} / delete
  RUnlock                           Unlock
```

**设计决策**：

| Q | 选 | 理由 |
|---|-----|------|
| 为什么 RWMutex？ | 高频 RLock | 每个 event 调一次 OwnsNamespace |
| 为什么读锁粒度是整个 Owns？ | 简单 | ShardOf 是纯函数无锁，Owns 只 RLock ShardSet |

### 2.11 MultiLeaseManager — 异步通知循环

**位置**：`internal/sharding/multi_lease.go`

**并发模型**：

```
Run(ctx):
  for { select { case <-ticker: reconcileShards(ctx) } }

reconcileShards:
  1. 续约已持有 shard (可能失去)
  2. 抢占新 shard (quota 允许)
  3. diff: added / removed
  4. if diff: go callback(added, removed)  // 异步通知
```

**设计决策**：

| Q | 选 | 理由 |
|---|-----|------|
| 为什么 callback 异步？ | 不阻塞 lease 循环 | callback 可能做 I/O(ForceResyncAll) |
| 为什么 shards RWMutex？ | 高频读 OwnsNamespace | reconcile 路径每次事件查一次 |

---

## 三、生产者-消费者关系图

```
┌──────────────────────────────────────────────────────────────────┐
│                                                                  │
│  K8s API (NDJSON)                                                │
│    │                                                             │
│    │ [单生产者]                                                    │
│    ▼                                                             │
│  runWatchLoop ──dispatchToSubscribers──► subscriber.inflight     │
│                                            │                     │
│                                            │ [1:N]                │
│                                            ▼                     │
│                                     PodCacheBridge               │
│                                     NodeCacheBridge              │
│                                                                  │
│  ─────────────────────────────────────────────────────────────  │
│                                                                  │
│  Watchers (3 goroutines)                                         │
│    │                                                             │
│    │ [3:1]                                                       │
│    ▼                                                             │
│  tokenBucket ──► WorkerPool.tasks ──► Workers (20 goroutines)    │
│    (信号量)        (chan 80)            (for range)              │
│                                                                  │
│  ─────────────────────────────────────────────────────────────  │
│                                                                  │
│  etcd Watch                                                      │
│    │                                                             │
│    │ [1:1]                                                       │
│    ▼                                                             │
│  ReconcileQueue (sync.Cond FIFO) ──► single goroutine            │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

---

## 四、并发反模式（已避免）

| 反模式 | 替代 | 位置 |
|--------|------|------|
| 全局 Mutex 锁一切 | 双锁粒度（projects/machines 分开） | GlobalState |
| channel send 阻塞生产者 | non-blocking select+default | subscriber dispatch |
| 读路径持写锁 | RWMutex.RLock | ShardSet, rollbackTracker, PodCache |
| 回调内做重 I/O | go callback() 异步 | MultiLeaseManager.OnShardChanged |
| 每次 flush 分配新 map | 复用 seenPool (sync.Pool) | PodCache.ListAll merge |
| time.Now() 在锁外 → 过期 | time.Now() 在锁内 | tokenBucket.allow() |

---

## 五、已知并发待办

| # | 漏洞 | 严重度 | 排期 | 修复方向 |
|---|------|--------|------|---------|
| 1 | subscriber inflight 无背压 | 中 | v3.4 | upstream ack channel |
| 2 | WorkerPool 无优先级 | 低 | v3.4+ | Hybrid queue (ns-hash VIP) |
| 3 | migration hint 脏数据 | 中 | v3.4 | hint TTL + 双 key 同步 Pop |
| 4 | FlushDelta 无并发去重 | 低 | v3.4 | atomic flag 或 channel gate |
| 5 | ReconcileQueue goroutine churn | 低 | - | Go 1.23+ `sync.Cond.WaitContext` 后迁移 |
| 6 | rollbackTracker cleanup 竞态 | 极低 | - | 生产几乎不触发 |
| 7 | ~~token bucket 令牌白花~~ ✅ v3.4 | - | 后置代币，channel 满不浪费令牌 |

---

## 六、设计 Q

```
Q1: 为什么 WorkerPool 用 channel + token bucket 两层，而不是一层？
    A. channel-only → 无背压
    B. token-only  → 无缓冲弹性
    C. token → channel ✅
    选 C — 信号量限流 + channel 缓冲突发，两层互补

Q2: 为什么 ReconcileQueue 不用 channel？
    A. channel 不能 O(1) 查"key 是否在队列中"
    B. channel 不能实现 dirty/processing 三集合语义
    C. sync.Cond 是标准答案 ✅

Q3: 为什么 subscriber dispatch 不阻塞？
    A. watch loop 是单 goroutine，阻塞 = 丢失事件窗口
    B. drop 优于 block（AP 语义） ✅

Q4: 为什么 token bucket 不用 rate.Limiter？
    A. 0 外部依赖 ✅
    B. 手写 40 行，足够正确
```

---

## 七、编辑记录

```
2026-05-09  创建。覆盖 11 种并发构造 + 生产者-消费者关系图 +
            已知漏洞 + 设计 Q。
2026-05-09  #1 WorkerPool 后置代币 (channel-first, token-after)。
            channelDropped 独立计数器, 令牌不白花。
            共同作者: qc + DeepSeek
```
