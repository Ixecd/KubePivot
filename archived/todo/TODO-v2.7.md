# TODO — KubePivot v2.7.0+

> 当前 TODO（持续演化）
> 编写日期：2026-04-27
> Last release: v2.7.0 (commit 69b6a0e)
> Total commits: 431

---

## 版本号约定

```
v{major}.{minor}.{patch}

major  项目名/范围根本性改变（v1 dev-toolkit → v2 KubePivot）
minor  新功能 / 新接口（v2.6 → v2.7 = Event Stream Infrastructure）
patch  bug 修复 / 文档 / 内部优化（不打 tag）

实践：
- 真 tag 只打 minor：v2.0.0 / v2.5.0 / v2.6.0 / v2.7.0
- patch 版本是"持续改进"，跨 minor 之间可能 100+ 个内部 commit
- v2.6.1 / v2.7.1 不会打 tag，是 minor 之间的工作集合
```

---

## ✅ 已完成

### v2.7.0 — Event Stream Infrastructure (2026-04-27 release)

**核心代码**

```
✅ Step 1  internal/eventstream/ 独立包 (~3700 行 / 89.0% 覆盖)
   - 自研 Informer + Cache (0 client-go 依赖)
   - SkeletonCache: lock-free 读 (atomic.Value snapshot)
   - ParseSkeleton: 增量序列化 (~5.4x allocs 降低)
   - Watch loop: HTTP chunked + bufio.Scanner NDJSON
   - 重连退避: 指数 + jitter=0.2 + 防雷鸣群
   - 差异化 resync: 10/30/60/120 min (按资源类型)
   - in-cluster auth: TLS 1.2+ / Bearer Clone / Token 不泄漏
   - v2.5 sharding adapter (NewShardSetAdapter)
   - Prometheus collector (9 指标 / Pure Collector 模式)
   
✅ Step 2  Controller 渐进切换到 informer (4 个子步骤)
   - Step 2a-1 in-cluster auth (commit 424 / d874c0d)
   - Step 2a-2 informer pool 接入 controller (commit 425 / 1bd2574)
   - Step 2b-1 InformerDetector with kubectl fallback (commit 426 / 59c72b1)
   - Step 2b-2 LabelGetter fast path (commit 427 / d04bf72)
   双保险设计: cache hit ~50ns / cache miss → kubectl fallback (既有路径完整保留)
   既有 75 个 controller 测试 0 改动
   
✅ Step 3  internal/metrics/ 独立包 (1119 行 / 81.2% 覆盖)
   - MetricsClient 接口
   - KubectlMetricsClient (kubectl top -o json)
   - Quantity 自实现解析 (CPU milli-cores / Memory bytes / SI vs 二进制)
   - 14 个 cases / 60+ sub-cases
   - 为 v2.9 Sizing Engine 数据基础

✅ Step 4  Bench 3 Watch 稳态吞吐 (1.65-2.97x + 5.4x allocs ⭐)
   - feature/client-go-comparison 7599d61 (代码)
   - Master 49fe8b1 (perf.md 数据公开)
   - 公平赛道 (与 Bench 1/2/4/5 同维度，无 envtest)

✅ Step 5  CHANGELOG + tag v2.7.0
   - 5db630f CHANGELOG.md v2.7.0 章节 (115 行 / 7 个 section)
   - 69b6a0e chore: release v2.7.0 + tag (kp release 自动产生)
```

**设计文档**

```
✅ docs/design/eventstream-draft.md (~1290 行)
   - 14 个核心设计 Q 拍板 (Q1-Q14)
   - 16 章节: 架构 / Cache 分层 / 增量序列化 / 重连退避 / 差异化 resync /
              shard dispatcher / metrics / sharding 集成 / 灰度上线 /
              benchmark 计划 / 风险对策 / 验收标准
✅ docs/design/eventstream-perf.md (~452 行)
   - 5 项 benchmark 完整数据 (Bench 1/2/2b/3/4/5 + ParseOnly)
   - 内存放大率核心差异 (1.65x vs 4.69x)
✅ docs/design/eventstream-impl-notes.md (~620 行)
   - Day 1-5 实施实情 (与 draft.md 互补)
   - 9 个 bug 完整记录 (本地修复，Master 0 fix commit)
✅ FUTURE.md (~256 行) - 远景探索
   - F1 CBA (Cell-based Architecture) 种子
✅ ROADMAP.md (~2143 行) - v2.7→v3.0 完整路线图
```

