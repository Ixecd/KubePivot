# KubePivot 命令参考

> 版本：v2.0.0
> CLI 二进制：`kp`
> 模块：`github.com/Ixecd/kubepivot`

---

## 全局约定

大多数命令支持以下通用 flags：

```
--namespace    <ns>       kubernetes namespace（默认读 project.env KUBE_NAMESPACE）
--context      <ctx>      kubernetes context（默认读 project.env KUBE_CONTEXT）
--kubeconfig   <path>     kubeconfig 文件路径（默认 ~/.kube/config）
--env          <name>     指定部署环境（kp context add 配置的 env）
```

---

## kp init

生成完整可部署的项目骨架。

```bash
kp init --name <name> --module <go-module>
```

| Flag | 说明 | 默认 |
|------|------|------|
| `--name` | 项目名（必填） | — |
| `--module` | Go module 路径（必填） | — |
| `--output` | 输出目录 | `./<name>` |
| `--template` | 模板根目录 | 内置 |

生成内容：`cmd/` `internal/` `migrations/` `configs/` `deployments/` `scripts/`，自带 Pod Security Context、Network Policy、资源 limits、RBAC 最小权限。

---

## kp deploy

多服务部署主命令。流程：OPA 策略检查 → 迁移兼容性 → CVE 扫描 → AI 规划 → DAG 拓扑排序 → 逐层 build/push/helm upgrade → 状态追踪。

```bash
kp deploy [flags]
```

| Flag | 说明 | 默认 |
|------|------|------|
| `--dry-run` | 只打印规划，不执行 | false |
| `--changed-only` | 只部署有 git diff 的服务（HEAD~1 → HEAD）| false |
| `--parallelism` | 同层最大并发数（0=不限，大规模集群建议 4-8）| 0 |
| `--env` | 指定部署环境 | — |
| `--preview` | 蓝绿部署后生成 Header 路由模板（Istio/Nginx/降级 README）| false |
| `--sign` | 部署后对镜像进行 cosign keyless 签名 | false |
| `--force-migrate` | 忽略破坏性迁移警告强制部署（不推荐）| false |

```bash
kp deploy
kp deploy --dry-run
kp deploy --changed-only --parallelism 4
kp deploy --env prod
kp deploy --preview               # 蓝绿 + Header 路由模板
LOG_FORMAT=json kp deploy         # 结构化日志
```

---

## kp resume

从中断点恢复部署。检查 K8s 实际状态，服务正常则同步 RUNNING，服务不存在则从头部署。

```bash
kp resume [flags]
```

---

## kp rollback

手动触发整组 helm rollback，按拓扑逆序回滚所有 release。

```bash
kp rollback [flags]
```

---

## kp upgrade

跨版本升级全链路：兼容性检查 → DB 迁移 → 服务部署 → 健康校验。

```bash
kp upgrade [flags]
```

| Flag | 说明 | 默认 |
|------|------|------|
| `--target` | 目标版本号 | 当前 VERSION |
| `--service` | 只升级指定服务 | 所有服务 |
| `--dry-run` | 预览升级计划 | false |
| `--force` | 忽略兼容性警告 | false |
| `--no-healthcheck` | 跳过升级后健康检查 | false |

升级前自动触发 PVC 快照（有 CSI 才执行），失败后双层回滚（pvc restore + helm rollback）。

---

## kp status

查看部署状态，包括状态机状态、K8s pod 状态、helm release 信息、StatefulSet 详情。

```bash
kp status [flags]
```

| Flag | 说明 |
|------|------|
| `--env` | 指定环境 |
| `--all-envs` | 跨集群统一视图（动态列宽） |
| `--history` | 显示状态转换历史 |

```bash
kp status
kp status --env prod
kp status --all-envs
kp status --history
```

---

## kp down

彻底下线：删除所有 helm release + namespace。

```bash
kp down [flags]
```

⚠️ 会删除 namespace 下所有资源包括 Secret，重新部署前必须重建 Secret。

---

## kp diff

对比 helm values 变更、配置漂移、环境间差异。

```bash
kp diff [flags]
```

| Flag | 说明 | 默认 |
|------|------|------|
| `--service` | 指定服务名 | 所有服务 |
| `--from` | 起始 revision | latest-1 |
| `--to` | 目标 revision | latest |
| `--drift` | 检测配置漂移（需要 helm-diff 插件）| false |
| `--migrate` | 同时分析迁移建议 | false |
| `--env` | 指定环境 | — |
| `--from-env` | 源环境（留空=local）| — |
| `--to-env` | 目标环境 | — |

