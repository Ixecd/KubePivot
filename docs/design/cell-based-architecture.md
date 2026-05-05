# 乾枢 Cell-based Architecture (CBA) — 有状态故障隔离

> 编写日期：2026-05-05（从 FUTURE.md F1 种子演化）
> 状态：🌱 设计草案（v3.1 前置条件部分满足，待 v3.2+ 重新评估）
> 来源：[FUTURE.md](../../FUTURE.md) F1 种子
> 关联：[sharding.md](sharding.md) / [state-machine.md](state-machine.md)

---

## 一、摘要

v3.1 已完成 Controller 从 Deployment 到 StatefulSet 的迁移（含 etcd Learner sidecar）。
稳定 Pod 身份（`controller-0/1/2`）为 CBA 提供了基础设施前提——我们第一次有稳定的
Pod 标识来承载"哪个 Pod 管哪个 Cell"的映射。

CBA 的核心思想：**从 1D Hash-sharding 演化到 2D Matrix-orchestration**
（Shard × Workload-class），实现有状态资源的故障隔离边界。

当前 v2.5+ 的 hash-sharding 模型对无状态资源（Deployment/Service/ConfigMap）
工作良好——接管成本低、秒级恢复、对外不可见。但 v3.2+ 计划引入的有状态资源保护
（StatefulSet/PVC/Database）不适合接管模型：数据迁移成本极高（GB-TB）、
接管时数据不一致风险大（split-brain）、接管延迟从秒级→分钟级。

CBA 将故障处理按 Workload-class 差异化：
- **Stateless Cell**：保留 v2.5 接管模式（快速接管，故障不可见）
- **Stateful Cell**：就地隔离/自愈（故障可见但数据完整）

核心 trade-off：**用"故障可见 + 数据安全"换"故障不可见 + 数据风险"**。

---

## 二、当前状态（v3.1）

### 2.1 已有基础设施

- [x] Controller StatefulSet + etcd Learner sidecar（稳定 Pod 身份）
- [x] Shard-based hash distribution（1D 模型，v2.5+）
- [x] Self-healing reconcile loop（drift 检测 + 自动恢复）
- [ ] Workload-class 自动判定（Stateless vs Stateful）
- [ ] Cell-to-Pod 映射管理
- [ ] Cell 故障隔离策略

### 2.2 仍缺失

- 集群规模 < 100 namespace（cell 数不够，2D 收益边际有限）
- Workload-class 自动判定需要基于 K8s 资源类型 + PVC 挂载分析
- 团队工程容量不足以承担 O(N²) 测试矩阵
- 尚未遇到真实生产场景的"接管时数据迁移"痛点

---

## 三、架构设计

### 3.1 从 1D 到 2D

```
v2.5 模型 (1D Hash-sharding)：

  Shard 0 │ Shard 1 │ Shard 2 │ ... │ Shard 9
  ────────┼─────────┼─────────┼─────┼─────────
  所有资源无差别分配，故障时任意 pod 接管

CBA 模型 (2D Matrix-orchestration)：

                ┌──────────────┬──────────────┐
                │   Stateless  │   Stateful   │
  ┌─────────────┼──────────────┼──────────────┤
  │ Shard 0     │  C[0][SL]    │  C[0][SF]    │
  │ Shard 1     │  C[1][SL]    │  C[1][SF]    │
  │ ...         │     ...      │     ...      │
  │ Shard N     │  C[N][SL]    │  C[N][SF]    │
  └─────────────┴──────────────┴──────────────┘

  C[i][SL] 挂 → 走 v2.5 快速接管路径（其他 SL Pod 接管）
  C[i][SF] 挂 → 就地隔离/自愈，不触发数据迁移
```

### 3.2 Cell 到 Pod 的映射

```
StatefulSet Pod 身份：
  controller-0 → Shard[0..3] 的 Stateless Cell + Shard[0..3] 的 Stateful Cell
  controller-1 → Shard[4..6] 的 Stateless Cell + Shard[4..6] 的 Stateful Cell
  controller-2 → Shard[7..9] 的 Stateless Cell + Shard[7..9] 的 Stateful Cell

接管规则：
  controller-0 挂 → controller-1 接管 Shard[0..3][SL]（Stateless），
                     Shard[0..3][SF] 等待 controller-0 自愈（Stateful）
```

