# dtk 命令参考手册

> 适用：dev-toolkit v0.8.0+

---

## 全局说明

dtk 命令都在项目根目录下执行（含 `configs/project.env` 的目录）。

`--namespace`、`--context`、`--kubeconfig` 三个 flag 所有部署类命令通用，优先级高于 `project.env` 里的配置。

---

## dtk init

生成完整 Go 项目骨架。

```bash
dtk init --name <n> --module <module> [flags]
```

| Flag | 说明 | 默认值 |
|------|------|--------|
| `--name` | 项目名，必填，只允许小写字母、数字、`-` | - |
| `--module` | Go module 路径 | 同 `--name` |
| `--output` | 输出目录 | `./<n>` |
| `--template` | 模板根目录 | `DTK_TEMPLATE_ROOT` 或当前目录 |
| `--force` | 强制覆盖已有目录 | false |
| `--with-frontend` | 同时生成 React + Vite + Tailwind 前端骨架 | false |

**生成内容**：

```
<n>/
├── cmd/<n>/main.go              # HTTP 服务入口，含 /healthz
├── internal/
│   ├── api/                     # handler + 路由
│   ├── auth/                    # JWT + RBAC 中间件
│   ├── db/                      # 数据库连接 + migrations
│   ├── metrics/                 # Prometheus 指标
│   └── pkg/code/                # 业务错误码
├── deployments/<n>/             # 自包含 Helm chart
├── migrations/                  # SQL 迁移文件（golang-migrate 格式）
├── configs/
│   ├── project.env              # 部署配置（ARCH 自动检测填入）
│   ├── components.yaml          # AI 规划输入
│   └── resources.yaml           # controller 监控资源列表
├── monitoring/                  # Prometheus + Alertmanager + Grafana
├── handoff/
│   ├── HANDOFF.md               # 项目上下文，写给下一个 Claude
│   └── AI-CODING-GUIDE.md       # AI 编码约束指南
└── test/                        # e2e / integration / smoke
```

**注意**：
- 目录不存在时自动创建；目录已存在且非空时报错，需加 `--force`
- `ARCH` 自动通过 `go env GOARCH` 检测填入 `project.env`
- 生成完成后自动执行 `git init` + `go mod tidy`

---

## dtk deploy

AI 规划资源 → build → push → helm upgrade → 状态追踪。

```bash
dtk deploy [flags]
```

| Flag | 说明 | 默认值 |
|------|------|--------|
| `--components` | components.yaml 路径 | `configs/components.yaml` |
| `--namespace` | K8s namespace | 读 `project.env` |
| `--context` | kubectl context | 读 `project.env` |
| `--kubeconfig` | kubeconfig 路径 | `~/.kube/config` |
| `--dry-run` | 只打印规划，不执行 | false |

**部署流程**：

```
IDLE / RUNNING
      │ dtk deploy
      ▼
前置检查（helm release 状态 / 依赖检查）
      ↓
INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                   ↓              ↓
             ROLLING_BACK ←───────┘  （失败自动回滚）
```

**自动处理**：
- `pending-rollback`：询问用户确认后自动清理
- `pending-install`：询问用户确认后删除 release 重新安装
- `failed`：提供回滚或重新部署选项
- SSA managedFields 冲突：自动清除后重试一次
- 镜像拉取失败：输出可操作的排查提示

---

## dtk resume

检查 K8s 实际状态，从中断点恢复。

```bash
dtk resume [flags]
```

适用于：进程意外终止、网络中断导致状态机停在中间状态。

**恢复逻辑**：
- K8s 实际服务正常 → 同步状态机为 RUNNING
- K8s 服务不存在 → 从头重新部署

---

## dtk rollback

手动触发 helm rollback，回到上一个版本。

```bash
dtk rollback [flags]
```

**注意**：
- revision=1 时（首次部署）无法回滚，报错提示
- 回滚失败时状态机保持 RUNNING，不转 CLEANING

---

## dtk release

打版本 tag，更新 VERSION，可选触发部署。

```bash
dtk release --version v1.0.0 [flags]
```

| Flag | 说明 | 默认值 |
|------|------|--------|
| `--version` | 版本号，格式 `v{major}.{minor}.{patch}` | 必填 |
| `--deploy` | 打完 tag 后触发 dtk deploy | false |
| `--push` | 是否推送 commit 和 tag 到远端 | true |