```bash
kp diff                              # 最新两个 revision 对比
kp diff --service wallet-service     # 指定服务
kp diff --drift                      # 配置漂移检测（三级分层）
kp diff --drift --service wallet-service
kp diff --to-env staging             # local vs staging 配置对比
kp diff --from-env staging --to-env prod
```

漂移三级分类：
- ❌ 硬冲突：kp 拥有字段所有权，将 force-sync
- ⚠️ 受控偏离：透明展示，不强制同步
- ℹ️ 已豁免：no-sync-fields 声明豁免

---

## kp promote

蓝绿发布：切换 Service selector 到 inactive slot，全量流量切换。

```bash
kp promote [flags]
```

| Flag | 说明 |
|------|------|
| `--service` | 指定服务（留空=所有蓝绿服务）|

```bash
kp promote
kp promote --service wallet-service
```

---

## kp warmup

线性流量预热：按步骤调整 inactive slot 权重，每步监控 error rate，超阈值自动回滚。

```bash
kp warmup [flags]
```

| Flag | 说明 | 默认 |
|------|------|------|
| `--service` | 服务名（必填）| — |
| `--steps` | 权重步骤（逗号分隔）| `10,50,100` |
| `--interval` | 每步等待时间 | `2m,5m` |
| `--err-threshold` | 错误率阈值（超过自动回滚）| `0.01`（1%）|
| `--dry-run` | 只打印计划 | false |

```bash
kp warmup --service wallet-service --dry-run
kp warmup --service wallet-service \
  --steps 10,50,100 --interval 2m,5m --err-threshold 0.01
```

需要 Istio 或 Nginx Ingress，否则打印指引。需要 Prometheus + `http_requests_total` 指标，否则跳过 error rate 检查。

---

## kp sandbox

Operation Sandbox：保证 DB 迁移和服务升级的原子性。

```bash
kp sandbox <subcommand> [flags]
```

### kp sandbox start

```bash
kp sandbox start [--dry-run] [flags]
```

执行 5 阶段：LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING。失败后双层回滚。

`--dry-run`：只打印执行计划，不实际执行。

### kp sandbox status

```bash
kp sandbox status [flags]
```

查看当前沙盒阶段，COMMITTING 阶段会显示 force-unlock 禁止警告。

### kp sandbox unlock

```bash
kp sandbox unlock --force --reason "<原因>" [flags]
```

强制解锁（COMMITTING 阶段永远禁止）。`--reason` 必填。

---

## kp migrate

数据库迁移管理。支持 golang-migrate 和 Atlas，自动检测。

```bash
kp migrate <subcommand> [flags]
```

### kp migrate status

查看当前迁移版本、工具、dirty 状态。

### kp migrate plan

分析待执行迁移风险（安全/潜在风险/破坏性）。

### kp migrate run

```bash
kp migrate run [--dry-run] [--full-sql] [--target <version>] [flags]
```

执行迁移。`--dry-run` 预览 SQL 不执行。执行前自动触发 PVC 快照（有 CSI 才执行），失败后双层回滚。

### kp migrate fix-dirty

交互式 dirty 状态修复指引（只保护，不越权——不自动修改迁移表，打印命令让用户确认执行）。

---

## kp compat

API 兼容性检查（依赖 oasdiff）。

```bash
kp compat check [flags]
```

---

## kp pvc

PVC 快照管理（需要 CSI VolumeSnapshot）。

```bash
kp pvc <subcommand> [flags]
```

| 子命令 | 说明 |
|--------|------|
| `backup` | 创建 PVC 快照 |
| `restore` | 恢复最新快照 |
| `list` | 列出所有快照 |

---

## kp secret

Secret 生命周期管理。

```bash
kp secret <subcommand> [flags]
```

### kp secret rotate

```bash
kp secret rotate --secret <name> [--strategy immediate|graceful] [flags]
```

- `immediate`：直接 rollout restart 所有引用服务（默认）
- `graceful`：双密码过渡期（DB 类 Secret 零宕机轮转）

### kp secret cleanup

```bash
kp secret cleanup --secret <name> [flags]
```

删除 graceful 轮转遗留的 `*_OLD` 字段。

### kp secret audit

```bash
kp secret audit [flags]
```

检测所有 TLS 类型 Secret 的证书过期时间（30天 warn，7天 error）。

### kp secret sync

```bash
kp secret sync --from vault \
  --secret <name> \
  --vault-path <path> \
  [--vault-addr <addr>] \
  [--vault-token <token>] \
  [--dry-run] \
  [flags]
```

从 Vault KV v2 同步到 K8s Secret（net/http 实现，幂等 apply）。`VAULT_TOKEN` 和 `VAULT_ADDR` 可通过环境变量提供。

