# KubePivot 当前快照

> 版本：v1.9.0
> 日期：2026-04-04
> 状态：✅ 全绿，待打 tag

---

## 快速状态

```
go test ./... -race  → 全绿
make dev             → build + test + install 一键完成
当前版本             → v1.9.0（未 tag）
companion            → github.com/Ixecd/web3-blitz
```

---

## v1.9.0 新增能力

### 多集群管理
```bash
kp context add --name staging --context orbstack --namespace web3-blitz-staging
kp context list
kp context show staging
kp context remove staging

kp deploy --env staging
kp status --env staging
kp status --all-envs              # 跨集群统一视图
kp diff --to-env staging          # local vs staging
kp diff --from-env local --to-env staging
```

### 企业合规
```bash
# 审计日志
kp audit --format table
kp audit --format jsonl --output audit.jsonl
kp audit --format csv --since 2026-04-01
kp audit --source deploy          # 只看部署操作

# OPA 策略
kp policy add --name no-latest-tag --file examples/policies/no-latest-tag.rego
kp policy list
kp policy check
kp deploy  # 自动执行策略检查（无 opa 命令静默跳过）

# Vault 同步
kp secret sync --from vault \
  --secret wallet-service-secret \
  --vault-path secret/data/web3-blitz \
  --dry-run
```

---

## 技术债提示
- OPA stdin pipe（input JSON 传递）：TODO stub
- drift etcd 审计：collectDriftAudit 是 stub
- kp secret sync：curl 实现，v2.0.0 换 Vault SDK
- 其余待验证项见 TODO.md

---

## 下一步

v2.0.0 — 企业级插件平台 + 全文档统一大版本更新
