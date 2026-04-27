# SNAPSHOT — KubePivot v2.7.0

> 当前状态精确快照
> 编写日期：2026-04-27
> Last commit: 69b6a0e (chore: release v2.7.0)
> Last tag: v2.7.0
> Total commits: 431

---

## 一、版本与 commit

```
当前 branch:        Master
当前 commit:        69b6a0e
last tagged:        v2.7.0
upstream:           https://github.com/Ixecd/KubePivot
total commits:      431
```

**v2.7.0 核心 commit 链**：

| Commit | 内容 |
|--------|------|
| `36c2e7f` (feature) | bench: v2.7 Day 1 client-go vs KubePivot 对比基准 |
| `1553e9a` | docs: v2.7 Event Stream 性能基准决策文档 |
| `5c1b24d` | docs(design): v2.7 Event Stream Infrastructure 设计草案 |
| `8be0d41` | feat(eventstream): Day 2 — 包基础 (Cache + Skeleton + ParseSkeleton) |
| `075ecf8` | feat(eventstream): Day 3.2 — watch loop impl + 14 cases |
| `12b0966` | feat(eventstream): Day 4 — v2.5 sharding adapter |
| `5d7f1be` | docs(eventstream): impl-notes Day 1-4 实施日志 |
| `65219e8` | docs: introduce FUTURE.md — F1 CBA 种子 |
| `792c84a` | feat(eventstream): Day 5 — Prometheus metrics collector (9 指标) |
| `d874c0d` | feat(eventstream): Step 2a-1 — in-cluster auth + integration |
| `1bd2574` | feat(controller): Step 2a-2 — informer pool 接入 controller |
| `59c72b1` | feat(controller): Step 2b-1 — InformerDetector with kubectl fallback |
| `d04bf72` | feat(controller): Step 2b-2 — LabelGetter fast path |
| `4f6df60` | feat(metrics): Step 3 — MetricsClient 业务指标层 |
| `7599d61` (feature) | bench: Bench 3 — Watch 稳态吞吐对比 (1.65-2.97x + 5.4x allocs) |
| `49fe8b1` | docs(eventstream-perf): Step 4 — Bench 3 数据公开 |
| `5db630f` | docs(changelog): v2.7.0 章节 |
| `69b6a0e` | **chore: release v2.7.0** ← 第 431 commit + tag ✨ |

**节奏巧合**：

```
v2.0.0  → commit 329  → 生日 3.29        (2026-03-29)
v2.6.0  → commit 404  → 蓝绿 Found       (2026-04-26)
v2.7.0  → commit 431  → Event Stream    (2026-04-27)
```

诚实标注：v2.7.0 commit 落点 (431) 与日期 (4/27) 未刻意对齐。
工程纪律 > 数字仪式感（参 v2.7 工程教训第 4 条）。
但 4月27日 tag v2.7.0 仍是项目史诗的连续性记号。

---

## 二、代码结构

