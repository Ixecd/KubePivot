# 乾枢调度器 (Scheduler) 设计文档

> 编写日期：2026-04-30  
> 状态：✅ 已实现 (v3.0.0)  
> 关联文档：[ROADMAP.md](../../ROADMAP.md) / [adapter.go](../../internal/scheduler/adapter.go) / [bin_pack.go](../../internal/scheduler/bin_pack.go) / [coordinator.go](../../internal/scheduler/coordinator.go) / [webhook.go](../../internal/scheduler/webhook.go)

---

## 摘要

乾枢调度器是 KubePivot v3.0 的**核心决策引擎**。它不再依赖 K8s 默认调度器的贪心算法，而是通过**维度 A (Bin Packing) 与维度 B (Sizing) 的双 DP 协同**，在部署时计算出 Pod 的最优节点分配和资源建议，并通过 GitOps 或 Webhook 方式将决策落地。

核心论断：**调度不应该是“能找到节点就放”，而应该是“怎样放能让集群整体资源利用率最高”。**

---

## 整体架构

```
                           [kp deploy / webhook]
                                    │
                                    ▼
┌──────────────────────────────────────────────────────────────┐
│                     Scheduler 主入口                          │
│  - Schedule(ctx) → 双 DP 协同求解                              │
│  - AssignPod(ctx, pod) → 轻量贪心实时分配 (Webhook)             │
└──────────────────────────┬───────────────────────────────────┘
                           │
          ┌────────────────┼────────────────┐
          ▼                ▼                ▼
┌─────────────────┐ ┌─────────────┐ ┌─────────────────┐
│ PodLister       │ │ NodeLister  │ │ PlanWriter      │
│ (Informer/kubectl)│ │(kubectl)   │ │ (GitOps 写入)   │
└─────────────────┘ └─────────────┘ └─────────────────┘
          │                │                ▲
          ▼                ▼                │
┌─────────────────┐ ┌─────────────┐         │
│ MetricsProvider │ │ SizingProvider│        │
│ (Prom/kubectl)  │ │ (v2.9 DP引擎)│        │
└─────────────────┘ └─────────────┘         │
          │                │                │
          └────────┬───────┘                │
                   ▼                        │
        ┌───────────────────┐               │
        │  Coordinator      │               │
        │  (双 DP 协同循环)   │               │
        └────────┬──────────┘               │
                 │                          │
                 ▼                          │
        ┌───────────────────┐               │
        │  Bin Packing 引擎 │               │
        │  (FFD + 0-1 背包) ├───────────────┘
        └───────────────────┘
```

### 核心模块对比

| 模块 | 职责 | 核心算法 | 输出 |
|------|------|----------|------|
| `BinPack` | 维度 A：装箱 | FFD 排序 + 0-1 背包 DP | Pod → Node 映射 |
| `Coordinator` | 双 DP 协同 | 外层装箱失败 → 裁剪重算，迭代收敛 | 最终调度计划 |
| `Assigner` | 实时单 Pod 分配 | 贪心 Best Fit | 单个 Pod 的目标节点 |
| `Webhook` | 运行时拦截 | 调 Assigner + JSON Patch | 注入 nodeSelector |

---

## 维度 A：Bin Packing 装箱求解器

### 设计意图

给定一组 Pod（含资源请求）和一组 Node（含可分配资源），找出一个映射，使得**尽可能多的 Pod 被放置，且节点资源利用率最高**。这是一个经典的多维装箱问题，NP-hard，因此采用启发式 + DP 近似求解。

### 算法核心

1. **FFD 预排序**：将 Pod 按“归一化资源吞噬度”降序排列。  
   公式：`Score = α·(PodReqCPU / NodeMaxCPU) + β·(PodReqMem / NodeMaxMem)`  
   目的：大 Pod 优先处理，减少碎片。

2. **0-1 背包 DP**：对每个节点独立求解，选出装入该节点的最优 Pod 组合。  
   状态定义：`dp[c]` = 在 CPU 分配量为 `c` 时，能装入的最大内存总量。  
   采用二维 `keep[i][c]` 表进行回溯，解决一维 DP 路径劫持问题。

3. **离散化粒度**：CPU 步长 50m，Memory 步长 64Mi。与 v2.9 sizing 保持一致。

### 关键接口

```go
// BinPack 维度 A 顶层入口
func BinPack(nodes []*NodeInfo, pods []*PodInfo) (*SchedulingPlan, error)
```

### 局限

