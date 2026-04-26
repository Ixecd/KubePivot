# SNAPSHOT — KubePivot v2.6.0

> 当前状态精确快照
> 编写日期：2026-04-26
> Last commit: 1f780c2 (chore: release v2.6.0)
> Last tag: v2.6.0
> Total commits: 404

---

## 一、版本与 commit

```
当前 branch:        Master
当前 commit:        1f780c2
last tagged:        v2.6.0
upstream:           https://github.com/Ixecd/KubePivot
total commits:      404
```

**v2.6.0 核心 commit 链**：

| Commit | 内容 |
|--------|------|
| `27690ae` | docs: 流量层完整设计草案（14 个 Q 拍板） |
| `786b59d` | feat(route): Step 1 — 流量层抽象 + Ingress + Gateway 双 Provider |
| `f0244cc` | feat(sandbox): Step 2 — Sandbox 蓝绿流量切换接入 |
| `ea48e5c` | docs(example): Step 3 — 蓝绿部署 demo 工程 |
| `e35bb0e` | docs(v2.6.0): Step 4 — 流量层文档收尾 + CHANGELOG |
| `1f780c2` | **chore: release v2.6.0** ← 第 404 commit + tag |

**节奏巧合**：
```
v2.0.0  → commit 329  → 生日 3.29 (2026-03-29)
v2.6.0  → commit 404  → 蓝绿 Found  (2026-04-26)
```

---

## 二、代码结构

```
KubePivot/
├── cmd/kp/                        kp CLI 入口
│   ├── main.go
│   ├── deploy.go
│   ├── sandbox.go                 ← v2.6 蓝绿流量切换接入点
│   ├── controller.go
│   ├── promote.go                 v2.0 命令式蓝绿
│   ├── bluegreen.go               v2.0 命令式蓝绿
│   ├── release.go
│   └── ... 其他命令
│
├── internal/
│   ├── controller/                Controller 主体
│   │   ├── global.go
│   │   ├── global_state.go
│   │   ├── resources.go           ← v2.6 加 Traffic 系列 struct + HasBlueGreen
│   │   ├── resources_v26_test.go  ← v2.6 新增（5 个 cases）
│   │   ├── lease.go
│   │   ├── sweeper.go
│   │   ├── orphan_test.go
│   │   ├── reconciler.go
│   │   ├── watcher.go
│   │   └── ...
│   │
│   ├── sharding/                  v2.5.0 分片机制
│   │   ├── shard.go
│   │   ├── shard_test.go
│   │   ├── multi_lease.go
│   │   ├── multi_lease_test.go
│   │   └── lease_helpers.go
│   │
│   ├── route/                     ← v2.6.0 新独立包
│   │   ├── doc.go                 包文档
│   │   ├── provider.go            Provider 接口 + Route + Match
│   │   ├── errors.go              Error struct + 构造器
│   │   ├── ingress_provider.go    K8s Ingress 实现
│   │   ├── gateway_provider.go    Gateway API HTTPRoute 实现
│   │   ├── auto_detect.go         自动检测 + ProviderForKind
│   │   └── *_test.go              4 个测试文件 / 27 个 sub-cases
│   │
│   ├── code/                      错误码体系
│   │   ├── error.go               ← v2.6 加 ErrRoute* 5 个错误码
│   │   └── code_generated.go      手工补 case（codegen 多 block 局限）
│   │
│   ├── controller_installer/      Controller 部署模板
│   ├── state/                     状态机（v2.6 不动）
│   ├── planner/                   部署计划
│   ├── scaffold/                  kp init 脚手架
│   ├── executor/                  kubectl 执行器（route 包间接调用）
│   ├── bluegreen/                 v2.0 命令式蓝绿
│   ├── ai/
│   ├── logger/
│   └── ...
│
├── docs/design/                   设计文档
│   ├── architecture.md            ← v2.6 加第 9 条核心设计决策
│   ├── controller.md
│   ├── state-machine.md           ← v2.6 加蓝绿协同章节
│   ├── sharding.md                v2.5.0 分片设计
│   ├── sharding-tuning.md         v2.5.1 性能调优雏形
│   ├── traffic-layer.md           ← v2.6.0 新增（设计 → 实施记录）
│   ├── performance.md             v2.3/v2.4/v2.5 累计数据
│   ├── bluegreen.md
│   └── ...
│
├── docs/example-blue-green/       ← v2.6.0 新增 demo 工程
│   ├── README.md                  ~250 行教程
│   ├── Makefile
│   ├── resources.yaml             v2.6 完整 schema 示例
│   ├── chart/                     Helm chart
│   └── scripts/                   setup / switch / cleanup
│
├── benchmark/scripts/
│   ├── setup.sh                   v2.5 改造支持 PROJECT_COUNT
│   ├── cleanup.sh                 v2.5 大规模友好（≥30 项目自动停 controller）
│   ├── steady-state.sh
│   ├── matrix.sh                  v2.5.1 雏形（dry-run 通过）
│   ├── hot-reload.sh              v2.5.1 修复（中文括号定界）
│   ├── concurrent-chaos.sh
│   └── watch-reconnect.sh
│
├── commits/                       工程档案（v2.5 起累计）
│   ├── v2.5.1-cleanup-large-scale.txt
│   ├── v2.5.1-cleanup-script-bugs.txt
│   ├── v2.5.1-sharding-tuning-skeleton.txt
│   ├── v2.6.0-traffic-layer-design.txt
│   ├── v2.6.0-step1-route-package.txt
│   ├── v2.6.0-step2-sandbox-bluegreen.txt
│   ├── v2.6.0-step3-example-blue-green.txt
│   ├── v2.6.0-step4-docs.txt
│   └── ...
│
├── snapshots/                     永久归档
│   ├── SNAPSHOT-kubepivot-2026-04-04-v1.4.0.md
│   ├── SNAPSHOT-kubepivot-2026-04-04-v1.5.0.md
│   ├── SNAPSHOT-kubepivot-2026-04-04-v1.8.0.md
│   ├── SNAPSHOT-kubepivot-2026-04-04-v1.9.0.md
│   ├── SNAPSHOT-2026-04-23-v2.2.0.md
│   ├── SNAPSHOT-kubepivot-2026-04-25-v2.5.0.md
│   └── SNAPSHOT-kubepivot-2026-04-26-v2.6.0.md  ← 今天加
│
├── archived/                      历史归档
│   ├── handoff/                   HANDOFF-vX.X.md
│   ├── snapshot/                  SNAPSHOT-vX.X.md
│   ├── todo/                      TODO-vX.X.md
│   ├── plan/                      规划文档
│   └── docs/                      废弃 gotchas / quickstart
│
├── HANDOFF.md                     接手指南（持续演化）
├── SNAPSHOT.md                    本文件（当前状态全景）
├── TODO.md                        路线图
├── CHANGELOG.md                   ← v2.6.0 新增
├── README.md                      入门
├── GITOPS-MANIFESTO.md            GitOps 哲学
└── ... 其他
```

