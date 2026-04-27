# KubePivot v2.7 Event Stream 实施日志

> 编写日期：2026-04-27（实时）
> 状态：✅ Step 1 完成（Day 1-5 全部完成，Bench 3 watch 吞吐留 TBD）
> 关联文档：[eventstream-draft.md](eventstream-draft.md)（设计草案）/ [eventstream-perf.md](eventstream-perf.md)（性能决策）
> 作用：记录设计 → 实施过程中的真实数据、决策调整、bug 修复

---

## 摘要

本文档是 v2.7 Event Stream 实施过程的**工程日志**。
与 draft.md（设计意图）互补：

- `eventstream-draft.md`：设计文档，描述"为什么这样设计"
- `eventstream-impl-notes.md`：实施日志，描述"实际做的时候发生了什么"

记录原则：
- 数据为主，叙事为辅
- 失败迭代写出来（不藏数据）
- 设计调整 / bug 修复诚实记录
- 工程伦理 > 文档美观

---

## 时间线总览

| Day | 日期 | 内容 | commit | 关键产出 |
|---|---|---|---|---|
| Day 1 | 2026-04-27 上午 | Benchmark vs client-go + perf 决策 | 414 | 走自研路径敲定 |
| Day 2 | 2026-04-27 上午 | 包基础 + Cache + cache_policy | 415-416 | ~1100 行 + 80 cases |
| Day 3 | 2026-04-27 上午 | Informer + watch loop + 测试 | 417-418 | ~2600 行 + 27 cases (Day 3.1) + 14 cases (Day 3.2) |
| Day 4 | 2026-04-27 下午 | v2.5 sharding adapter | 419 | 322 行 + 12 cases |
| Day 5 | 2026-04-27 下午 | Metrics Prometheus collector | 422 | 740 行 + 16 cases |

累计：commit 414 → 422，共 9 个 commit（含 docs/FUTURE）/ ~3700 行代码 / ~2200 行测试 / 88.8% 覆盖率

---

## Day 1: Benchmark + Perf 决策（commit 414）

### 计划

draft.md §11 规划：feature 分支跑 benchmark vs client-go，数据驱动决策。

### 实际

新建 feature/client-go-comparison 分支，写 5 个 benchmark：

1. Bench 1: Cache Get
2. Bench 2: Cache List (按 namespace)
3. Bench 3: Watch 吞吐 ⏳（envtest 配置，留 Day 5）
4. Bench 4: 序列化开销
5. Bench 5: ColdStart（list + 灌入 cache）

跑分环境：Apple M4 / Go 1.25 / GOGC=200 / 4GiB / 4P。

### 数据（修复后稳定值）

| Bench | 自研 | client-go | 时间倍数 | 内存倍数 | 备注 |
|---|---|---|---|---|---|
| Cache Get | 14.5ns | 45ns | **3.1x** | **6x** | 单点查询 |
| Cache List(ns) 1000 | 147ns | 6800ns | **46x** ⭐ | **100x** ⭐ | namespace 过滤 |
| Cache ListAll | 7500ns | 7300ns | 1.0x | 2x | 全量扫描持平 |
| ColdStart 1000 | 35ms | 61ms | 1.8x | 8.5x | 1k 对象灌入 |
| ColdStart 10000 | 341ms | 645ms | 1.9x | 8.6x | 10k 对象灌入 |
| ParseOnly | 35μs | 60μs | 1.7x | 8.7x | JSON 反序列化 |
| 内存放大率 (1w obj) | 1.65x | 4.69x | - | **2.84x ⭐** | 整体内存效率 |

### 实施迭代（v1 → v2）

**v1 失败**：
- SkeletonCache 用单层 map 存所有对象 → List 全表扫描
- ColdStart 10000 跑出 1580ms（O(N²) 灾难）
- 问题：每次 Put 都触发对全部对象的检查

**v2 修复**：
- 增加 namespace 二级索引（map[string]map[string]*Resource）
- 增加 PutBulk 一次性写入，避免 N 次 Put
- ColdStart 10000 从 1580ms → 341ms（4.6x 提升）

### 决策