---

## kp context

多集群环境管理。

```bash
kp context <subcommand> [flags]
```

### kp context add

```bash
kp context add --name <name> \
  [--kubeconfig <path>] \
  [--context <ctx>] \
  [--namespace <ns>] \
  [--registry <prefix>] \
  [--arch <arm64|amd64>]
```

配置存储在 `~/.kp/envs/<name>.yaml`。`"local"` 是保留字，无需创建。

### kp context list

列出所有已配置环境（含 namespace、context、kubeconfig）。

### kp context show

```bash
kp context show <name>
```

### kp context remove

```bash
kp context remove <name>
```

---

## kp audit

统一审计日志导出（SOC2/ISO27001）。聚合三个来源：deploy 历史（状态机）、secret 操作、drift 记录（etcd）。

```bash
kp audit [flags]
```

| Flag | 说明 | 默认 |
|------|------|------|
| `--format` | 输出格式：jsonl \| csv \| table | `jsonl` |
| `--output` | 输出文件路径（留空=stdout）| — |
| `--since` | 起始时间（如 2026-04-01）| 全部 |
| `--source` | 来源过滤：deploy \| secret \| drift | 全部 |

```bash
kp audit --format table
kp audit --format jsonl --since 2026-04-01
kp audit --format csv --output audit.csv
kp audit --source deploy
kp audit --source secret
```

---

## kp policy

OPA 策略引擎。策略存储在 `~/.kp/policies/*.rego`，`kp deploy` 前自动运行。未安装 `opa` 则静默跳过。

```bash
kp policy <subcommand> [flags]
```

### kp policy add

```bash
kp policy add --name <name> --file <path>
```

### kp policy list

列出已安装策略（含行数、路径）。

### kp policy remove

```bash
kp policy remove <name>
```

### kp policy check

```bash
kp policy check [flags]
```

手动运行所有策略检查（不部署）。

策略文件格式（Rego，`package kp`）：

```rego
package kp

deny[msg] {
    input.version == "latest"
    msg := "禁止使用 latest tag 部署"
}
```

input 对象：`{project, namespace, version, services[], env{}}`

示例策略见 `examples/policies/`。

---

## kp chaos

混沌工程（依托 Chaos Mesh）。需要 Chaos Mesh 安装并 port-forward 到 `localhost:2333`。

```bash
kp chaos <subcommand> [flags]
```

### kp chaos inject

```bash
kp chaos inject --service <name> --kind <type> [flags]
```

| Flag | 说明 | 默认 |
|------|------|------|
| `--service` | 目标服务名（必填）| — |
| `--kind` | 混沌类型 | `pod-kill` |
| `--duration` | 持续时间 | `30s` |
| `--dry-run` | 打印 CRD 配置，不实际注入 | false |
| `--latency` | 网络延迟（network-delay 类型）| `100ms` |
| `--workers` | 压测线程数（cpu/memory-stress）| `1` |
| `--chaos-mesh` | Chaos Mesh API 地址 | `http://127.0.0.1:2333` |

混沌类型：`pod-kill` / `network-delay` / `cpu-stress` / `memory-stress`

```bash
kp chaos inject --service wallet-service --kind pod-kill --duration 30s --dry-run
kp chaos inject --service wallet-service --kind network-delay --latency 200ms
kp chaos inject --service wallet-service --kind cpu-stress --workers 2 --duration 5m
```

### kp chaos list

```bash
kp chaos list [--namespace <ns>] [--chaos-mesh <addr>]
```

### kp chaos stop

```bash
kp chaos stop --uid <uid> [--chaos-mesh <addr>]
```

### kp chaos status

```bash
kp chaos status --uid <uid> [--chaos-mesh <addr>]
```

---

## kp doctor

一键环境检查：Go/Docker/kubectl/helm/trivy/oasdiff/helm-diff/K8s 集群连通性、project.env、REGISTRY_PREFIX、etcd 健康、VolumeSnapshot CRD、TLS 证书过期、跨 namespace 依赖、drift 告警。

```bash
kp doctor [flags]
```

| Flag | 说明 |
|------|------|
| `--perf` | 测试 Apiserver P99 延迟（采样 10 次），给出并发度建议 |

```bash
kp doctor
kp doctor --perf
kp doctor --perf --context prod
ETCD_ENDPOINTS=<host>:2379 kp doctor
```

---

## kp scan

镜像 CVE 扫描（依赖 trivy）。

```bash
kp scan [flags]
```

---

## kp history

查看部署历史。

```bash
kp history [--export json|csv]
```

---

## kp network

跨 namespace NetworkPolicy 管理。

```bash
kp network gen [flags]
```

