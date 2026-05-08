# kp scheduler

> 乾枢 (QianShu) v3.0 智能调度系统。
> 最后更新：2026-05-08（v3.2）

---

## 概览

乾枢调度器是 KubePivot 的两维资源调度引擎：

- **维度 A (Bin Packing)** — 静态装箱：FFD 排序 + 0-1 背包 DP 逐节点求解，输出 Node → Pod 分配
- **维度 B (Sizing)** — 动态画像：Prometheus 7 天历史 → sizing.Compute 推荐资源配置
- **双 DP 协同** — 装箱不收敛时自动降配重试（最多 5 次迭代）
- **运行时重调度** — 周期扫描节点不平衡 → 自动迁移 Pod
- **池化调度** — GPU / label 自动分组 + O(1) 原子计数器 + 三代性能方案
- **CBA (Cell-Based Architecture)** — 分片 + 一致性 Hash + Fencing 协议
- **Admission Webhook** — Pod 创建时注入 nodeSelector，确保调度决策落地

---

## CLI 子命令

### status

查看集群资源摘要 + 节点/Pod 统计。

```
kp scheduler status [flags]
```

| Flag | 默认值 | 说明 |
|---|---|---|
| `--kubeconfig` | — | kubeconfig 路径 |
| `--carbon-region` | — | 碳感知区域代码（如 US-West，空则跳过） |

**输出内容**：节点数 / Pod 总数 / Running 数 / 集群 CPU & Memory 总量 / 利用率百分比 / 碳排放强度（可选）。

**RBAC**：`PermSizing`

### reschedule

手动触发一次运行时重调度（BinPack + 迁移执行）。

```
kp scheduler reschedule [flags]
```

| Flag | 默认值 | 说明 |
|---|---|---|
| `--kubeconfig` | — | kubeconfig 路径 |
| `--max-migrations` | `5` | 单次最大迁移数（0 = 5% 总 Pod 数） |

**执行流程**：

1. `KubectlAdapter.ListAllNodes / ListAllPods` — 全量扫描集群
2. `Scheduler.Schedule()` — 运行双 DP 协同调度
3. 输出 Plan（分配数 + 收敛状态）
4. `Rescheduler.RunOnce()` — 检测不平衡 + 执行迁移
5. 调度结果写入 `configs/components.yaml`（nodeSelector）

**RBAC**：`PermSizing`

---

## 核心数据结构

### ResourceRequest

```go
type ResourceRequest struct {
    CPU    int64 // 毫核 (millicores)
    Memory int64 // 字节 (bytes)
    GPU    int64 // 毫卡 (MilliGPUUnit = 1000 per GPU card)
}
```

### NodeInfo

```go
type NodeInfo struct {
    Name              string
    AllocatableCPU    int64             // millicores
    AllocatableMemory int64             // bytes
    GPU               []GPUInfo
    Labels            map[string]string
}

type GPUInfo struct {
    Product      string // e.g. "NVIDIA-A100-SXM4-40GB"
    Index        int
    MemTotal     int64    // bytes
    MemUsed      int64    // bytes
    Health       string   // "Healthy" | "Degraded" | "Failed"
    NVLinkDomain int      // NVSwitch domain ID
}
```

### PodInfo

```go
type PodInfo struct {
    Namespace   string
    Name        string
    NodeName    string         // ← 当前所在节点，关键字段
    Phase       string         // "Running", "Pending" 等
    Labels      map[string]string
    Annotations map[string]string
    Requests    ResourceRequest
}
```

### SchedulingPlan

```go
type SchedulingPlan struct {
    PodAssignments    map[string]string              // "ns/name" → nodeName
    SizingSuggestions map[string]*sizing.Suggestion   // 维度 B 资源推荐
    Iterations        int
    Converged         bool
}
```

---

## 接口体系

调度器所有依赖均为接口，支持真实实现 + 测试 mock 注入：

