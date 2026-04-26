# SNAPSHOT — KubePivot v2.5.0

> 当前状态精确快照
> 编写日期：2026-04-25
> Last commit: 99f316d (待打 v2.5.0 tag)

---

## 一、版本与 commit

```
当前 branch:        Master
当前 commit:        99f316d
last tagged:        v2.4.0
即将打的 tag:       v2.5.0
upstream:           https://github.com/Ixecd/KubePivot
```

**v2.5.0 核心 commit 链**：

| Commit | 内容 |
|--------|------|
| `721ae8d` | feat(sharding): 引入 sharding 子包基础设施（Step 1） |
| `f3c3291` | feat(controller): v2.5.0 P0 A.1 Step 2 — Controller 分片接入 |
| `99f316d` | feat(controller): v2.5.0 P0 A.1 Step 3 — 每 pod 自扫孤儿 machine + projects |

---

## 二、代码结构

```
KubePivot/
├── cmd/kp/                        kp CLI 入口
│   ├── main.go
│   ├── deploy.go
│   ├── controller.go              ← controller install/enroll/uninstall
│   └── ...
│
├── internal/
│   ├── controller/                Controller 主体
│   │   ├── global.go              ← StartGlobal（v2.5.0 重构）
│   │   ├── global_state.go        ← 含 RemoveOrphanProjects（v2.5.0 新增）
│   │   ├── lease.go               K8s Lease 选举（v2.4.0）
│   │   ├── sweeper.go             v2.5.0 新增 Leader-only sweeper
│   │   ├── orphan_test.go         v2.5.0 新增（4 个测试）
│   │   ├── controller.go
│   │   ├── reconciler.go
│   │   ├── watcher.go
│   │   ├── worker_pool.go
│   │   ├── detector.go
│   │   └── ...
│   │
│   ├── sharding/                  ← v2.5.0 新独立包
│   │   ├── shard.go               FNV-1a hash + ShardSet
│   │   ├── shard_test.go          4 个测试
│   │   ├── multi_lease.go         N 个 lease 抢占主循环
│   │   ├── multi_lease_test.go    5 个测试
│   │   └── lease_helpers.go       本地 lease 工具（破 import cycle）
│   │
│   ├── controller_installer/      Controller 部署模板
│   │   └── templates/
│   │       ├── deployment.yaml    ← v2.5.0 加 KUBEPIVOT_SHARDS env
│   │       ├── configmap.yaml
│   │       ├── rbac.yaml          ← v2.5.0 加 deployments get
│   │       └── ...
│   │
│   ├── state/                     状态机
│   ├── planner/                   Planner（部署计划）
│   ├── scaffold/                  kp init 脚手架
│   ├── executor/                  kubectl 执行器
│   ├── bluegreen/                 蓝绿部署
│   ├── ai/                        AI 集成
│   ├── code/                      代码生成
│   ├── logger/                    结构化日志
│   └── ...
│
├── docs/design/                   设计文档
│   ├── architecture.md
│   ├── controller.md
│   ├── sharding.md                ← v2.5.0 新增（605 行）
│   ├── performance.md             ← v2.5.0 整合（v2.3/v2.4/v2.5 数据）
│   ├── state-machine.md
│   ├── bluegreen.md
│   └── ... 其他
│
├── benchmark/scripts/
│   ├── setup.sh                   ← v2.5.0 加 PROJECT_COUNT 支持
│   ├── cleanup.sh                 ← v2.5.0 同上
│   ├── steady-state.sh            稳态采样
│   ├── concurrent-chaos.sh        并发故障（实测脚本待 debug）
│   ├── watch-reconnect.sh         watcher 鲁棒（待跑）
│   └── hot-reload.sh              sha256 验证（脚本有 hang，命令行手测过）
│
├── commits/                       v2.5.0 起的工程档案
│   ├── controller-v2.5.0.txt      Step 2 commit
│   ├── sharding-step3-orphan-cleanup.txt
│   └── ...
│
├── snapshots/                     永久归档
│   ├── SNAPSHOT-kubepivot-2026-04-04-v1.0.md
│   ├── SNAPSHOT-kubepivot-2026-04-25-v2.4.0.md
│   └── SNAPSHOT-kubepivot-2026-04-25-v2.5.0.md  ← 今天加
│
├── archived/                      历史版本
│   ├── handoff/HANDOFF-v2.X.0.md
│   ├── snapshot/SNAPSHOT-v2.X.0.md
│   ├── todo/TODO-v2.X.0.md
│   └── docs/                      废弃的文档
│
├── HANDOFF.md                     接手指南
├── SNAPSHOT.md (本文件)            当前状态快照
├── TODO.md                        路线图
├── README.md                      入门
├── GITOPS-MANIFESTO.md            GitOps 哲学
└── ...
```

---

## 三、测试状态