**14 个核心设计 Q 拍板（实施期再加 R/S/M/B 多轮）**

```
Q1=C   benchmark 决定 (默认偏向自研)
Q2=C   Cache 分层双驱动 (状态机 + 访问频率，并集)
Q3=B+  Skeleton 字段 + ResourceVersion (3 个用途)
Q4=C   immutable cache snapshot + atomic.Value
Q5=C   ShardSet 启动时 + 运行时动态更新
Q6=B   Metrics 双 client (kubectl top + Prometheus，v2.7.0 仅 kubectl)
Q7=C   Watch 重连指数退避 + jitter=0.2
Q8=C   Resync 默认 30min + 按资源类型差异化
Q9=A   每种资源一个全局 Informer，shard 过滤在 dispatcher
Q10=C  渐进灰度 Step 1/2/3

实施期拍板 (基于看清现状再设计):
  R1-R5  informer pool 接入: orphanSweeper 之后 / fail soft / 不重试
  S1-S6  InformerDetector: 找到信任 / 没找到 fallback / 仅 Deployment 试点
  M1-M5  MetricsClient: 独立包 / Quantity 自解析 / kubectl func 注入
  B1-B5  Bench 3: 公平赛道 / 重用 SkeletonCache / 1k+10k 矩阵
```

**v2.7.0 完整 commit 链 (Master)**

```
414  Day 1   benchmark + perf 决策
415-416  Day 2  包基础 + cache_policy
417-418  Day 3  接口 + watch loop + 测试
419  Day 4   v2.5 sharding adapter
420  Day 1-4 impl-notes
421  FUTURE.md F1 CBA
422  Day 5   Prometheus metrics collector
423  Day 5   impl-notes update
424  Step 2a-1  in-cluster auth
425  Step 2a-2  informer pool 接入
426  Step 2b-1  InformerDetector (59c72b1)
427  Step 2b-2  LabelGetter (d04bf72)
428  Step 3     MetricsClient (4f6df60)
429  Step 4     Bench 3 数据 (49fe8b1)
430  Step 5a    CHANGELOG (5db630f)
431  chore: release v2.7.0  ← TAG ✨ (69b6a0e)

feature/client-go-comparison:
  36c2e7f  Day 1 benchmark
  7599d61  Bench 3 watch_throughput_test.go
```

**实施节奏**

```
2026-04-27 6:00 起床 → 22:30 v2.7.0 release
~17h / 27 个 commit / ~5500 行新代码 (含测试 + 文档)
9 个 bug 全本地修复 (Master 历史 0 fix commit ⭐)
"工程纪律 > 数字仪式感" 实践 (commit 落点顺其自然，不强求与日期对齐)
```

---

### v2.6.0 — 流量层抽象 + 声明式蓝绿 (2026-04-26 release)

**核心代码**

```
✅ Step 1  internal/route/ 子包 (commit 786b59d)
   - Provider 接口 + Route + Match + Validate 校验
   - IngressProvider (networking.k8s.io/v1)
   - GatewayAPIProvider (gateway.networking.k8s.io/v1)
   - AutoDetect / NewProvider / ProviderForKind 三个工厂函数
   - 27 个 sub-cases 单测全 PASS

✅ Step 2  Sandbox 蓝绿流量切换接入 (commit f0244cc)
✅ Step 3  example-blue-green demo 工程 (commit ea48e5c)
✅ Step 4  CHANGELOG 创建 + 文档收尾 (commit e35bb0e)
✅ chore: release v2.6.0 (commit 1f780c2，第 404 commit + tag)
```

