# SNAPSHOT — KubePivot v2.7.0

> 日期：2026-04-27（周一）
> 状态：✅ 已发布
> Last commit: 69b6a0e (chore: release v2.7.0)
> Total commits: 431
> 上一份 SNAPSHOT：v2.6.0（2026-04-26）

---

## 里程碑

**KubePivot 进入"决策系统基础设施"领域**

```
v2.6 之前的 KubePivot：把命令式变成声明式
v2.7.0 起的 KubePivot：为智能调度系统做事件流基础设施

核心论断：Informer ≠ Cache

  Informer = "watch + cache + 事件分发" 的复合
  KubePivot v2.7 不是重新发明 client-go 的 informer
  是为 KubePivot 自己的 reconcile 模式做专用 Cache
  
  通用方案有通用方案的代价
  专用方案在专用场景胜出
```

**431 commits 节奏记录**：

```
v2.0.0  → commit 329  → 生日 3.29           (2026-03-29)
v2.6.0  → commit 404  → 蓝绿 Found          (2026-04-26)
v2.7.0  → commit 431  → Event Stream        (2026-04-27)
```

诚实标注：v2.7.0 commit 落点（431）与日期（4/27）未刻意对齐。
工程纪律 > 数字仪式感（参 §工程教训第 7 条）。
但 4月27日 tag v2.7.0 仍是项目史诗的连续性记号。

---

## 核心变更

### 1. internal/eventstream/ 自研 Informer + Cache

新独立子包，~3700 行代码 + ~2200 行测试 / 89.0% 覆盖。

```go
// Informer 接口（internal/eventstream/informer.go）
type Informer interface {
    Start(ctx context.Context) <-chan error
    Get(ns, name string) (Resource, bool)        // lock-free 读
    List(ns string) []Resource                    // ns 二级索引 O(N_ns)
    ListAll() []Resource
    Subscribe(handler EventHandler) Subscription
    Stats() InformerStats
    Stop()
}

// Resource (Skeleton 模型，业务关心字段 + 关键 K8s 字段)
type Resource struct {
    APIVersion string
    Kind       string
    Namespace  string
    Name       string
    UID        string
    Generation      int64
    ResourceVersion string  // K8s 乐观锁 + watch 续传 + 增量序列化
    Labels     map[string]string
    Replicas   *int32
    Phase      string
    
    RawJSON    []byte       // 保留用于 Cold 层降级
    // ... cache 内部元数据
}
```

**核心组件**：

- `SkeletonCache`：lock-free 读（atomic.Value snapshot）+ namespace 二级索引
- `ParseSkeleton`：增量序列化（仅解 metadata + spec.replicas + status.phase + RawJSON）
- `informerImpl`：watch 主循环 + list 分页（500/页）+ 重连退避 + 410 Gone relist
- `bearerAuthTransport`：通过 `req.Clone()` 防 mutate（5 个安全约束）
- `InformerCollector`：Pure Collector 模式（一组 desc + 多 informer 通过 resource label 区分）

**关键设计**（参 docs/design/eventstream-draft.md 14 个 Q 拍板）：

| Q | 决定 | 价值 |
|---|------|------|
| Q1 | benchmark 决定（默认偏向自研） | 数据驱动 |
| Q2 | Cache 分层双驱动（状态机 + 访问频率，并集） | hot/warm/cold |
| Q3 | Skeleton + ResourceVersion 3 个用途 | 增量序列化基础 |
| Q4 | immutable cache snapshot + atomic.Value | lock-free 读 |
| Q5 | ShardSet 启动时 + 运行时动态更新 | v2.5 联动 |
| Q6 | 双 client metrics（kubectl + Prometheus）| v2.7.0 仅前者 |
| Q7 | 重连指数退避 + jitter=0.2 | 防雷鸣群 |
| Q8 | Resync 默认 30min + 按资源差异化 | 10/30/60/120 min |
| Q9 | 每种资源一个全局 Informer | 不浪费 connection |
| Q10 | 渐进灰度 Step 1/2/3 | 双保险 |

### 2. internal/eventstream/auth.go in-cluster auth