生成跨 ns NetworkPolicy 模板到 `deployments/<project>/network/`。⚠️ 不自动 apply，审查后手动执行。

---

## kp ai-plan

扫描代码仓库，调用 LLM 自动生成 `configs/components.yaml`。

```bash
kp ai-plan [flags]
```

支持 LLM Provider：Grok / Claude / OpenAI / 豆包，通过环境变量配置：

```bash
export KP_LLM_PROVIDER=grok
export KP_LLM_API_KEY=xai-xxx
kp ai-plan
```

---

## kp migrate + kp diff --migrate

数据库迁移与 helm diff 联动：

```bash
kp diff --service wallet-service --migrate
```

同时展示 values 变更和待执行迁移风险。

---

## kp release

打版本 tag 并推送，自动更新 `configs/project.env` 的 VERSION 和 `cmd/kp/version.go` 的 `kpVersion` 常量。

```bash
kp release --version v1.2.3 [--deploy]
```

| Flag | 说明 |
|------|------|
| `--version` | 版本号，格式 `v{major}.{minor}.{patch}`（必填）|
| `--deploy` | 打完 tag 后自动触发 kp deploy |

流程：检查工作区干净 → 更新 VERSION + kpVersion → git add + commit → git tag → git push + push tags。

---

## kp version

查看当前版本。

```bash
kp version
```

输出：kp 版本号、Go 版本、模块路径。

---

## kp update

自动更新到最新版本（查询 GitHub releases API，通过 `go install` 安装）。

```bash
kp update
```

---

## kp plugin

插件市场。插件安装到 `~/.kp/plugins/`，未知命令自动转发到插件。

```bash
kp plugin <subcommand>
```

### kp plugin install

```bash
kp plugin install <name>[@version]
```

通过 `go install` 安装，支持 `name@version` 格式。

### kp plugin list

列出已安装插件。

### kp plugin remove

```bash
kp plugin remove <name>
```

### kp plugin run

```bash
kp plugin run <name> [args...]
```

### 插件兜底

任意未知命令自动转发到插件：

```bash
kp my-custom-plugin args...
# 等价于 kp plugin run my-custom-plugin args...
```

查找顺序：`~/.kp/plugins/<name>` → PATH 中的 `kp-<name>`。

---

## kp controller

启动 A2 Reconciliation Controller（通常由 `deployments/<project>-controller/` 里的 Deployment 管理，不需要手动运行）。

```bash
kp controller [flags]
```

---

## 环境变量

| 变量 | 说明 |
|------|------|
| `LOG_FORMAT` | `json` 启用结构化日志（接入 ELK/Loki）|
| `KP_LLM_PROVIDER` | AI 规划 LLM provider（grok/claude/openai/doubao）|
| `KP_LLM_API_KEY` | LLM API Key |
| `VAULT_ADDR` | Vault 地址（kp secret sync）|
| `VAULT_TOKEN` | Vault Token（kp secret sync）|
| `ETCD_ENDPOINTS` | etcd 地址，留空使用本地文件存储 |

---

## 命令速查表

| 命令 | 说明 |
|------|------|
| `kp init` | 生成项目骨架 |
| `kp deploy` | 部署 |
| `kp resume` | 恢复中断的部署 |
| `kp rollback` | 手动回滚 |
| `kp upgrade` | 跨版本升级 |
| `kp down` | 下线所有资源 |
| `kp status` | 查看状态 |
| `kp status --all-envs` | 跨集群视图 |
| `kp diff` | values 对比 |
| `kp diff --drift` | 漂移检测 |
| `kp diff --to-env` | 环境对比 |
| `kp promote` | 蓝绿切流 |
| `kp warmup` | 线性预热 |
| `kp sandbox start` | 原子性迁移 |
| `kp migrate run` | 执行迁移 |
| `kp migrate fix-dirty` | 修复 dirty |
| `kp compat check` | API 兼容性 |
| `kp pvc backup/restore` | PVC 快照 |
| `kp secret rotate` | Secret 轮转 |
| `kp secret sync` | 从 Vault 同步 |
| `kp context add` | 添加集群环境 |
| `kp audit` | 审计日志导出 |
| `kp policy add` | 添加 OPA 策略 |
| `kp chaos inject` | 混沌注入 |
| `kp doctor` | 环境检查 |
| `kp scan` | CVE 扫描 |
| `kp history` | 部署历史 |
| `kp network gen` | NetworkPolicy 模板 |
| `kp ai-plan` | AI 规划 |
| `kp release` | 发布版本 |
| `kp version` | 查看版本 |
| `kp update` | 自动更新 |
| `kp plugin install` | 安装插件 |