```
KubePivot/
├── cmd/kp/                        kp CLI 入口（v2.7 不动）
│   ├── main.go
│   ├── deploy.go
│   ├── sandbox.go                 v2.6 蓝绿流量切换接入点
│   ├── controller.go
│   ├── promote.go                 v2.0 命令式蓝绿
│   ├── bluegreen.go               v2.0 命令式蓝绿
│   ├── release.go
│   └── ... 其他命令
│
├── internal/
│   ├── eventstream/               ← v2.7.0 新增独立包 (~3700 行 / 89.0% 覆盖)
│   │   ├── doc.go                 包文档
│   │   ├── resource.go            Resource / Skeleton / SkeletonChanged
│   │   ├── cache.go               SkeletonCache (lock-free 读 + immutable snapshot)
│   │   ├── cache_policy.go        三层分层判定（双驱动并集）
│   │   ├── serializer.go          ParseSkeleton (增量序列化)
│   │   ├── informer.go            Informer 接口 + InformerOptions
│   │   ├── informer_impl.go       watch loop + list 分页 + 重连
│   │   ├── reconnect.go           ReconnectPolicy + jitter=0.2
│   │   ├── resync.go              按资源差异化 (10/30/60/120 min)
│   │   ├── auth.go                in-cluster auth (TLS 1.2+ / Bearer / Clone)
│   │   ├── adapter_sharding.go    v2.5 sharding 桥接
│   │   ├── metrics.go             Prometheus collector (9 指标)
│   │   └── *_test.go              ~2200 行测试 / 230+ cases
│   │
│   ├── metrics/                   ← v2.7.0 新增独立包 (~1119 行 / 81.2% 覆盖)
│   │   ├── doc.go                 包文档
│   │   ├── errors.go              ErrNotFound
│   │   ├── client.go              MetricsClient 接口 + Pod/Node/Container 数据结构
│   │   ├── quantity.go            Quantity 自解析 (CPU milli-cores / Memory bytes)
│   │   ├── kubectl.go             KubectlMetricsClient (kubectl top -o json)
│   │   ├── quantity_test.go       重头戏 (35+ sub-cases)
│   │   └── kubectl_test.go        client 测试
│   │
│   ├── controller/                Controller 主体（v2.7 加 ~600 行）
│   │   ├── global.go              ← v2.7 加 informer pool 启动 + handleTask 改造
│   │   ├── informer_pool.go       ← v2.7 新增 (双保险渐进引入)
│   │   ├── informer_detector.go   ← v2.7 新增 (Detector + LabelGetter)
│   │   ├── informer_pool_test.go      v2.7 新增 (10 cases)
│   │   ├── informer_detector_test.go  v2.7 新增 (15 cases)
│   │   ├── heal.go                ← v2.7 加 LabelGetter fast path (6 行)
│   │   ├── resources.go           v2.6 traffic 字段（v2.7 不动）
│   │   ├── reconciler.go          既有路径完全保留
│   │   ├── watcher.go             KubectlWatcher 完全保留 (双保险底层)
│   │   ├── lease.go / sweeper.go  v2.5
│   │   └── ...
│   │
│   ├── route/                     v2.6.0 流量层（v2.7 不动）
│   │   ├── provider.go / errors.go
│   │   ├── ingress_provider.go
│   │   ├── gateway_provider.go
│   │   ├── auto_detect.go
│   │   └── *_test.go
│   │
│   ├── sharding/                  v2.5.0 分片机制（v2.7 通过 adapter 接入）
│   │   ├── shard.go / multi_lease.go / lease_helpers.go
│   │   └── *_test.go
│   │
│   ├── code/                      错误码体系（v2.7 不动）
│   ├── controller_installer/      Controller 部署模板
│   ├── state/                     状态机
│   ├── planner/                   部署计划
│   ├── scaffold/                  kp init 脚手架
│   ├── executor/                  kubectl 执行器（KpExecutor 单例）
│   ├── bluegreen/                 v2.0 命令式蓝绿
│   ├── ai/ / logger/
│   └── ...
│
├── benchmark/                     ← feature/client-go-comparison 分支独有
│   └── eventstream/               5 项 benchmark
│       ├── go.mod                 独立 module (含 client-go v0.32.0)
│       ├── testdata.go            generateComplexDeployment
│       ├── cache_get_test.go              Bench 1
│       ├── cache_list_test.go             Bench 2 + 2b
│       ├── watch_throughput_test.go       ← v2.7 Step 4 新增 (Bench 3)
│       ├── cold_start_test.go             Bench 4
│       ├── memory_amplification_test.go   Bench 5 ⭐
│       ├── run-benchmarks.sh
│       └── README.md
│
├── docs/design/                   设计文档
│   ├── architecture.md            整体架构 (v2.6 加第 9 条)
│   ├── controller.md
│   ├── state-machine.md           v2.6 协同章节
│   ├── sharding.md / sharding-tuning.md
│   ├── traffic-layer.md           v2.6.0
│   ├── eventstream-draft.md       ← v2.7.0 新增 (~1290 行 / 14 个 Q 拍板)
│   ├── eventstream-perf.md        ← v2.7.0 新增 (~452 行 / 5 项 benchmark 数据)
│   ├── eventstream-impl-notes.md  ← v2.7.0 新增 (~620 行 / Day 1-5 实施日志)
│   ├── decision-stack.md          三层资源决策栈
│   └── performance.md             v2.3/v2.4/v2.5 累计数据
│
├── docs/example-blue-green/       v2.6 demo 工程
│
├── commits/                       工程档案 (~50 个，v2.7 新增 ~17 个)
│   ├── v2.7-eventstream-draft.txt
│   ├── v2.7-eventstream-perf-decision.txt
│   ├── v2.7-step1-day2-pkg-base.txt
│   ├── v2.7-step1-day2-policy.txt
│   ├── v2.7-step1-day3-1-interface.txt
│   ├── v2.7-step1-day3-2-watch-loop.txt
│   ├── v2.7-step1-day4-sharding-adapter.txt
│   ├── v2.7-step1-day5-impl-notes.txt
│   ├── v2.7-future-md-cba.txt
│   ├── v2.7-step1-day5-metrics.txt
│   ├── v2.7-step1-day5-impl-notes-update.txt
│   ├── v2.7-step2a1-incluster-auth.txt
│   ├── v2.7-step2a2-informer-pool.txt
│   ├── v2.7-step2b1-informer-detector.txt
│   ├── v2.7-step2b2-labelgetter.txt
│   ├── v2.7-step3-metrics-client.txt
│   ├── v2.7-step4-bench3-data.txt
│   ├── v2.7-step5a-changelog.txt
│   └── v2.7-v3.0-roadmap-batch1.txt / -complete.txt
│
├── snapshots/                     永久归档
│   ├── ... (历史)
│   └── SNAPSHOT-kubepivot-2026-04-27-v2.7.0.md  ← 今天加
│
├── archived/                      历史归档 (v2.6.x 旧文档归这里)
├── HANDOFF.md                     接手指南（v2.7 视角）
├── SNAPSHOT.md                    本文件（v2.7 视角）
├── TODO.md                        路线图（v2.7 视角）
├── CHANGELOG.md                   变更日志（v2.7.0 章节已加）
├── FUTURE.md                      ← v2.7.0 新增 (远景探索 / F1 CBA)
├── ROADMAP.md                     ← v2.7.0 新增 (~2143 行 / v2.7→v3.0)
├── README.md
├── GITOPS-MANIFESTO.md
└── ...
```

