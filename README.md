# KubePivot (kp) 🚀

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链
> 一条命令生成合规项目骨架，一键完成安全扫描 + 多服务拓扑部署 + 自动自愈。

[![Go Version](https://img.shields.io/badge/go-1.21+-blue.svg)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![Version](https://img.shields.io/badge/version-v2.3.0-blue.svg)](https://github.com/Ixecd/KubePivot/releases)

---

## 为什么是 KubePivot？

搭一个新的 Go 服务通常需要：手写 Dockerfile、配 Helm chart、接 postgres/etcd、写迁移脚本、配 RBAC、搞 Prometheus、还要满足各种安全合规要求……这些和业务无关的工作往往要花掉一两天。

**KubePivot 把这些全部模板化，并天生内置合规基线。**

`kp init` 一条命令，完整可部署的项目骨架生成完毕，自带：
- Pod Security Context（非 root、只读文件系统）
- Network Policy（默认拒绝入站）
- 资源 limits / Secret 管理 / RBAC 最小权限
- 错误码体系（`internal/pkg/code/` + `internal/pkg/response/`）
- GitOps hooks（`.githooks/post-receive`，git push = kp deploy）

`kp deploy` 一条命令：OPA 策略检查 → 迁移兼容性 → CVE 扫描 → AI 规划 → 拓扑排序 → build/push → 多服务独立 helm release → 状态追踪 → 耗时统计，全自动。

---

## 快速开始

```bash
# 安装
go install github.com/Ixecd/kubepivot/cmd/kp@latest

# 生成项目（零配置，go install 后直接可用）
kp init --name myapp --module github.com/me/myapp
cd myapp

# 安装工具链 + 注册 git hooks
make tools

# 部署（自动创建 dev Secret）
vim configs/project.env   # 填 REGISTRY_PREFIX
kp deploy

# 查看状态
kp status
```

---

## 核心命令

### 项目脚手架
```bash
kp init --name myapp --module github.com/me/myapp
kp sync                    # 升级框架文件，不动业务代码
kp sync --dry-run          # 预览变更
kp ai-plan                 # AI 规划服务配置（可选）
```

### 部署
```bash
kp deploy                          # 全量部署（自动创建 dev Secret）
kp deploy --changed-only           # 增量部署（git diff）
kp deploy --parallelism 4          # 控制并发
kp deploy --env prod               # 指定环境
kp deploy --preview                # 蓝绿 + Header 路由模板
kp resume                          # 从中断点恢复
kp rollback                        # 回滚
kp upgrade --target v1.2.0         # 跨版本升级
```

### 状态 & 对比
```bash
kp status                          # 当前状态
kp status --all-envs               # 跨集群统一视图
kp diff --drift                    # 配置漂移检测（三级分层）
kp diff --to-env prod              # 环境间配置对比
```

### 蓝绿发布

KubePivot 提供两种蓝绿模式，按场景选择：

#### 命令式（v2.0+，原有）

```bash
kp deploy --bluegreen              # 部署到非活跃 slot
kp promote --service wallet        # 手动切换流量
kp warmup --service wallet \
  --steps 10,50,100 --interval 2m,5m --err-threshold 0.01
```

#### 声明式（v2.6+，推荐 GitOps 场景）

在 `configs/resources.yaml` 声明流量配置：

```yaml
traffic:
  kind: Ingress              # 可选：Ingress / Gateway / 自动检测
  strategy: blue-green
  refs:
    name: wallet-ingress
  routes:
    - service: wallet-blue
      weight: 100
    - service: wallet-green
      weight: 0
```

切换流量：

```bash
# 修改 routes 的 weight（blue: 100→0, green: 0→100）
vim configs/resources.yaml

# 触发 sandbox commit：
#   LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
#   COMMITTING 内部分两步：helm upgrade + 流量切换
#   失败自动 RESTORING
kp sandbox commit
```

完整 demo：[`docs/example-blue-green/`](docs/example-blue-green/README.md)（< 2 分钟跑通）。

设计文档：[`docs/design/traffic-layer.md`](docs/design/traffic-layer.md)。

### 数据库迁移
```bash
kp migrate status / plan / run / fix-dirty
kp compat check                    # API 兼容性检查（oasdiff）
```

### Operation Sandbox（迁移原子性）
```bash
kp sandbox start --dry-run
kp sandbox start                   # LOCKED→SNAPSHOTTING→SIMULATING→COMMITTING→RUNNING
kp sandbox status / unlock
```

### Secret 管理
```bash
kp secret rotate --secret myapp-secret --strategy graceful
kp secret sync --from vault --secret myapp-secret --vault-path secret/data/myapp
kp secret audit                    # TLS 证书过期检测
```

### 多集群管理
```bash
kp context add --name prod --context my-k8s --namespace production
kp deploy --env prod
kp status --all-envs
kp diff --from-env staging --to-env prod
```

### 企业合规
```bash
kp audit --format table            # SOC2/ISO27001 审计导出
kp policy add --name no-latest-tag --file examples/policies/no-latest-tag.rego
kp policy check
```

### 混沌工程
```bash
kp chaos inject --service wallet --kind pod-kill --duration 30s --dry-run
kp chaos inject --service wallet --kind network-delay --latency 200ms
kp chaos list / stop / status
```

### 工具链
```bash
kp doctor / kp doctor --perf       # 环境检查 + 性能基准
kp scan                            # CVE 扫描（trivy）
kp version / kp update             # 版本管理
kp plugin install <name>           # 安装插件
```

---

## 架构

```
kp CLI（26 子命令）
├── 项目脚手架（kp init + kp sync）
├── 部署引擎（kp deploy）
│   ├── AI 规划（可选，grok/openai）
│   ├── DAG 拓扑排序（Kahn 算法）
│   ├── 并行部署（semaphore + goroutine）
│   └── 状态机（etcd 持久化）
├── A2 Reconciliation Controller
│   ├── Leader Election（etcd 分布式锁）
│   ├── WorkQueue（三集合去重）
│   ├── Drift Sync Loop（30s 扫描）
│   ├── Sandbox GC Loop（5m 扫描）
│   └── CRD 自愈（healCRDApply）
└── 企业工具链
    ├── 迁移引擎（golang-migrate / Atlas）
    ├── 蓝绿发布 + Preview + Warmup
    ├── Operation Sandbox（原子性迁移）
    ├── OPA 策略引擎
    ├── 多集群管理
    └── 混沌工程（Chaos Mesh）
```

---

## 状态机

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                           ↓
                      ROLLING_BACK → RUNNING

RUNNING → LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
                                                    ↓
                                                RESTORING → IDLE
```

COMMITTING 阶段禁止 force-unlock（DB 正在迁移，强制解锁会导致数据不一致）。

---

## 设计原则

**只保护，不越权**：kp 只对自己声明所有权的字段执行 force-sync，不干预 Istio/HPA/云厂商注入的字段。

**降级不阻断**：Trivy/OPA/Prometheus/CSI 任意一个缺失，核心流程继续运行。

**确定性优先**：部署顺序由 DAG 决定，漂移治理由规则决定，不依赖 AI 做关键路径决策。

---

## GitOps 愿景

`kp init` 生成的项目天生支持 GitOps：

```bash
make tools          # 自动注册 .githooks/post-receive 到 .git/hooks/
git push            # → post-receive → kp deploy --changed-only → RUNNING
```

Git 本身就是部署系统的控制平面。详见 [GITOPS-MANIFESTO.md](GITOPS-MANIFESTO.md)。

---

## 项目边界（Out of Scope）

KubePivot 是 **应用层** 工具——专注于 Go 服务的脚手架、部署、自愈。
它**不做**以下事情：

| 不做的事 | 由谁做 |
|---------|--------|
| K8s 集群本身的生命周期管理（创建 / 升级 / 销毁 cluster） | 未来的 [Cloud](#) 项目 |
| K8s NodePool 管理（不同机型 / 标签 / taints / 弹性伸缩） | 未来的 [Cloud](#) 项目 |
| 裸金属 / 虚机 / 数据中心规划 | 未来的 [Cloud](#) 项目 |
| 业务流量负载均衡（节点抽象 + 调度算法） | KubePivot v2.6.0 (流量层) |

**为什么这样划分**：

- KubePivot 的定位是"研发脚手架 + 部署运维工具链"，介入层在 K8s 之上
- 节点级管理属于"基础设施"域，由独立的 Cloud 项目专门处理
- 边界清晰能让两个项目都保持纯粹——KubePivot 不碰节点，Cloud 不碰应用

KubePivot 假设 K8s 集群已经存在且可用（任何来源都行：orbstack / k3s / EKS / GKE / 自建集群）。

---

## 文档

- [快速开始](docs/guide/zh-CN/quickstart.md)
- [命令参考](docs/guide/zh-CN/commands.md)
- [常见问题](docs/guide/zh-CN/gotchas.md)
- [架构设计](docs/design/architecture.md)
- [状态机设计](docs/design/state-machine.md)
- [Operation Sandbox](docs/design/sandbox.md)
- [GitOps 宣言](GITOPS-MANIFESTO.md)

---

## Companion 项目

[web3-blitz](https://github.com/Ixecd/web3-blitz) — KubePivot 的端到端验证项目，BTC/ETH 充提，跑在 k3s + OrbStack 上。

**计划中**：Cloud 项目（K8s 集群与节点池管理工具）—— 与 KubePivot 互补，
专注基础设施层，让 KubePivot 可以纯粹聚焦于应用层。

---

## License

MIT © 2026 qc（Ixecd）