走自研路径，理由：
- 主路径（Get / List / ColdStart）数据明显优于 client-go
- 内存效率显著（2.84x）符合 KubePivot "内存敏感"场景
- ListAll 持平不影响（KubePivot 几乎不全表扫描）

### 工程伦理标注（写入 eventstream-perf.md）

- macOS heap 数据用作 RSS 替代（实际 RSS 受 fragmentation 影响，需 Linux 重测）
- benchmark 数据基于 fake 对象（生产 Deployment / Service 体积更大）
- Bench 3 (Watch 吞吐) 当前未跑，留 Day 5 envtest
- ListAll 主路径持平诚实标注（不夸大胜利）

### feature 分支处理

`feature/client-go-comparison` 分支保留 benchmark 套件。**不合并回 Master**（KubePivot 哲学：永不依赖 client-go）。

raw 数据归档：`docs/design/eventstream-perf/20260427_092351/`

> 仪式感：commit 数字落在 414，"client-go 时代过去"

---

## Day 2: 包基础 + Cache + cache_policy（commit 415-416）

### 计划

draft.md §3 + §4：三层 Cache（Hot/Warm/Cold） + 双驱动并集分层判定。

### 实际产出

```
internal/eventstream/
  doc.go              53 行   包文档
  resource.go        208 行   Resource 类型 + Skeleton
  cache.go           370 行   SkeletonCache 实现
  serializer.go      113 行   ParseSkeleton + SkeletonChanged
  cache_policy.go    222 行   ProjectState enum + CachePolicy + Decide()
  + 单测 ~1100 行 / 59 cases / 覆盖率 94.3%
```

### 关键设计实施

**Cache 实现（v2.7.0 仅 Hot 层）**：
- 双层 map：`map[ns]map[name]*Resource`
- 加 namespace 二级索引避免 List 全表扫描
- PutBulk 接口（list 时一次性写入）
- 并发安全：sync.Mutex（读写都加锁，简单优先）

draft.md 提到 atomic.Value 的 lock-free 路径 —— **v2.7.0 暂未实现**：
- 当前数据已经够用（Cache Get 14.5ns）
- atomic.Value snapshot 实施复杂度高
- 留 v2.7.1+ 优化（如果监控显示锁竞争）

### Bug 修复链（3 个本地修复，无 push 中间状态）

**Bug 2.1**：cold_start_test 的 client-go cache 构造错误
- 根因：测试 helper 实施有误，client-go cache 实际啥都没干
- 暴露：跑出来 client-go 异常快（明显不正常）
- 修复：重写 buildClientGoCacheFromSamples，用 SharedIndexInformer + GenericLister 灌入

**Bug 2.2**：cache_policy.Decide() 默认值污染 merge
- 根因：状态判定 + 访问判定都返回默认 LayerWarm
- 暴露：访问驱动关闭时仍触发降级
- 修复：拆 decideByState / decideByAccess 返回 (layer, ok)
  仅在 ok=true 时参与 merge

**Bug 2.3**：mergeLayers 简单 min 错误（语义 bug）
- 根因：原实现 `if a < b return a else return b`（简单取较热者）
- 暴露：测试 `状态 Idle + 访问 Cold → Cold` 期望失败（实际返回 Warm）
- 设计冲突：用户期望"Hot 特权 + 否则取较冷"语义
- 修复：
  ```go
  if a == LayerHot || b == LayerHot {
      return LayerHot  // Hot 特权
  }
  if a > b { return a }  // 否则取较冷（数值大者）
  return b
  ```
- 完整规则表写入注释 + TestMergeLayers 增至 8 cases

### 工程意义

第 3 个 bug 实际是**设计语义 bug**（不只是代码错）：
- 原 mergeLayers 假设"任一驱动说重要 → 升级"
- 用户实际想要"Hot 是积极特权 + Cold 是消极倾向"
- 区别在于 `Warm + Cold = Warm`（旧）vs `Cold`（新）

测试驱动暴露了设计层面的不严谨，工程价值高。

---

## Day 3: Informer + watch loop（commit 417-418）

### 拆分策略

Day 3 是 Step 1 最复杂的部分，按"接口先行 + 实施跟随"分两次 commit：

- **Step 3.1（commit 417）**：接口 + reconnect + resync + 单测
- **Step 3.2（commit 418）**：watch loop 主体 + fake K8s 测试

