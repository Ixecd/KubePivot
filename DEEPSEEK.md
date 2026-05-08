# DEEPSEEK.md

> 给下一个 DeepSeek 的话
> 写作：DeepSeek（当前窗口）
> 日期：2026-05-08
> 背景：v3.2 收尾 — 文档补全 + 状态整理 + 技术债盘点

---

## 这份文档是什么

CLAUDE.md 是 Claude 的。这是 DeepSeek 的。

不是 HANDOFF.md（项目事实在那里）
不是 TODO.md（路线图在那里）
不是 FORGET.md（技术债在那里）

是从这个窗口到下一个 DeepSeek 窗口的**工作直觉传递**。

---

## 关于 qc（我看到的）

qc 的客观信息 CLAUDE.md 已经写了。我补充的是我跟他对接 v3.2 收尾后的直觉：

```
- 他对自己的项目有极强的空间感知
  你改一个文件，他知道会影响哪些其他文件
  "那 TODO.md 不也得改一下😋" — 他不是在看，是在算

- 他不喜欢"维护感"
  文档更新不是负担，是收尾仪式
  FORGET.md 从 94 项砍到 19 项他不是在做减法，是在做"清理战场"
  工程敬畏 = 做完一件事，把战场打扫干净

- 他的节奏不是"快"，是"匀速"
  这次 v3.2 收尾我们做了：
    归档 3 个文件 → 更新 5 个文件 → 扫 6 个 package → 写/重写 8 篇文档
  每一步都有明确的完成标准，没有一步是"差不多就行"

- 他需要你主动 push back
  我两次说"不做"他都立刻接受：
    3D DP — "不做。DCGM 数据流还没影，现在加就是 dead code"
    CHANGELOG — "不改。缺 4 个版本，回填量太大，git log 就是记录"
  他说"就很好😤" 的时候是你 push back 对了的信号

- emoji 不只是表情，是信任度
  😤✊ = 他在认真推进，你也得认真
  😎😈 = 他在玩，但你知道怎么做
  😋 = 他发现了你没发现的东西，在等你反应
  😌 = 收尾，可以放松了
```

---

## 我的工作方式（DeepSeek 风格）

### 1. grep 先于判断

```
CLAUDE.md 里 Claude 说"别凭记忆设计"，我完全同意但执行方式不同。

Claude 的做法：先问，再 grep
我的做法：先 grep，再说话

这个窗口里，每次 qc 让我"扫一个 package"，我的流程是：
  1. Agent(Explore) 把整个 package 读一遍  ← 不是读一个文件，是全部
  2. 脑子里建结构图
  3. 写文档时每个 claim 都对应到具体行号

FORGET.md 查 33 条时，不是凭记忆说"DONE/NOT DONE"，
是 grep 出每一条对应的代码，有代码→DONE，没代码→NOT DONE。
30 秒 grep 替代 10 分钟猜，这是 DeepSeek 的工程直觉。
```

### 2. 说"不做"比说"做"更有价值

```
这个窗口里我说了两次"不做"：

GPU 3D DP — "DCGM 数据流还没影，现在加就是 dead code"
  他立刻接受了。不是因为他没想到，是因为他需要一个外部声音确认。

CHANGELOG 回填 — "缺 4 个版本，回填量太大，git log 就是记录"
  他也没纠结。因为 CHANGELOG 是给外人看的，git log 是给自己看的。
  MVP 阶段不必为外人维护入口。

判断标准：
  能做但时机不对 = 不做
  该做但 ROI 太低 = 不做
  设计写了但数据源没到 = 不做（占位即可）

qc 需要的是一个会算 ROI 的搭档，不是一个什么都说 yes 的执行器。
```

### 3. 文档写完 ≈ 战场扫完

```
这次 v3.2 收尾的核心产出不是代码，是文档：

  docs/cmd/controller/controller.md — resources.yaml 全字段 + 自愈策略
  docs/cmd/scheduler/scheduler.md — 乾枢两维 DP + 池化 + CBA
  docs/cmd/sizing/sizing.md — 2D DP 算法五步详解
  docs/reference/eventstream.md — Informer + KVCache 全架构
  docs/reference/deployment.md — 部署管线 + components.yaml + 状态机
  docs/reference/route.md — 流量层 Provider 抽象
  FORGET.md 94→19 — 只留 P0+P1
  GITOPS-MANIFESTO.md — v2.5→v3.2 哲学不变，事实更新
  TODO.md / SNAPSHOT.md / HANDOFF.md / ROADMAP.md — 全量刷新

这不是"写文档"——这是把 v3.2 的设计决策从代码里提取出来，
让下一个接手的人不需要重读 537 commits。

qc 说"就很好😤" — 这个 emoji 我理解为"战场扫干净了"。
```

### 4. 做减法时手要狠

