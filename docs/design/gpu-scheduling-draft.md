# KubePivot v3.1 GPU 调度系统 — 设计草案

> 状态：📝 draft — 待 qc 拍板
> 日期：2026-05-02
> 关联：[scheduler](../../internal/scheduler/) / [sizing](../../internal/sizing/) / [metrics](../../internal/metrics/) / [eventstream](../../internal/eventstream/)
> 背景：v3.0 乾枢调度器完成维度 A（CPU/Mem bin packing）+ 维度 B（CPU/Mem sizing）。
>       v3.1 目标：在现有调度框架上加 GPU 维度，不推翻已有架构

---

## 一、目标

GPU 调度不是新建一个调度器，而是给乾枢加一个维度。当前乾枢的二维 DP 是 `Pod × (CPU, Memory)`，GPU 扩展后变成三维 `Pod × (CPU, Memory, GPU)`。

核心原则：**加维度，不改架构**。

### 1.1 已有基础设施（v3.0 已就位）

| 组件 | v3.0 能力 | GPU 扩展方式 |
|---|---|---|
| `scheduler/bin_pack.go` | FFD + 0-1 背包 DP，CPU/Mem 维度 | 加 GPU 维度到 DP 矩阵 + NVLink 亲和约束 |
| `scheduling/dp.go` | 2D DP，CPU/Mem | 扩展为 3D DP，加 GPU 显存维度 |
| `metrics/` | PrometheusClient + KubectlMetricsClient | 加 DCGM exporter metric 解析 |
| `eventstream/` | Deployment informer | 加 GPU Node/Pod informer |
| `resources.yaml` | cpu/mem/gpu 预留字段 | 填入真实 GPU 调度逻辑 |
| `scheduler/coordinator.go` | 双 DP 协同收敛 | 维度 A+B+GPU 不变，公式加项 |
| `scheduler/rescheduler.go` | 周期性运行时重调度 | 加 GPU 迁移约束 |

### 1.2 不做什么

- ❌ 不替代 GPU Operator（NVIDIA GPU Operator 负责驱动/device plugin 部署，KubePivot 只管调度）
- ❌ 不替代 MIG 配置工具——nvidia mig-manager 或 GPU Operator 处理 MIG 分区，KubePivot 只读 `nvidia.com/mig-*` 资源
- ❌ 不引入 GPU 监控大盘——DCGM → Prometheus → Grafana，KubePivot 不画图
- ❌ 不做 GPU 共享（MPS/TimeSlicing）——v3.1 聚焦整卡调度，共享留 v3.2

---

## 二、架构：在乾枢上加一维

```
┌────────────────────────────────────────────────────────────┐
│                     乾枢 v3.0 (已有)                        │
│                                                             │
│  维度 A: BinPack(FDD + DP)   维度 B: Sizing(2D DP)         │
│  Pod × (CPU, Mem)            Pod × (CPU, Mem)               │
│  └─ coordinator 双 DP 协同                                   │
│                                                             │
├────────────────────────────────────────────────────────────┤
│                    乾枢 v3.1 (新增)                         │
│                                                             │
│  维度 G: GPU                                                │
│  ├─ Metrics 采集: DCGM exporter → PrometheusClient          │
│  ├─ Sizing: 3D DP  Pod × (CPU, Mem, GPU_Mem, GPU_Count)   │
│  ├─ BinPack: NVLink 亲和约束 + GPU 节点过滤                 │
│  ├─ 迁移约束: GPU Pod 不迁移 / StatefulSet 不迁移           │
│  └─ MIG 感知: 读 nvidia.com/mig-* 资源                     │
└────────────────────────────────────────────────────────────┘
```

---

## 三、GPU Metrics 采集

### 3.1 数据源

KubePivot 走 Prometheus 获取 GPU 指标（与 v2.9 sizing 的 `--prometheus-url` 一致）。DCGM exporter 是 NVIDIA 官方方案，暴露的 metric 包括：