| 接口 | 方法 | 实现 |
|---|---|---|
| `PodLister` | `ListAllPods(ctx)` | `KubectlAdapter` / `InformerAdapter` |
| `NodeLister` | `ListAllNodes(ctx)` | `KubectlAdapter` / `InformerAdapter` |
| `MetricsProvider` | `GetPodMetrics()`, `QueryRange()` | `SizingAdapter` (Prometheus 未接线，降级本地采样) |
| `SizingProvider` | `Compute(samples, profile)` | `SizingAdapter` → `sizing.Compute` |
| `PlanWriter` | `WriteAssignments(path, assignments)` | `FilePlanWriter` → `configs/components.yaml` |
| `PodAssigner` | `AssignPod(ctx, pod)` | `Scheduler.AssignPod` (Best Fit) |

---

## 维度 A：Bin Packing（装箱调度）

### 算法流程

```
BinPack(nodes, pods)
  │
  ├─ 1. Pre-filter GPU nodes
  │     HasGPURequest(pod) → FilterGPUNode(nodes, product, minGPUs)
  │     GPU product 不匹配 → 跳过该节点
  │
  ├─ 2. Filter Running pods
  │     Phase != "Running" → 跳过
  │     Requests.CPU == 0 && Requests.Memory == 0 → 跳过
  │
  ├─ 3. FFD sort (sortPods)
  │     score = α * (CPU/maxCPU) + β * (Memory/maxMem) + γ * GPU
  │     默认: α=0.5, β=0.5
  │     GPU Pod 优先（大块优先装箱，减少碎片）
  │
  ├─ 4. Per-node 0-1 Knapsack DP (dpNode)
  │     ┌─ CPU 离散化: 50mc slot
  │     ├─ Memory 离散化: 64MiB slot
  │     ├─ 目标: max Σ Memory, 约束: CPU capacity
  │     ├─ GPU 预过滤: product + NVLink domain 匹配
  │     └─ 2D keep 表回溯选中的 pod
  │
  └─ 5. Aggregate
        剩余未分配 pod → Converged = false
```

### FFD 排序策略

`scheduling/sort_pods.go`（在 `bin_pack.go` 中）：

- **GPU Pod** 优先排列（大块优先，减少装箱碎片）
- 普通 Pod 按 `alpha * CPU_norm + beta * Mem_norm` 降序
- α = 0.5, β = 0.5 为默认权重（CPU/Memory 等权）

### GPU 调度规则

GPU 资源单位为 **毫卡 (MilliGPU)**，`1 GPU = 1000 mGPU`。

**分配约束**：
1. GPU product 必须匹配（如 `NVIDIA-A100-SXM4-40GB` 只能分到同型号节点）
2. NVLink domain 亲和（同 NVSwitch 的 GPU 优先配给同一 Pod）
3. 跨代 GPU 迁移禁止（H100 → A100 blocked）
4. GPU Pod 默认不可迁移，除非 `kubepivot.io/gpu-hardware-failure=true`（DCGM Xid 硬件故障）

### AssignPod（Best Fit 单 Pod 调度）

Webhook 触发或迁移时的单 Pod 调度：

```
AssignPod(nodes, pod)
  │
  ├─ Filter: GPU 匹配 + 容量满足
  │
  └─ Best Fit: 找剩余容量最小的可满足节点
     → 最小化碎片，提高装箱密度
```

---

## 维度 B：Sizing（资源画像）

双 DP 协同中维度 B 的触发条件：维度 A 无法收敛（有 Pod 未分配）。

```
Coordinator loop (最多 5 次迭代):
  │
  ├─ 1. BinPack → 收集 unassigned pods
  │
  ├─ 2. 每个 unassigned pod:
  │     ReduceCap(cap, -10%)           ← 降配 10%
  │     MetricsProvider.QueryRange()    ← 查询 7 天 Prometheus 历史
  │     SizingProvider.Compute(samples) ← 调用 sizing 推荐引擎
  │     Request = min(sizing推荐, cap)  ← 取较小值
  │
  ├─ 3. 重新 BinPack(modifiedPods)
  │
  └─ 终止条件:
      ├─ Converged = true (全部装箱成功)
      ├─ Iterations >= 5
      └─ 资源配置触及下限 (CPU < 50m 或 Memory < 64MiB)
```

---

## Rescheduler（运行时重调度）

### 配置

在 `configs/system.yaml` 的 `scheduler` 段：

```yaml
scheduler:
  rescheduleInterval: 5m      # 扫描间隔
  maxMigrations: 0            # 单次最大迁移数（0 = 5% 总 Pod 数）
  jitterThreshold: 0.95      # CPU 利用率抖动阈值
  jitterWindow: 5m            # 抖动检测窗口
  jitterSpikeCount: 3         # 窗口内允许的最大 spike 次数
```

