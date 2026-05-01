# kp secret

K8s Secret 管理：轮转、同步、审计、密封。

## 用法

```
kp secret <子命令>
```

## 子命令

### rotate

轮转 Secret（更新值 + 滚动重启引用服务）。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--secret` | (必填) | Secret 名称 |
| `--strategy` | `immediate` | 轮转策略：`immediate` / `graceful` |
| `--namespace` | (project.env → default) | K8s namespace |
| `--context` | | kube context |
| `--kubeconfig` | | kubeconfig 路径 |

**策略**：
- `immediate`：直接 `rollout restart` 所有引用服务
- `graceful`：双密码过渡期（备份旧值 → 滚动重启 → 健康检查通过后清理旧值）

**扫描范围**：`deployments/` 目录下所有 YAML 模板，检测 `secretKeyRef`（env 引用）和 `secretName`（volume 引用）。支持 Deployment、StatefulSet、DaemonSet、CRD 等。对 CRD 通过删关联 Pod 触发重启。

### seal

用 kubeseal 加密 Secret，生成可安全提交到 Git 的 SealedSecret YAML。

| Flag | 默认值 | 说明 |
|---|---|---|
| (位置参数) | (必填) | Secret 名称 |
| `--from-literal` | (可多次) | `KEY=VALUE` |
| `--from-file` | (可多次) | `KEY=PATH` |
| `--namespace` | `default` | K8s namespace |
| `--scope` | `namespace-wide` | 解密 scope |
| `--cert` | | 公钥文件（空时从集群获取） |
| `--output` | (stdout) | 输出文件路径 |

### sync

从外部源同步 Secret 到 K8s。目前支持 Vault。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--from` | `vault` | 来源 |
| `--vault-addr` | (`VAULT_ADDR` env) | Vault 地址 |
| `--vault-token` | (`VAULT_TOKEN` env) | Vault Token |
| `--vault-path` | (必填) | Vault KV 路径 |
| `--secret` | (必填) | 目标 K8s Secret 名称 |
| `--namespace` | (project.env → default) | K8s namespace |
| `--dry-run` | `false` | 只打 key，不写入 |

### audit

审计 Secret 引用情况（只读）。

### cleanup

清理过期 Secret（只读诊断）。

## RBAC

`PermSecret`（rotate / seal / sync）。audit 和 cleanup 是只读操作，不加 RBAC。

## 相关命令

- `kp supply-chain verify` — 镜像签名验证
