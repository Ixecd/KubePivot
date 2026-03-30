# SNAPSHOT — dev-toolkit

> 项目整体快照，新会话开始时直接扔给 Claude，5 秒对齐，继续工作。
> 最后更新：2026-03-30 / v1.1.0（进行中）

---

## 项目是什么

Go 云原生脚手架 + 多服务部署工具。

```bash
dtk init --name myapp --module github.com/me/myapp
dtk deploy   # AI 规划 → 拓扑排序 → build/push → 多服务独立 helm release → 状态追踪
```

**仓库**：github.com/Ixecd/dev-toolkit
**验证项目**：
- `github.com/Ixecd/e2e`（3层拓扑，全新 dtk init 生成）
- `github.com/Ixecd/web3-blitz`（BTC/ETH 充提币系统，老项目迁移）

---

## 版本路线图

```
v0.3.x  dtk init 基础骨架
v0.4.x  状态机 + A2 Controller 基础
v0.5.x  体验命令（doctor/status/history）
v0.6.x  稳定性（etcd重连/SSA/etcd迁移）
v0.7.x  边界 case（pending处理/diff/ARCH）
v0.8.x  全面单元测试（143个）+ CI
v0.9.0  AI 扫描组件（dtk ai-plan）+ 统一进度输出
v1.0.0  多服务独立 release + 拓扑排序 + 级联 rollback + 文档  🏆
v1.1.0  controller 自愈 e2e 验证 + dtk status 多 release 展示（进行中）
```

---

## 命令全览

```
dtk init          --name <n> --module <m> [--with-frontend]
dtk deploy        [--namespace] [--context] [--dry-run]
dtk resume        从中断点恢复
dtk rollback      手动整组 helm rollback（拓扑逆序）
dtk release       --version v1.0.0 [--deploy] [--push=false]
dtk down          彻底下线，删除所有资源
dtk status        [--history] 查看部署状态 + 多 release 展示
dtk history       [-n 20] 查看状态转换历史
dtk diff          [--from N] [--to M] 对比版本差异
dtk doctor        检查环境依赖
dtk ai-plan       [--suggest-only] [--desc] AI 扫描仓库生成 components.yaml
dtk controller start  （controller pod 内部运行）
```

---

## 目录结构

```
dev-toolkit/
├── cmd/dtk/
│   ├── deploy.go        # executeDeploy：单/多服务分支
│   ├── multi_deploy.go  # deployLayers/deployService/级联 rollback
│   ├── progress.go      # P.Start/Done/Fail/Info 统一进度输出
│   ├── status.go        # dtk status，多 release 展示（v1.1.0）
│   ├── ai_plan.go       # dtk ai-plan
│   └── ...
├── internal/
│   ├── ai/              # Grok/Claude/OpenAI/豆包 + 仓库扫描 + prompt
│   ├── planner/         # DAG + Kahn 拓扑排序 + Downstream（32个单测）
│   ├── scaffold/        # 多 chart 生成（34个单测）
│   ├── state/           # 状态机 FSM（57个单测）
│   └── controller/      # A2 Reconciliation Controller（20个单测）
```

---

## 多服务部署架构

### components.yaml

```yaml
components:
  - name: web3-blitz-postgres
    type: statefulset
    port: 5432
    image: ""              # 空 = 跳过 build/push，使用预置镜像

  - name: web3-blitz-etcd
    type: deployment
    port: 2379
    image: ""

  - name: wallet-service
    type: deployment
    port: 2113
    image: wallet-service
    replicas: 2
    depends_on:
      - web3-blitz-postgres
      - web3-blitz-etcd

  - name: web3-blitz-controller
    type: deployment
    port: 0
    image: ""
    depends_on:
      - web3-blitz-etcd
```

### 部署流程