### 3.3 故障处理策略

**Stateless Cell 故障**：
```
Cell[5][SL] 所属 Pod 挂
  ↓
其他 SL Cell 所属 Pod 接管这些 namespace 的 Deployment/Service/ConfigMap
  ↓
快速 reconcile（秒级）
  ↓
对外表现：单点故障不可见（与 v2.5 行为一致）
```

**Stateful Cell 故障**：
```
Cell[5][SF] 所属 Pod 挂
  ↓
不触发数据迁移（跨 Pod 接管有状态资源的成本太高）
  ↓
等待 Pod 自愈（K8s StatefulSet 自动重启）
  ↓
若长时间不恢复：报警 + 暴露给用户（业务感知降级，但数据完整）
  ↓
对外表现：故障可见但可控
```

---

## 四、Workload-class 自动判定

### 4.1 判定规则（草案）

```go
type WorkloadClass int

const (
    ClassStateless WorkloadClass = iota
    ClassStateful
)

func ClassifyWorkload(resource ResourceInfo) WorkloadClass {
    // 明确有状态
    if resource.Kind == "StatefulSet" || resource.Kind == "PersistentVolumeClaim" {
        return ClassStateful
    }
    // Deployment 但有 PVC 挂载 → 有状态
    if resource.Kind == "Deployment" && hasPVCMount(resource) {
        return ClassStateful
    }
    // Service 类型判定
    if resource.Kind == "Service" && resource.Spec.ClusterIP == "None" {
        // headless service 通常服务有状态工作负载
        return ClassStateful
    }
    return ClassStateless
}
```

### 4.2 CRD 判定：OwnerReference 递归

Operator 管理的自定义资源（TiDB、Argo Rollouts 等）虽然 Kind 不是 StatefulSet，
但它们管理的底层 Pod 往往挂载了 PVC。需要通过 OwnerReference 递归判定：

```go
func ClassifyCRD(resource ResourceInfo) WorkloadClass {
    // 向下递归：这个 CR 的 ownerReferences 链条上
    // 是否有 StatefulSet 或挂载 PVC 的 Pod？
    chain := resolveOwnerChain(resource)
    for _, r := range chain {
        if class := ClassifyWorkload(r); class == ClassStateful {
            return ClassStateful
        }
    }
    return ClassStateless
}
```

判定原则：**追踪到叶子资源再判断**。CRD → Deployment/StatefulSet → Pod → PVC 挂载检查。

### 4.3 Annotation 逃生舱

自动判定永远有边界 case。提供手工覆盖通道：

```yaml
# namespace 级别
apiVersion: v1
kind: Namespace
metadata:
  name: my-database
  labels:
    kubepivot.io/workload-class: stateful  # 强制标记为有状态

# 单个资源级别
metadata:
  annotations:
    kubepivot.io/workload-class: stateful
```

优先级：Resource annotation > Namespace label > 自动判定。

### 4.4 误判风险

| 误判类型 | 后果 | 缓解 |
|---------|------|------|
| Stateless → Stateful | 资源的接管路径被阻断，故障恢复依赖 Pod 自愈而非快速接管 | Annotation 逃生舱 + 人工覆盖 |
| Stateful → Stateless | 有状态资源被错误接管，可能触发数据不一致 | PVC 挂载检测作为最后防线 + CRD OwnerReference 递归 |

---

## 五、扩容时的 Stateful Cell 迁移（Handover 协议）

### 5.1 问题

当 S 从 3 扩到 5（例如 `kp controller update --apply`），Hash 重分配会强制
部分 Stateful Cell 从 `controller-0/1/2` 迁移到新 Pod `controller-3/4`。
如果即时 Hash 重分配，所有涉及的 Stateful Cell 会在一个 reconcile 周期内
同时释放旧分片 + 抢占新分片——大规模有状态资源抖动。

### 5.2 Handover 协议

分两步走，避免数据搬运风暴：