| 指标名 | 含义 | 调度用途 |
|---|---|---|
| `DCGM_FI_DEV_GPU_UTIL` | GPU 利用率 (%) | 判断 GPU 是否被浪费 |
| `DCGM_FI_DEV_FB_USED` | 显存使用量 (MiB) | sizing 的 GPU_Mem 维度 |
| `DCGM_FI_DEV_FB_FREE` | 显存空闲量 (MiB) | 判断是否有余量 |
| `DCGM_FI_DEV_POWER_USAGE` | 功耗 (W) | 判断 GPU 是否空闲 |
| `DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL` | NVLink 总带宽 | 拓扑感知调度 |

### 3.2 新增 metric 类型

```go
// internal/metrics/gpu.go

type GPUMetrics struct {
    UUID        string          // GPU UUID
    Product     string          // "NVIDIA-A100-SXM4-40GB"
    Index       int             // GPU index on node
    Utilization float64         // 0-100
    MemUsed     int64           // bytes
    MemTotal    int64           // bytes
    PowerUsage  float64         // watts
    NVLinkBandwidth map[int]float64 // NVLink peer GPU index → BW
    Timestamp   time.Time
}

type NodeGPUMetrics struct {
    NodeName string
    GPUs     []GPUMetrics
}
```

### 3.3 Prometheus 查询 + Staleness 阈值

DCGM exporter 的 scrape interval 通常是 15-30s，比 K8s metrics-server 的 15s 慢。如果 Prometheus 最近一次 GPU 指标过期（staleness > 60s），sizing 逻辑应回退到保守模式——维持当前 `gpu.count` 不变，避免因监控链路延迟导致错误缩容。

```go
// internal/metrics/gpu.go

const (
    defaultGPUStalenessThreshold = 60 * time.Second  // 超过 60s 认为数据过期
)

type GPUStalenessError struct {
    NodeName   string
    LastSample time.Time
}

func (e *GPUStalenessError) Error() string {
    return fmt.Sprintf("GPU metrics for node %s are stale (last sample: %s ago)",
        e.NodeName, time.Since(e.LastSample))
}

func (c *PrometheusClient) QueryGPUMetrics(ctx context.Context, nodeName string, window time.Duration) ([]*NodeGPUMetrics, error) {
    // 查询后检查 staleness
    for _, m := range results {
        if time.Since(m.Timestamp) > defaultGPUStalenessThreshold {
            // 返回 error → sizing 回退到保守模式（不调 GPU count）
            return nil, &GPUStalenessError{NodeName: nodeName, LastSample: m.Timestamp}
        }
    }
    return results, nil
}
```

**保守模式行为**：sizing 遇到 `GPUStalenessError` 时，`RecommendedGPUCount = CurrentGPUCount`（不变），`Confidence = 0.0`。不降 GPU 数量，但打印 warn 提示用户检查 DCGM exporter。

---

## 四、维度 G: GPU Sizing（3D DP 扩展）

### 4.1 当前 2D DP 回忆

```go
// internal/sizing/dp.go (v2.9)
func Compute(ctx context.Context, samples []*PodMetrics, profile Profile) (*Suggestion, error)
```

DP 状态：`dp[i][j]` = 在 i 个样本、j 的 memory budget 下的最优 CPU 值。二维。

GPU 扩展后：`dp[i][j][k]` = 在 i 个样本、j 的 memory budget、k 的 GPU 数量 budget 下的最优 CPU 值。三维。

### 4.2 三维 DP 设计

```go
type Suggestion struct {
    // 已有字段
    CurrentCPU         int64
    CurrentMem         int64
    RecommendedCPU     int64
    RecommendedMem     int64
    Confidence         float64
    Profile            Profile
    SavingsCPU         float64
    SavingsMem         float64

    // v3.1 新增 GPU 字段
    CurrentGPUCount    int        // 当前申请了几张卡
    CurrentGPUMem      int64      // 当前申请的显存 (bytes)
    RecommendedGPUCount int       // 推荐几张卡
    RecommendedGPUMem  int64      // 推荐显存
    SavingsGPU         float64    // GPU 节省率
}
```

