# KubePivot 当前快照

> 版本：v1.6.0
> 日期：2026-04-03
> 状态：✅ 全绿，可发布

---

## 快速状态

```
go test ./... -race  → 全绿
make dev             → build + test + install 一键完成
当前 tag             → v1.6.0（待打）
companion            → github.com/Ixecd/web3-blitz（BTC/ETH 充提币）
```

---

## v1.5.2 ~ v1.6.0 新增能力

### kp secret
```bash
kp secret rotate --secret <n> [--strategy graceful|immediate]
kp secret cleanup --secret <n>
kp secret audit
```

### kp network
```bash
kp network gen [--output <dir>]   # 生成跨 ns NetworkPolicy 模板，不自动 apply
```

### kp deploy 新 flag
```bash
kp deploy --parallelism 4         # 同层最大并发数
kp deploy --changed-only          # 基于 git diff 增量部署
```

### kp doctor 新检查
```bash
kp doctor --perf [--context <ctx>]  # Apiserver P99 延迟 + 并发度建议
# 新增：helm-diff 检测、TLS 证书过期、跨域嗅探
```

### kp history
```bash
kp history --export json
kp history --export csv
```

### 可观测性
```bash
LOG_FORMAT=json kp deploy 2>deploy.log   # 结构化 JSON 日志
LOG_LEVEL=debug kp deploy                # 调试模式
```

### components.yaml 跨 namespace 依赖
```yaml
depends_on:
  - web3-blitz-postgres          # 同 namespace，参与 DAG
  - kube-system/coredns          # 跨 namespace，只嗅探
```

---

## Controller HA

```
kubepivot-controller（多副本）
  ├── etcd Leader Election（TTL=15s，无 client-go）
  ├── WorkQueue 三集合去重（queue/dirty/processing）
  └── RBAC 最小权限（含 leases 读写权）
```

---

## 压测基准（KWOK 500 节点）

```
Apiserver P50: 288ms  P99: 562ms → 建议 --parallelism=4
DAG 规划  P50: 10ms   P99: 40ms  → 纯内存计算，不是瓶颈
```

---

## 下一步

v1.7.0 — 状态漂移治理（SSA FieldManager + force-sync）