```
Phase 1 — 预热（Warm-up）：
  目标 Pod（controller-3/4）启动 etcd Learner sidecar
  从 controller-0/1/2 拉取 WAL 快照
  直到数据同步延迟 < 100ms 且落后 < 1000 entries
  → kp explain 显示: Status: Warming (synced 95%, eta 12s)

Phase 2 — 切换（Handover）：
  目标 Pod 已追上数据 → promote 当前 etcd Learner
  → 原子切换 Cell 归属（ConfigMap 更新 → 旧 Pod 释放 Lease → 新 Pod 抢占）
  → 切换窗口: < 2s
  → kp explain 显示: Status: Active
```

### 5.3 触发方式

**手动触发 + Score 辅助**，不自动迁移：

```
$ kp controller scale --shards 5

📊 扩容影响分析：
  Stateless Cell: 12 个 → 即时接管，无影响
  Stateful Cell:   3 个 → 需要迁移

  Cell                    当前 Pod      目标 Pod     数据量     迁移评分
  ─────────────────────────────────────────────────────────────────
  my-database[SF]         controller-0  controller-3  2.3 GiB    85/100
  redis-cluster[SF]       controller-1  controller-4  856 MiB    62/100
  kafka-broker[SF]        controller-2  controller-3  4.1 GiB    91/100

  推荐迁移顺序: redis-cluster → my-database → kafka-broker

💡 运行 kp controller scale --apply 开始逐个迁移
```

Score 公式：
```
Score = w1 × (1 - migration_cost/max_cost)     # 成本越低分越高
      + w2 × data_size_rank                     # 数据量越小分越高
      + w3 × (1 - reconcile_init_time/max_time) # 启动越快分越高
```

### 5.4 Fencing——防脑裂

Handover 切换窗口内，旧 Pod 可能因网络分区延迟释放 Lease，
新 Pod 已通过 Warm-up Gate 准备抢占。两个写入者同时存在 → etcd 数据冲突。

解决方案：**Migration Barrier + Graceful Stop**。

```
Phase 2 — 切换（含 Fencing）：

  Step 1: 新 Pod 在 etcd 写入 fence key：
    /kubepivot/cells/my-database/fence {owner: controller-3, epoch: 42}

  Step 2: 旧 Pod (controller-0) 的 sidecar watch 到 fence key
    → 旧 Pod 进入 ReadOnly 模式（拒绝新写入）
    → 旧 Pod 释放 Lease（主动退出）

  Step 3: 新 Pod 确认旧 Pod Lease 已释放（等 2×TTL = 30s 或显式确认）
    → 新 Pod 抢占 Lease + 删除 fence key
    → 新 Pod 进入 Active
```

超时处理：
- 旧 Pod 在 30s 内未响应 fence key → 新 Pod 强制接管（记录 WARN 日志），K8s 最终会杀掉旧 Pod
- Fence key 自带 TTL（60s），防止 fence 残留导致 Cell 永久锁死

### 5.5 Warm-up 并发控制

多个 Stateful Cell 同时 Handover 时，WAL 同步是 IO 和带宽密集型操作。
若 5 个 Cell 同时拉快照，可能挤兑 Controller 自身 gRPC 带宽，
甚至导致 Stateless Cell 心跳超时触发不必要接管。

**并发槽位限制**：

```go
const MaxConcurrentWarmups = 2  // 集群级上限

var warmupSlots = make(chan struct{}, MaxConcurrentWarmups)

func (h *Handover) StartWarmup(cell string) error {
    select {
    case warmupSlots <- struct{}{}:
        defer func() { <-warmupSlots }()
        return h.doWarmup(cell)
    case <-time.After(30 * time.Second):
        return fmt.Errorf("warmup slot not available, retry later")
    }
}
```

`kp controller scale` 输出中展示排队状态：

```
📊 扩容影响分析：
  Stateful Cell: 3 个 → 需要迁移

  Cell                    状态       数据量     迁移评分
  ────────────────────────────────────────────────────
  my-database[SF]         排队中      2.3 GiB    85/100
  redis-cluster[SF]       预热 87%    856 MiB    62/100
  kafka-broker[SF]        等待槽位    4.1 GiB    91/100

  并发限制: 2 个，当前 1 个活跃 + 0 个排队
```