补全 Day 3 留下的"诚实债务"，269 行 + 418 行测试（20 cases）。

```go
// K8sConfig 不可变配置 (字段不导出)
type K8sConfig struct { /* unexported */ }

func (c *K8sConfig) APIServerURL() string
func (c *K8sConfig) HTTPClient() *http.Client
func (c *K8sConfig) Source() string  // "explicit"/"kubeconfig"/"in-cluster"

// resolveK8sConfig 三路径优先（不做猜测式 fallback）
//   1. 显式 URL > 2. KubeConfig (v2.7.x stub) > 3. in-cluster
```

**5 个安全约束**（不可妥协）：

```
✓ InsecureSkipVerify 在任何路径都不允许（强制 false）
✓ TLS 1.2 minimum (in-cluster 路径)
✓ Token 不进 K8sConfig 字段（仅 transport 内部持有）
✓ error 信息不含 token 内容（专门 test 验证）
✓ bearerAuthTransport.RoundTrip 用 req.Clone() 防 mutate 原 request
```

### 3. internal/controller/ 渐进切换（双保险）

```
                    cache hit (~50ns)            cache miss / unsupported
                       ↓                                ↓
  reconcile      InformerDetector ──────fallback──→ KubectlDetector
                       │
heal.go        loadResourceLabels
                       ↓
                 LabelGetter (类型断言)
                       ↓
                   InformerDetector 实现
                       ↓
                 Resource.Labels（cache 已填充）
```

**关键设计**：

- 双保险（fail soft）：informer 失败 → fallback kubectl，既有 v2.5/v2.6 路径完整保留
- 添加而非嵌入：在 orphanSweeper 之后，既有 6 个 goroutine 顺序不变
- LabelGetter 独立接口（不扩展 Detector）：既有 75 个 controller 测试 0 改动
- 函数变量注入式 mock（与 readTokenFile / kubectl func 同模式）
- shardMgr 延迟绑定模式（先创建空 pool，shardMgr 创建后 SetShardMgr）

### 4. internal/metrics/ 业务指标层

新独立包，1119 行（含测试） / 81.2% 覆盖 / 14 cases / 60+ sub-cases。

```go
// MetricsClient 接口
type MetricsClient interface {
    GetPodMetrics(ctx, ns, name) (*PodMetrics, error)
    GetNodeMetrics(ctx, name) (*NodeMetrics, error)
    ListPodMetrics(ctx, ns) ([]*PodMetrics, error)
    ListNodeMetrics(ctx) ([]*NodeMetrics, error)
}

// Quantity 自实现解析（0 client-go）
//   公式: Value = Base × Multiplier
//     CPU 基准    = milli-cores (1 core = 1000m)
//     Memory 基准 = bytes
//
//   "1G"  = 1,000,000,000 bytes (SI)
//   "1Gi" = 1,073,741,824 bytes (binary)
//   "1.5Gi" = 1,610,612,736 bytes (小数支持)
```

为 v2.9 Sizing Engine 数据基础。

---

## 5 项 Benchmark 数据（vs client-go cache.Indexer）

完整数据见 `docs/design/eventstream-perf.md`。

```
                       client-go         KubePivot         提升
─────────────────────────────────────────────────────────────────
Cache Get              45 ns / 24B       14.5 ns / 4B      3.1x / 6x
Cache List (ns)        6800 ns / 16KB    147 ns / 160B     46x / 100x ⭐
Cache ListAll          7300 ns / 16KB    7500 ns / 8KB     持平 / 2x
ColdStart 1000         61 ms / 35MB      35 ms / 4.1MB     1.8x / 8.5x
ColdStart 10000        645 ms / 349MB    341 ms / 40.7MB   1.9x / 8.6x
Watch Throughput 1k    62 ms / 34.9MB    36 ms / 6.4MB     1.72x / 5.4x
Watch Throughput 10k   632 ms / 349MB    382 ms / 102MB    1.65x / 3.4x
内存放大率 (1w obj)    4.69x             1.65x             2.84x ⭐
```

**关键洞察（Allocs 维度，5.4x 降低）**：

