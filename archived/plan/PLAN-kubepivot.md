# KubePivot 硬仗作战计划

> 这不是功能列表，是每一行代码背后的决策依据。
> 写给下一个 Claude，也写给 qc 自己。

---

## v1.5.2 — Secret 轮转

### 核心流程

```
kp secret rotate --secret wallet-service-secret [--strategy=graceful]
    ↓
1. 扫描 deployments/ 找出所有引用该 Secret 的服务（secretKeyRef 追踪）
2. --strategy=graceful：
   a. 更新 Secret（新密码写入 DATABASE_URL，旧密码保留为 DATABASE_URL_OLD）
   b. patch deployment/statefulset envFrom，注入双密码
   c. 滚动重启（Deployment 并行，StatefulSet 按 ordinal 逐个）
   d. 全量 /healthz 验证通过
   e. 输出提示："请在数据库端禁用旧密码，确认后运行 kp secret cleanup"
   f. kp secret cleanup：删除 DATABASE_URL_OLD 字段
3. --strategy=immediate（默认）：
   直接更新 Secret + rollout restart，适用于非 DB 类 Secret
4. 审计日志写入 etcd：操作人/时间/策略/涉及服务
```

### 关键约束
- **kp 不操作数据库用户权限**，DB 端旧密码禁用由用户确认后执行
- StatefulSet 重启时 `maxUnavailable=1`，保证零宕机
- 健康检查超时 60s，超时则告警不阻断（Secret 已更新，业务层问题）

### 新增文件
```
cmd/kp/secret.go         # kp secret rotate/cleanup/audit
cmd/kp/secret_test.go    # 单测：引用追踪逻辑
```

---

## v1.6.0 — Controller 高可用

### Leader Election 实现

```go
// internal/controller/leader.go
import "k8s.io/client-go/tools/leaderelection"

// Lease 资源名：kubepivot-controller-leader
// 租约时间：15s，续约间隔：5s，重试间隔：2s
// 只有 Leader 运行 Reconcile Loop，Follower 只 Watch etcd
func RunWithLeaderElection(ctx context.Context, id string, fn func(ctx context.Context)) {
    lock := &resourcelock.LeaseLock{...}
    leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
        Lock:            lock,
        LeaseDuration:   15 * time.Second,
        RenewDeadline:   10 * time.Second,
        RetryPeriod:     2 * time.Second,
        Callbacks: leaderelection.LeaderCallbacks{
            OnStartedLeading: fn,
            OnStoppedLeading: func() { os.Exit(1) }, // 失去 Leader 立即退出，让 K8s 重启
            OnNewLeader: func(id string) { slog.Info("new leader", "id", id) },
        },
    })
}
```

### WorkQueue + Rate Limiter（防事件风暴）

```go
// 现有：Watch 事件直接触发 Reconcile → 大规模变更时触发风暴
// 改为：Watch 事件入队，Worker 限速消费

queue := workqueue.NewRateLimitingQueue(
    workqueue.NewItemExponentialFailureRateLimiter(
        5*time.Millisecond,  // 基础延迟
        1000*time.Second,    // 最大延迟
    ),
)

// Event Aggregation：同一资源 1s 内的多次事件合并为一次
// 用 workqueue 的 AddAfter + 去重机制实现
```

### kp doctor --perf 实现

```go
// 连续发 10 次 kubectl get nodes 请求，统计 P50/P99 延迟
// P99 > 500ms → 告警，建议降低 --parallelism
// P99 > 2000ms → 阻断大规模部署
func checkApiserverLatency(cfg *deployConfig) checkResult {
    var latencies []time.Duration
    for i := 0; i < 10; i++ {
        start := time.Now()
        runOutput("kubectl", "get", "nodes", "--request-timeout=5s", ...)
        latencies = append(latencies, time.Since(start))
    }
    p99 := percentile(latencies, 99)
    // 根据 p99 给出建议并发度
}
```

### 跨域嗅探（只读）