---

## 六、Sidecar etcd 数据预热

### 6.1 问题

StatefulSet Pod 漂移或重启后，新 Pod 的 etcd Learner sidecar 需要从集群
重新同步数据。如果同步未完成就接管 Cell，可能出现数据不一致。

### 6.2 Warm-up Gate

Pod 在以下条件全部满足前，不接管任何 Stateful Cell：

```go
func (s *SidecarGate) CanTakeCell() bool {
    // 1. Learner 已连接到集群
    if s.learnerStatus != "connected" { return false }
    // 2. 同步延迟 < 100ms
    if s.syncLatency > 100*time.Millisecond { return false }
    // 3. 落后 entries < 1000
    if s.behindEntries > 1000 { return false }
    // 4. 已追平 95% 以上
    if s.syncProgress < 0.95 { return false }
    return true
}
```

### 6.3 绝对阈值 vs 相对健康度

§6.2 的 100ms 延迟阈值是**绝对阈值**。当主 etcd 集群负载过高，
所有 sidecar 延迟都抖动到 200ms 时，所有副本都会失去接管权限——
Stateful 资源管理全集群死锁。

需要引入**相对健康度**（"在一群烂苹果里挑个不太烂的"）：

```go
func (s *SidecarGate) CanTakeCellRelative(clusterPeers []SidecarStatus) bool {
    // 如果全集群延迟 > 100ms，不再用绝对阈值
    allSlow := true
    for _, peer := range clusterPeers {
        if peer.syncLatency <= 100*time.Millisecond {
            allSlow = false
            break
        }
    }

    if allSlow {
        // "烂苹果模式"：延迟最低的 1/3 节点仍可接管
        rank := s.rankByLatency(clusterPeers)
        return rank <= len(clusterPeers)/3 && s.syncProgress >= 0.95
    }

    // 正常工作模式：绝对阈值
    return s.syncLatency <= 100*time.Millisecond && s.syncProgress >= 0.95
}
```

决策流程：
```
1. 检查全集群是否有 ≥1 个节点延迟 < 100ms
   → 有：走绝对阈值（正常模式）
   → 无：走相对排名（降级模式，取前 1/3）

2. 降级模式下记录 WARN：
   "cluster-wide etcd latency degradation detected, falling back to relative health ranking"
```

### 6.4 kp explain 集成

```
$ kp explain --cell my-database

Cell: my-database
  Class: Stateful
  Current Pod: controller-2

  ── Sidecar Status ──
  Role:       Learner
  Sync:       99.8% (behind 47 entries, latency 12ms)
  Status:     Active

  上次切换: 2026-05-05T10:00:00Z (controller-1 → controller-2)
  切换耗时: 1.8s
```

---

## 七、可观测性——Holding 状态与审计追踪

### 7.1 问题

`controller-0` 挂掉后，`controller-1` **主动决定不接管** `Shard[0..3][SF]`。
如果没有清晰的审计日志，运维人员看到"资源没人管"会误以为是 Bug。

### 7.2 Holding 状态

在 `kp explain` 和 controller 日志中引入明确的 Holding 状态：

```
$ kp explain --cell my-database

Cell: my-database
  Status: Holding
  Reason: controller-0 self-healing in progress (Policy: Stateful-In-Place)
  Since:  2026-05-05T10:15:00Z (2min 30s ago)
  Expected Recovery: < 5min (StatefulSet pod restart in progress)
  Fallback: Manual takeover after 10min timeout
```

### 7.3 审计日志

controller 在拒绝接管 Stateful Cell 时必须记录：

```
level=INFO msg="cell takeover skipped"
  cell=my-database
  class=Stateful
  reason="Stateful-In-Place policy: waiting for controller-0 self-healing"
  holding_since=2m30s
  fallback="manual takeover after 10min or kp controller takeover --cell my-database"
```

---

## 八、迁移成本量化模型

### 8.1 目的

为 Handover 的 Score 排序提供数据支撑，为自动决策提供阈值。

### 8.2 成本公式