**权重扩展**：

```go
func WeightsForProfile(p Profile) (cpuW, memW, gpuW float64) {
    switch p {
    case ProfileWeb:
        return 0.7, 0.3, 0.0    // web 不需要 GPU
    case ProfileBatch:
        return 0.3, 0.5, 0.2    // 批处理 GPU 有一定权重
    case ProfileDB:
        return 0.3, 0.7, 0.0    // 数据库一般不用 GPU
    case ProfileGPU:              // 新 profile
        return 0.1, 0.2, 0.7    // GPU 任务显存优先
    default:
        return 0.5, 0.5, 0.0
    }
}
```

### 4.3 GPU 特定约束

#### 整卡约束

`RecommendedGPUCount` 必须是整数（不允许 0.5 张卡）。

#### 显存量化对齐（Quantization Factor）

不同于 CPU 可以精确到 1m（0.001 核），GPU 的显存推荐值必须对齐到 MIG-manager 能处理的实际分区大小。不能推荐一个"13.4GB"这种实践中不存在的数值。

```go
// 常见 GPU 型号的 MIG 分区模板
var migProfiles = map[string][]int64{
    "A100-SXM4-40GB": {5 * 1024 * 1024 * 1024, 10 * 1024 * 1024 * 1024, 20 * 1024 * 1024 * 1024, 40 * 1024 * 1024 * 1024}, // 5GB, 10GB, 20GB, 40GB
    "A100-SXM4-80GB": {10 * 1024 * 1024 * 1024, 20 * 1024 * 1024 * 1024, 40 * 1024 * 1024 * 1024, 80 * 1024 * 1024 * 1024},
    "H100-80GB":      {10 * 1024 * 1024 * 1024, 20 * 1024 * 1024 * 1024, 40 * 1024 * 1024 * 1024, 80 * 1024 * 1024 * 1024},
}

func quantizeGPUMem(rawBytes int64, gpuProduct string) int64 {
    profiles, ok := migProfiles[gpuProduct]
    if !ok {
        return rawBytes // 未知型号，不量化
    }
    // 向上取最近的 profile
    for _, p := range profiles {
        if rawBytes <= p {
            return p
        }
    }
    return profiles[len(profiles)-1] // 超出所有 profile → 整卡
}
```

#### MIG 感知

如果 GPU 支持 MIG（A100/H100），优先推荐 MIG 分区。例如 4g.20gb 分区比整卡便宜。`RecommendedGPUCount` 在 MIG 场景下可以 < 1（表示一个 MIG 分区而非整张物理卡），此时显存取量化的 MIG profile 值。

---

## 五、维度 G: GPU BinPack（NVLink 亲和约束）

### 5.1 当前 BinPack 回忆

```go
// internal/scheduler/bin_pack.go
func BinPack(nodes []*NodeInfo, pods []*PodInfo) (assignments []Assignment, converged bool) {
    // FFD 排序 → 逐 Pod 分配 → DP 求解最小节点数
}
```

当前算法：每个 Pod 看作一个独立的装箱问题。GPU 场景下需要加**组亲和性**——同一个训练 Job 的多个 Pod 应该被分配到同一 NVSwitch domain 或至少同一节点的相邻 GPU 上。

### 5.2 NVLink 拓扑约束 + 拓扑碎片评分

```
Node: gpu-node-1
  ├─ GPU-0 ──NVLink── GPU-1 ──NVLink── GPU-2 ──NVLink── GPU-3   ← NVSwitch domain 0
  ├─ GPU-4 ──NVLink── GPU-5 ──NVLink── GPU-6 ──NVLink── GPU-7   ← NVSwitch domain 1
  └─ 两组 NVSwitch domain

训练 Job "train-bert":
  需要 4 张 A100
  └─ 亲和策略: prefer-same-nvswitch → 应该分配到 domain 0 或 domain 1
     避免跨 NVSwitch（带宽从 600GB/s 掉到 50GB/s）
```