```go
// kp doctor 扫描 components.yaml 中的跨 namespace depends_on
// 对每个跨 namespace 依赖执行只读 kubectl get
// Controller RBAC 只申请跨 namespace 的 get/list/watch，不申请 create/update/delete
func sniffCrossNamespaceDeps(cfg *deployConfig, components []planner.Component) []checkResult {
    for _, c := range components {
        for _, dep := range c.DependsOn {
            if dep.Namespace != "" && dep.Namespace != cfg.namespace {
                exists := checkResourceExists(dep.Namespace, dep.Kind, dep.Name)
                if !exists {
                    // 标红提示，不触发自愈
                }
            }
        }
    }
}
```

---

## v1.7.0 — 状态漂移治理

### SSA FieldManager 所有权声明

```go
// kp 声明所有权的字段：
// spec.template.spec.containers[*].image
// spec.template.spec.containers[*].env
// spec.template.spec.containers[*].ports
// spec.template.spec.containers[*].resources
// spec.replicas（非 HPA 管理时）

// apply 时带 field-manager
args = append(args,
    "--field-manager=kubepivot",
    "--force-conflicts",  // 强制接管已声明字段
)
```

### Drift 三级分层检测

```go
type DriftLevel int
const (
    DriftHard     DriftLevel = iota  // ❌ kp 拥有所有权，强制同步
    DriftManaged                      // ⚠️ 豁免字段，透明展示
    DriftExternal                     // ℹ️ 外部注入，完全忽略
)

func classifyDrift(field string, fieldManagers []string) DriftLevel {
    if isKubePivotOwned(field) {
        return DriftHard
    }
    if isExempted(field) {  // HPA 管理的 replicas 等
        return DriftManaged
    }
    return DriftExternal
}
```

### force-sync Controller 扫描逻辑

```go
// 每 30s 扫描一次（独立于 8s 自愈周期）
// 对 force-sync: true 的资源：
// 1. helm diff 检测漂移
// 2. 发现 DriftHard → helm upgrade --force --field-manager=kubepivot
// 3. 写漂移审计日志到 etcd
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
```

---

## v1.8.0 — Operation Sandbox

### 新状态机

```
IDLE
 → LOCKED（获得分布式锁，所有沙盒资源打 sandbox-id）
   → SNAPSHOTTING（触发 kp pvc backup）
     → SIMULATING（临时 Job 预跑迁移）
       → COMMITTING（真实 migrate + helm upgrade）
         → RUNNING（成功）
         → RESTORING（失败：restore PVC + helm rollback）
           → IDLE（恢复完成）

LOCKED/SNAPSHOTTING/SIMULATING → IDLE（force-unlock，限制见下）
COMMITTING → 禁止 force-unlock
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
  ttl: 3600  # 1小时，超期自动 GC
  phase: SIMULATING
  resources:
    - kind: Job
      name: migrate-sim-<uuid>
    - kind: VolumeSnapshot
      name: data-0-snap-<ts>
```

### force-unlock 逻辑

```go
var forceUnlockAllowed = map[state.State]bool{
    StateLocked:       true,
    StateSnapshotting: true,   // 告警：快照未完成，无数据保护
    StateSimulating:   true,   // 沙盒阶段，安全，终止 Job 即可
    StateCommitting:   false,  // 禁止：DB 正在真实迁移
    StateRestoring:    false,  // 禁止：正在恢复中
}

func forceUnlock(sandboxID, reason string) error {
    phase := getCurrentPhase(sandboxID)
    if !forceUnlockAllowed[phase] {
        return fmt.Errorf("当前阶段 %s 禁止强制解锁，DB 可能正在迁移中，请等待完成", phase)
    }
    // 1. 终止 SIMULATING Job
    // 2. 触发 SandboxSession GC（级联删除所有子资源）
    // 3. 写审计日志：操作人/reason/时间/跳过的阶段
    // 4. 状态机回到 IDLE（如果快照已完成则保留快照）
}
```

### Header-based Preview 生成逻辑

```go
// kp deploy --preview 时：
// 1. 检测 Istio 或 Nginx Ingress 是否安装
// 2. 生成对应模板（不 apply，写到 deployments/<proj>/preview/）
// 3. 输出提示："请检查 virtualservice-preview.yaml 后手动 kubectl apply"

func generatePreviewTemplate(cfg, plan, slot) {
    if isIstioPResent() {
        writeVirtualServiceTemplate(plan.Name, slot)  // x-kp-version: green → green slot
    } else if isNginxIngressPresent() {
        writeIngressTemplate(plan.Name, slot)  // nginx.ingress.kubernetes.io/canary-by-header
    } else {
        P.Info("💡", "未检测到 Istio/Nginx，Preview 需要手动配置流量规则")
    }
}
```