### 不平衡检测

**高负载节点**：CPU 或 Memory 利用率 > 平均值的 1.2x
**低负载节点**：两个维度都 < 平均值的 0.8x

配对：每个高负载节点找一个低负载节点，计算建议迁移量：
```
suggestedCPU = min(excessCPU, availCPU)
suggestedMem = min(excessMem, availMem)
```

### 迁移执行

```
for each imbalance pair:
  for each Running pod on High node:
    ├─ isMigratable(pod) ?
    │   ├─ StatefulSet managed → NO
    │   ├─ blue-green locked   → NO
    │   ├─ GPU (no hardware failure) → NO
    │   └─ GPU + hardware failure   → YES
    │
    ├─ Pod fits in SuggestedCPU/SuggestedMem ?
    │
    ├─ AssignPod(pod) → target node (Best Fit)
    ├─ SetMigrationTargetHint(ns, name, targetNode)
    ├─ kubectl delete pod --grace-period=30 --wait=false
    └─ migrated++
```

### 抖动降级层级

| Level | 触发 | 行为 |
|---|---|---|
| 0 (Normal) | 默认 | 完整重调度 |
| 1 (Observation) | 窗口内 spikes >= 3 | 迁移量减半，最小 1 |
| 2 (Emergency) | 窗口内 spikes >= 6 或 OOM | 停止迁移，只记录日志 |
| 3 (Paused) | 用户手动暂停 (`Pause()`) | 停止一切操作，需手动 `Resume()` |

层级恢复：窗口到期后逐步降级（Level 3 → 2 → 1 → 0）。

---

## 池化调度层

### Pool 自动发现

`AutoDiscoverPool(node)` 优先级：

1. **Label**：`kubepivot.io/pool=<name>`
2. **GPU**：`gpu-<product>`（如 `gpu-NVIDIA-A100-SXM4-40GB`）
3. **Memory/CPU ratio**：`mem<ratio>x`（如 `mem2x` 表示 Memory/CPU = 2:1）
4. **Default**：`"cpu"`

### 三代性能方案

| 方案 | 方法 | 延迟 (100k Pods, 2k Nodes) | 加速比 | 适用场景 |
|---|---|---|---|---|
| Exact | `ComputePoolUtilization` | 1805 ms | 1x | 小型集群 / 精确统计 |
| Sampled | `ComputePoolUtilizationSampled` | 1099 ms | 1.6x | 中型集群 / 误差 <3% |
| O(1) Counters | `PoolUtilTracker.ComputeUtilO1` | 75.5 ms | **23.9x** | 大规模集群 |

### PoolIndex 增量分组

`Rebuild / Upsert / Remove` 操作，单次 O(1)。通过 label 匹配分组 Pod，避免全量重建。

### DetectPoolImbalance

Pool 级别的不平衡检测：

- **High pool**：任一资源利用率 > 平均值的 1.2x
- **Low pool**：所有资源 < 平均值的 0.8x

### GPU 池化约束

- `CanMigrateGPU(source, target)` — 验证 GPU 型号是否兼容跨节点迁移
- 同 NVLink domain 优先
- 跨代 GPU 禁止迁移（H100 ↔ A100）

### PoolScore

```
PoolScore = (1 - maxUtil) * 0.5 + avgFragmentRate * 0.5
```

低分 Pool 优先接受新任务，促进负载均衡。

---

## CBA (Cell-Based Architecture)

### Cell 类型

```go
type CellClass string // "stateless" | "stateful"

type Cell struct {
    Name      string
    Class     CellClass
    Shard     int
    Resources []string   // 资源名列表
    Labels    map[string]string
}
```

- **Stateless**：Deployment / Service / ConfigMap — 快速 takeover
- **Stateful**：StatefulSet / PVC / 数据库 — 需要 Fencing 协议

### HashRing — 一致性负载分布

FNV32a 加权 Hash 环，默认 40 vnodes/pod：

```
NewHashRing(pods, vnodes=40)
NewWeightedHashRing(pods, weights, baseVnodes=40)
```