```
Cost_migration = Size_etcd_snapshot × Network_latency + Time_reconcile_init

其中：
  Size_etcd_snapshot  = etcd Learner 的 WAL 快照大小（Bytes）
  Network_latency     = 源 Pod 到目标 Pod 的网络延迟（ms）
  Time_reconcile_init = 接管后首次 reconcile 预期时间（s）
                       估算：reconcile_init ≈ projects_in_cell × avg_reconcile_time

阈值：
  Cost_migration > SLO_threshold → 强制执行就地隔离策略
  SLO_threshold = 30s（v3.2 可配置）
```

### 8.3 与自动判定的集成

```go
func ShouldMigrate(cell CellInfo) (bool, string) {
    cost := estimateMigrationCost(cell)
    if cost > SLOThreshold {
        return false, fmt.Sprintf(
            "migration cost %.1fs exceeds SLO threshold %.1fs, fallback to in-place isolation",
            cost.Seconds(), SLOThreshold.Seconds())
    }
    return true, fmt.Sprintf("migration cost %.1fs within threshold", cost.Seconds())
}
```

---

## 九、工程税

1. **Cell 粒度选择**：太细（1000+ cells）管理爆炸，太粗（< 10 cells）失去 CBA 意义。KubePivot 当前规模 < 100 ns，cell 数 < 20，收益边际有限。

2. **测试矩阵**：CBA 隔离需测单 cell 正常、故障不扩散、cross-cell 通信、拓扑变化、class 边界判定——O(N²) 测试成本。

3. **Workload-class 判定准确率**：Deployment + PVC 的边界 case 需要准确检测，误判代价高。

4. **Cell 间通信开销**："完全隔离"意味着 cross-cell 调用走 gRPC over network 而非进程内 channel，延迟从 μs → ms。

---

## 十、重新评估时机

v3.2+ 设计阶段重新评估 CBA 是否提升到 ROADMAP。触发条件（需同时满足）：

- [x] Controller StatefulSet + etcd Learner 已落地（v3.1）
- [ ] 集群规模 ≥ 100 namespace
- [ ] 有状态资源保护（StatefulSet/PVC）已引入 reconcile loop
- [ ] 团队工程容量可承担 O(N²) 测试矩阵
- [ ] 已有真实生产场景遇到"接管时数据迁移"痛点

---

## 十一、可视化设想

```
正常时：grid 均匀色温（每个 cell 绿色）
故障时：某区域出现"红斑"
扩散时：红斑沿 cell 邻接关系蔓延，直到隔离边界（cell 边界）即停

故障的视觉呈现像生物炎症 — 比 log/metrics 直观 10x。

适用前提：cell 数 < 20 时意义不大（list 视图够用），cell 数 > 50 时成为核心 debug 工具。
```

---

## 十二、相关文档

- [FUTURE.md](../../FUTURE.md) — CBA 种子（F1）
- [sharding.md](sharding.md) — 当前 1D hash-sharding 实现
- [state-machine.md](state-machine.md) — 部署状态机
- [decision-stack.md](decision-stack.md) — 三层资源决策栈

---

## 编辑记录

```
2026-05-05  从 FUTURE.md F1 种子演化为正式设计文档
            v3.1 前置条件更新：StatefulSet 迁移已完成
            qc 审查补充 8 项关键设计：
            - §4.2 CRD OwnerReference 递归判定 + §4.3 Annotation 逃生舱
            - §5 Stateful Cell 扩容 Handover 协议（Warm-up + 原子切换 + 手动触发 + Score）
            - §5.4 Fencing 防脑裂（Migration Barrier + Graceful Stop）
            - §5.5 Warm-up 并发控制（MaxConcurrentWarmups=2 + 排队状态展示）
            - §6 Sidecar etcd 数据预热 Gate（4 条件满足才接管）
            - §6.3 绝对阈值 vs 相对健康度（全集群变慢时不死锁，取前 1/3）
            - §7 可观测性：Holding 状态 + 审计日志（防止运维恐慌）
            - §8 迁移成本量化模型（Cost_migration + SLO 阈值）
            设计草案保留 🌱 状态，待 v3.2+ 重新评估
```