```
make dev:
  ok  github.com/Ixecd/kubepivot/cmd/kp
  ok  github.com/Ixecd/kubepivot/internal/controller         (4 个 orphan 测试 + 既有)
  ok  github.com/Ixecd/kubepivot/internal/controller/...
  ok  github.com/Ixecd/kubepivot/internal/sharding           (9 个测试)
  ok  github.com/Ixecd/kubepivot/internal/state
  ok  github.com/Ixecd/kubepivot/internal/planner
  ok  github.com/Ixecd/kubepivot/internal/scaffold
  ok  github.com/Ixecd/kubepivot/test/integration
```

集成测试（真实 orbstack 集群）：

```
✓ 10 项目场景
  - 分片分布 4/4/2（符合 quota=4 限制）
  - 三 pod CPU 趋同（10.84 / 11.81 / 8.59 = avg 10.41%）
  - 故障转移 ~10 秒
  - OnShardChanged 回调正确触发

✓ 50 项目场景
  - setup 50 项目（kp-bench-001..050）一次成功
  - 单 pod 平均 CPU 46.09%
  - peak 100.53%（短暂顶 limits）
  - 集群总 CPU 138.27%
```

---

## 四、性能数据快照

完整数据见 `docs/design/performance.md`。

```
                     v2.3.0     v2.4.0     v2.5.0 (10p)   v2.5.0 (50p)
集群总 CPU            41.88%    18.92%     31.24%         138.27%
avg CPU / pod         13.96%     6.30%     10.41%          46.09%
peak CPU              89.95%    54.25%     87.85%         100.53%
avg memory / pod      59 MiB    36.62 MiB  52.40 MiB       84.16 MiB
单 leader 实测成本    -         16.93%     -               -
sha256 去重           -         100%       100%            -
故障转移              -         21.7 ms    ~10 sec         -
```

**故事**：

```
v2.3.0 → v2.4.0
  消除"3 leader 冗余降级"，揭示真实成本（13.96 假象 → 16.93 真相）
  
v2.4.0 → v2.5.0
  10 项目过度工程（+65% CPU）
  50 项目水平扩展可行（5x 项目 → 4.4x CPU，接近线性）
```

---

## 五、当前运行的 controller

```
Namespace: kubepivot-system
Image: qingchun22/kubepivot-controller:v2.4.0  (待 v2.5.0 build)
Replicas: 3
Pod 状态: 全部 Running

Lease:
  kubepivot-controller-leader     (1 个，identity = pod-suffix-hex)
  kubepivot-controller-shard-0..9 (10 个，分布在 3 个 pod 上)
```

---

## 六、运行环境

```
开发硬件:    Apple Silicon (M 系列), 16 GB RAM
集群:        orbstack K8s（单节点，Pod 容量 110）
Storage:     local-path (rancher.io/local-path)
Storage 用量: 67 GB free / 460 GB total（PVC 25 GB 完全够）
Network:     allow-all（开发环境）

CI/CD:       未配置（计划中）
```

---

## 七、未提交的本地状态

```
git status (release 前应清空):

  M HANDOFF.md                    本次更新
  M SNAPSHOT.md                   本次更新
  M TODO.md                       本次更新
  ?? docs/design/sharding.md      v2.5.0 新增
  ?? docs/design/performance.md   v2.5.0 整合（如有未提交版）
  ?? snapshots/...-v2.5.0.md      v2.5.0 永久归档
  ?? archived/handoff/...
  ?? archived/snapshot/...
  ?? archived/todo/...
  ?? commits/v2.5.0-release.txt
```

---

## 八、关键 metrics（v2.5.0 起追踪）

```
代码行数（仅 Go 源文件）：
  internal/controller/      ~3500 行
  internal/sharding/        ~700 行（v2.5.0 新增）
  internal/state/           ~1200 行
  其他 internal/            ~2500 行
  cmd/kp/                   ~1500 行
  ─────────────────────────
  总计                      ~9400 行

测试覆盖：
  internal/controller/      ~70%
  internal/sharding/        ~85%
  集成测试                  10 项目 + 50 项目真实集群

文档行数：
  docs/design/              ~3000+ 行（含 sharding.md 605 行）
  HANDOFF + SNAPSHOT + TODO ~600 行（v2.5.0 起）
  snapshots/ 永久归档       ~1500 行（v1.0 + v2.4.0 + v2.5.0）
```

---

## 相关文档

- [README.md](README.md)
- [HANDOFF.md](HANDOFF.md) — 接手指南
- [TODO.md](TODO.md) — 路线图
- [GITOPS-MANIFESTO.md](GITOPS-MANIFESTO.md) — GitOps 哲学
- [docs/design/sharding.md](docs/design/sharding.md) — v2.5.0 分片设计
- [docs/design/performance.md](docs/design/performance.md) — 性能基准