**完整流程**：校验格式 → 检查工作区干净 → 更新 project.env → git commit + tag + push → 可选 deploy

**注意**：zsh 下 `!` 有特殊含义，commit message 含 `!` 时用单引号：
```bash
git commit -m 'feat!: breaking change'
```

---

## dtk down

彻底下线服务，删除所有集群资源和本地状态文件。

```bash
dtk down [flags]
```

**执行内容**（二次确认后）：
1. 删除 ClusterRole / ClusterRoleBinding
2. 删除 namespace（含所有资源）
3. 删除本地状态文件 `~/.dtk/state/<project>/<ns>.json`

⚠️ 此操作不可逆，PVC 数据会丢失。

---

## dtk status

查看当前部署状态，三层信息一屏看清。

```bash
dtk status [flags]
```

| Flag | 说明 |
|------|------|
| `--history` | 同时显示最近 10 条状态转换历史 |

**输出示例**：

```
项目: myapp     命名空间: myapp     版本: v0.1.0

部署状态: ✅ RUNNING
  最后更新: 2026-03-27 09:15:48
  原因:     部署验证通过

K8s 实际状态:
  ✓ myapp-68dc84cb47-jppws       Running
  ✓ myapp-etcd-5bd65499ff-twvk8  Running
  ✓ myapp-postgres-0             Running

Helm:
  Release:   myapp
  Revision:  3
  Status:    deployed
  Updated:   2026-03-27 09:15:26
```

---

## dtk history

查看部署状态转换历史。

```bash
dtk history [-n N] [flags]
```

| Flag | 说明 | 默认值 |
|------|------|--------|
| `-n` | 显示最近 N 条，0 = 全部 | 20 |

**输出示例**：

```
项目: myapp  命名空间: myapp

  #    时间                   从               →  到               版本        原因
  ---  -------------------  --------------  -  --------------  --------  --------------------
  1    2026-03-27 09:14:51  IDLE            →  INITIALIZING    v0.1.0    开始部署 v0.1.0
  2    2026-03-27 09:14:51  INITIALIZING    →  DEPLOYING       v0.1.0    执行 helm upgrade
  3    2026-03-27 09:15:48  DEPLOYING       →  VALIDATING      v0.1.0    验证部署结果
  4    2026-03-27 09:15:48  VALIDATING      →  RUNNING         v0.1.0    部署验证通过
```

---

## dtk diff

对比两个版本的 helm values 差异。

```bash
dtk diff [flags]
```

| Flag | 说明 | 默认值 |
|------|------|--------|
| `--from` | 起始 revision | 最新 revision - 1 |
| `--to` | 目标 revision | 最新 revision |

**输出示例**：

```
对比 myapp revision 2 → 3

  ~ image.tag                              v0.1.9 → v0.1.10
  ~ replicaCount                           1 → 2
  + controller.enabled                     (added) → true
```

符号说明：`~` 变更 / `+` 新增 / `-` 删除

---

## dtk doctor

检查环境依赖，出问题前先诊断。

```bash
dtk doctor
```

**检查项**：

| 检查项 | 失败时 |
|--------|--------|
| Go 版本 ≥ 1.21 | error |
| Docker 运行状态 | error |
| kubectl 可用性 | error |
| helm 可用性 | error |
| K8s 集群连通性 | warn |
| project.env 存在性 | warn |
| REGISTRY_PREFIX 已填写 | error |

exit code：有 error 时返回 1，只有 warn 时返回 0。

---

## dtk controller start

在 controller pod 内部运行，不需要手动调用。

```bash
dtk controller start
```

启动 Reconciliation Controller：etcd Watch（指数退避重连）+ 8s 周期 Reconcile。

---

## 状态机速查

| 状态 | 含义 | 允许的后续操作 |
|------|------|----------------|
| `IDLE` | 未部署 | deploy |
| `INITIALIZING` | 初始化中 | - |
| `DEPLOYING` | 部署中 | - |
| `VALIDATING` | 验证中 | - |
| `RUNNING` | 正常运行 | deploy / rollback / down |
| `ROLLING_BACK` | 回滚中 | - |
| `CLEANING` | 清理中（首次失败） | - |
| `TERMINATED` | 已下线 | deploy |