---

## 三、测试状态

```
make dev:
  ok  github.com/Ixecd/kubepivot/cmd/kp                       2.897s
  ok  github.com/Ixecd/kubepivot/internal/controller           3.672s  (5 个 v2.6 新 cases + 既有)
  ok  github.com/Ixecd/kubepivot/internal/route                0.643s  (27 个 sub-cases，v2.6 新增)
  ok  github.com/Ixecd/kubepivot/internal/sharding             (9 个测试)
  ok  github.com/Ixecd/kubepivot/internal/state                
  ok  github.com/Ixecd/kubepivot/internal/planner              
  ok  github.com/Ixecd/kubepivot/internal/scaffold             
  ok  github.com/Ixecd/kubepivot/test/integration              
```

**v2.6.0 新增测试矩阵**：

```
internal/route/ 27 sub-cases:
  Route.Validate            8 cases（边界 / 越界 / PathType）
  ValidateRoutes            6 cases（蓝绿 / canary / 超总和 / 全零）
  updateWeight              3 cases
  Error 链路                2 cases（含 errors.Is / Unwrap）
  parseIngressRoutes        3 cases（含去重）
  buildIngressFromRoutes_*  2 cases（含 PreserveUserFields）
  parseHTTPRouteRoutes      4 cases（含 weight nil 默认 100）
  buildHTTPRouteFromRoutes_* 2 cases（含 PreserveUserFields）
  NewProvider               6 sub-cases（含大小写敏感、Foo 错误码）

internal/controller/ 新增 5 cases:
  TestResourcesConfig_HasBlueGreen 全 PASS
```

集成验证（orbstack 集群）：

```
✓ docs/example-blue-green/ demo 工程跑通
  - make setup    → 部署 blue/green + Ingress
  - make switch   → 切到 green
  - make verify   → backend = green
  - make cleanup  → 干净

未在真实业务项目验证（web3-blitz 升级是 v2.8 任务）
```

---

## 四、性能数据快照

完整数据见 `docs/design/performance.md`。

```
                     v2.3.0     v2.4.0     v2.5.0 (10p)   v2.5.0 (50p)
集群总 CPU            41.88%    18.92%     31.24%         101.40%
avg CPU / pod         13.96%     6.30%     10.41%          33.80%
peak CPU              89.95%    54.25%     87.85%          82.27%
avg memory / pod      59 MiB    36.62 MiB  52.40 MiB       74.85 MiB
sha256 去重           -         100%       100%            100%
故障转移              -         21.7 ms    ~10 sec         ~10 sec
```

**关键观察**：