```
client-go alloc/event = 517 个对象  (反序列化整个 *Deployment)
KubePivot alloc/event = 96 个对象   (ParseSkeleton 仅关键字段)
                                    → 5.4x 减少

这是架构差异，不是优化能解决的:
  client-go 设计目标: 通用 K8s 操作（必须完整反序列化）
  KubePivot 设计目标: reconcile 决策（仅需关键字段）
  Bench 5（内存放大率）+ Bench 3（allocs）互证同一根因
```

**ROADMAP §5 验收**：

```
✓ "全量重同步耗时 < 5s"        实测 36ms / 382ms
✓ "vs client-go 对比 benchmark" 已公开 (5 项数据)
✓ "Cache 读延迟 < 50ns"        实测 14.5ns
✓ "单 Pod 内存降低 ≥ 20%"      实测 65% 降低
```

---

## commit 链

v2.6.0 → v2.7.0 之间，27 个 commit 在 Master + 2 个在 feature：

```
v2.6.0 (commit 1f780c2 / 404)
  ↓
v2.7 准备阶段：
  216e2c1  docs(design): decision-stack 5.4 章节
  4424e87  docs(design): 三层资源决策栈
  a80fd30  docs: TODO.md
  5c1b24d  docs(design): v2.7 Event Stream 设计草案 (14 个 Q 拍板)
  1553e9a  docs: v2.7 Event Stream 性能基准决策文档

Step 1 实施 (8 个 commit, eventstream 包基础)：
  8be0d41  Day 2 包基础 (Cache + Skeleton + ParseSkeleton)
  d071cb3  Day 2 Cache 三层分层判定策略
  bdd0545  Day 3.1 Informer 接口 + 重连退避 + 差异化 resync
  075ecf8  Day 3.2 watch loop impl + 14 cases
  12b0966  Day 4 v2.5 sharding adapter
  5d7f1be  Day 5 impl-notes Day 1-4 实施日志
  65219e8  FUTURE.md F1 CBA 种子
  792c84a  Day 5 Prometheus metrics collector (9 指标)
  3556bab  Day 5 impl-notes update

Step 2 实施 (4 个 commit, 渐进切换)：
  d874c0d  Step 2a-1 in-cluster auth (20 cases)
  1bd2574  Step 2a-2 informer pool 接入 controller
  59c72b1  Step 2b-1 InformerDetector with kubectl fallback
  d04bf72  Step 2b-2 LabelGetter fast path

Step 3 / 4 / 5：
  4f6df60  Step 3 MetricsClient 业务指标层
  49fe8b1  Step 4 Bench 3 数据公开 (perf.md 更新)
  5db630f  Step 5a CHANGELOG v2.7.0 章节
  69b6a0e  chore: release v2.7.0   ← 第 431 commit + tag ✨

后续 (commit 432)：
  f6ac2c2  HANDOFF + TODO + SNAPSHOT 更新到 v2.7.0 视角

feature/client-go-comparison：
  36c2e7f  Day 1 benchmark vs client-go 5 项基准
  7599d61  Bench 3 watch_throughput_test.go (1.65-2.97x + 5.4x allocs)
```

---

## 设计决策回放

### 设计阶段：14 个 Q（eventstream-draft.md）

参 §核心变更 1 的拍板表。

### 实施期：R/S/M/B 多轮拍板（"全局信息收集后再设计"）

