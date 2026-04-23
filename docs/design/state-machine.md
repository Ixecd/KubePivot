# kp 状态机设计文档

> 版本：v2.0.0

---

## 设计动机

`kp deploy` 是一个多步骤、有副作用的长流程操作。任意一步失败或进程被中断，都会留下不确定的中间状态。状态机从**命令式**升级为**声明式 + Reconciliation Loop**，核心目标：

- 真正实现**自动自愈**（资源缺失、OOMKilled、CrashLoop 均可处理）
- **原子性迁移**（Operation Sandbox 保证 DB 迁移和服务升级的原子性）
- 完整操作历史，支持 SOC2/ISO27001 审计导出

---

## 状态定义

### 核心部署状态

```
IDLE          初始状态，无部署记录或已清理完成
INITIALIZING  部署流程已启动，正在准备
DEPLOYING     正在执行 helm upgrade + rollout
VALIDATING    rollout 完成，正在验证服务健康
RUNNING       部署成功，服务正常运行
ROLLING_BACK  验证失败或手动触发，正在执行 helm rollback
CLEANING      首次部署失败，正在清理 namespace
TERMINATED    服务已下线，终态
```

### Sandbox 状态（v1.8.0+）

```
LOCKED        持有分布式锁，阻止其他 kp deploy
SNAPSHOTTING  正在创建 PVC 快照（有 CSI 才执行）
SIMULATING    临时 K8s Job 预跑 DB 迁移（降级 dry-run）
COMMITTING    真实迁移 + helm upgrade（禁止 force-unlock）
RESTORING     失败后恢复 PVC 快照 + helm rollback
```

---

## 状态转换表

```
当前状态          可转换到
─────────────────────────────────────────────────────────
IDLE            → INITIALIZING, LOCKED
INITIALIZING    → DEPLOYING, CLEANING
DEPLOYING       → VALIDATING, ROLLING_BACK, CLEANING
VALIDATING      → RUNNING, ROLLING_BACK
RUNNING         → INITIALIZING, TERMINATED, CLEANING, ROLLING_BACK, LOCKED
ROLLING_BACK    → RUNNING, CLEANING
CLEANING        → IDLE, TERMINATED
TERMINATED      → （终态）

LOCKED          → SNAPSHOTTING, IDLE（force-unlock 合法）
SNAPSHOTTING    → SIMULATING, RESTORING, IDLE
SIMULATING      → COMMITTING, RESTORING, IDLE
COMMITTING      → RUNNING, RESTORING（禁止直接到 IDLE）
RESTORING       → IDLE
```

**关键约束**：COMMITTING 阶段永远禁止 force-unlock。DB 正在迁移，强制解锁会造成 DB 已变更但服务未升级的数据不一致。

---

## 完整流程图

### 正常部署

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
```

### 部署失败

```
DEPLOYING/VALIDATING → ROLLING_BACK → RUNNING
DEPLOYING/INITIALIZING → CLEANING → IDLE（首次部署失败）
```

### Operation Sandbox（原子性迁移）

```
IDLE/RUNNING → LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
                                              ↓（任意失败）
                                          RESTORING → IDLE
```

### 自愈流程

```
RUNNING（资源缺失）→ Controller 检测 → on-missing 策略
  recreate/rollback → helm rollback → RUNNING
  scale-down        → kubectl scale 0（降级保护）
  alert             → slog 告警，不自动处理
  custom            → 执行 fallback shell 命令