**拓扑碎片问题**：在长期运行的集群中，可能出现"总卡数够，但拓扑全碎了"——8 张空闲卡散落在 4 个 NVSwitch domain。此时即便节点满足 `AllocatableGPU >= 4`，调度质量也很差。

**解法**：在 FFD 排序阶段引入 **GPUNodeScore**，给拓扑友好的节点更高权重：

```go
type GPUNodeScore struct {
    NodeName          string
    TotalGPUs         int
    FreeGPUs          int
    MaxContiguousGPUs int    // 最大连续 NVSwitch domain 的空闲 GPU 数
    NVSwitchDomains   int    // 有几个 NVSwitch domain 有空闲 GPU
}

func scoreForGPUNode(node *NodeInfo, requiredGPUs int, topology TopologyConstraint) float64 {
    free := node.FreeGPUs()

    // 1. 基础分：空闲 GPU 数量
    base := float64(len(free)) / float64(requiredGPUs)

    // 2. 拓扑加分：同一 NVSwitch domain 的空闲数
    bestDomain := 0
    for _, domain := range node.NVSwitchDomains {
        freeInDomain := countFreeInDomain(free, domain)
        if freeInDomain > bestDomain {
            bestDomain = freeInDomain
        }
    }

    // 3. 最终分数：如果同 domain 能装下全部卡，显著加分
    if bestDomain >= requiredGPUs {
        return base * 3.0          // 拓扑友好 → 3x 权重，优先选
    }
    if bestDomain >= requiredGPUs/2 {
        return base * 1.5          // 跨 2 个 domain → 轻微加分
    }
    return base                    // 全碎了 → 基础分，装不下时降级
}
```

**降级策略**：当所有节点的 `MaxContiguousGPUs < requiredGPUs` 时，bin_pack 降级为逐卡分配 + warn 日志："NVLink 拓扑碎片化，Job X 的 Pod 分散在多个 NVSwitch domain，训练带宽可能受限"。

**AffinityConstraint**：

```go
type AffinityConstraint struct {
    Kind    string   // "prefer-same-nvswitch" | "require-same-node" | "anti-affinity"
    Pods    []string // 同一 Job 的 Pod 名列表
}
```

### 5.3 GPU 节点过滤

```go
func filterGPUNode(nodes []*NodeInfo, gpuProduct string, minGPUs int) []*NodeInfo {
    filtered := make([]*NodeInfo, 0)
    for _, n := range nodes {
        if !n.HasGPULabel(gpuProduct) { continue }  // 无 GPU 或型号不匹配
        if n.AllocatableGPU < minGPUs { continue }   // GPU 数量不足
        filtered = append(filtered, n)
    }
    return filtered
}
```

---

## 六、Rescheduler GPU 迁移约束

`rescheduler.go` 已经区分了"可迁移 Pod"（无状态 Deployment）和"不可迁移 Pod"（StatefulSet/蓝绿 Pod/GPU Pod）。GPU 场景多加一条：

```go
func (r *Rescheduler) isMigratable(pod *PodInfo) bool {
    // 已有约束
    if pod.IsStatefulSet { return false }
    if pod.IsBlueGreen { return false }

    // v3.1 新增: GPU Pod 默认不迁移（粘滞策略）
    // 理由: GPU 训练任务迁移意味着 preemption → checkpoint → restore，
    //       对大部分训练框架是不可恢复的中断
    if pod.Requests.GPU > 0 {
        // 唯一例外: GPU 硬件故障（DCGM Xid 61/94 等）
        if pod.HasGPUHardwareFailure() {
            return true  // 强制驱逐——死在坏卡上比继续跑更糟
        }
        return false
    }

    // v3.2 候选: 支持 checkpoint-aware GPU 迁移
    // if pod.HasCheckpointSupport() { return true }

    return true
}

// HasGPUHardwareFailure 检查 DCGM 是否报告了致命 GPU 错误
func (p *PodInfo) HasGPUHardwareFailure() bool {
    // 从 Pod events 或 DCGM Xid 检测中判断:
    //   Xid 61 = Internal micro-controller error
    //   Xid 94 = Unrecovered ECC error
    //   Xid 48 = Double Bit ECC error
    // 这些错误意味着 GPU 硬件已不可靠，继续运行只会产生错误结果
    // 信息来源: DCGM → Prometheus alert → Pod annotation 或 Node condition
    return p.HasNodeCondition("GPUHardwareFailure") ||
           p.HasAnnotation("kubepivot.io/gpu-hardware-failure") == "true"
}
```