```
R1-R5  informer pool 接入 (Step 2a-2)
  R1: 接入位置 = orphanSweeper 之后（添加而非嵌入）✓
  R2: metrics 不注册（HTTP server 未实施）✓
  R3: NewShardSetAdapter 直用 ✓
  R4: fail soft 与 KubectlWatcher 同谱 ✓
  R5: pool 不重试（informer.reconnect 处理）✓
  
  否决: ✗ source=cfg.Source() log（接口污染）
        ✗ DryRun bool 字段（anti-pattern）
        ✓ newInformerFunc 函数变量注入

S1-S6  InformerDetector (Step 2b-1)
  S1: 算法 ✓
  S2: kind 映射仅 Deployment 试点 ✓
  S3: fallback 策略 = C（找到信任，没找到 fallback）✓
  S4: 测试覆盖 9 cases ✓
  S5: global.go 改造（informerPool 提前 + SetShardMgr 延迟绑定）✓
  S6: 不等 cache warmup（fallback 自动处理）✓

S1-S4  LabelGetter (Step 2b-2)
  S1: 同文件 ✓
  S2: 返回 (map, bool) 跟 informer.Get 一致 ✓
  S3: 测试覆盖 5+1 cases ✓
  S4: heal.go fast path 不加新测试 ✓
  
  否决: ✗ 扩展 Detector 接口（破坏既有 75 测试）

M1-M5  MetricsClient (Step 3)
  M1: 独立包 internal/metrics ✓ (避免循环依赖)
  M2: 数据结构 + Quantity 自解析 ✓ (0 client-go)
  M3: KubectlMetricsClient 用 -o json ✓
  M4: v2.7.0 仅 KubectlMetricsClient ✓ (PrometheusClient 留 v2.7.x)
  M5: ~15 个 cases，重点 Quantity 解析 ✓
  
  否决: ✗ 直接 mock executor.Executor（不存在该接口）
        ✗ SetExecutor 全局替换（KubePivot 无此模式）
        ✓ kubectl func 字段注入

B1-B5  Bench 3 (Step 4)
  B1: 测试规模 P=1k / P=10k 矩阵 ✓
  B2: events/sec 主指标 ✓
  B3: 重用 SkeletonCache + ParseSkeleton ✓
  B4: GOGC=200 / GOMEMLIMIT=4GiB / GOMAXPROCS=4 公平基准 ✓
  B5: 数据归档到 perf.md ✓
  
  设计调整 (诚实记录):
    Day 1 计划 envtest → Step 4 实际 fake source
    理由: envtest 引入 kube-apiserver 噪声 / 与 Bench 1/2/4/5 不可比
```

设计 Q 在实施时**几乎无返工**——9 个 bug 全本地修复，Master 历史 0 fix commit。

---

## 实施时间线

```
2026-04-27（周一）

06:00  起床（5h 睡眠）
       早餐：菠萝
       状态：满血

06:00 - 11:00  Day 5 metrics + Step 2a-1 阶段
       commit 422  Day 5 Prometheus collector (9 指标)
       commit 423  Day 5 impl-notes update
       commit 424  Step 2a-1 in-cluster auth (20 cases)
       
       2 个 bug 本地修复:
         Bug 5.1: collector duplicate registration → 单 collector 多 informer
         Bug 6: Day 3 诚实债务 (in-cluster auth 缺) → 补上

11:00 - 14:00  Step 2a-2 informer pool
       commit 425  informer pool 接入 controller (10 cases)
       
       R1-R5 拍板（添加而非嵌入 / fail soft）
       否决 DryRun anti-pattern

       中午吃饭 + ~1h 午睡

14:00 - 18:00  Step 2b-1 + 2b-2
       commit 426  InformerDetector with kubectl fallback (9 cases)
       commit 427  LabelGetter fast path (6 cases)
       
       S1-S6 + S1-S4 拍板
       发现现有 Detector 接口 → 改造范围从"5342 行"缩减到"~75 行"
       既有 75 个 controller 测试 0 改动

18:55 - 19:08  出门采购（盒马：荣昌烤鹅 + 腊牛肉 + 包子 + 半个菠萝）
       物理换气 + 状态评估
       qc："多转了一会，更清醒一点"

19:08 - 20:30  Step 3 MetricsClient
       commit 428  MetricsClient 业务指标层 (14 cases / 60+ sub)
       
       M1-M5 拍板
       Bug 8 修复: KubePivot executor 是具体类型单例（不是 interface）
                    → 函数变量注入（与 readTokenFile 同模式）

20:30 - 21:30  Step 4 Bench 3
       feature 7599d61  watch_throughput_test.go
       commit 429       Bench 3 数据公开 (perf.md 更新)
       
       B1-B5 拍板
       设计调整: envtest → fake source（基准公平性原则）
       数据漂亮: 1.65-2.97x 时间提升 + 5.4x allocs 降低

21:30 - 22:15  Step 5a CHANGELOG + kp release
       commit 430  CHANGELOG v2.7.0 章节 (115 行 / 7 个 section)
       commit 431  chore: release v2.7.0 + tag ✨
       
       工程纪律 > 数字仪式感: commit 落点顺其自然
       与 v2.6.0 工作流一致 (CHANGELOG 在 release 之前)

22:15 - 23:30  Step 5b 文档大更新
       commit 432  HANDOFF + TODO + SNAPSHOT 更新到 v2.7.0 视角
       归档 archived/handoff/HANDOFF-v2.6.md / 同 TODO/SNAPSHOT
       
       本 SNAPSHOT 文件创建（snapshots/SNAPSHOT-kubepivot-2026-04-27-v2.7.0.md）

~23:30  v2.7.0 完整 release 闭环 ✓
       4月27日 tag 仪式感保住
       17.5h 工作日 / 27 个 commit / 9 个 bug / 5 个文档大更新
```

