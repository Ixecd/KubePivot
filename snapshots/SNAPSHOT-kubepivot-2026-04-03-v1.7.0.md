# SNAPSHOT — KubePivot v1.7.0

> 日期：2026-04-03
> 作者：qc（Ixecd）
> 版本：v1.7.0

---

## 一、版本范围

本快照覆盖 v1.7.0 状态漂移治理完整开发历程。

---

## 二、新增文件

```
cmd/kp/
├── drift.go          # kp diff --drift，三级分层，蓝绿 slot 感知
├── hpa.go            # applyHPA，autoscaling/v2

internal/controller/
├── drift_sync.go     # StartDriftSyncLoop，30s 扫描，etcd 审计日志
└── heal.go           # 完整重写，on-missing 全策略 + OOMKilled + CrashLoop
```

---

## 三、核心设计

### Drift 三级分层

```go
DriftHard     // ❌ kp 拥有所有权（image/env/resources）→ force-sync
DriftManaged  // ⚠️ 豁免字段（replicas，默认）→ 透明展示
DriftExternal // ℹ️ no-sync-fields 声明豁免 → 明确标注跳过
```

`helm diff --three-way-merge` 对比 live 集群 vs chart 期望状态，`--force-conflicts` 解决 SSA 字段冲突。

### on-missing 全策略

| 策略 | 行为 |
|------|------|
| `recreate` / `auto-heal` | helm rollback 到 latest-1 |
| `rollback` | helm rollback 到 latest-1 |
| `scale-down` | kubectl scale replicas=0 |
| `alert` | slog 告警，不自动处理 |
| `custom` | 执行 fallback 字段的 shell 命令 |

### OOMKilled 自动调整

```go
// bumpMemory：纯函数，Mi/Gi 支持，最小步长 +1
// 256Mi → 320Mi（+25%）
// 4Gi   → 5Gi（+25%）
// 触发：kubectl patch deployment --type=merge
```

### CrashLoopBackOff 分析

```go
// classifyCrashLogs：纯函数，从 --previous 日志分析
// startup：connection refused / dial tcp / no such host / permission denied
// runtime：panic: / runtime error / segmentation fault
// 处理：startup→告警等待人工；runtime+restarts≥5→自动 healRollback
```

---

## 四、单测覆盖

| 测试 | 数量 | 新增 |
|------|------|------|
| bumpMemory（Mi/Gi/Invalid） | 6 | ✅ v1.7.0 |
| classifyCrashLogs（startup/runtime/unknown）| 3 | ✅ v1.7.0 |
| on-missing rollback/scale-down/custom | 3 | ✅ v1.7.0 |

---

## 五、技术债记录

| 项 | 原因 | 当前方案 |
|----|------|---------|
| SSA `--field-manager=kubepivot` | helm v4 不支持此 flag | `--force-conflicts` 替代 |
| 蓝绿 timing 为 `-` | deployBlueGreen 分支未接入 deployTiming | v1.8.0 顺手修 |
| drift etcd 审计端到端 | writeDriftAuditLog 实现完整，但未在真实 etcd 集群验证 | v1.8.0 |

---

## 六、压测基准（沿用 v1.6.0）

```
Apiserver P50: 288ms  P99: 562ms
DAG 规划  P50: 10ms   P99: 40ms
```

---

## 七、常用命令

```bash
# Drift 检测
kp diff --drift
kp diff --drift --service wallet-service

# 制造漂移并验证
kubectl scale deployment/web3-blitz-wallet-service-blue -n web3-blitz --replicas=3
kp diff --drift --service wallet-service
# → ⚠️ 受控偏离：replicas: 3 → 2（no-sync-fields 豁免时显示 ℹ️）

# force-sync（kp deploy 会自动 --force-conflicts 对齐）
kp deploy

# doctor 含 drift
kp doctor
```