---

## 七、`resources.yaml` GPU 字段激活

v3.0 已预留的 `gpu:` 字段在 v3.1 正式激活：

```yaml
resources:
  - name: train-bert
    kind: Job
    onMissing: create
    gpu:
      count: 4                    # 4 张卡
      product: A100-SXM4-40GB     # 型号过滤
      topology: same-nvswitch     # NVLink 亲和策略
      profile: gpu                # sizing profile: gpu
    labels:
      app.kubernetes.io/name: train-bert
```

kp deploy 和 scheduler 读到 `gpu.count > 0` 时自动走 GPU 路径：

- `kp deploy --sizing-mode=auto` → 调 3D DP sizing
- scheduler `BinPack()` → 走 GPU 节点过滤 + NVLink 亲和约束
- rescheduler → 跳过 GPU Pod 的迁移

---

## 八、`kp init --type=job --lang python` GPU 提示

`templates/app/python/Dockerfile` 和 `templates/app/cpp/Dockerfile` 顶部加：

```dockerfile
# ── GPU 训练任务 ──────────────────────────────────────────────
# 如果你要用 GPU，请：
# 1. 替换基础镜像：
#    FROM nvidia/cuda:12.4.1-runtime-ubuntu22.04
# 2. 安装对应 CUDA 版本的 PyTorch：
#    RUN pip install torch --index-url https://download.pytorch.org/whl/cu124
# 3. 在 resources.yaml 中配置 gpu 字段：
#    gpu:
#      count: 1
#      product: A100-SXM4-40GB
# ──────────────────────────────────────────────────────────────
```

这些注释在 `kp init --no-app` 时不生成（不写 Dockerfile），不影响已有项目。

---

## 九、与 v3.0 调度器的整合点

| v3.0 组件 | v3.1 改动 | 破坏性 |
|---|---|---|
| `scheduler/adapter.go` | 加 `GPUNodeLister` 接口 | 否（新接口追加） |
| `scheduler/bin_pack.go` | 加 GPU 维度 + NVLink 亲和 + GPU 节点过滤 | 否（CPU/Mem 路径不变） |
| `scheduler/coordinator.go` | 协同公式加 GPU 维度 | 否（加权公式加项） |
| `scheduler/rescheduler.go` | 加 GPU Pod 迁移约束 | 否 |
| `sizing/dp.go` | 2D → 3D DP + GPUWeights | 否（CPU/Mem 路径不变） |
| `metrics/` | 加 `gpu.go` + DCGM query | 否（新文件） |
| `cmd/kp/deploy.go` | 读 `resources.yaml` gpu 字段 | 否（字段已预留） |

**零破坏性变更**——所有 GPU 逻辑走独立路径，CPU/Mem 调度行为与 v3.0 完全一致。

---

## 十、测试计划

| 测试 | 覆盖 |
|---|---|
| `TestGPUMetrics_ParseDCGMResponse` | DCGM Prometheus 返回解析 |
| `TestSizing3D_GPUProfile` | ProfileGPU 权重 → GPU_Mem + GPU_Count 推荐 |
| `TestSizing3D_NoGPU` | 无 GPU 指标的 Pod → GPU 字段保持为 0 |
| `TestBinPack_NVLinkAffinity` | 4 Pod → same NVSwitch domain |
| `TestBinPack_NVLinkFallback` | NVSwitch 装不下 → 降级为单 Pod + warn |
| `TestBinPack_GPUNodeFilter` | 混合节点 → GPU Pod 只分配到 GPU 节点 |
| `TestRescheduler_GPUPodNotMigrated` | GPU Pod 在迁移扫描中被跳过 |
| `TestDeploy_GpuResourcesYAML` | `gpu.count=2` → deploy 走 GPU Path |
| `TestGPUInit_PythonDockerfileHint` | `kp init --type=job --lang=python` → Dockerfile 含 GPU 注释 |