---

## 三、测试状态

```
make dev:
  ok  github.com/Ixecd/kubepivot/cmd/kp                      cached
  ok  github.com/Ixecd/kubepivot/internal/controller         cached  (含 v2.7 informer 接入测试)
  ok  github.com/Ixecd/kubepivot/internal/eventstream        cached  (89.0% 覆盖, 230+ cases)
  ok  github.com/Ixecd/kubepivot/internal/metrics            1.226s  (81.2% 覆盖, 14 cases / 60+ sub)
  ok  github.com/Ixecd/kubepivot/internal/route              cached  (27 sub-cases, v2.6)
  ok  github.com/Ixecd/kubepivot/internal/sharding           cached
  ok  github.com/Ixecd/kubepivot/internal/state              cached
  ok  github.com/Ixecd/kubepivot/internal/planner            cached
  ok  github.com/Ixecd/kubepivot/internal/scaffold           cached
  ok  github.com/Ixecd/kubepivot/test/integration            cached
```

**v2.7.0 新增测试矩阵**：

```
internal/eventstream/ 230+ cases / 89.0% 覆盖:
  resource_test.go            14 cases (SkeletonChanged 等)
  cache_test.go               20 cases (含并发 race)
  serializer_test.go          12 cases
  cache_policy_test.go        13 cases (双驱动并集 5 sub)
  informer_test.go             6 cases (类型契约)
  reconnect_test.go            9 cases (退避序列 + jitter)
  resync_test.go              12 cases (4 频率组)
  informer_impl_test.go       14 cases (含 fake K8s server)
  adapter_sharding_test.go    12 cases (v2.5 联动)
  metrics_test.go             16 cases (Prometheus 9 指标)
  auth_test.go                20 cases (TLS / Bearer / Clone / Token 安全)
  informer_pool_test.go       10 cases (双保险路径)
  informer_detector_test.go   15 cases (Detector + LabelGetter)

internal/metrics/ 14 cases / 60+ sub-cases / 81.2% 覆盖:
  parseCPU                    18 sub-cases
  parseMemory                 17 sub-cases (SI vs 二进制)
  splitBaseUnit                9 sub-cases
  Quantity 行为                4 cases
  KubectlMetricsClient         7 cases (kubectl func 注入 mock)
  Helper                       1 case
```