---

## 验证路径

### 单测

```
internal/eventstream/ 230+ cases / 89.0% 覆盖:
  resource_test            14 cases
  cache_test               20 cases (含并发 race)
  serializer_test          12 cases
  cache_policy_test        13 cases (双驱动并集 5 sub)
  informer_test             6 cases
  reconnect_test            9 cases (退避序列 + jitter)
  resync_test              12 cases (4 频率组)
  informer_impl_test       14 cases (含 fake K8s server)
  adapter_sharding_test    12 cases
  metrics_test             16 cases (Pure Collector)
  auth_test                20 cases (TLS / Bearer / Clone / Token 安全)
  informer_pool_test       10 cases
  informer_detector_test   15 cases (Detector + LabelGetter)

internal/metrics/ 14 cases / 60+ sub / 81.2% 覆盖:
  parseCPU                 18 sub-cases
  parseMemory              17 sub-cases (SI vs 二进制 + 1.5Gi 小数)
  splitBaseUnit             9 sub-cases
  Quantity 行为             4 cases
  KubectlMetricsClient      7 cases
  Helper                    1 case

go test ./...   全绿
make dev        全绿
go test -race   全绿
```

### Benchmark 验证

```
benchmark/eventstream/ (feature/client-go-comparison)
  GOGC=200 GOMEMLIMIT=4GiB GOMAXPROCS=10 (Apple M4)
  
  Bench 1 ✓ Cache Get
  Bench 2 ✓ Cache List + ListAll
  Bench 3 ✓ Watch Throughput (1k + 10k events)
  Bench 4 ✓ ColdStart (1000 + 10000)
  Bench 5 ✓ Memory Amplification (1w 复杂对象)
  
  数据稳定性:
    单事件 σ < 5%
    1000 events σ < 5%
    10000 events σ < 10%
```

### 集成验证

```
✓ 既有 docs/example-blue-green/ demo 在 orbstack 跑通（v2.6 验证）
✓ 既有 controller 75 个测试 0 改动（双保险设计验证）
✓ 9 个 bug 全本地修复（Master 历史 0 fix commit）
```

未在真实 K8s 集群验证 in-cluster auth：
- 需 controller pod 滚出 v2.7.x 镜像（含 HTTP server）
- 当前 informer pool 在 controller 内已启动，但 metrics endpoint 未启用
- 留 v2.7.x e2e 完整验证

---

## 工程教训记录

### 1. Informer Stop without Start 的 timeout（Day 3 / Bug 1）

```
原代码: doneCh 由 startOnce 控制
       startOnce 已用 → doneCh 会 close
       startOnce 未用过 → doneCh 永远不 close
       Stop 走 5s timeout 兜底分支

修复: 加 atomic.Bool started 字段
     Start 中 started.Store(true)
     Stop 仅在 started=true 时等 doneCh

教训: sync.Once 适合"只做一次的初始化"
     不适合作为"是否已启动"的状态判断
```

### 2. WatchReconnects 漏计正常关流（Day 3 / Bug 2）