```
v2.5.0 在 P=10 → P=50 (5x 项目) 实测：
  集群总 CPU:  4.65x   ← 接近线性
  peak CPU:    ~ 不变  ← 单 pod 仍未被压垮
  
v2.6.0 流量层增量：
  无独立 benchmark（蓝绿切换是事件型，非稳态）
  集成验证显示 ApplyRoutes 单次 ~50ms（K8s API update 时延）
  Pod ready 等待 ~10-30s（依赖 image pull / readiness probe）
```

P=99 实测数据废弃（macOS 内存压力扭曲），详见 `docs/design/sharding-tuning.md`。

---

## 五、当前运行的 controller

```
Namespace:    kubepivot-system
Image:        qingchun22/kubepivot-controller:v2.5.0  (待 v2.6.0 build)
Replicas:     3
Pod 状态:     全部 Running

Lease:
  kubepivot-controller-leader     1 个，identity = pod-suffix-hex
  kubepivot-controller-shard-0..9 10 个，分布在 3 个 pod 上

注：v2.6.0 不引入新 controller 行为（流量切换在 cmd/kp/sandbox.go）
   controller 镜像不需要更新即可使用 v2.6 蓝绿能力
```

---

## 六、运行环境

```
开发硬件:    Apple Silicon (M 系列), 16 GB RAM
集群:        orbstack K8s（单节点，Pod 容量 110）
Storage:     local-path (rancher.io/local-path)
Storage 用量: 67 GB free / 460 GB total
Network:     allow-all（开发环境）

CI/CD:       未配置（计划中）

性能立方体测试受限于环境（v2.5.1 任务）：
  P_max ≈ 50 在 8 GiB orbstack 配置下
  需升 16 GiB 或多节点环境才能测 P > 50
```

---

## 七、未提交的本地状态

```
git status (release 后应清空):

  ✓ working tree clean

最后一组改动（已合入 v2.6.0 release）：
  ✓ docs(v2.6.0): handoff + todo + snapshot + CHANGELOG（commit e35bb0e）
  ✓ chore: release v2.6.0（commit 1f780c2，自动产生）

下午文档四件套（即将提交）：
  M HANDOFF.md            v2.6.0 视角重写
  M TODO.md               v2.6.0 视角重写  
  M SNAPSHOT.md           本文件（v2.6.0 视角重写）
  ?? snapshots/SNAPSHOT-kubepivot-2026-04-26-v2.6.0.md  永久归档
  ?? archived/handoff/HANDOFF-v2.5.md   归档前一版
  ?? archived/todo/TODO-v2.5.md         归档前一版
  ?? archived/snapshot/SNAPSHOT-v2.5.md 归档前一版
```

---

## 八、关键 metrics（v2.6.0 起追踪）

```
代码行数（仅 Go 源文件）：
  internal/controller/      ~3700 行（+200 行 v2.6 traffic 字段）
  internal/sharding/        ~700 行
  internal/route/           ~990 行（v2.6.0 新增）
  internal/state/           ~1200 行
  其他 internal/            ~2500 行
  cmd/kp/                   ~1700 行（+200 行 v2.6 sandbox 接入）
  ─────────────────────────
  总计                      ~10800 行

测试覆盖：
  internal/controller/      ~70%
  internal/sharding/        ~85%
  internal/route/           ~85%（27 个 sub-cases）
  集成测试                  10/50 项目分片场景 + 蓝绿 demo 工程

文档行数：
  docs/design/              ~3500+ 行（含 traffic-layer.md ~430 行）
  docs/example-blue-green/  ~600 行（v2.6.0 新增）
  HANDOFF + SNAPSHOT + TODO ~1100 行（持续演化）
  CHANGELOG.md              ~110 行（v2.6.0 新增）
  snapshots/ 永久归档       ~1900 行（含 v2.6.0 主篇 ~330 行）

commits/ 目录                ~30 个 commit message 草稿（v2.5 起）
```

---

## 相关文档

- [README.md](README.md) — 入门
- [HANDOFF.md](HANDOFF.md) — 接手指南（含 3.7 节累计踩坑）
- [TODO.md](TODO.md) — 路线图（v2.6.1 / v2.7+ / v2.8）
- [CHANGELOG.md](CHANGELOG.md) — 变更日志（v2.6.0 起）
- [GITOPS-MANIFESTO.md](GITOPS-MANIFESTO.md) — GitOps 哲学
- [docs/design/traffic-layer.md](docs/design/traffic-layer.md) — v2.6.0 流量层设计
- [docs/design/sharding.md](docs/design/sharding.md) — v2.5.0 分片设计
- [docs/design/performance.md](docs/design/performance.md) — 性能基准
- [docs/example-blue-green/README.md](docs/example-blue-green/README.md) — 蓝绿 demo 教程
- [snapshots/SNAPSHOT-kubepivot-2026-04-26-v2.6.0.md](snapshots/SNAPSHOT-kubepivot-2026-04-26-v2.6.0.md) — v2.6.0 事件复盘