**核心方法**：
- `GetPod(key)` — 根据 key 确定 Owner Pod
- `DiffCells(cells, newPods)` — Pod 列表变化时计算 ownership drift
- `MapCellsToPods(cells, podNames)` — 将 Cells 分配到 Controller Pods

### Fencing 协议

六阶段状态机，用于有状态服务的故障接管：

```
None → Requested → Draining → Paused → Ready → Complete
```

**FencingConfig**：

```go
type FencingConfig struct {
    Protocol     SignalProtocol  // annotation-watch | webhook | sigterm
    RetryMax     int            // default 3
    RetryBackoff time.Duration  // default 5s
    Timeout      time.Duration  // default 15min
    HardTimeout  time.Duration  // default 60min (Point of No Return)
}
```

**Fallback 链**：`AnnotationWatch → Webhook → SIGTERM`

**ShouldTakeover 规则**：
- Stateless Cell：health failure 直接 takeover
- Stateful Cell：必须等待 Fencing phase = Ready

---

## Migration Manager（迁移状态机）

5 阶段迁移状态机：

```
Evicting → WaitingForReady → Complete   (正常路径)
  ├→ Paused → WaitingForReady           (有状态重试)
  └→ Failed                             (超时 / 重试超限)
```

**Migration 结构**：

```go
type Migration struct {
    ID         string
    PodNS      string
    PodName    string
    SourceNode string
    TargetNode string
    Phase      MigrationPhase
    StartedAt  time.Time
    CellType   string          // "stateless" | "stateful"
    RetryCount int
}
```

**关键方法**：
- `Rehydrate(pods)` — controller 重启时从 Pod annotations 恢复进行中的迁移
- `Reconcile(ctx)` — 周期推进迁移状态机
- `StartMigration(mig)` — 发起新迁移
- `DefaultLeaseFencer()` — K8s Lease-based 隔离检查（有状态 Pod）
- `HasMigrationAnnotation(annotations)` — 检查 Pod 是否具有迁移 annotation

**迁移 Annotation**：

| Annotation | 说明 |
|---|---|
| `kubepivot.io/migration-id` | 迁移 ID |
| `kubepivot.io/migration-source-node` | 源节点 |
| `kubepivot.io/migration-target-node` | 目标节点 |
| `kubepivot.io/migration-phase` | 当前阶段 |
| `kubepivot.io/migration-start-timestamp` | 开始时间 (RFC3339) |
| `kubepivot.io/migration-cell-type` | stateless / stateful |
| `kubepivot.io/migration-active` | Label：标记活跃迁移 |

**配置**（`configs/system.yaml` 中 `migration` 段）：

```yaml
migration:
  evictTimeout: 10m
  waitReadyTimeout: 5m
  pausedBackoff: 30s
```

---

## Admission Webhook

HTTPS Admission Webhook 运行在 Controller Pod 内（`:443`），拦截 Pod CREATE 事件，注入调度决策。

### 端点

| Path | 方法 | 用途 |
|---|---|---|
| `/mutate` | POST | Pod 变异：注入 `nodeSelector.kubernetes.io/hostname` |
| `/health` | GET | 健康检查 |

### Mutate 逻辑

```
Pod CREATE → WebhookServer.mutate()
  │
  ├─ 1. 检查 migrationTargetHints (sync.Map)
  │     有 → 直接路由到目标节点（迁移场景）
  │
  ├─ 2. 无 hint → Scheduler.AssignPod(Best Fit)
  │     计算最佳节点
  │
  ├─ 3. 生成 JSON Patch:
  │     [{op: "add", path: "/spec/nodeSelector", value: {kubernetes.io/hostname: "node-1"}}]
  │
  └─ 返回 AdmissionReview
        Always Allowed: true (调度失败不阻断创建)
        FailurePolicy: Ignore
```

### TLS 证书

自签名证书自动生成（`GenerateSelfSignedCert()`）：

- 2048-bit RSA
- 1 年有效期
- Webhook YAML 含 `caBundle` 自动注入

### WebhookDeployConfig

```go
type WebhookDeployConfig struct {
    ServiceName      string   // webhook service DNS 名
    ServiceNamespace string
    ServicePort      int      // default 443
    CAPEM            []byte   // CA cert PEM for caBundle
}
```

---

## Prometheus 指标

通过 `RegisterMetrics(reg)` 注册 7 个指标：