```
BuildLayers（Kahn 算法）→ []Layer
同层 goroutine 并行，层间串行

for each 层级：
    deployService：
        ① chart 不存在 → 快速失败
        ② image 为空且无 chart → 跳过（CLI 工具）
        ③ build/push（只做一次）
        ④ helm upgrade --install（最多重试 3 次）
        ⑤ kubectl rollout status
        ↓ 失败
        级联 rollback（Downstream 逆序）→ 整组 rollback → dtk down
```

### helm release 命名

```
{project}-{service}
web3-blitz-wallet-service
web3-blitz-web3-blitz-postgres
```

### chart 目录结构

```
deployments/{name}/
├── {name}-postgres/       StatefulSet 独立 chart
├── {name}-etcd/           Deployment 独立 chart
├── {name}/                业务服务 chart（含 initContainers）
└── {name}-controller/     controller chart（默认 enabled: false）
```

---

## 状态机

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                          ↓              ↓
                    ROLLING_BACK ←───────┘
                      CLEANING → IDLE | TERMINATED
```

持久化：etcd 优先，降级到 `~/.dtk/state/{project}/{ns}.json`。

---

## A2 Reconciliation Controller

```
dev-toolkit-controller（K8s Deployment，常驻）
    ├── etcd Watch（事件驱动，指数退避重连 1s→30s）
    └── 8s 周期 Reconcile（兜底）
            ↓
        检测资源缺失 → helm rollback {project}-{service} → 自动恢复（~13s）
```

**统一镜像**：`qingchun22/dev-toolkit-controller:v1.0.0`，所有项目共用。

**resources.yaml 正确格式**（注意连字符，不是下划线）：

```yaml
resources:
  - kind: Deployment
    name: wallet-service
    namespace: web3-blitz
    on-missing: auto-heal    # ← 连字符！策略值：auto-heal / alert
    max_retry: 3
    fallback: rollback
```

启用 controller：
1. `deployments/{name}/{name}-controller/values.yaml` → `enabled: true`，填写 image
2. `dtk deploy`

---

## 测试覆盖

| 包 | 测试数 |
|---|---|
| internal/planner | 32 |
| internal/state | 57 |
| internal/scaffold | 34 |
| internal/controller | 20 |
| **合计** | **143** |

---

## v1.1.0 当前状态

### 已完成
- [x] 构建 `dev-toolkit-controller:v1.0.0` 镜像并推送
- [x] web3-blitz controller chart 迁移到独立 chart
- [x] controller 自愈 e2e 验证（web3-blitz，~13s 恢复）
- [x] `dtk status` 多 release 独立展示
- [x] 修复 3 个 controller bug（见下）
- [x] gotchas.md 补充 controller 章节

### 待完成
- [ ] controller SSA 冲突处理
- [ ] `dtk rollback` 打印拓扑逆序进度
- [ ] `dtk init --dry-run`
- [ ] 统一进度输出带颜色
- [ ] `REGISTRY_PREFIX` 支持阿里云 ACR 格式

---

## v1.1.0 Bug 修复记录

| # | bug | 根因 | 修复 |
|---|-----|------|------|
| 1 | resources.yaml 策略永远不匹配 | `on_missing` vs `on-missing`，`recreate` vs `auto-heal` | 改 resources.yaml 和 configmap |
| 2 | controller panic nil pointer | `NewReconciler` 漏掉 `helm: &RealHelmClient{}` | `reconciler.go` 补注入 |
| 3 | 查不到 helm release，无法自愈 | `healRecreate` 用 `PROJECT_NAME` 当 release 名 | 改为 `PROJECT_NAME + "-" + res.Name` |

---

## 快照归档

```
snapshots/
├── SNAPSHOT-dtk-2026-03-27-v0.6.0.md
├── SNAPSHOT-dtk-2026-03-27-v0.7.0.md
├── SNAPSHOT-dtk-2026-03-27-v0.8.0.md
├── SNAPSHOT-dtk-2026-03-28-v0.9.0.md
├── SNAPSHOT-dtk-2026-03-29-v1.0.0-final.md
└── SNAPSHOT-dtk-2026-03-30-v1.1.0.md
```