集成验证（既有 demo + 新引入路径）：

```
✓ docs/example-blue-green/ demo 工程跑通 (v2.6 既有)
✓ controller 既有 75 个测试 0 改动 (v2.7 双保险设计验证)
✓ Race detector 全绿
✓ 9 个 bug 全本地修复（Master 历史 0 fix commit）

未在真实 K8s 集群验证 in-cluster auth：
  需 controller pod 滚出 v2.7.0 镜像（v2.7.x 计划，含 HTTP server）
  当前 informer pool 在 controller 内已启动，但 metrics 未注册
```

---

## 四、性能数据快照

完整数据见 `docs/design/eventstream-perf.md`。

**v2.7.0 vs client-go cache.Indexer**：

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

**关键洞察（Allocs 维度）**：

```
client-go alloc/event = 517 个对象  (反序列化整个 *Deployment)
KubePivot alloc/event = 96 个对象   (ParseSkeleton 仅关键字段)
                                    → 5.4x 减少 ⭐

这是架构差异，不是优化能解决的:
  client-go 设计目标: 通用 K8s 操作 (必须完整反序列化)
  KubePivot 设计目标: reconcile 决策 (仅需关键字段)
  Bench 5 (内存放大率) + Bench 3 (allocs) 互证同一根因
```

**ROADMAP §5 验收**：

```
✓ "全量重同步耗时 < 5s"        实测 36ms / 382ms (1k / 10k events)
✓ "vs client-go 对比"          已公开 (5 项 benchmark 数据)
✓ "Cache 读延迟 < 50ns"        实测 14.5ns
✓ "单 Pod 内存降低 ≥ 20%"      实测 65% 降低 (1.65x vs 4.69x)
```

继承自 v2.5.0 的 controller 性能（v2.7 不动）：

```
                     v2.5.0 (10p)   v2.5.0 (50p)
集群总 CPU            31.24%         101.40%
avg CPU / pod         10.41%          33.80%
peak CPU              87.85%          82.27%
故障转移              ~10 sec         ~10 sec
```

---

## 五、当前运行的 controller

```
Namespace:    kubepivot-system
Image:        qingchun22/kubepivot-controller:v2.5.0  (待 v2.7.x build 含 HTTP server)
Replicas:     3
Pod 状态:     全部 Running

Lease:
  kubepivot-controller-leader     1 个，identity = pod-suffix-hex
  kubepivot-controller-shard-0..9 10 个，分布在 3 个 pod 上

注：v2.7.0 是"代码 release"，不是"镜像 release"
   - InformerPool 在 controller 内已启动（fail soft 兜底）
   - InformerDetector 双保险接入 reconcile / heal
   - 但 controller HTTP server + /metrics endpoint 未启用
   - 生产 pod 行为对外完全等价 v2.6.0
   - v2.7.x 镜像 build 后才暴露 9 项 Prometheus 指标
```

---

## 六、运行环境

```
开发硬件:    Apple Silicon (M4), 16 GB RAM
集群:        orbstack K8s（单节点，Pod 容量 110）
Storage:     local-path (rancher.io/local-path)
Network:     allow-all（开发环境）

CI/CD:       未配置（v2.7 仍未上）

性能立方体测试受限于环境（v2.5.1 任务，仍受阻）：
  P_max ≈ 50 在 8 GiB orbstack 配置下
  需升 16 GiB 或多节点环境才能测 P > 50

Benchmark 环境（v2.7 Bench 1-5）：
  GOGC=200 / GOMEMLIMIT=4GiB / GOMAXPROCS=4 (公平基准约定)
  GOMAXPROCS=10 (Bench 3 实测，Apple M4 全核)
```

---

## 七、未提交的本地状态

