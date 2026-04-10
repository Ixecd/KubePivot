# KubePivot (kp) 🚀

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链
> 一条命令生成合规项目骨架，一键完成安全扫描 + 多服务拓扑部署 + 自动自愈。

[![Go Version](https://img.shields.io/badge/go-1.21+-blue.svg)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![Version](https://img.shields.io/badge/version-v2.0.0-blue.svg)](https://github.com/Ixecd/KubePivot/releases)

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

`kp deploy` 一条命令：OPA 策略检查 → 迁移兼容性 → CVE 扫描 → AI 规划 → 拓扑排序 → build/push → 多服务独立 helm release → 状态追踪 → 耗时统计，全自动。

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

# 部署
kp deploy

# 查看状态
kp status
```

---

## 核心命令

### 项目脚手架
```bash
kp init --name myapp --module github.com/me/myapp
kp ai-plan                        # AI 规划服务配置（可选）
```

### 部署
```bash
kp deploy                          # 全量部署
kp deploy --changed-only           # 增量部署（git diff）
kp deploy --parallelism 4          # 控制并发
kp deploy --env prod               # 指定环境
kp deploy --preview                # 蓝绿 + Header 路由模板
kp resume                          # 从中断点恢复
kp rollback                        # 回滚
kp upgrade --target v1.2.0         # 跨版本升级（兼容性检查 + 迁移 + 部署）
```

### 状态 & 对比
```bash
kp status                          # 当前状态
kp status --env prod               # 指定环境
kp status --all-envs               # 跨集群统一视图
kp diff                            # helm values 变更对比
kp diff --drift                    # 配置漂移检测（三级分层）
kp diff --to-env prod              # 环境间配置对比
```

### 蓝绿发布
```bash
kp promote --service wallet        # 切换流量
kp warmup --service wallet \       # 线性预热（10%→50%→100%）
  --steps 10,50,100 \
  --interval 2m,5m \
  --err-threshold 0.01
```

### 数据库迁移
```bash
kp migrate status                  # 查看当前版本
kp migrate plan                    # 分析迁移风险
kp migrate run                     # 执行迁移（自动 PVC 快照保护）
kp migrate run --dry-run           # 预览
kp migrate fix-dirty               # 修复 dirty 状态
kp compat check                    # API 兼容性检查（oasdiff）
```

### Operation Sandbox（迁移原子性）
```bash
kp sandbox start --dry-run         # 查看执行计划
kp sandbox start                   # LOCKED→SNAPSHOTTING→SIMULATING→COMMITTING→RUNNING
kp sandbox status                  # 查看沙盒状态
kp sandbox unlock --force \        # 强制解锁（COMMITTING 阶段禁止）
  --reason "原因"
```

### PVC 管理
```bash
kp pvc backup                      # 创建 PVC 快照
kp pvc restore                     # 恢复快照
kp pvc list                        # 列出快照
```

### Secret 管理
```bash
kp secret rotate --secret myapp-secret --strategy graceful
kp secret cleanup --secret myapp-secret
kp secret audit                    # TLS 证书过期检测
kp secret sync --from vault \      # 从 Vault 同步
  --secret myapp-secret \
  --vault-path secret/data/myapp
```

### 多集群管理
```bash
kp context add --name prod \
  --context my-k8s \
  --namespace production
kp context list
kp deploy --env prod
kp status --all-envs
kp diff --from-env staging --to-env prod
```

### 企业合规
```bash
kp audit --format table            # 审计日志（SOC2/ISO27001）
kp audit --format csv --output audit.csv
kp policy add --name no-latest-tag \
  --file examples/policies/no-latest-tag.rego
kp policy check                    # 手动运行策略检查
```

### 混沌工程
```bash
kp chaos inject --service wallet \
  --kind pod-kill --duration 30s --dry-run
kp chaos inject --service wallet \
  --kind network-delay --latency 200ms
kp chaos list
kp chaos stop --uid <uid>
```

### 工具链
```bash
kp doctor                          # 环境检查（含 drift 告警）
kp doctor --perf                   # 性能基准（Apiserver P99）
kp scan                            # CVE 扫描（trivy）
kp history --export json           # 部署历史导出
kp network gen                     # 生成跨 namespace NetworkPolicy 模板
kp version                         # 查看版本
kp update                          # 自动更新
kp plugin install <name>           # 安装插件
```

---

## 架构

```
kp CLI
├── 项目脚手架（kp init）
├── 部署引擎（kp deploy）
│   ├── AI 规划（可选，grok/openai）
│   ├── DAG 拓扑排序（Kahn 算法）
│   ├── 并行部署（semaphore + goroutine）
│   └── 状态机（etcd 持久化）
├── A2 Reconciliation Controller
│   ├── Leader Election（etcd 分布式锁）
│   ├── WorkQueue（三集合去重）
│   ├── Drift Sync Loop（30s 扫描）
│   └── Sandbox GC Loop（5m 扫描）
├── 企业工具链
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

**只保护，不越权**：kp 只对自己声明所有权的字段（image/env/ports/resources）执行 force-sync，不干预 Istio/HPA/云厂商注入的字段，避免无限套娃。

**降级不阻断**：Trivy 未安装跳过扫描，OPA 未安装跳过策略检查，Prometheus 不可达跳过 error rate 监控，CSI 未安装跳过 PVC 快照——不因为可选组件缺失而阻止核心流程。

**确定性优先**：部署顺序由 DAG 决定，漂移治理由规则决定，不依赖 AI 做关键路径决策。

---

## 文档

- [快速开始](docs/guide/zh-CN/quickstart.md)
- [命令参考](docs/guide/zh-CN/commands.md)
- [常见问题](docs/guide/zh-CN/gotchas.md)
- [架构设计](docs/design/architecture.md)
- [状态机设计](docs/design/state-machine.md)
- [Operation Sandbox](docs/design/sandbox.md)
- [蓝绿发布](docs/design/bluegreen.md)
- [OPA 策略示例](examples/policies/README.md)

---

## Companion 项目

[web3-blitz](https://github.com/Ixecd/web3-blitz) — KubePivot 的端到端验证项目，BTC/ETH 充提，跑在 k3s + OrbStack 上。

---

## License

MIT © 2026 qc（Ixecd）