| 指标 | 类型 | 标签 | 说明 |
|---|---|---|---|
| `kubepivot_scheduler_iterations_total` | Counter | — | 调度迭代总数 |
| `kubepivot_scheduler_solve_duration_seconds` | Gauge | — | 最后一次解决耗时 |
| `kubepivot_node_utilization_cpu_ratio` | GaugeVec | node | 每节点 CPU 利用率 |
| `kubepivot_node_utilization_memory_ratio` | GaugeVec | node | 每节点 Memory 利用率 |
| `kubepivot_pod_oomkill_total` | Counter | — | OOMKilled Pod 累计数 |
| `kubepivot_scheduler_fallback_level` | Gauge | — | Rescheduler 降级层级 (0-3) |
| `kubepivot_pod_migration_total` | Counter | — | 迁移 Pod 累计数 |

---

## 适配器层

### KubectlAdapter

通过 `kubectl` CLI 获取集群状态：

- `ListAllNodes`：`kubectl get nodes -o json` — 解析 Allocatable CPU/Mem/GPU
- `ListAllPods`：`kubectl get pods -A -o json` — 解析 Name/NodeName/Phase/Labels/Requests
- GPU 解析来源：`nvidia.com/gpu.product` / `nvidia.com/gpu.memory` / `nvidia.com/gpu.nvswitch` labels

### InformerAdapter

从 eventstream KVCache 读取数据（v3.2 接线）：

- 主路径：`PodCache / NodeCache` → KVCache 内存读取
- 降级路径：cache 未就绪 → fallback 到 `KubectlAdapter`
- fallback 限流：10s 冷却

### FilePlanWriter

调度结果写回 `configs/components.yaml`：

```
components:
  - name: myapp
    namespace: default
    nodeSelector:
      kubernetes.io/hostname: node-3
```

通过 `WriteAssignments(path, map["ns/name"]nodeName)` 注入 nodeSelector，原子写入（tmp file + rename）。

---

## Scheduler 架构全景

```
Controller (global.go)
  │
  ├── Scheduler (scheduler.go)
  │     │
  │     ├── Coordinator (coordinator.go)
  │     │     ├── BinPack (bin_pack.go)       ← 维度 A
  │     │     ├── MetricsProvider.QueryRange  ← 维度 B
  │     │     └── SizingProvider.Compute      ← 维度 B
  │     │
  │     └── AssignPod (adapter.go)            ← Best Fit
  │
  ├── Rescheduler (rescheduler.go)
  │     ├── computeNodeUtilization
  │     ├── detectImbalance
  │     ├── detectJitter (降级层级)
  │     ├── migratePods (AssignPod + evict)
  │     └── isMigratable (约束过滤)
  │
  ├── Pool Layer (pool.go)
  │     ├── ComputePoolUtilization (三代方案)
  │     ├── PoolIndex (增量分组)
  │     ├── PoolUtilTracker (O(1) atomic counters)
  │     ├── AutoDiscoverPool
  │     └── DetectPoolImbalance
  │
  ├── CBA (cba.go)
  │     ├── ClassifyWorkload (stateless/stateful)
  │     ├── HashRing (FNV32a 一致性 hash)
  │     ├── Fencing (6-phase state machine)
  │     └── ShouldTakeover
  │
  ├── MigrationManager (migration_manager.go)
  │     ├── Rehydrate (controller 重启恢复)
  │     ├── Reconcile (推进状态机)
  │     └── DefaultLeaseFencer (Lease 隔离)
  │
  ├── WebhookServer (webhook.go)
  │     ├── /mutate → AssignPod + migration hint
  │     ├── /health
  │     └── TLS self-signed certs
  │
  └── Adapters
        ├── KubectlAdapter (kubectl CLI)
        ├── InformerAdapter (KVCache + fallback)
        ├── SizingAdapter (metrics → sizing.Compute)
        └── FilePlanWriter (→ configs/components.yaml)
```

---

## 相关命令

- `kp bench scale` — 池化调度性能 benchmark
- `kp bench pack` — bin packing 性能 benchmark
- `kp bench reschedule` — 重调度性能 benchmark
- `kp sizing` — 资源画像
- `kp controller start --global` — Controller 启动时接线 Scheduler + Rescheduler + Webhook