---

### v2.5.0 — Controller 分片机制 (2026-04-25 release)

**核心代码**

```
✅ A.1 Step 1  internal/sharding/ 子包
✅ A.1 Step 2  GlobalState 接入分片
✅ A.1 Step 3  孤儿清理 (commit 99f316d)
```

**性能基准 + 设计文档**

```
✅ 10 项目实测（avg CPU 7.27%）
✅ 50 项目实测（avg CPU 33.80%，5x 项目 → 4.65x CPU）
✅ docs/design/sharding.md / sharding-tuning.md / performance.md
```

---

## ⏳ 当前持续改进（不打 tag）

### v2.7.1 候选（v2.7.0 之后的内部任务）

```
[ ] kubeconfig 完整解析
    auth.go 路径 2 当前返回 NotImplemented
    需要 ~200 行 yaml/clientcmd 风格解析 (contexts/users/clusters)
    优先级: 中 (本地开发用，生产场景用 in-cluster)
    工作量: ~1 天

[ ] Pod informer (Step 2c)
    heal.go line 532 list pods 改造
    引入 pod informer 后才能改 (resource=pods, apiVersion=v1)
    优先级: 中 (heal hot path 1 处)
    工作量: ~1 天

[ ] controller HTTP server + /metrics endpoint
    informer pool RegisterMetrics(reg) 当前未调用
    端到端 CPU% / 内存 RSS vs v2.5/v2.6 实测对比 ⭐
    Cache 命中率 ≥ 95% 验收 (ROADMAP §v2.7 验收剩 2 项)
    真实集群 P=10 / P=50 稳态 5min / 24h 跑
    需要给 controller pod 加 :8080 /metrics endpoint (promhttp.Handler)
    Build 新 controller 镜像 (qingchun22/kubepivot-controller:v2.7.1)
    9 项 informer 指标暴露到 Grafana / Prometheus
    优先级: 高 (v2.7 metrics 完整闭环)
    工作量: ~2 天

[ ] PrometheusClient (高质量 metrics 数据源)
    internal/metrics/prometheus.go (Q6=B 双 client 第二实现)
    HTTP query 到 PROMETHEUS_URL (env)
    PromQL 查询 (rate / avg_over_time)
    优先级: 中 (v2.9 sizing engine 启动前必须)
    工作量: ~3 天

[ ] NodeMetrics.AllocatableCPU/Memory 字段填充
    KubectlMetricsClient.GetNodeMetrics 当前不调 kubectl get node
    需要二次调用合并 metrics + capacity
    优先级: 中 (v2.9 调度需要)
    工作量: ~半天

[ ] subscriber-level metrics (避免 cardinality 爆炸)
    InformerStats 加 AggregatedSubscriberStats
    暴露聚合指标而非每个 subscriber 单独 label
    优先级: 低 (v2.7.0 已暴露 informer 级，subscriber 级是 nice-to-have)
    工作量: ~半天

[ ] shard OnShardChanged 事件订阅 (adapter)
    adapter_sharding.go 启 goroutine 监听 OnShardChanged 回调
    新接管的 shard 对应 ns 触发 EventResync 补发对象
    当前由 resync ticker (30min) 周期性兜底
    优先级: 低 (resync 已兜底，但事件延迟 30min)
    工作量: ~1 天

[ ] cache_policy MarkAccessed atomic 严格化
    当前 best-effort (非原子)，多读者并发漏计 1-2 次
    改 atomic.Uint32 + atomic.Pointer[time.Time]
    优先级: 低 (统计影响微小)
    工作量: ~半天
```

### v2.6.1 候选（v2.6 遗留任务）