- 目前仅处理 CPU / Memory 二维装箱，不感知 GPU、磁盘等扩展资源。
- 不考虑 Pod 亲和性/反亲和性、拓扑约束；这些由外层策略层通过 `ConstraintChecker` 注入处理。
- 全集群装箱时间随 Pod 数量线性增长，但通过 FFD + 离散化可将单次求解控制在 500ms 内（100 Pods / 10 Nodes 场景）。

---

## 维度 B 与双 DP 协同 (Coordinator)

### 设计意图

当 BinPack 无法将所有 Pod 装入节点时，不能只是报错，而应**主动调整 Pod 的资源需求**（借助 v2.9 sizing 引擎）来促成收敛。

### 协同逻辑

1. 首次直接用原始请求装箱。
2. 若失败，对未分配的 Pod 启动协同循环：
   - 将未分配 Pod 的 `requestCap` 下调 10%。
   - 调用 `SizingProvider.Compute` 在约束下重算最优资源。
   - 取 `min(sizing建议, requestCap)` 更新 Pod 资源。
   - 重新装箱。
3. 迭代上限 5 次；收敛条件：所有 Pod 分配成功。
4. 若仍失败，返回不收敛计划，由上层降级或人工介入。

### 关键接口

```go
type SizingProvider interface {
    Compute(ctx context.Context, samples []*metrics.PodMetrics, profile sizing.Profile) (*sizing.Suggestion, error)
}
```

### 局限

- 依赖 v2.9 sizing 引擎提供准确的历史数据与置信度，若数据不足则裁剪效果不佳。
- 协同仅裁剪“未分配 Pod”，不主动调整已分配 Pod，可能在极端场景下无法收敛。

---

## GitOps 写入 (PlanWriter)

调度结果通过 `PlanWriter` 接口原子写回 `components.yaml`，遵循 GitOps 原则：**Git 是唯一真相源**。

- 读取现有文件 → 修改目标组件的 `nodeSelector` → 写入临时文件 → `os.Rename`。
- 仅添加 `kubernetes.io/hostname` 选择器，不触动用户其他配置。

---

## Webhook 实时调度 (Level 4)

### 设计意图

为已运行集群提供**运行时调度优化**。Pod 创建时，Webhook 拦截请求，实时分配节点并注入 `nodeSelector`。

### 核心流程

1. 接收 `AdmissionReview` 请求。
2. 提取 Pod 资源请求，构造 `PodInfo`。
3. 调用 `AssignPod`（贪心 Best Fit）实时决定目标节点。
4. 构造安全的 JSON Patch（若已有 nodeSelector 则追加，否则新建）。
5. 失败时设置 `Allowed: true`，放行由 K8s 默认调度器处理，保证可用性。

### 安全设计

- TLS 自签证书，支持 caBundle 注入。
- 非 Pod 资源直接放行。
- 资源解析失败时记录警告并放行。

---

## 与现有系统集成

- **Pod/Node 数据来源**：通过 `PodLister` / `NodeLister` 接口解耦，生产环境使用 kubectl adapter，未来可切换为 Informer 缓存。
- **指标与 Sizing**：通过 `MetricsProvider` / `SizingProvider` 接口调用 v2.7 指标层和 v2.9 引擎。
- **Controller 启动**：Webhook 服务器作为 goroutine 随 controller 一起运行。

---

## 测试覆盖

核心算法（装箱、协同、排序、实时分配）均通过独立单测验证；Webhook 通过 mock AdmissionReview 完成端到端测试；真实集群验证（orbstack）确认数据流正常。

---

## 编辑记录

- 2026-04-30 初稿：基于 v3.0.0 已实现代码编写。

---

# 乾枢重调度器 (Rescheduler) 设计文档

> 编写日期：2026-04-30  
> 状态：✅ 已实现 (v3.0.0)  
> 关联文档：[rescheduler.go](../../internal/scheduler/rescheduler.go) / [metrics.go](../../internal/scheduler/metrics.go) / [global.go](../../internal/controller/global.go)

---

## 摘要

乾枢重调度器是**运行时集群优化引擎**。它周期性扫描节点利用率，当检测到资源分布不均时，自动将 Pod 从高负载节点迁移到低负载节点。为保障稳定性，内置了抖动检测与多级降级机制，防止过度重调度引发雪崩。

核心论断：**调度部署后不是终点，持续的负载均衡才是资源效率的关键。**

---

## 整体架构