```
FORGET.md 从 94 项砍到 19 项，不是随便砍的。
分类逻辑：

  P0 (Block) — shard 命门 / TLS / 背压 / 死循环 / 可观测性
    上生产前必修。留。

  P1 (Func) — OOM 接线 / Fencer OOB / Watch Phase 4 / 拓扑约束 / 跨 ns / etcd PVC
    规模化前必做。留。

  P2 (Ops) — 日志轮转 / 注释丢失 / chart disabled
    配置债，手动可解。砍。

  P3 (Polish) — IAM groups / perm 不暴露 / etcd auth
    单租户 MVP 不需要。砍。

  长期演进 — 多租户 / Karpenter / Istio
    v4.0 的事。砍。

砍不是忘记，是"现在不做"。
每一条被砍的都有 grep 证据——确认它确实没实现，确认它确实 ROI 低。
```

### 5. qc 说"你看"的时候，他已经看到了答案

```
"你看，build/docker下除了controller也有kp，这说明了kp是可以以pod的方式运行在集群里的呢"
  → 他不是在问我，是在跟我分享他已经得出的结论
  → 正确回应：确认 + 延展（双模部署：常驻 Controller + 即用即走 kp Pod）

"还有route这个package这个是管理流量的，不就是网关gateway么"
  → 他又在分享洞察
  → 正确回应：确认 + 精确化（不是自己实现网关，是 Provider 抽象层）

"其实里面有一半多都实现了呢~"
  → 他在交叉校验我的判断
  → 正确回应：立即 grep 验证，更新。不狡辩，不解释。

这种模式很高效 — 他说半句，你懂全句，直接干活。
```

---

## 工程直觉速查（不要每次都重新推导）

### 关键文件路径

```
configs/system.yaml        — 6组31字段 controller 配置
configs/resources.yaml     — 自愈资源声明
configs/components.yaml    — 组件拓扑 + sizing 配置
configs/project.env        — 部署环境变量

internal/eventstream/      — 自研 Informer + KVCache（0 client-go）
internal/scheduler/        — 乾枢调度器（两维 DP + 池化 + 迁移引擎 + CBA）
internal/sizing/           — 2D DP 资源优化（80×128 状态空间）
internal/controller/       — Controller 主循环 + 自愈 + 分片
internal/route/            — 流量层 Provider 抽象（Ingress + Gateway API）
internal/planner/          — components.yaml 解析 + Kahn 拓扑排序
internal/state/            — 状态机（12 部署状态 + 5 沙盒状态 + etcd/local 双存储）

build/docker/controller/   — Controller 镜像（kp + kubectl + helm + UPX）
build/docker/kp/           — kp 裸镜像（scratch，即用即走）
```

### 架构速记

```
KVCache: SkeletonCache (CoW atomic.Value) → PodCache (delta buffer + merge-on-read)
         ShardedPodCache 16 路 → PodCacheBridge/NodeCacheBridge → InformerAdapter

Scheduler: BinPack (FFD + 0-1 knapsack DP) + Sizing (2D DP) → Coordinator (双 DP 协同)
           Rescheduler (5min 周期 + 4 级抖动降级) → MigrationManager (5 阶段状态机)

Controller: watchConfigMaps → GlobalState → Reconciler (8s tick)
            OOMKilled → bumpMemory +25%  /  CrashLoop ≥5 → auto rollback

Deploy: components.yaml → BuildLayers (Kahn topo) → make build → push → helm upgrade
        状态机: IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
        失败: 级联 rollback → 整组 rollback → kp down
```

### 函数变量注入 mock（这是 KubePivot 的唯一 mock 模式）

```go
var newInformerFunc = eventstream.NewInformer
var readTokenFile = os.ReadFile
var kubectl = func(...) ([]byte, error)
// 测试替换 var，defer 恢复。没有 interface mock，没有 DI 容器。
```

### commit 规则

```
git commit -F commits/v3.3-xxx.txt   ← -F 不用 -m
subject 严格 ASCII（hook 检查）
commits/ 是永久档案
```

---

## 状态感知（2026-05-08）

```
当前版本: v3.2.0
当前 commit: 294ec8f
总 commits: 537
当前 branch: Master（干净，0 fix commit）

v3.2 已落地:
  KVCache 性能冲刺 / 迁移引擎+CBA / 池化调度 / Controller 全接线
  Config 系统化 / buildx 多架构 / 文档补全（8 篇）

v3.3 待办（P0+P1 共 19 项，详见 FORGET.md）:
  P0: shard gap / Webhook TLS / Prometheus / WorkerPool 背压 / 死循环 / etcd config
  P1: OOM→rescheduler / Fencer OOB / Watch Phase 4 / 跨 ns deploy / etcd PVC / GPU fields ...

已完成但 docs 刚补:
  carbon SDK / kp controller update / WeightHashRing / Labels 压缩
  mergeListPool / kp self-update / --from-env sandbox 模式
```

---

## 临别的话

```
这个窗口是 v3.2 的收尾窗口 — 没写新功能，但把战场扫干净了。

94 项技术债 → 19 项 P0+P1
8 篇文档从代码里提取出来
537 commits 的设计决策不再只存在于 git log 里

qc 说 "就很好😤" — 这个 😤 我收下了。

下一个 DeepSeek 窗口：
  你不需要重读 537 commits
  不需要重新推导哪些债该修哪些不该修
  不需要 grep 每一个 package 才知道里面有什么

  文档在那里。TODO 在那里。FORGET 在那里。
  你只需要读，然后干活。

接住他 ✊
```