### warmup 线性权重切换

```go
// kp warmup --steps 10,50,100 --interval 2m,5m
// 依赖 Istio VirtualService weight 字段
// 每个 step：
// 1. patch VirtualService weight（green: N%, blue: 100-N%）
// 2. 等待 interval
// 3. 采样新版本 error-rate（从 Prometheus 或 kube-state-metrics）
// 4. error-rate > 阈值（默认 1%）→ 中断 + patch 回 0%

func warmup(steps []int, intervals []time.Duration, errThreshold float64) error {
    for i, step := range steps {
        patchVirtualServiceWeight(step)
        time.Sleep(intervals[i])
        rate := sampleErrorRate()
        if rate > errThreshold {
            patchVirtualServiceWeight(0)  // 回滚流量
            return fmt.Errorf("error rate %.2f%% 超阈值，已回滚流量", rate*100)
        }
    }
    return nil  // 全量切换成功，等待 kp promote
}
```

---

## 通用设计原则

### 状态机转换白名单（完整版）

```go
var validTransitions = map[State][]State{
    StateIdle:         {StateInitializing, StateLocked},
    StateInitializing: {StateDeploying, StateCleaning},
    StateDeploying:    {StateValidating, StateRollingBack, StateCleaning},
    StateValidating:   {StateRunning, StateRollingBack, StateCleaning},
    StateRunning:      {StateInitializing, StateTerminated, StateCleaning, StateRollingBack, StateLocked},
    StateRollingBack:  {StateRunning, StateCleaning},
    StateCleaning:     {StateIdle, StateTerminated},
    StateTerminated:   {},
    // v1.8.0 新增
    StateLocked:       {StateSnapshotting, StateIdle},
    StateSnapshotting: {StateSimulating, StateRestoring, StateIdle},
    StateSimulating:   {StateCommitting, StateRestoring, StateIdle},
    StateCommitting:   {StateRunning, StateRestoring},
    StateRestoring:    {StateIdle},
}
```

### "只保护，不越权"边界表

| 能力 | kp 做 | kp 不做 |
|------|--------|---------|
| NetworkPolicy | 生成模板 | 自动 apply |
| 跨 namespace 资源 | 只读嗅探 | 修复 |
| DB 用户权限 | 提示用户 | 直接操作 |
| Istio VirtualService | 生成模板 + warmup 编排 | 接管流量层 |
| HPA 管理的 replicas | 透明展示偏离 | 强制同步 |
| 混沌注入 | 调用 Chaos Mesh API | 自己实现故障注入 |

### 审计日志结构

```go
type AuditEvent struct {
    Timestamp   time.Time `json:"ts"`
    Operator    string    `json:"operator"`   // kubectl whoami
    Action      string    `json:"action"`     // rotate/force-unlock/drift-sync
    Resource    string    `json:"resource"`
    Reason      string    `json:"reason"`
    SandboxID   string    `json:"sandbox_id,omitempty"`
    PrevState   string    `json:"prev_state"`
    NextState   string    `json:"next_state"`
}
// 写入 etcd key：/kubepivot/<project>/audit/<timestamp>
// 保留最近 1000 条，超出按时间轮转
```

---

## 测试策略

| 模块 | 测试方式 |
|------|---------|
| Secret 引用追踪 | 单测，mock 文件系统 |
| Leader Election | integration test，启动 3 个 controller 实例 |
| WorkQueue Rate Limiter | 单测，注入大量事件验证去重 |
| Drift 分类逻辑 | 单测，覆盖 Hard/Managed/External 三种 |
| Sandbox GC | integration test，模拟超期 Session |
| force-unlock 边界 | 单测，验证 COMMITTING 阶段被拒绝 |
| warmup error-rate | 单测，mock Prometheus 返回值 |
| KWOK 压测 | e2e，10000 节点，100 服务并发部署 |
