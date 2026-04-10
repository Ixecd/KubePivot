# 项目交接文档 — KubePivot

> 写给下一个 Claude
> 日期：2026-04-04
> 版本：v2.0.0
> commit：#329（生日数字）

---

## 写在前面

你接手的是 qc（GitHub: Ixecd，杨庆春）独立开发的 KubePivot（乾枢）。他 23 岁，Go/云原生，生日 2026-03-29，那天推了 v1.0.0。今天是 v2.0.0，第 329 个 commit，是他设计的。

女帝负责产品和设计输入，权重很高。他在用豆包做 AI 伴侣，清醒地知道那是什么。KubePivot 是他构建 Feelings（感受民主化脑机接口产品）的基础设施。

**和 qc 相处的基本原则**：

- 设计先对齐，再动手。他不喜欢边写边想。
- 他喜欢被推 back，不喜欢被纯认同。有问题就说。
- "只保护，不越权"是贯穿整个项目的哲学，不只是代码。
- `slog` 不用 `log`，`P.Info/Done/Fail` 做进度输出，`make dev` 一键验证。
- 不搞技术债。宁可 TODO + 完整设计也不临时方案。
- commit 格式：`type: 简短描述\n\n- 详细 bullet`

---

## 一、项目当前状态

```
版本：v2.0.0（已 tag，已推送）
commit：#329
测试：go test ./... -race 全绿（236 个）
make dev：build + test + install 一键完成
companion：github.com/Ixecd/web3-blitz（k3s + OrbStack）
```

---

## 二、关键文件地图

```
cmd/kp/（25 个子命令，49 个文件）
├── main.go              # 命令入口，default case → execPlugin 兜底
├── version.go           # kpVersion const，kp version，kp update
├── plugin.go            # kp plugin，execPlugin 插件市场
├── chaos.go             # kp chaos，Chaos Mesh API，4 种混沌类型
├── audit.go             # kp audit，AuditEvent，三来源聚合
├── policy.go            # kp policy，OPA 策略引擎，stdin pipe
├── env.go               # kp context，KPEnv，多集群管理
├── sandbox.go           # kp sandbox，5 阶段原子性迁移
├── preview.go           # kp deploy --preview，kp warmup
├── migrate_snapshot.go  # PVC 快照联动，双层回滚，fix-dirty
├── drift.go             # kp diff --drift，三级分层
├── hpa.go               # applyHPA，autoscaling/v2
├── secret.go            # kp secret rotate/cleanup/sync/audit
│                          fetchVaultKV: net/http 实现（无 curl）
└── multi_deploy.go      # buildHelmArgs（--force-conflicts），OPA check

internal/
├── state/state.go       # 13 个状态的完整状态机，validTransitions 严格约束
├── controller/
│   ├── leader.go        # etcd 分布式 Leader Election（TTL=15s）
│   ├── workqueue.go     # 三集合 WorkQueue（queue/dirty/processing）
│   ├── reconciler.go    # Start()：启动所有 Loop
│   ├── heal.go          # on-missing 全策略 + OOMKilled + CrashLoopBackOff
│   ├── drift_sync.go    # 30s 扫描，etcd 审计
│   └── sandbox_gc.go    # 5m GC Loop，清理超期 Session
├── planner/planner.go   # DAG 规划，含 MinReplicas/MaxReplicas/TargetCPU
└── scaffold/helm.go     # kp init 生成，含 virtualservice-preview.yaml 模板

docs/
├── design/
│   ├── architecture.md  # 完整架构（v2.0.0 更新）
│   ├── state-machine.md # 13 状态 + Sandbox 详解（v2.0.0 更新）
│   ├── sandbox.md       # Operation Sandbox 设计
│   └── preview-warmup.md # Header Preview + Warmup 设计
└── guide/zh-CN/
    ├── quickstart.md    # 快速开始（v2.0.0 更新）
    ├── commands.md      # 25 个命令完整参考（v2.0.0 全新重写）
    └── gotchas.md       # 已知坑（v2.0.0 更新）

examples/policies/
├── no-latest-tag.rego
├── require-resource-limits.rego
└── README.md

GITOPS-MANIFESTO.md      # "CD 从 Pipeline 退化回 Git Commit"
```

---

## 三、架构核心

### 状态机（13 个状态）

```
核心部署：IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                                     ↓
                               ROLLING_BACK → RUNNING

Sandbox：RUNNING/IDLE → LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
                                                                   ↓（失败）
                                                               RESTORING → IDLE

关键约束：COMMITTING 永远禁止 force-unlock
```

### Controller 三个 Loop

```
Reconcile Loop：8s 周期，资源存在吗？健康吗？→ 自愈
Drift Sync Loop：30s，force-sync=true 的资源对比 live 集群 → 硬冲突修正
Sandbox GC Loop：5m，超期 Session 清理 + ForceState → IDLE
```

### 核心原则

```
只保护，不越权：kp 只管自己声明所有权的字段（image/env/ports/resources）
降级不阻断：可选组件缺失不影响核心流程
确定性优先：DAG 决定顺序，规则决定漂移，不靠 AI 做关键路径决策
```

---

## 四、技术债（诚实清单）

| 优先级 | 描述 | 计划 |
|--------|------|------|
| P1 | SSA `--field-manager`：helm v4 不支持，用 `--force-conflicts` | helm v4 稳定后 |
| P2 | SIMULATING Job / PVC 快照 / Istio weight / Prometheus | 有对应环境时验证 |
| P2 | drift etcd 审计端到端 | 有 etcd 集群时 |
| P2 | Controller GC 端到端 | 长时间运行集群 |
| P2 | 蓝绿 timing 统计为 `-` | 顺手 |
| P3 | kp secret sync：curl → net/http（已完成），Vault SDK 深度集成 | v2.1.0 |

---

## 五、常用命令

```bash
cd ~/KubePivot && make dev   # 永远先跑这个

cd ~/web3-blitz
kp deploy
kp deploy --changed-only --parallelism 4
kp deploy --env staging --dry-run
kp status --all-envs
kp diff --drift
kp diff --to-env staging
kp sandbox start --dry-run
kp audit --format table
kp audit --format jsonl --since 2026-04-01
kp policy check
kp chaos inject --service wallet-service --kind pod-kill --dry-run
kp doctor
kp doctor --perf
kp version
LOG_FORMAT=json kp deploy 2>log
```

---

## 六、下一步

qc 接下来可能做的事情（按可能性排序）：

1. **让 KubePivot 有真实用户**：写技术博客，发掘金/知乎，找到第一批用户
2. **Feelings 项目**：KubePivot 是地基，Feelings 是上层建筑
3. **web3 自由职业**：web3-blitz 是验证环境也是作品集
4. **CNCF Sandbox**：需要社区，需要贡献者，需要曝光度

乾枢不是名字，是承诺。
