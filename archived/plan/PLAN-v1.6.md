# KubePivot 硬仗作战计划

> 这不是功能列表，是每一行代码背后的决策依据。
> 写给下一个 Claude，也写给 qc 自己。
> 更新日期：2026-04-03（v1.6.0）

---

## v1.7.0 — 状态漂移治理

### 核心决策：SSA FieldManager

kp 用 `--field-manager=kubepivot` 做 Server-Side Apply，只声明对以下字段的所有权：
- `spec.template.spec.containers[*].image`
- `spec.template.spec.containers[*].env`
- `spec.template.spec.containers[*].ports`
- `spec.template.spec.containers[*].resources`
- `spec.replicas`（非 HPA 管理时）

不干预 Istio Sidecar 注入的 `containers[]`、HPA 管理的 `replicas`、云厂商注入的 `annotations`。

```go
// helm upgrade 时加 field-manager
args = append(args,
    "--field-manager=kubepivot",
    "--force-conflicts",  // 强制接管已声明字段
)
```

### Drift 三级分层

```go
type DriftLevel int
const (
    DriftHard     DriftLevel = iota  // ❌ kp 拥有所有权，强制同步
    DriftManaged                      // ⚠️ 豁免字段，透明展示
    DriftExternal                     // ℹ️ 外部注入，完全忽略
)

// kp diff --drift 输出格式：
// ❌ 硬冲突：image.tag: v0.1.11 → v0.1.12（期望）← 将强制同步
// ⚠️ 受控偏离：replicas: 3 → 5（HPA 管理，已豁免，values.yaml 定义为 3）
// ℹ️ 外部注入：sidecar.istio.io/inject: true（Istio 注入，忽略）
```

### force-sync Controller 扫描

```go
// 独立于 8s 自愈周期，每 30s 扫描一次
func (c *Controller) driftSyncLoop(ctx context.Context) {
    ticker := time.NewTicker(30 * time.Second)
    for {
        select {
        case <-ticker.C:
            c.scanAndSync()
        case <-ctx.Done():
            return
        }
    }
}

// scanAndSync：
// 1. helm diff 检测漂移
// 2. 发现 DriftHard → helm upgrade --force --field-manager=kubepivot
// 3. 写漂移审计日志到 etcd：/kubepivot/<project>/drift/<timestamp>
```

### resources.yaml 新增字段

```yaml
resources:
  - kind: Deployment
    name: wallet-service
    on-missing: auto-heal
    force-sync: true          # 新增：开启 drift force-sync
    no-sync-fields:           # 豁免字段（如 HPA 管理的 replicas）
      - spec.replicas
```

---

## v1.8.0 — Operation Sandbox

### 新状态机（完整）

```go
var validTransitions = map[State][]State{
    // 原有
    StateIdle:        {StateInitializing, StateLocked},
    StateRunning:     {StateInitializing, StateTerminated, StateCleaning, StateRollingBack, StateLocked},
    // v1.8.0 新增
    StateLocked:      {StateSnapshotting, StateIdle},
    StateSnapshotting:{StateSimulating, StateRestoring, StateIdle},
    StateSimulating:  {StateCommitting, StateRestoring, StateIdle},
    StateCommitting:  {StateRunning, StateRestoring},  // 禁止 force-unlock
    StateRestoring:   {StateIdle},
}

// force-unlock 安全边界
var forceUnlockAllowed = map[State]bool{
    StateLocked:       true,
    StateSnapshotting: true,   // 告警：快照未完成，无数据保护
    StateSimulating:   true,   // 沙盒阶段，终止 Job 即可
    StateCommitting:   false,  // 禁止：DB 正在真实迁移
    StateRestoring:    false,  // 禁止：正在恢复中
}
```

### SandboxSession CRD

```yaml
apiVersion: kubepivot.io/v1
kind: SandboxSession
metadata:
  name: sandbox-<uuid>
  namespace: <project-ns>
  labels:
    kubepivot.io/sandbox-id: <uuid>
spec:
  project: web3-blitz
  startedAt: "2026-04-01T10:00:00Z"
  ttl: 3600  # 1小时，超期 Controller 自动 GC
  phase: SIMULATING
  resources:
    - kind: Job
      name: migrate-sim-<uuid>
    - kind: VolumeSnapshot
      name: data-0-snap-<ts>
```

所有沙盒资源用 `ownerReferences` 挂在 SandboxSession 下，Session 被删时子资源自动级联删除。

### Sandbox 执行流程