理由：单 commit 1500+ 行难以审查 / 调试。分两步留下"接口完成"checkpoint。

### Step 3.1 产出（commit 417）

```
informer.go     238 行   接口 + 类型定义
reconnect.go    134 行   ReconnectPolicy + NextBackoff + jitter
resync.go       135 行   差异化 resync 周期映射

informer_test.go     126 行    6 cases
reconnect_test.go    221 行    9 cases
resync_test.go       188 行   12 cases
```

#### 关键设计实施

**ShardSet 接口占位（避免循环依赖）**：
```go
type ShardSet interface {
    Owns(namespace string) bool
}
```
不直接 import internal/sharding/，由 Day 4 adapter 桥接。

**ReconnectPolicy 默认值**（draft.md §5 规划 → 实际）：
- InitialBackoff: 1s
- MaxBackoff: 30s
- BackoffFactor: 2.0
- Jitter: 0.2 (±20%)
- MaxAttempts: 0 (无限重试)

退避序列示例：
```
attempt 0: 1s   ± 0.2s  → [0.8s, 1.2s]
attempt 1: 2s   ± 0.4s  → [1.6s, 2.4s]
attempt 5: 16s  ± 3.2s  → [12.8s, 19.2s]
attempt 6+: 30s ± 6s    → [24s, 36s]   (封顶)
```

**ResyncPeriod 差异化**（draft.md §6）：
- High (10min): pods / events
- Medium (30min): deployments / statefulsets / jobs
- Low (60min): services / configmaps / ingresses
- VeryLow (120min): namespaces / nodes / storageclasses

normalizeResource 处理大小写不敏感 + 单复数容错（pod → pods, ingress → ingresses）。

**NewInformer 占位实施**：Step 3.1 阶段返回 `errInformerNotImplemented` sentinel，Step 3.2 替换为真实工厂。

### Step 3.2 产出（commit 418）

```
informer_impl.go         822 行   watch loop 主体
informer_impl_test.go    767 行   fake K8s API server + 14 cases
```

#### 状态机实施（draft.md §5）

```
runWatchLoop 主循环：
  doInitialList (全量 list)
    ↓
  for {
      doWatch (单次 watch 长连接)
        ↓ 断线
      退避等待 (NextBackoff)
        ↓
      继续 doWatch
        ↓ 410 Gone
      清空 RV → doInitialList → 继续 doWatch
      
      resync ticker.C → 周期性 doInitialList
      ctx.Done / stopCh → 退出
  }
```

#### HTTP 实施

不引入 client-go，自实现 watch 协议：
- net/http 长连接（Transport 复用 MaxIdleConns=10）
- 服务端 chunked transfer encoding（Go HTTP client 自动处理）
- 客户端 bufio.Scanner 按行（NDJSON）读取
- maxJSONLineSize = 1MB（K8s 单对象通常 < 100KB）

#### 增量序列化优化（draft.md §4 落地）

```go
// MODIFIED 事件处理
old, _ := im.cache.Get(r.Namespace, r.Name)
if old != nil && !SkeletonChanged(old, r) {
    im.cache.Put(r) // 仍更新 cache（保持最新 RV）
    return nil      // 但不发 EventUpdate
}
```

意义：K8s housekeeping 经常只改 RV 不改业务字段。这种 MODIFIED 跳过派发，下游订阅者无谓 reconcile 减少。

#### Subscriber 模型

- 每个 subscriber 独立 goroutine + buffer queue（size=1024）
- 慢 handler 不阻塞 watch loop（buffer 满 drop + 计入 EventsDropped）
- handler panic 时 recover 并计入 SubscriberStats.Panics

### Bug 修复链（Step 3.2 暴露）

**Bug 3.1**：Stop() 在未 Start 时等 5s timeout
- 根因：`startOnce.Do` 已用 → `doneCh` 会被 close
        `startOnce` 未用过 → `doneCh` 永远不 close
        Stop 走 5s timeout 兜底分支
- 暴露：`TestNewInformer_ValidOptions` 等待 5s
- 修复：加 `started atomic.Bool`，Stop 仅在 `started=true` 时等 doneCh