```

---

## Operation Sandbox 详解

`kp sandbox start` 执行以下 5 个阶段：

1. **LOCKED**：生成 sandbox-id（UUID 前 8 位），写 `.kp/sandbox/<id>.json`，阻止其他 `kp deploy`
2. **SNAPSHOTTING**：检查 CSI VolumeSnapshot CRD，有则触发 `kp pvc backup`，无则告警继续
3. **SIMULATING**：将迁移文件打包进 ConfigMap，创建 golang-migrate K8s Job，等待完成（timeout=300s）。Job 创建失败降级为 `kp migrate run --dry-run`
4. **COMMITTING**：真实执行 `kp migrate run` + `kp deploy`
5. **RUNNING**：清理 Session 文件，释放锁

失败双层回滚：
- Layer 1：`kp pvc restore`（有快照才执行）
- Layer 2：`kp rollback`（helm rollback）

Controller GC Loop 每 5 分钟扫描 `.kp/sandbox/`，超过 TTL（默认 3600s）的 Session 自动清理带 `kubepivot.io/sandbox-id` label 的资源，并 ForceState → IDLE。

---

## Reconciliation Controller

**运行位置**：`{project}-controller` Deployment（独立 pod，支持多副本 HA）

### Leader Election

```
多副本通过 etcd 分布式锁竞选 Leader（TTL=15s，心跳 5s 续约）
非 Leader 等待，Leader 宕机后最长 15s 内自动切换
```

### WorkQueue 三集合去重

```
queue      []string              待处理队列
dirty      map[string]struct{}   已知待处理（含处理中收到的新事件）
processing map[string]struct{}   正在处理中

防止事件风暴：同一 key 在处理完成前不会重复入队
```

### 自愈增强（v1.7.0+）

```
OOMKilled：
  检测 pod 状态 → kubectl patch deployment memory limit +25%
  bumpMemory 纯函数（Mi/Gi 支持）

CrashLoopBackOff：
  分析 --previous 日志
  startup 错误（dial/permission）→ 告警等待人工
  runtime 错误（panic/nil）+ restarts≥5 → 自动 healRollback
```

---

## Drift Sync Loop（v1.7.0+）

```
每 30s 扫描 force-sync=true 的资源
helm diff --three-way-merge 对比 live 集群 vs chart 期望值
分三级：
  ❌ 硬冲突（kp 拥有字段所有权）→ helm upgrade --force-conflicts
  ⚠️  受控偏离（no-sync-fields 豁免）→ 透明展示
  ℹ️  外部注入（Istio sidecar 等）→ 完全忽略
审计日志写入 etcd /kubepivot/<project>/<ns>/drift/<ts>
```

---

## 持久化

**etcd 优先**，key 格式：`kubepivot/<project>/<namespace>/state`

**本地文件降级**：无 etcd 时自动降级到 `~/.kp/state/<project>/<namespace>.json`

```go
store := state.NewAutoStore(env["ETCD_ENDPOINTS"])
// ETCD_ENDPOINTS 留空 → 本地文件
```

### DeployRecord

```json
{
  "project":    "web3-blitz",
  "namespace":  "web3-blitz",
  "state":      "RUNNING",
  "version":    "v0.1.12",
  "is_first":   false,
  "reason":     "部署验证通过",
  "updated_at": "2026-04-04T07:14:14+08:00",
  "history": [
    {
      "from":      "DEPLOYING",
      "to":        "VALIDATING",
      "reason":    "验证部署结果",
      "version":   "v0.1.12",
      "timestamp": "2026-04-04T07:14:02+08:00"
    }
  ]
}
```

每次状态转换追加到 `history`，可通过 `kp audit` 导出。

---

## 并发保护

以下状态时 `kp deploy` 拒绝执行：

```
INITIALIZING / DEPLOYING / VALIDATING / ROLLING_BACK / CLEANING
LOCKED / SNAPSHOTTING / SIMULATING / COMMITTING / RESTORING
```

Sandbox 状态有专属提示：
```
❌ 当前处于 Sandbox 会话（状态: LOCKED），禁止发起新部署
   等待 Sandbox 完成，或运行: kp sandbox unlock --force --reason "..."
```

---

## configs/resources.yaml 完整字段

```yaml
resources:
  - kind: Deployment
    name: wallet-service
    namespace: web3-blitz
    on-missing: recreate     # recreate|rollback|scale-down|alert|custom
    force-sync: true         # Controller 30s 强制对齐
    no-sync-fields:          # 豁免字段（HPA 管理的 replicas）
      - replicas
    fallback: ""             # on-missing=custom 时执行的 shell 命令
```