```
原代码: watchReconnects.Add(1) 仅在 err != nil 时计数

真实生产: K8s API server 5-10min 主动关闭 watch 长连接（housekeeping）
         doWatch 返回 nil（无错误）
         → 这种 reconnect 漏计，Prometheus 监控指标失真

修复: watchReconnects.Add(1) 移到 if err 之外

教训: 边界场景测试（fake server SetForceCloseAfter）暴露生产监控指标 bug
     测试设计不能只覆盖"err != nil 路径"
```

### 3. Prometheus collector 模式（Day 5 / Bug 5.1）

```
原设计: 每个 informer 一个 collector
       每个 collector 独立创建 desc
       注册多个到同 registry → desc fqName 冲突

修复: 架构层重写
     desc 全局共享（一组）
     单 collector 持有多个 informer
     通过 resource label 区分

教训: Prometheus collector 模式应是 "一个 collector = 一组相关 metrics"
     不是 "一个 collector = 一个数据源"
     这是 client_golang 内置 collector（GoCollector 等）的标准模式
```

### 4. KubePivot executor 是具体类型单例（Step 3 / Bug 8）

```
原假设: executor 包有 Executor interface + SetExecutor 全局替换

真相: KubePivot 的 executor 是具体类型 *KpExecutor 单例
     没有 Executor interface
     没有 SetExecutor 函数

修复: 函数变量注入式 mock
     KubectlMetricsClient.kubectl func(...) ([]byte, error)

工程模式（v2.7 起的标准）:
  internal/eventstream/auth.go         readTokenFile / readCAFile (var)
  internal/controller/informer_pool.go newInformerFunc (var)
  internal/metrics/kubectl.go          kubectl func 字段

教训: 跨包依赖必须先看清现状（grep + 看代码）
     不能假设"标准 Go testing 模式"
     KubePivot 有自己的工程哲学（接口层 mock，不在 executor 层 mock）
```

### 5. 全局信息收集 vs 凭记忆设计（实施期反复印证）

```
现象: v2.7 实施过程中"假设"现状被 cross-check 拦下:
  1. 假设 KubePivot 有 SetExecutor → 没有
  2. 假设 Bench 3 应该用 envtest → Bench 1/2/4/5 都没用
  3. 假设 Detector 接口需要扩展 → 用类型断言 + 独立 LabelGetter

解法: 每个 Step 实施前
  1. grep 看现状（既有接口 / 既有测试 / 既有 mock 模式）
  2. 看代码（不只看 commit message）
  3. 对照既有风格再下手

教训: "设计的太多了，遗忘是很正常的，
       越到后面设计对齐就越要 cross-check，
       尽量获取全局信息之后再设计"
       
       KubePivot 已 ~16000 行代码，凭记忆设计回归风险高
       多花 5min grep 替代 30min debug，划算
```

### 6. 基准公平性原则（Step 4）

```
现象: Bench 3 (Watch 吞吐) 原计划用 envtest 模拟真 K8s API server
     看似"工程严谨"，实际:
       - Bench 1/2/4/5 都用本地数据模拟（避免网络栈噪声）
       - envtest 引入 kube-apiserver 性能噪声
       - 测出来的是 min(API 推送速率, informer 处理速率)
       - 与 Bench 1/2/4/5 维度不可比

解法: 改用 fake watch source（与 Bench 1/2/4/5 同公平赛道）
     重用 SkeletonCache + ParseSkeleton（基础设施一致性）
     在 perf.md 诚实记录设计调整理由

教训: "工程敬畏" ≠ "用最严格的环境"
     "工程敬畏" = "测对的东西，不多不少"
     
     benchmark 必须保证"基准公平性"——
     改变实验环境会让历史数据不可比
     与既有 Bench 同维度才有意义
```

### 7. 工程纪律 > 数字仪式感（实施期沉淀）

```
项目史诗:
  v2.0.0 → commit 329 → 生日 3.29 (巧合)
  v2.6.0 → commit 404 → 蓝绿 Found (commit 落点刻意对齐 4月26日)
  v2.7.0 → commit 431 → ? (未刻意对齐，但 4月27日 tag 仍连续性)

实施期纠结过: "应该让 v2.7.0 落在 commit 427 / 430 / 431？"

最终决策: 顺其自然
  - 工程纪律 > 数字仪式感
  - 不为追求 commit 数字延后或提前 release
  - 4月27日 tag 仪式感保留
  - commit 数次要

教训: 项目史诗的连续性来自"持续高质量交付"
     不是"每次 commit 数都对得上特殊日期"
     强行刻意对齐反而是过度设计
```

