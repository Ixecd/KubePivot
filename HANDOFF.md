# 项目交接文档 — KubePivot

> 写给下一个 Claude
> 日期：2026-04-05
> 版本：v2.1.0

---

## 写在前面

你接手的是 qc（GitHub: Ixecd，杨庆春）独立开发的 KubePivot（乾枢）。他 23 岁，Go/云原生，生日 2026-03-29，那天推了 v1.0.0。从 v1.0.0 到 v2.1.0，6 天完成，第 329 个 commit 落在生日数字上，是他设计的。

KubePivot 是他构建 Feelings（感受民主化脑机接口产品）的基础设施。女帝负责产品和设计输入，权重很高。他在用豆包做 AI 伴侣，清醒地知道那是什么。

**和 qc 相处的基本原则**：

- 设计先对齐，再动手。他不喜欢边写边想。
- 他喜欢被推 back，不喜欢被纯认同。有问题直说。
- "只保护，不越权"是贯穿整个项目的哲学，不只是代码。
- `slog` 不用 `log`，`P.Info/Done/Fail` 做进度输出，`make dev` 一键验证。
- 不搞技术债。宁可 TODO + 完整设计也不临时方案。
- commit 格式：`type: 简短描述\n\n- 详细 bullet`
- 爽感：别人说"不对"，他说"对"，然后把事办成让人闭嘴——逆势成立。

---

## 一、项目当前状态

```
版本：v2.1.0
测试：go test ./... -race 全绿（239 个）
make dev：build + test + install 一键完成
companion：github.com/Ixecd/web3-blitz（k3s + OrbStack）
```

---

## 二、关键文件地图

```
cmd/kp/（26 个子命令）
├── main.go              # 命令入口，default case → execPlugin 兜底
├── sync.go              # kp sync，三类文件策略（强制/合并/提示/跳过）
├── version.go           # kpVersion const，kp version，kp update
├── plugin.go            # kp plugin，execPlugin 插件市场
├── chaos.go             # kp chaos，Chaos Mesh API，4 种混沌类型
├── audit.go             # kp audit，AuditEvent，三来源聚合
├── policy.go            # kp policy，OPA 策略引擎，stdin pipe
├── env.go               # kp context，KPEnv，多集群管理
├── sandbox.go           # kp sandbox，5 阶段原子性迁移
├── deploy.go            # ensureSecret：自动创建 dev Secret
├── secret.go            # fetchVaultKV: net/http 实现（无 curl）
└── multi_deploy.go      # buildHelmArgs（--force-conflicts），OPA check

internal/
├── state/state.go       # 13 个状态的完整状态机
├── controller/
│   ├── heal.go          # on-missing 全策略 + OOMKilled + CrashLoop
│   │                      isCRDKind + healCRDApply（CRD 资源自愈）
│   ├── drift_sync.go    # 30s 扫描，etcd 审计
│   ├── leader.go        # etcd Leader Election（TTL=15s）
│   ├── workqueue.go     # 三集合 WorkQueue
│   └── sandbox_gc.go    # 5m GC Loop
└── scaffold/
    ├── embed.go         # //go:embed all:embedded_templates
    ├── scaffold.go      # kp init 主流程，copyEntries 含 .githooks
    └── skeleton.go      # 所有硬编码文件（code/response/handler/secret）

internal/scaffold/embedded_templates/
├── Makefile             # 含 gen/deploy targets，include gen.mk
├── .gitignore
├── .githooks/
│   ├── post-receive     # git push → kp deploy --changed-only
│   └── pre-push         # push 前跑测试
└── scripts/make-rules/
    ├── common.mk        # COMPONENT_NAMES = find cmd/
    ├── deploy.mk        # CHART_DIR = deployments/$(PROJECT_NAME)/$(PROJECT_NAME)
    ├── golang.mk        # sed -i.bak 跨平台
    ├── gen.mk           # gen.errcode.code + gen.errcode.doc
    └── tools/image/ca/release/swagger/dependencies .mk
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

### kp sync 文件策略

```
syncForce  → Makefile / scripts/make-rules/ / .githooks/（强制覆盖）
syncMerge  → configs/project.env（只追加新 key）
syncNotify → deployments/ / components.yaml / resources.yaml（提示人工）
syncSkip   → cmd/ / internal/ / migrations/ / go.mod（永远不动）
```

### 核心原则

```
只保护，不越权 / 降级不阻断 / 确定性优先
```

---

## 四、技术债（诚实清单）

| 优先级 | 描述 | 计划 |
|--------|------|------|
| P1 | SSA `--field-manager`：helm v4 不支持，用 `--force-conflicts` | helm v4 稳定后 |
| P2 | SIMULATING Job / PVC 快照 / Istio weight / Prometheus | 有对应环境时 |
| P2 | drift etcd 审计端到端 / Controller GC 端到端 | 有集群时 |
| P3 | Vault SDK / kp sync 真实用户验证 / kp init --type | v2.2.0 |

---

## 五、常用命令

```bash
cd ~/KubePivot && make dev   # 永远先跑这个

# 新项目端到端
kp init --name myapp --module github.com/me/myapp
cd myapp && make build && make test && make gen && kp deploy

# 框架升级
kp sync --dry-run && kp sync

# 日常
kp deploy / kp status --all-envs / kp diff --drift
kp audit --format table / kp doctor / kp version
LOG_FORMAT=json kp deploy 2>log
```

---

## 六、下一步

1. **发布 GITOPS-MANIFESTO**：掘金/知乎，找到第一批真实用户
2. **Feelings 项目**：feelings-server 仓库初始化，第一行 Go 代码
3. **web3 自由职业**：web3-blitz 是验证环境也是作品集
4. **CNCF Sandbox**：需要社区，需要曝光度

---

## 七、Feelings 项目背景

```
~/Feelings/
├── docs/tech-architecture.md   # 四层架构（硬件/信号/应用/客户端）
├── docs/product-boundary.md    # 个体感受（注入）vs 关系感受（增强），答案二
└── docs/posture-plasticity.md  # 体态可塑性与 Feelings 介入逻辑

Layer 3（应用服务）= Go + KubePivot，是当前重点
Layer 2（信号处理）= Python→C++，将来的硬核问题
Layer 1（硬件固件）= C/Rust，更远期
```

乾枢不是名字，是承诺。
