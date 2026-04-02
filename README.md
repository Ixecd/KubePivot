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

`kp deploy` 一条命令：迁移检查 → CVE 扫描 → AI 规划 → 拓扑排序 → build/push → 多服务独立 helm release → 状态追踪 → 耗时统计，全自动。

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
export KP_LLM_PROVIDER=grok
export KP_LLM_API_KEY=xai-xxx
kp ai-plan

# 部署
kp deploy
```

---

## 命令

| 命令 | 说明 |
|------|------|
| `kp init` | 生成完整 Go 项目骨架（含安全基线） |
| `kp deploy` | 迁移检查 + CVE 扫描 + 多服务拓扑部署 |
| `kp upgrade` | 跨版本全链路升级（DB迁移 + 部署 + 健康校验） |
| `kp migrate` | DB 迁移状态检查 + 破坏性变更分析 + 执行迁移 |
| `kp compat` | API 兼容性检测（oasdiff） |
| `kp diff` | 对比两个 revision 的 helm values（含迁移建议） |
| `kp promote` | 蓝绿发布流量切换 |
| `kp scan` | 独立 CVE 扫描（Trivy） |
| `kp doctor` | 环境检查 + 安全检查 + 性能检查 + 跨域嗅探 |
| `kp doctor --perf` | 测试 Apiserver P99 延迟，推荐并发度 |
| `kp ai-plan` | AI 扫描仓库，生成 components.yaml |
| `kp status` | 查看所有 helm release + StatefulSet pod 详情 |
| `kp rollback` | 拓扑逆序回滚所有服务 |
| `kp resume` | 从中断点恢复部署 |
| `kp release` | 打版本 tag，可选触发部署 |
| `kp down` | 彻底下线，删除所有集群资源 |
| `kp history` | 查看状态转换历史（支持 --export json/csv） |
| `kp pvc` | PVC 快照备份和恢复（需要 CSI） |
| `kp secret` | Secret 轮转 / 清理 / 审计 |
| `kp network gen` | 生成跨 namespace NetworkPolicy 模板 |

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
| RBAC 最小权限 | controller 只授予必要资源（含 leases 读写权） |
| CVE 扫描 | `kp scan` + `kp deploy` 自动集成 Trivy |
| 明文密码检测 | `kp doctor` 扫描 values.yaml |
| DB 迁移安全 | `kp deploy` 前自动检测破坏性变更，阻断部署 |
| Secret 轮转 | `kp secret rotate` 双密码过渡期，零宕机 |

---

## kp deploy 流程

```
迁移兼容性检查（破坏性变更阻断）
    ↓
Secret 检查（缺失则警告）
    ↓
CVE 扫描（Trivy）
    ↓
BuildLayers（Kahn 拓扑排序）→ []Layer
    ↓
for each 层级（同层并行 --parallelism 控制，层间串行）：
    build → push → helm upgrade --install → rollout status
    ↓ 失败
    级联 rollback → 整组 rollback → kp down
    ↓
部署耗时统计（build/push/helm/rollout 各阶段）
```

状态机：

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                          ↓              ↓
                    ROLLING_BACK ←───────┘
                      CLEANING → IDLE
```

---

## kp secret rotate

```bash
# 立即轮转（非 DB 类 Secret）
kp secret rotate --secret wallet-service-secret

# 优雅轮转（DB 类 Secret，双密码过渡）
kp secret rotate --secret wallet-service-secret --strategy=graceful

# 确认 DB 端已禁用旧密码后清理
kp secret cleanup --secret wallet-service-secret

# TLS 证书过期审计
kp secret audit
```

---

## 跨 namespace 依赖

`components.yaml` 支持声明跨 namespace 依赖：

```yaml
components:
  - name: wallet-service
    depends_on:
      - web3-blitz-postgres      # 同 namespace（参与 DAG 排序）
      - kube-system/coredns      # 跨 namespace（只嗅探，不参与 DAG）
```

```bash
# kp doctor 自动嗅探跨 namespace 依赖是否存在
kp doctor

# 生成跨 namespace NetworkPolicy 模板（不自动 apply）
kp network gen
kubectl apply -f deployments/myapp/network/   # 用户审查后手动执行
```

---

## 可观测性

```bash
# 结构化 JSON 日志（接入 ELK/Loki）
LOG_FORMAT=json kp deploy 2>deploy.log

# 调试模式
LOG_LEVEL=debug kp deploy

# 导出部署历史
kp history --export json
kp history --export csv

# Apiserver 性能检测
kp doctor --perf
kp doctor --perf --context prod-cluster
```

---

## A2 Reconciliation Controller（高可用）

Controller 支持多副本 Leader Election，任意节点故障不影响自愈：

```
kp deploy → 写状态到 etcd → 退出
                  ↓
kubepivot-controller（3 副本，etcd Leader Election）
    ├── Leader 节点运行 Reconcile Loop
    ├── etcd Watch（事件驱动，WorkQueue 去重防风暴）
    └── 8s 周期 Reconcile（兜底）
            ↓
        检测资源缺失 → helm rollback → 自动恢复（~13s）
```

---

## AI 规划

```bash
export KP_LLM_PROVIDER=grok      # grok / claude / openai / doubao
export KP_LLM_API_KEY=xai-xxx
kp ai-plan --suggest-only
kp ai-plan --desc "BTC/ETH 充提币系统"
```

支持 Grok / Claude / OpenAI / 豆包，`KP_LLM_ENDPOINT` 支持私有化部署。

---

## 开发

```bash
# 一键 build + test + install
make dev

# 单独安装
make install

# KWOK 压测（需要 kwokctl）
./scripts/bench/kwok_dag_bench.sh 500 20
```

---

## 版本路线图

```
v1.0.0  多服务独立 release + 拓扑排序 + 级联 rollback        🏆
v1.1.0  controller e2e + status 多 release + rollback 进度   🏆
v1.2.0  安全合规基线（Secret/Pod 安全/NetworkPolicy/doctor）  🏆
v1.3.0  供应链安全（Trivy CVE + cosign + SBOM）              🏆
v1.4.0  跨版本迁移（DB迁移感知 + API兼容 + kp upgrade）      🏆
v1.5.0  StatefulSet 支持（etcd 健康监控 + pod 详情）          🏆
v1.5.1  蓝绿 e2e + 状态机 P1 bug 修复                        🏆
v1.5.2  Secret 轮转（双密码过渡 + TLS 过期审计）             🏆
v1.6.0  Controller HA + 大规模场景 + 可观测性                🏆  ← 当前
v1.7.0  状态漂移治理（SSA FieldManager + force-sync）
v1.8.0  Operation Sandbox + DB 迁移原子性 + 蓝绿增强
v1.9.0  多集群联邦 + 企业合规
v2.0.0  企业级插件平台（Vault + Web UI + Chaos Mesh）
```

---

## 文档

- [Quickstart](docs/guide/zh-CN/quickstart.md)
- [部署指南](docs/guide/zh-CN/deploy.md)
- [命令参考](docs/guide/zh-CN/commands.md)
- [已知坑和注意事项](docs/guide/zh-CN/gotchas.md)
- [整体架构设计](docs/design/architecture.md)
- [状态机设计](docs/design/state-machine.md)
- [A2 Controller 设计](docs/design/controller.md)
- [数据库迁移设计](docs/design/migrate.md)
- [蓝绿发布设计](docs/design/bluegreen.md)
- [跨版本升级设计](docs/design/upgrade.md)

---

## License

MIT