非 `IDLE / RUNNING / TERMINATED` 状态时，`dtk deploy` 会被拒绝，用 `dtk resume` 恢复。

---

## 手动重置状态机

所有自动手段都失败时的最后手段：

```bash
python3 -c "
import json, os
p=os.path.expanduser('~/.dtk/state/<project>/<ns>.json')
d=json.load(open(p))
d['state']='IDLE'   # 或 RUNNING
d['reason']='手动重置'
json.dump(d,open(p,'w'),indent=2)
"
```

重置前先确认 K8s 实际状态与设置的值一致。

---
## 多服务部署

v1.0.0 起，`dtk deploy` 自动检测 `components.yaml` 里的服务数量和依赖关系，决定走单服务还是多服务路径。

**触发条件**：多层依赖 或 同层多个服务时自动进入多服务模式：

```
[16:43:48] 🗂  多服务模式：2 层，独立 helm release
[16:43:48] 📦 部署第 1 层（共 2 层，2 个服务）
[16:43:48] ⏭  跳过 chain-miner（CLI 工具，无 chart）
[16:43:48] 🏗  构建 wallet-service:v0.1.10

...

[16:43:55] ✓  推送 wallet-service 完成（7.7s）
[16:43:55] ⛵ helm upgrade web3-blitz-wallet-service
[16:44:11] ✓  helm upgrade web3-blitz-wallet-service 完成
[16:44:11] ✓  wallet-service 就绪

```
**helm release 命名**：`{project}-{service}`，每个服务独立 release，独立回滚。

**CLI 工具跳过**：`image` 为空且无对应 chart 目录的服务自动跳过，不参与 build/push/deploy。

**失败策略**：

```

单服务重试 3 次失败
    ↓
级联 rollback（失败服务 + 所有下游，逆拓扑顺序）
    ↓ 级联也失败
整组 rollback（所有已部署 release，逆序）
    ↓ 整组也失败
dtk down（清理 namespace）

```
---

## dtk ai-plan

AI 扫描仓库，自动生成 `configs/components.yaml`。

```bash
dtk ai-plan [flags]
```

| Flag             | 说明                          | 默认值 |
| ---------------- | ----------------------------- | ------ |
| `--suggest-only` | 只打印建议，不写入文件        | false  |
| `--desc`         | 补充描述，帮助 LLM 更准确分析 | 空     |

**配置方式**：

```bash
export DTK_LLM_PROVIDER=grok      # grok / claude / openai / doubao
export DTK_LLM_API_KEY=xai-xxx
export DTK_LLM_MODEL=grok-3       # 可选，有默认值
export DTK_LLM_ENDPOINT=          # 可选，私有化部署时覆盖
```

**示例**：

```bash
dtk ai-plan --suggest-only
dtk ai-plan --desc "BTC/ETH 充提币系统，wallet-service 是核心，基础设施不要列进来"
dtk ai-plan && dtk deploy
```

详见 [AI 使用手册](ai.md)。

---

## 状态机速查

| 状态           | 含义               | 允许的后续操作           |
| -------------- | ------------------ | ------------------------ |
| `IDLE`         | 未部署             | deploy                   |
| `INITIALIZING` | 初始化中           | -                        |
| `DEPLOYING`    | 部署中             | -                        |
| `VALIDATING`   | 验证中             | -                        |
| `RUNNING`      | 正常运行           | deploy / rollback / down |
| `ROLLING_BACK` | 回滚中             | -                        |
| `CLEANING`     | 清理中（首次失败） | -                        |
| `TERMINATED`   | 已下线             | deploy                   |

非 `IDLE / RUNNING / TERMINATED` 状态时，`dtk deploy` 会被拒绝，用 `dtk resume` 恢复。

---

## 手动重置状态机

所有自动手段都失败时的最后手段：

```bash
python3 -c "
import json, os
p=os.path.expanduser('~/.dtk/state/<project>/<ns>.json')
d=json.load(open(p))
d['state']='IDLE'   # 或 RUNNING
d['reason']='手动重置'
json.dump(d,open(p,'w'),indent=2)
"
```

重置前先确认 K8s 实际状态与设置的值一致。