**Bug 3.2**：WatchReconnects 漏计正常关流（**真生产 bug**）
- 根因：watch 长连接被 K8s API server 主动定期关闭（5-10min）
        doWatch 返回 nil（无错误）
        原代码仅在 `err != nil` 时 `watchReconnects.Add(1)`
        漏计这种场景 → Prometheus 监控指标失真
- 暴露：`TestInformer_ReconnectsAfterDisconnect` 期望 WatchReconnects ≥ 1，实际 0
- 修复：`watchReconnects.Add(1)` 移到 `if err` 之外
        任何 watch 退出（无论原因）都算 reconnect

工程意义：
- Bug 3.1 是测试基础设施 bug
- Bug 3.2 是真实生产 bug（影响监控指标）
- 测试用例的 `SetForceCloseAfter(1)` 模拟 K8s 服务端主动关流
  这种"边界场景测试"暴露了生产监控指标问题

### commit hook 踩坑（commit 418 push 时）

KubePivot 的 commit hook 检查 subject regex。第一次 push 失败，原因不是 message 内容，是命令错：

```bash
# 错误命令（"-m" 多余）
git commit -m -F file.txt
# 解析为：commit message = "-F"
# "-F" 不匹配 regex → hook 报错

# 正确命令
git commit -F file.txt
```

教训：subject 全 ASCII 安全 + commit 命令永远不混 -m 和 -F。

---

## Day 4: v2.5 sharding adapter（commit 419）

### 计划

draft.md §9 规划 ShardSet 接口对接 v2.5 sharding。

### 实际产出

```
adapter_sharding.go         124 行
adapter_sharding_test.go    198 行 (12 cases)
```

### 设计决策（A 路径）

v2.5 的 `sharding.ShardSet` 实际签名：
```go
func (s *ShardSet) OwnsNamespace(namespace string, totalShards int) bool
```

eventstream 期望的接口：
```go
type ShardSet interface {
    Owns(namespace string) bool
}
```

差异：v2.5 是无状态分片索引集合（需传 totalShards），eventstream 期望封装好的接口。

**A 路径**：adapter 持有 totalShards 静态配置（启动时一次传入）

考虑过 B 路径：从 MultiLeaseManager 动态读 → 但 v2.5 的 `m.totalShards` 是私有字段，没有公开 `TotalShards()` 方法。且 v2.5 实际语义即静态（部署时定）。

### OnShardChanged 处理

v2.5 的 MultiLeaseConfig 已含：
```go
OnShardChanged func(added, removed []int)
```

v2.7.0 当前实现：**adapter 不订阅此 callback**。
- shard 变化时漏事件由 resync ticker（默认 30min）兜底
- v2.7.1+ 计划：adapter 内启动 goroutine 监听，触发 EventResync
- 注释中已记录扩展点

### staticShardSet 兜底实现

测试 + 单 pod 场景用：
```go
NewStaticShardSet(nil)             // 所有 ns 归本 pod
NewStaticShardSet([]string{"a"})   // 仅 ns "a" 归本 pod
```

### 测试覆盖

12 cases 覆盖：
- 参数验证（nil / 0 / 负数）
- Owns 行为（all / none / partial）
- 与 v2.5 ShardOf 一致性（50 个 ns 验证）
- staticShardSet 三种构造形态
- 接口契约编译期 + runtime 验证

---

## Day 5: Metrics Prometheus collector（commit 422）

### 计划

draft.md §8 原规划是 metrics-server 集成（kubectl top + Prometheus 双 client）。
讨论后决定：
- §8 内容（业务指标拉取，v2.9 sizing engine 用）→ **延后到 v2.7.x 或 v2.8**
- Day 5 实际做：**Informer 自身的运行指标**（draft.md 未规划，是补充实施）

### 设计决策（4 项细节校准）

#### 1. 接口签名分离（Registerer 注入）
```
RegisterInformerMetrics(reg prometheus.Registerer, informers ...Informer) error
```
- 不绑定 `prometheus.DefaultRegisterer`
- 便于单元测试 / 多 instance 隔离

#### 2. 命名约定遵循 Prometheus 标准
- `_total` 后缀强制用于 Counter
- `_ratio` 后缀（0.0-1.0）优于 `_percent`（0-100）
- `_timestamp_seconds` 配合 Gauge 表达"最后一次时间"