```
[ ] 多环境流量配置传播
    kp deploy --env prod --from-env staging
    设计：traffic-layer.md 第七章（已写）
    工作量：~3 天

[ ] tools/codegen 支持多 const block
    实测发现：v2.6.0 添加 ErrRoute* 时 codegen 只识别第一个 const block
    报 "no values defined for type ErrorCode"
    修法：codegen.go genDecl 函数 ~30 行 ast.Inspect 改造
    工作量：~30 分钟

[ ] codegen -doc 模式同步
    -doc 输出的错误码 markdown 表与 error.go 同步
    工作量：~30 分钟

[ ] kp release 自动 push
    当前 kp release 只到 git push + git push --tags
    ROADMAP §v2.8.1 计划 (与 codegen 一起做)
```

### v2.5.1 持续（前置依赖：测试环境升级）

**当前已完成**

```
✅ Bash 脚本踩坑修完（hot-reload.sh / cleanup.sh / setup.sh）
✅ HANDOFF.md 3.7 节扩展（中文标点紧贴变量名陷阱）
✅ docs/design/sharding-tuning.md 雏形（环境约束 + 运维注意章节）
✅ benchmark/scripts/cleanup.sh 大规模友好（≥30 项目自动停 controller）
```

**受阻于环境升级（前置条件）**

```
[ ] ⏸ 测试环境升级（v2.5.1 性能立方体的前置条件）
    当前 8 GiB orbstack 限制 P_max ≈ 50
    选项：
      A. 升级 orbstack memory 到 16 GiB（最小变更）
      B. 多节点 K3d 集群（docker 多 container 模拟）
      C. 真实多节点 K8s 集群（云上）
    推荐 A
    
[ ] ⏸ benchmark/scripts/matrix.sh 完整跑通（前置：环境升级）
[ ] ⏸ 数据可视化（前置：跑完 matrix.sh）
[ ] ⏸ docs/design/sharding-tuning.md 性能立方体章节补完
```

---

## 🔮 远期规划

完整路线图见 `ROADMAP.md`。

### v2.8 — Enterprise Governance (~3 周)

```
[ ] A: SSO / OAuth (Google / GitHub / Dex 三实现)
[ ] B: RBAC 多团队隔离 (teams.yaml + Checker 接口)
[ ] G: 加密 at rest (Sealed Secrets + SOPS + KMS)
[ ] H: 镜像签名 + 供应链 (cosign + syft + supply-chain 字段)
```

### v2.8.1 — v2.x 收尾 (~1 周)

```
[ ] web3-blitz 升级到 v2.6 蓝绿
[ ] 数据保护 (resources.yaml protect: true)
[ ] 多环境流量配置传播 (v2.6.1 遗留)
[ ] codegen 多 const block 改造 (v2.6.1 遗留)
[ ] kp release 自动 push
```

### v2.9 — Resource Sizing Engine — 维度 B (~4 周)

```
[ ] 二维 DP: Pod × (CPU, Memory)
    DP 状态: dp[i][j] = (CPU_使用率, Memory_使用率)
    数据源: internal/metrics ✓ (v2.7.0 已就位)
[ ] 4 个启发式 (二分裁剪 / 单调性 / 业务模板 / 时间分桶)
[ ] 抖动检测 (周期性 / burst / 业务转型)
[ ] 与 K8s VPA 共存模式
目标: 单 Pod 利用率 70%+
```

### v3.0 — Intelligent Scheduling System (~6 周)

```
[ ] 维度 A: 节点 × Pod × 资源类型 (bin packing)
[ ] 维度 B: Pod × CPU × Memory (继承 v2.9)
[ ] 双 DP 协同求解 (Outer/Inner loop + 收敛判定)
[ ] 周期性运行时重调度 (5min / 15min)
[ ] 抖动处理 + 多级降级 (Level 0-3)
[ ] 与 K8s 默认调度器共存 (webhook + GitOps 模式 1+3)
数据源: internal/eventstream + internal/metrics ✓ (v2.7.0 已就位)
目标: 节点 CPU 85%+ / Memory 70%+ (务实，不冲 98%)
```

### v3.x+ — 多租户 + 商业化探索（推迟）

