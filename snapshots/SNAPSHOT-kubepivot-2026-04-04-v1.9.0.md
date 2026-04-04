# SNAPSHOT — KubePivot v1.9.0

> 日期：2026-04-04
> 作者：qc（Ixecd）
> 版本：v1.9.0

---

## 一、版本范围

本快照覆盖 v1.9.0 多集群联邦 + 企业合规完整开发历程。

---

## 二、新增文件

```
cmd/kp/
├── env.go       # kp context 命令，KPEnv 结构，loadEnv/applyEnvToConfig/applyEnvToMap
├── audit.go     # kp audit，AuditEvent，三来源聚合，jsonl/csv/table
└── policy.go    # kp policy，OPA 策略引擎，checkOPAPolicies

examples/policies/
├── no-latest-tag.rego            # 禁止 latest tag + production 版本格式
├── require-resource-limits.rego  # 要求资源 limits 声明
└── README.md                     # input context schema + 使用指南
```

---

## 三、核心设计

### 多集群 env 管理

```
~/.kp/envs/<name>.yaml  →  KPEnv{Name, Kubeconfig, Context, Namespace, Registry, Arch}

kp deploy --env prod    →  loadEnv("prod") → applyEnvToConfig + applyEnvToMap → 正常部署
kp status --all-envs    →  遍历 ~/.kp/envs/*.yaml，动态列宽，ANSI-safe pad() 对齐
kp diff --to-env staging →  getHelmValues(local) vs getHelmValues(staging) → diffValues
```

`"local"` 是保留字，`--from-env local` 等同于不传（使用 project.env）。

### AuditEvent 格式

```json
{
  "timestamp": "2026-04-04T07:14:14+08:00",
  "source":    "deploy",
  "action":    "deploy.complete",
  "actor":     "kp-cli",
  "resource":  "web3-blitz",
  "namespace": "web3-blitz",
  "from":      "VALIDATING",
  "to":        "RUNNING",
  "version":   "v0.1.12",
  "reason":    "部署验证通过",
  "outcome":   "success"
}
```

来源：
- `deploy`：state machine History（`~/.kp/state/` 或 etcd）
- `secret`：`~/.kp/audit/secret.jsonl`（ts/resource/action/namespace）
- `drift`：TODO stub（etcd 路径预留）

### OPA 策略引擎

```
input = {project, namespace, version, services[], env{}}
deny[msg] → 阻断部署
无 opa 命令 → 静默跳过（不阻断）
策略存 ~/.kp/policies/*.rego
```

**已知 TODO**：`checkOPAPolicies` 里 `inputJSON` 通过 stdin 传给 opa 的逻辑是 stub，需要实现 stdin pipe。

### kp secret sync --from vault

```
curl VAULT_ADDR/v1/<path> -H "X-Vault-Token: TOKEN"
→ 解析 KV v2 data.data
→ kubectl create secret --dry-run=client -o yaml | kubectl apply -f -
→ 写审计日志 ~/.kp/audit/secret.jsonl
```

---

## 四、已知 Bug 修复

- `kp diff --from-env local`：`"local"` 现在是保留字，不再去 `~/.kp/envs/local.yaml` 找
- WorkQueue 测试竞态：先 Add 再 Run，修复 CI 失败

---

## 五、技术债

| 项 | 描述 |
|----|------|
| OPA stdin pipe | checkOPAPolicies 的 input JSON 传递是 TODO |
| drift etcd 审计 | collectDriftAudit 是 stub |
| kp secret sync | curl 实现，v2.0.0 换 Vault Go SDK |
| 所有待 CSI/Istio/Prometheus 验证项 | 见 TODO.md |

---

## 六、常用命令

```bash
# 多集群
kp context add --name prod --context my-k8s --namespace production
kp status --all-envs
kp deploy --env prod --dry-run
kp diff --to-env prod

# 合规
kp audit --format table
kp audit --format jsonl --since 2026-04-01
kp policy add --name no-latest-tag --file examples/policies/no-latest-tag.rego
kp policy check

# Vault
export VAULT_TOKEN=hvs.xxx
kp secret sync --from vault --secret myapp-secret --vault-path secret/data/myapp --dry-run
```