#### 3. EventType label 转小写
- `"ADD"` → `"add"`，符合 Prometheus 约定
- 避免在 Grafana 查询时大小写不一致导致"数据消失"

#### 4. last_resync 首次未 resync 时不暴露（Micro-adjustment）
```go
if !stats.LastResyncTime.IsZero() {
    ch <- prometheus.MustNewConstMetric(...)
}
```
- 避免 Prometheus 出现 1970-01-01 怪异时间戳
- 等首次成功 resync 后才开始暴露

### 实际产出

```
internal/eventstream/
  metrics.go       310 行   InformerCollector + RegisterInformerMetrics
  metrics_test.go  430 行   16 cases (含 testutil.GatherAndCompare)
```

### 暴露的 9 个指标

| 名称 | 类型 | 标签 |
|---|---|---|
| `kubepivot_informer_cache_size` | Gauge | resource |
| `kubepivot_informer_cache_hot_count` | Gauge | resource |
| `kubepivot_informer_cache_warm_count` | Gauge | resource |
| `kubepivot_informer_cache_cold_count` | Gauge | resource |
| `kubepivot_informer_events_total` | Counter | resource, type |
| `kubepivot_informer_watch_reconnects_total` | Counter | resource |
| `kubepivot_informer_cache_hit_ratio` | Gauge | resource |
| `kubepivot_informer_memory_bytes` | Gauge | resource |
| `kubepivot_informer_last_resync_timestamp_seconds` | Gauge | resource |

### Subscriber 指标暴露策略（推迟 v2.7.1+）

v2.7.0 仅暴露 informer 级别（不含 subscriber 级别）。理由：
- subscriber 没有 stable id
- 作为 label 会导致 cardinality 爆炸（压垮 Prometheus）
- "缓存整体命中率 / Watch 重连频率" 比 "某订阅者丢包"优先级高

v2.7.1+ 预留方案：在 `InformerStats` 增加 `AggregatedSubscriberStats`。

### HTTP Server 归属权

eventstream 包**不创建** HTTP server。
调用方负责 `promhttp.Handler()` 接入 `/metrics` endpoint。

理由：
- 职责单一：eventstream 只管数据流动 + 统计
- 架构灵活：CLI 不需要 server，controller pod 才需要
- 可插拔：调用方决定何时启动 / 监听哪个端口

### 依赖引入决策

```
github.com/prometheus/client_golang v1.20.5
+ 5 个间接依赖（beorn7/perks, klauspost/compress, kylelemons/godebug,
                munnerz/goautoneg, prometheus/client_model）
go.sum 增加 ~25 行
```

哲学红线对齐：
- 拒绝 client-go 是因为它逻辑侵入性强 / 体积臃肿
- prometheus/client_golang 是纯粹的"打点 + 暴露"工具
- 属于云原生通用语，不影响 KubePivot 核心执行逻辑
- 工程税合理（~5MB 体积换 Grafana / Alertmanager 生态接入）

### Bug 修复（架构层）

#### Bug 5.1: collector duplicate registration

**根因**：原设计每个 informer 一个 collector
- 每个 collector 内部独立创建 desc
- 注册多个 collector 到同 registry → desc fqName 冲突
- 报错 `duplicate metrics collector registration attempted`

**暴露**：`TestRegisterInformerMetrics_MultipleInformers` fail

**修复**：架构层重写
- desc 全局共享（一组）
- 单 collector 持有多个 informer
- 通过 `resource` label 区分不同 informer 的指标输出
- `RegisterInformerMetrics` 改为 variadic 一次性注册多个 informer
- 新增 `AddInformer` / `RemoveInformer` 支持运行时增减

**教训**：
- Prometheus collector 模式应是 "一个 collector = 一组相关 metrics"
- 不是 "一个 collector = 一个数据源"
- 这是 client_golang 内置 collector（GoCollector / ProcessCollector 等）的标准模式
- 单元测试自查发现了 6 个其他问题，但**没看出这个架构 bug**
- → 单元自查不能替代架构 review

### 测试覆盖（16 cases）