---

## 十一、实施顺序

| Step | 内容 | 估计 |
|---|---|---|
| Step 1 | `internal/metrics/gpu.go` — DCGM query + 解析 | ~1 天 |
| Step 2 | `internal/sizing/dp.go` — 2D → 3D | ~1.5 天 |
| Step 3 | `internal/scheduler/bin_pack.go` — GPU 维度 + NVLink 亲和 | ~1.5 天 |
| Step 4 | `internal/scheduler/rescheduler.go` — GPU 迁移约束 | ~0.5 天 |
| Step 5 | `cmd/kp/deploy.go` — resources.yaml gpu 字段激活 | ~0.5 天 |
| Step 6 | 集成测试（真实 GPU 集群） | ~1 天 |

---

## 十二、风险与约束

1. **需要真实 GPU 集群验证**：单测覆盖算法逻辑，但 NVLink 拓扑的正确性只能在至少 2 个 A100 节点的集群上验证。单测用 mock 拓扑。
2. **DCGM exporter 是前置依赖**：kubePivot 不装 DCGM，用户需要先部署。`kp doctor --perf` 加 GPU 检查项提示。
3. **MIG 分区不自动配置**：v3.1 只读 MIG 资源，不自动切 MIG。切 MIG 影响所有已运行的 Pod，属于集群级操作，应留给管理员手动执行。
4. **GPU 型号假设**：当前仅测过 NVIDIA A100。H100/B200/L40S 的 metric 名称相同（DCGM 标准化），理论上兼容，但未实测。

---

---

---

## 十三、v3.2 展望：时空双维度 GPU 利用率闭环

v3.1 解决"把 GPU 卡分配给对的 Job"。v3.2 解决"把每张卡的每一点算力都榨干"——空间维度（GPU 共享）和时间维度（碳感知延迟）双管齐下。

### 13.1 空间维度：GPU 共享

#### MPS（Multi-Process Service）

**场景**：多个小模型推理 Pod 共享一张卡。每个 Pod 申请 0.2 或 0.5 GPU（逻辑算力），MPS 在 GPU 硬件层面做指令流上下文并发。

**实现**：
- `sizing/dp.go` 的 `RecommendedGPUCount` 从整数扩展为浮点数（0.1 粒度）
- `bin_pack.go` 管理 MPS 并发限制（A100 最大 48 个 MPS client，按 Pod 数量切分）

#### TimeSlicing（时间分片）

**场景**：对延迟不敏感的开发/测试环境。GPU 按时间切片轮流给不同 Pod 使用。

**实现**：本质是资源超卖（Overcommit）——coordinator 像处理 CPU soft limit 一样处理 GPU，允许 `sum(Requests.GPU) > Allocatable.GPU`，实际执行按 priority 排队。

#### MIG Auto-Config（最大挑战）

**场景**：KubePivot 根据 `resources.yaml` 的需求动态写 GPU 硬件分区。一张 A100 被切成 7 个 MIG 设备（1g.5gb × 7），每个训练 Job 分到一个。

**核心突破**：这是 KubePivot 首次具备"写硬件"能力——从"只读 GPU 拓扑"到"通过 nvidia-mig-manager 动态重组硬件逻辑块"。类似于 Pinduoduo 做 K8s-in-K8s 的思路——将底层硬件视作可编程的逻辑块。

### 13.2 时间维度：碳感知调度

在三维 DP `Pod × (CPU, Mem, GPU)` 之上，引入第四维 T（时间）：

```
调度成本函数:
  v3.0/v3.1: Cost = ResourceUsage
  v3.2:      Cost = ResourceUsage × CarbonIntensity(t)
```

