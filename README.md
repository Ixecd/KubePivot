# KubePivot (kp) 🚀

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链
> 一条命令生成合规项目骨架，一键完成安全扫描 + 多服务拓扑部署 + 自动自愈。

[![Go Version](https://img.shields.io/badge/go-1.21+-blue.svg)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![Tests](https://img.shields.io/badge/tests-143%20passing-brightgreen.svg)](/)

---

## 为什么是 KubePivot？

搭一个新的 Go 服务通常需要：手写 Dockerfile、配 Helm chart、接 postgres/etcd、写迁移脚本、配 RBAC、搞 Prometheus、还要满足各种安全合规要求……这些和业务无关的工作往往要花掉一两天。

**KubePivot 把这些全部模板化，并天生内置合规基线。**

`kp init` 一条命令，完整可部署的项目骨架生成完毕，自带：
- Pod Security Context（非 root、只读文件系统）
- Network Policy（默认拒绝入站）
- 资源 limits
- Secret 管理脚本
- RBAC 最小权限

`kp deploy` 一条命令：CVE 扫描 → AI 规划 → 拓扑排序 → build/push → 多服务独立 helm release → 状态追踪，全自动。

---

## 快速开始

```bash
# 安装
go install github.com/Ixecd/kubepivot/cmd/kp@latest

# 生成项目
kp init --name myapp --module github.com/me/myapp
cd myapp

# 创建 K8s Secret（敏感变量不进 git）
./scripts/create-secret.sh

# 编辑部署配置
vim configs/project.env   # 填 REGISTRY_PREFIX、KUBE_CONTEXT

# AI 规划服务配置（可选）
export DTK_LLM_PROVIDER=grok
export DTK_LLM_API_KEY=xai-xxx
kp ai-plan

# 部署
kp deploy
```

---

## 命令

| 命令 | 说明 |
|------|------|
| `kp init` | 生成完整 Go 项目骨架（含安全基线） |
| `kp deploy` | CVE 扫描 + 多服务拓扑部署 |
| `kp scan` | 独立 CVE 扫描（Trivy） |
| `kp doctor` | 环境检查 + 安全检查 |
| `kp ai-plan` | AI 扫描仓库，生成 components.yaml |
| `kp status` | 查看所有 helm release 状态 |
| `kp rollback` | 拓扑逆序回滚所有服务 |
| `kp resume` | 从中断点恢复部署 |
| `kp release` | 打版本 tag，可选触发部署 |
| `kp down` | 彻底下线，删除所有集群资源 |
| `kp history` | 查看状态转换历史 |
| `kp diff` | 对比两个 revision 的 helm values |
| `kp migrate` | 数据库迁移状态检查 + 破坏性变更分析 |
| `kp compat`  | API 兼容性检测（oasdiff） |
| `kp promote` | 蓝绿发布流量切换 |

---

## kp init 生成内容

```
myapp/
├── cmd/myapp/               # 服务入口（含 /healthz 路由）
├── internal/
│   ├── api/                 # HTTP handler + 路由
│   ├── auth/                # JWT + RBAC 中间件
│   ├── db/                  # 数据库连接 + 迁移（golang-migrate）
│   ├── metrics/             # Prometheus 指标
│   └── pkg/code/            # 业务错误码
├── deployments/myapp/
│   ├── myapp-postgres/      # StatefulSet + PVC
│   ├── myapp-etcd/          # StatefulSet + PVC
│   ├── myapp/               # 业务服务（Pod 安全 + NetworkPolicy + limits）
│   └── myapp-controller/    # A2 自愈控制器（默认 disabled）
├── scripts/
│   └── create-secret.sh     # 幂等创建 K8s Secret
├── configs/
│   ├── project.env          # 部署配置
│   ├── components.yaml      # 服务列表 + 依赖关系
│   └── resources.yaml       # controller 监控资源
├── monitoring/              # Prometheus + Alertmanager + Grafana
└── handoff/
    ├── HANDOFF.md           # 项目上下文交接文档
    └── AI-CODING-GUIDE.md   # AI 编码约束指南
```

---

## 安全基线（开箱即用）

| 特性 | 实现 |
|------|------|
| Secret 不进 git | `create-secret.sh` + 部署前自动检查 |
| Pod 安全 | runAsNonRoot / readOnlyRootFilesystem / allowPrivilegeEscalation=false |
| 网络隔离 | NetworkPolicy 默认拒绝入站 |
| 资源限制 | requests + limits 默认值 |
| RBAC 最小权限 | controller 只授予必要资源 |
| CVE 扫描 | `kp scan` + `kp deploy` 自动集成 Trivy |
| 明文密码检测 | `kp doctor` 扫描 values.yaml |

---

## kp deploy 流程

```
CVE 扫描（Trivy）
    ↓
Secret 检查（缺失则警告）
    ↓
BuildLayers（Kahn 拓扑排序）→ []Layer
    ↓
for each 层级（同层并行，层间串行）：
    build → push → helm upgrade --install → rollout status
    ↓ 失败
    级联 rollback → 整组 rollback → kp down
```

状态机：

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                          ↓              ↓
                    ROLLING_BACK ←───────┘
                      CLEANING → IDLE
```

---

## A2 Reconciliation Controller

Controller 作为独立 Deployment 运行在 K8s 里，统一镜像 `kubepivot-controller`：

```
kp deploy → 写状态到 etcd → 退出
                  ↓
kubepivot-controller（常驻 pod）
    ├── etcd Watch（事件驱动，指数退避重连）
    └── 8s 周期 Reconcile（兜底）
            ↓
        检测资源缺失 → helm rollback → 自动恢复（~13s）
```

---

## AI 规划

```bash
export DTK_LLM_PROVIDER=grok      # grok / claude / openai / doubao
export DTK_LLM_API_KEY=xai-xxx
kp ai-plan --suggest-only
kp ai-plan --desc "BTC/ETH 充提币系统"
```

支持 Grok / Claude / OpenAI / 豆包，`DTK_LLM_ENDPOINT` 支持私有化部署。

---

## 版本路线图

```
v1.0.0  多服务独立 release + 拓扑排序 + 级联 rollback  🏆
v1.1.0  controller e2e + status 多 release + rollback 进度
v1.2.0  安全合规基线（Secret/Pod 安全/NetworkPolicy/doctor 安全检查）
v1.3.0  供应链安全（Trivy CVE 扫描集成）  ← 当前
v2.0.0  企业级插件（Vault + 审计日志 + OPA）
```

---

## 文档

- [Quickstart](docs/guide/zh-CN/quickstart.md)
- [部署指南](docs/guide/zh-CN/deploy.md)
- [AI 使用手册](docs/guide/zh-CN/ai.md)
- [命令参考](docs/guide/zh-CN/commands.md)
- [已知坑和注意事项](docs/guide/zh-CN/gotchas.md)
- [整体架构设计](docs/design/architecture.md)
- [多服务支持设计](docs/design/multi-service.md)
- [状态机设计](docs/design/state-machine.md)
- [A2 Controller 设计](docs/design/controller.md)

---

## License

MIT