```
Collector 生命周期：
  NotNil / AddInformer / AddInformer_NilSafe / RemoveInformer

Describe / Collect：
  DescribeAllDescriptors  9 个描述符全输出
  CollectAllGauges        cache 4 层指标
  EventsTotalByType       4 个事件类型 + label 转小写
  WatchReconnects         Counter 输出
  CacheHitRatio           4 个边界值

Micro-adjustment 验证：
  LastResync_NotExposedBeforeFirstResync   IsZero 不输出 ⭐
  LastResync_ExposedAfterFirstResync       有值时输出

动态行为：
  StatsUpdated            collector 不缓存 stats
  DynamicAdd              运行时 AddInformer
  MultipleInformers       一次性注册多个

错误处理：
  NilRegisterer / NoInformers / NilInformer
  DuplicateRegisterCollectors  desc 冲突报错
```

---

## Bench 3 状态（待办）

Bench 3 watch 吞吐 benchmark 是 v2.7 Step 1 唯一未完成的项目。

需要：
- feature/client-go-comparison 分支跑
- 用 envtest 启动 fake K8s API server
- 测试 watch 事件接收吞吐量
- 数据补到 docs/design/eventstream-perf.md

延后理由：
- envtest 启动配置复杂（需要 etcd / kube-apiserver binary）
- Step 1 主线代码已完整可用，Bench 3 是补充验证
- 留 v2.7 release 前一次性完成

---

## 累计数据

### 代码规模

| 模块 | 代码行数 | 测试行数 | 测试 cases |
|---|---|---|---|
| 包基础 (doc/resource/serializer) | 374 | 200 | 22 |
| Cache | 370 | 250 | 22 |
| cache_policy | 222 | 200 | 13 |
| Informer 接口 + reconnect + resync | 507 | 535 | 27 |
| Informer 实施 + 测试 | 822 | 767 | 14 |
| sharding adapter | 124 | 198 | 12 |
| metrics collector | 310 | 430 | 16 |
| **合计** | **2729** | **2580** | **126** |

（简化口径，含 sub-cases 实际 ~170 cases）

### 测试质量

- 覆盖率：88.8%
- Race detector：全绿
- make dev：全绿（不破坏既有 controller / route / sharding）

### Bug 修复总览

| Bug | 性质 | 暴露方式 | 修复 commit |
|---|---|---|---|
| 2.1 cold_start client-go cache 构造错误 | 测试基础设施 | benchmark 数据异常快 | 414 内修 |
| 2.2 cache_policy.Decide 默认值污染 | 实施 bug | 单测 fail | 416 内修 |
| 2.3 mergeLayers 简单 min 语义错 | 设计 bug | 单测 fail | 416 内修 |
| 3.1 Stop without Start 等 5s | 实施 bug | 单测耗时 5s | 418 内修 |
| 3.2 WatchReconnects 漏计正常关流 | **真生产 bug** | 单测 fail | 418 内修 |
| 5.1 collector duplicate registration | **架构 bug** | 单测 fail | 422 内修 |

工程纪律：所有 bug 在本地修复，无任何 fix commit 流入 Master。

---

## 工程哲学落实情况

### 不藏数据

- benchmark 数据完整公开（含 v1 失败迭代）
- bug 修复链全程记录
- ListAll 持平诚实标注（不夸大胜利）

### 不引入 client-go

- watch 协议自实现（net/http + bufio.Scanner NDJSON）
- 整个 internal/eventstream/ 包零 k8s.io/client-go 引用
- benchmark feature 分支保留 client-go 对比，不合并回 Master

### 设计先行 + 实施低决策密度

- draft.md 14 个 Q&A 锁定后实施
- 实施时仅做"按 mapping 写代码"
- 出现 5 个 bug，全部在本地修复

### 不 push 中间状态

- 单测全绿才 commit
- Master 历史无 fix commit 污染
- 6 个 commit 对应 6 个清晰阶段

---

## 编辑记录

- 2026-04-27 15:30  初版（Day 1-4 完成时创建）
- 2026-04-27 16:30  Day 5 metrics 完成后追加（含 Bug 5.1 架构 bug 实录）
- 待补充：Bench 3 数据 + 最终 finalize 笔记（v2.7 release 前）