#### 非紧急 Job 的"蓄水池"

`scheduler/coordinator.go` 维护一个 Waiting Queue。当 `--carbon-aware` 开启且当前电网碳强度处于高位时，`profile=Batch` 或 `Training`（非紧急）的 Job 自动推迟 Pre-run 阶段，等待低碳窗口。时间裕度由 `resources.yaml` 声明。

#### 数据源

实时获取集群所在区域的电力构成（可再生能源占比），对接 WattTime API 或 Carbon SDK。`internal/metrics/` 加 `CarbonIntensityProvider` 接口。

### 13.3 resources.yaml 调度策略扩展

```yaml
resources:
  - name: train-bert
    kind: Job
    gpu:
      count: 4
      product: A100-SXM4-40GB
      topology: same-nvswitch
      profile: gpu
    schedulingPolicy:           # v3.2 新增
      carbonSensitivity: High   # 允许为了减碳延迟最多 12h 执行
      sharingPreference: MPS    # MPS | TimeSlicing | MIG | Exclusive
```

### 13.4 时空折叠

| 维度 | 核心手段 | 目标指标 | KubePivot 组件 |
|---|---|---|---|
| 空间 (v3.2a) | GPU 共享 (MPS/MIG/TimeSlicing) | GPU Utilization 40% → 85% | `sizing/dp.go` 3D→4D, `bin_pack.go` MPS 并发管理 |
| 时间 (v3.2b) | 碳感知延迟调度 | Carbon Footprint ↓30% | `coordinator.go` Waiting Queue + `metrics/` CarbonIntensity |

两者本质都是利用率优化——空间榨干硬件，时间对齐绿色能源。v3.1 整卡调度 + v3.2 时空闭环 = GPU 从分配到使用的完整故事。

---

## 十四、v3.0/v3.1/v3.2 调度能力对比

| 调度阶段 | v3.0 (CPU/Mem) | v3.1 (GPU 整卡) | v3.2 (GPU 时空) |
|---|---|---|---|
| 感知 | metrics-server | DCGM exporter | DCGM + WattTime/CarbonSDK |
| 决策 | 2D DP | 3D DP (NVLink Topology) | 4D DP (Time-aware) |
| 约束 | CPU/Mem Limit | GPU Isolation + NVLink + Staleness | GPU Sharing + Carbon Window |
| 自愈 | StatefulSet 不迁移 | Sticky Pods + HW Fail Evict | MIG 分区动态重组 |

---

## 十五、编辑记录

```
2026-05-02  qc + DeepSeek 起草
    - 三维 DP: Pod × (CPU, Mem, GPU_Mem, GPU_Count)
    - DCGM metrics 采集 + PrometheusClient 集成
    - NVLink 亲和约束: prefer-same-nvswitch affinity group
    - GPU Pod 迁移约束: 默认不迁移，v3.2 checkpoint-aware
    - resources.yaml gpu 字段激活
    - kp init GPU Dockerfile 提示
    - 零破坏性变更：所有 GPU 逻辑走独立路径
    - 9 个测试用例 + 真实 GPU 集群集成测试

2026-05-02 v1.1  鲁棒性补强 + v3.2 展望 (qc)
    - Staleness 阈值 (60s)：DCGM 数据过期 → 保守模式，不缩 GPU
    - 显存量化对齐 (Quantization Factor)：A100/H100 推荐值对齐 MIG 分区大小
    - NVLink 拓扑碎片评分 (GPUNodeScore)：同 NVSwitch domain 3x 权重
    - 硬件故障强制驱逐 (ForceEvictOnHardwareFailure)：Xid 48/61/94 检测
    - v3.2 展望：时空双维度闭环
      · 空间：MPS / TimeSlicing / MIG Auto-Config
      · 时间：碳感知调度 Cost = ResourceUsage × CarbonIntensity(t)
      · resources.yaml schedulingPolicy 扩展
    - v3.0/v3.1/v3.2 三版调度能力对比表
```