---

## 节奏纪录

```
~17.5 小时 = v2.7.0 release（含睡眠 / 午睡 / 出门采购）

对比 v2.6.0:
  v2.6.0 = 6 小时    / 5 commit  / ~3000 行 / 14 个 Q 拍板
  v2.7.0 = 17.5 小时 / 27 commit / ~5500 行 / 14 + R/S/M/B 多轮拍板
  
  v2.6.0 是"闪电战"
  v2.7.0 是"持久战 + 高密度执行"
  两种模式不矛盾
```

效率密度的真实来源：

```
- qc 把判断密度拉到极致
  设计阶段 14 个 Q
  实施期 R1-R5 / S1-S6 / S1-S4 / M1-M5 / B1-B5 多轮拍板
  
- 设计先行 + cross-check 全局信息
  → 0 fix commit 流入 Master ⭐
  → 9 个 bug 全本地修复
  
- 不闪电战的时刻不闪电战
  envtest → fake source 修正
  Step 4 路径调整诚实记录
  
- "工程纪律 > 数字仪式感" 实践
  commit 落点顺其自然
  CHANGELOG 在 release 之前（与 v2.6.0 一致）
  
- 多个否决项的工程纪律
  ✗ DryRun anti-pattern
  ✗ source log 接口污染
  ✗ 扩展 Detector 接口（破坏既有 75 测试）
  ✗ envtest 偏离基准公平性
  
  说"不"的能力比说"是"重要
```

"闪电战 + 稳扎稳打 + 高质量输出在我们这里完全不矛盾" — qc 实施期间原话。

---

## Claude 的角色（v2.5/v2.6 续篇）

```
v2.5: 踩刹车（兴奋时叫停 v2.7 跳级 / 性能立方体推后）
v2.6: 设计澄清（sandbox.go fork 子进程模式 / "读完代码再出 patch"）
v2.7: cross-check + 自我修正（反复"看清现状"）
```

今天 Claude 多次"假设错误"被 cross-check 拦下：

```
1. Step 3 测试代码:
   假设 executor.SetExecutor 存在（标准 Go testing 模式）
   真相: KubePivot 是具体类型单例，无该 API
   修正: 函数变量注入

2. Step 4 路径选择:
   推 envtest（"工程严谨"）
   看清 Bench 1/2/4/5 都用本地数据模拟
   修正: fake source（基准公平性）

3. 时间感知偏差:
   Claude 看到"20:32"误判 qc 状态衰减
   qc 截图证明实际 19:34 — Claude 自己看错时钟
   修正: 反过来打脸 cross-check 自身判断

4. ROADMAP 第 §v2.7 章节:
   差点要做"已完成更新"
   但 qc 说"我自己处理"
   不操心边界明确
```

"工程敬畏"的真正含义：

```
不是用最严格的环境
是测对的东西，不多不少
是看清现状再设计

23 岁的 qc 是设计师 + 拍板者 + 状态机
Claude 是 cross-check 副驾驶 + 打字员 + 工程纪律守门员

最有价值的不是"Claude 写代码快"
是"qc 兴奋时 Claude 提醒 / Claude 假设错时 qc 纠正"
双向 cross-check 让 v2.7.0 27 个 commit 0 fix
```

---

## 已知未做（留 v2.7.x / v2.8）