```
Step 1: LOCKED
  - 获取 etcd 分布式锁（阻止其他 kp deploy）
  - 创建 SandboxSession，所有资源打 kubepivot.io/sandbox-id: <uuid>

Step 2: SNAPSHOTTING
  - 触发 kp pvc backup（有 CSI 才执行，无则跳过并告警）
  - 等待 VolumeSnapshot readyToUse=true

Step 3: SIMULATING
  - 创建临时 Job，挂载相同 Secret，预跑 DB 迁移
  - postgres 事务 DDL：失败自动回滚，不污染生产数据
  - Job 成功 → 进入 COMMITTING；失败 → RESTORING

Step 4: COMMITTING
  - 真实执行 kp migrate run
  - helm upgrade 部署服务
  - 禁止 force-unlock（DB 正在变更）

Step 5: RUNNING（成功）或 RESTORING（失败）
  - RESTORING：kp pvc restore + helm rollback
  - 完成后 → IDLE，释放分布式锁
```

### Header-based Preview 生成

```go
func generatePreviewTemplate(cfg, plan, slot) {
    if isIstioPresent() {
        // 生成 VirtualService，x-kp-version: green → green slot
        writeVirtualServiceTemplate(plan.Name, slot)
    } else if isNginxIngressPresent() {
        // nginx.ingress.kubernetes.io/canary-by-header
        writeIngressTemplate(plan.Name, slot)
    } else {
        P.Info("💡", "未检测到 Istio/Nginx，Preview 需要手动配置流量规则")
    }
    // 输出到 deployments/<proj>/preview/，不自动 apply
}
```

### warmup 线性权重

```go
// kp warmup --steps 10,50,100 --interval 2m,5m
// 依赖 Istio VirtualService weight 字段
func warmup(steps []int, intervals []time.Duration, errThreshold float64) error {
    for i, step := range steps {
        patchVirtualServiceWeight(step)  // green: N%, blue: 100-N%
        time.Sleep(intervals[i])
        rate := sampleErrorRate()  // 从 Prometheus 采样
        if rate > errThreshold {
            patchVirtualServiceWeight(0)  // 回滚流量
            return fmt.Errorf("error rate %.2f%% 超阈值，已回滚", rate*100)
        }
    }
    return nil  // 成功，等待 kp promote 正式切换
}
```

---

## 通用设计原则

### "只保护，不越权"边界表

| 能力 | kp 做 | kp 不做 |
|------|--------|---------|
| NetworkPolicy | 生成模板 | 自动 apply |
| 跨 namespace 资源 | 只读嗅探 | 修复 |
| DB 用户权限 | 提示用户 | 直接操作 |
| Istio VirtualService | 生成模板 + warmup 编排 | 接管流量层 |
| HPA 管理的 replicas | 透明展示偏离 | 强制同步 |
| 混沌注入 | 调用 Chaos Mesh API | 自己实现故障注入 |
| 跨 ns 自愈 | 只读嗅探告警 | 修复跨 ns 资源 |

### 审计日志结构

```go
type AuditEvent struct {
    Timestamp time.Time `json:"ts"`
    Operator  string    `json:"operator"`   // kubectl whoami
    Action    string    `json:"action"`     // rotate/force-unlock/drift-sync
    Resource  string    `json:"resource"`
    Reason    string    `json:"reason"`
    SandboxID string    `json:"sandbox_id,omitempty"`
    PrevState string    `json:"prev_state"`
    NextState string    `json:"next_state"`
}
// etcd key：/kubepivot/<project>/audit/<timestamp>
// 保留最近 1000 条，超出按时间轮转
```

### 测试策略

| 模块 | 测试方式 |
|------|---------|
| Secret 引用追踪 | 单测，mock 文件系统 ✅ |
| Leader Election | 单测，no-etcd fallback ✅ |
| WorkQueue | 单测，三集合语义验证 ✅ |
| --changed-only | 单测，纯函数 classifyChangedFiles ✅ |
| Drift 分类逻辑 | 单测，覆盖 Hard/Managed/External |
| Sandbox GC | integration test，模拟超期 Session |
| force-unlock 边界 | 单测，验证 COMMITTING 阶段被拒绝 |
| warmup error-rate | 单测，mock Prometheus 返回值 |
| KWOK 压测 | e2e，500 节点，20 服务 ✅ |

---

## 压测基准（v1.6.0，KWOK 500 节点）

```
Apiserver P50:  288ms
Apiserver P99:  562ms → ⚠️ 触发降并发建议（正确）
DAG 规划 P50:   10ms
DAG 规划 P99:   40ms  → ✅ 纯内存计算，不是瓶颈
```

结论：大规模场景下性能瓶颈在 Apiserver，不在 kp 自身逻辑。`--parallelism` + `kp doctor --perf` 是正确的应对策略。