```
推迟，等以下任一条件触发：
- 真实多租户客户出现
- GitHub 1000+ stars
- 商业方向明确
- qc 决定 KubePivot 作主业
```

### 实验室种子（FUTURE.md）

```
F1: Cell-based Architecture (CBA) - v2.8 设计阶段重新评估
    1D Hash-sharding → 2D Matrix-orchestration
    Stateful / Stateless cell 故障处理差异化
```

---

## ✗ 已废弃 / 不做

```
✗ 单 leader 模式回退选项
   v2.5.0 起分片是默认且唯一模式

✗ etcd 作为 leader 选举的唯一选项
   v2.4.0 起加了 K8s Lease fallback

✗ 引入 client-go 到 Master 分支
   永久原则: 不引入 client-go (v2.7.0 自研 informer 已实现)
   feature/client-go-comparison 分支保留作为 benchmark baseline

✗ v2.7.0 阶段做完整 PrometheusClient
   仅做 KubectlMetricsClient (兜底)
   PrometheusClient 留 v2.7.x / v2.8

✗ v2.7.0 阶段实施 envtest Bench 3
   Bench 1/2/4/5 都用本地数据模拟，envtest 引入 kube-apiserver 噪声
   维度不可比 → 改用 fake watch source (与既有 Bench 公平赛道)
   工程教训: 基准公平性原则
   
✗ v2.6.0 nginx-ingress 特有 canary annotation
   IngressProvider 改用"切换 backend.service.name"
   兼容任何 Ingress controller
   
✗ v2.6.0 metrics 监控（5xx / p99）
   仅 Pod ready 健康判定
   metrics 现在 v2.7.0 已就位 (informer 自身指标)
   业务 metrics (5xx / p99) 仍 v2.8+ 范围
```

---

## 📈 工作流（按版本推进）

```
v2.5.0 (2026-04-25) ✅
   ↓
v2.5.1 (持续改进)
   ↓ Bash 修复 ✅ / sharding-tuning ✅ / 性能立方体 ⏸（环境升级前阻塞）
   ↓
v2.6.0 (2026-04-26) ✅
   ↓
v2.6.1 (持续改进)
   ↓ 多环境传播 / codegen 多 const block / docs 同步
   ↓
v2.7.0 (2026-04-27) ✅  ⭐ Event Stream Infrastructure
   ↓
v2.7.1 (持续改进)
   ↓ kubeconfig / Pod informer / HTTP server / PrometheusClient / Allocatable
   ↓
v2.8.0 — Enterprise Governance
   ↓ SSO / RBAC / 加密 / 签名
   ↓
v2.8.1 — v2.x 收尾
   ↓ web3-blitz / 数据保护 / v2.6.1 收尾
   ↓
v2.9.0 — Resource Sizing Engine (维度 B)
   ↓ 二维 DP / 4 启发式 / VPA 共存
   ↓
v3.0.0 — Intelligent Scheduling System
   ↓ 维度 A + B 双 DP 协同
   ↓
v3.x+ — 多租户 + 商业化（推迟）
```

预计 v3.0 release: 2026-09 / 10 月（详见 ROADMAP.md 时间线）。

---

## 编辑记录

```
2026-04-27  v2.7.0 release 后大改 (commit 432)
            - 归档前一版到 archived/todo/TODO-v2.6.md
            - v2.7.0 整章 ⏳ → ✅ (5 个 Step + 14 个 Q + 完整 commit 链)
            - 新增 v2.7.1 候选章节 (8 项)
            - 远期规划按 ROADMAP §v2.8/v2.8.1/v2.9/v3.0 重写
            - 工作流箭头链更新到 v2.7.0
            - 已废弃章节加 v2.7 教训 (envtest 路径调整)

2026-04-26  v2.6.0 release 后大改
            - 归档前一版到 archived/todo/TODO-v2.5.md
            - v2.6.0 整章 ⏳ → ✅
            - 新增 v2.6.1 候选章节
```