```
                  ┌───────────────────────────┐
                  │     Rescheduler           │
                  │  - Start(ctx) 周期性循环   │
                  │  - run() 单次扫描          │
                  │  - Pause/Resume 紧急刹车   │
                  └──────────┬────────────────┘
                             │
              ┌──────────────┼──────────────┐
              ▼              ▼              ▼
     ┌────────────┐ ┌────────────┐ ┌──────────────────┐
     │ PodLister  │ │ NodeLister │ │ PodAssigner      │
     └────────────┘ └────────────┘ │ (实时分配接口)     │
                                   └──────────────────┘
              │              │              │
              └──────┬───────┘              │
                     ▼                      │
          ┌────────────────────┐            │
          │  集群状态分析       │             │
          │  - computeNodeUtilization       │
          │  - detectImbalance │            │
          └────────┬───────────┘            │
                   ▼                        │
          ┌────────────────────┐            │
          │  迁移执行器         │◄───────────┘
          │  - migratePods     │
          │  - 迁移约束检查     │
          └────────────────────┘
```

---

## 周期性扫描与决策

### 扫描流程 (每 5 分钟)

1. 获取全集群 Pod 和 Node 快照。
2. 计算每个节点的 CPU / Memory 利用率。
3. **抖动检测**：统计窗口内的资源 spike，评估是否需要降级。
4. 根据当前降级级别调整重调度策略。
5. 识别不平衡节点对。
6. 执行 Pod 迁移。

### 不平衡检测算法

- 计算集群平均利用率 `avgCPU`, `avgMem`。
- 高负载节点：CPU 或 Memory 利用率超过 `avg × 1.2`。
- 低负载节点：CPU 且 Memory 利用率均低于 `avg × 0.8`。
- 配对并计算建议迁移量（取高节点超出部分与低节点剩余容量的较小值）。

---

## 安全阀门：抖动检测与多级降级

### 为什么需要

在高负载（利用率 85%+）下，突发流量可能导致节点瞬间 >95%，如果此时继续执行 Pod 迁移，可能引发连锁雪崩。

### 降级层级

| 级别 | 名称 | 行为 | 触发条件 |
|------|------|------|----------|
| Level 0 | 正常运行 | 完整重调度 | 无抖动 |
| Level 1 | 观察模式 | 迁移量减半 | 窗口内 ≥3 次 spike |
| Level 2 | 紧急降级 | 停止所有迁移 | ≥6 次 spike 或发生 OOM |
| Level 3 | 用户介入 | 暂停自动调度 | 用户手动 `Pause()` 或持续恶化 |

- **自动恢复**：抖动消失后，逐级降低降级层级。
- **用户接口**：`Pause()` / `Resume()` 可手动控制；`ReportOOM()` 供外部事件触发。

### 抖动检测实现

- 维护最近 5 分钟的 spike 时间戳列表。
- 每次扫描检查节点利用率是否 >95%，若是则记录 spike。
- 结合 OOM 事件（最近 5 分钟内）综合判定。

---

## 迁移约束

为保证生产环境安全，以下 Pod **永不迁移**：

- **StatefulSet**：通过 `statefulset.kubernetes.io/pod-name` 标签识别。
- **蓝绿部署锁定 Pod**：通过 `kubepivot.io/blue-green-locked=true` 标签识别。
- 单次迁移数量上限：总 Pod 数的 5%（可配置）。

---

## 可观测性 (Level 6)

重调度器暴露 7 个 Prometheus 指标：

- `kubepivot_scheduler_iterations_total`
- `kubepivot_scheduler_solve_duration_seconds`
- `kubepivot_node_utilization_cpu_ratio` / `memory_ratio`
- `kubepivot_pod_oomkill_total`
- `kubepivot_scheduler_fallback_level`
- `kubepivot_pod_migration_total`

指标在每次调度完成后更新，复用 v2.7 的 Pure Collector 模式。

---

## 与现有系统集成

- **Controller 启动**：在 `StartGlobal` 中作为独立 goroutine 运行。
- **调度器调用**：通过 `PodAssigner` 接口解耦，实际注入 `Scheduler.AssignPod`。
- **OOM 事件接收**：通过 `ReportOOM()` 方法，未来可由 controller 中的 Pod 状态监听器调用。

---

## 局限与后续规划

- 目前迁移仅修改内存中的 `Pod.NodeName`，尚未集成 K8s Eviction API 真正驱逐 Pod（计划 v3.0.x）。
- 不感知自定义指标（如 GPU 利用率、网络带宽），仅基于 CPU/Memory。
- 抖动检测阈值和降级策略基于经验值，需要生产数据反馈调优。

---

## 编辑记录

- 2026-04-30 初稿：基于 v3.0.0 已实现代码编写。