```
git status (release 后):

  ✓ working tree clean (commit 432 三文档大更新待提交)

最后一组改动（已合入 v2.7.0 release）：
  ✓ docs(eventstream-perf): Step 4 — Bench 3 数据公开（commit 49fe8b1）
  ✓ docs(changelog): v2.7.0 章节（commit 5db630f）
  ✓ chore: release v2.7.0（commit 69b6a0e，自动产生 + tag）

晚间文档三件套（即将提交为 commit 432）：
  M HANDOFF.md            v2.7.0 视角重写
  M TODO.md               v2.7.0 视角重写
  M SNAPSHOT.md           本文件（v2.7.0 视角重写）
  ?? snapshots/SNAPSHOT-kubepivot-2026-04-27-v2.7.0.md  永久归档
  ?? archived/handoff/HANDOFF-v2.6.md   归档前一版
  ?? archived/todo/TODO-v2.6.md         归档前一版
  ?? archived/snapshot/SNAPSHOT-v2.6.md 归档前一版
```

---

## 八、关键 metrics（v2.7.0 起追踪）

```
代码行数（仅 Go 源文件）：
  internal/eventstream/     ~3700 行（v2.7.0 新增独立包）
  internal/metrics/         ~1119 行（v2.7.0 新增独立包）
  internal/controller/      ~3700 + 600 行（v2.7 接入 informer pool/detector）
  internal/sharding/        ~700 行
  internal/route/           ~990 行（v2.6.0）
  internal/state/           ~1200 行
  其他 internal/            ~2500 行
  cmd/kp/                   ~1700 行
  ─────────────────────────
  总计                      ~16200 行 (vs v2.6.0 ~10800 行)

测试覆盖：
  internal/eventstream/     89.0%（v2.7.0 新增）
  internal/metrics/         81.2%（v2.7.0 新增）
  internal/controller/      ~70%（既有维持，v2.7 新增 25 cases 不影响既有 75 测试）
  internal/sharding/        ~85%
  internal/route/           ~85%
  集成测试                  保持

文档行数：
  docs/design/              ~6000+ 行（含 eventstream 三件套 ~2362 行）
  docs/example-blue-green/  ~600 行
  HANDOFF + SNAPSHOT + TODO ~1500+ 行（v2.7 视角重写）
  CHANGELOG.md              ~250 行（v2.7.0 章节 ~115 行）
  FUTURE.md                 ~256 行（v2.7.0 新增）
  ROADMAP.md                ~2143 行（v2.7.0 新增）
  snapshots/ 永久归档       ~2200 行（v2.7.0 主篇 ~330 行）

commits/ 目录                ~50 个 commit message 草稿（v2.7 新增 ~17 个）

Test cases 累计：             480+ cases（v2.7 新增 ~250 cases）
覆盖率（含新增）：             eventstream 89.0% / metrics 81.2%
```

---

## 相关文档

- [README.md](README.md) — 入门
- [HANDOFF.md](HANDOFF.md) — 接手指南（含 3.7 节累计踩坑，v2.7 加 7 条新教训）
- [TODO.md](TODO.md) — 路线图（v2.7.1 候选 / v2.8 / v2.9 / v3.0）
- [CHANGELOG.md](CHANGELOG.md) — 变更日志（v2.7.0 章节）
- [FUTURE.md](FUTURE.md) — 远景探索（F1 CBA 种子）
- [ROADMAP.md](ROADMAP.md) — v2.7→v3.0 完整路线图（~2143 行）
- [GITOPS-MANIFESTO.md](GITOPS-MANIFESTO.md) — GitOps 哲学
- [docs/design/eventstream-draft.md](docs/design/eventstream-draft.md) — v2.7.0 Event Stream 设计
- [docs/design/eventstream-perf.md](docs/design/eventstream-perf.md) — 5 项 benchmark 数据
- [docs/design/eventstream-impl-notes.md](docs/design/eventstream-impl-notes.md) — Day 1-5 实施日志
- [docs/design/traffic-layer.md](docs/design/traffic-layer.md) — v2.6.0 流量层
- [docs/design/sharding.md](docs/design/sharding.md) — v2.5.0 分片设计
- [docs/design/performance.md](docs/design/performance.md) — 性能基准（controller 部分）
- [snapshots/SNAPSHOT-kubepivot-2026-04-27-v2.7.0.md](snapshots/SNAPSHOT-kubepivot-2026-04-27-v2.7.0.md) — v2.7.0 事件复盘