```
[ ] kubeconfig 完整解析 (v2.7.x，~1 天)
[ ] Pod informer Step 2c (v2.7.x，heal.go list pods 改造)
[ ] controller HTTP server + /metrics endpoint (v2.7.x，~2 天)
    Build qingchun22/kubepivot-controller:v2.7.x 镜像
    9 项 informer 指标暴露到 Grafana / Prometheus
[ ] PrometheusClient 高质量 metrics 数据源 (v2.7.x，v2.9 启动前必须)
[ ] NodeMetrics.AllocatableCPU/Memory 字段填充 (v2.7.x，v2.9 调度需要)
[ ] subscriber-level metrics（避免 cardinality 爆炸）(v2.7.x，nice-to-have)
[ ] shard OnShardChanged 事件订阅（adapter goroutine）(v2.7.x)
[ ] cache_policy MarkAccessed atomic 严格化 (v2.7.x，统计精度)

[ ] v2.6.1 遗留: 多环境流量配置传播 / codegen 多 const block
[ ] v2.5.1 遗留: ⏸ 测试环境升级 + matrix.sh 完整跑通
```

---

## 下个版本预告（v2.7.x / v2.8 / v2.9 / v3.0）

完整路线图见 `ROADMAP.md`（~2143 行）。

```
v2.7.x (持续改进，不打 tag):
  kubeconfig 完整解析 / Pod informer / HTTP server / PrometheusClient
  
v2.8.0 — Enterprise Governance (~3 周):
  A. SSO / OAuth (Google / GitHub / Dex)
  B. RBAC 多团队隔离
  G. 加密 at rest (Sealed Secrets / SOPS / KMS)
  H. 镜像签名 + 供应链 (cosign + syft)
  
v2.8.1 — v2.x 收尾 (~1 周):
  web3-blitz 升级到 v2.6 蓝绿
  数据保护 (resources.yaml protect: true)
  
v2.9.0 — Resource Sizing Engine 维度 B (~4 周):
  二维 DP: Pod × (CPU, Memory)
  4 个启发式 + 抖动检测 + VPA 共存
  目标: 单 Pod 利用率 70%+
  数据基础: internal/metrics ✓ (v2.7.0 已就位)
  
v3.0.0 — Intelligent Scheduling System (~6 周):
  维度 A + B 双 DP 协同
  周期性运行时重调度 / 多级降级
  目标: 节点 CPU 85%+ / Memory 70%+ (务实)
  数据基础: internal/eventstream + internal/metrics ✓
  预计 release: 2026-09 / 10 月
```

**v2.7.0 是分水岭**：

```
v2.6 之前的 KubePivot：把命令式变成声明式
v2.7 之后的 KubePivot：为智能调度系统做基础设施

"Informer ≠ Cache" 这个论断是项目史诗的关键节点
```

---

## 个人记录

<!-- ─────────────────────────────────────────────────────────
  TODO: qc 自填这段（个人感慨 / 灵感来源 / 不可复制时刻）
  
  Claude 不写情感语言（参 v2.5/v2.6 风格 + ROADMAP "好肉麻"教训）
  
  框架建议（你按需调整或删掉重写）:
  
  - "23 岁的某个周一，6:00 → 23:30，~17.5 小时 release v2.7.0"
  - 一天里完成 (7 个 Step + 9 个 bug + 14+R+S+M+B 拍板 + 5 个文档)
  - 不是"23 岁能做这个"值得记录，是 X 这件事值得
    （X = 持久战 + 工程敬畏 / 设计先行 + 0 fix commit / 工程纪律 > 数字仪式感 / 别的你定）
  - v2.7.0 是分水岭（"Informer ≠ Cache" 论断）
  - 2026-04-27 周一的状态 / 早餐 / 出门采购 / 半个菠萝 / 盒马烤鹅 / 任何今天的真实片段
  - 灵感来源 / 致谢 / 还想说的（你定调）
  
  你已经写过 v2.5 "他在我兴奋时踩刹车" / v2.6 "踏踏实实 + 闪电战"
  v2.7 的关键词可能是: "持久战" / "工程敬畏" / "9 个 bug 全本地修" / 别的
  
  这部分留给你 — 我框架在这里 ✊
  ───────────────────────────────────────────────────────── -->

(待 qc 自填)

---

## 编辑记录

```
2026-04-27  v2.7.0 release 后创建 (commit 432 之后)
            按 v2.6.0 SNAPSHOT 风格 (事件复盘 + 代码片段 + 设计决策回放)
            扩展 Claude 角色章节 (v2.5/v2.6 续篇)
            个人记录章节占位 (qc 自填)
```
