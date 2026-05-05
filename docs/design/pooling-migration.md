# 池化调度与迁移引擎设计文档

> 编写日期：2026-05-05  
> 状态：🌱 设计草案（v3.1 基础设施就绪，v3.2+ 实施）  
> 关联文档：[scheduler.md](scheduler.md) / [cell-based-architecture.md](cell-based-architecture.md) / [sharding.md](sharding.md)

---

## 一、摘要

当前 KubePivot 调度器的 bin-packing 按节点做贪心，缺少全局容量视角。碎片化（每个节点剩一点资源但拼不出完整 Pod）和维度 A/B 协同（Sizing ↔ Placement）都需要一个跨节点的聚合视图。

本文定义 **池化调度层** 和 **迁移执行引擎** 的架构：
- **池化层**：在现有 scheduler 上叠加 Node Pool / CPU-Memory Pool 的分层拓扑，提供全局碎片率、有效容量等池级指标
- **迁移引擎**：Rescheduler（决策） + MigrationManager（执行）分离，通过 Pod Annotation 持久化迁移状态，实现异步生命周期管理
- **Fencing 协议**：Stateful Cell 迁移的安全隔离机制，利用 sidecar Informer + 业务容器信号实现零数据丢失的故障可见迁移

核心原则：**池是翻译层——Sizing 对着池容量决策，Placement 在池内做优化，Rescheduler 按池级碎片率触发迁移。**

---

## 二、池化分层拓扑

### 2.1 三层池模型

三个"池"并非平级，而是 "物理容器 → 逻辑聚合 → 属性投影" 的嵌套结构：

```
Cluster
  ├── Node Pool: GPU-Nodes         (物理 + 拓扑边界)
  │   ├── Aggregated CPU Pool      (逻辑容量)
  │   └── Aggregated Memory Pool   (逻辑容量)
  │
  ├── Node Pool: CPU-Nodes
  │   ├── Aggregated CPU Pool
  │   └── Aggregated Memory Pool
  │
  └── Node Pool: Memory-Optimized
      ├── Aggregated CPU Pool
      └── Aggregated Memory Pool
```

### 2.2 节点池（Node Pool）— 第一层：物理与拓扑边界

节点池是硬隔离边界，由硬件规格或拓扑位置决定：
- GPU 类型（A100 vs H100）
- 存储介质（NVMe vs SSD）
- Availability Zone（跨 AZ 容灾）
- 专用/共享标记（tenant isolation）

**声明方式**：Node Labels（人工标注）+ Taints，KubePivot 不自动创建或合并节点池。

**调度作用**：第一道过滤。调度器先匹配 `nodeAffinity` → 确定目标节点池 → 再在该池内部做 placement。

### 2.3 CPU/内存池 — 第二层：跨节点逻辑容量

节点池自动投影出聚合 CPU 池和聚合内存池。它们是同一池内所有节点的资源总和：

```
PoolCPU = Σ Node_i.AllocatableCPU    (i ∈ Pool)
PoolMem = Σ Node_i.AllocatableMemory (i ∈ Pool)
```

派生指标：

| 指标 | 公式 | 用途 |
|------|------|------|
| 池利用率 | Used / PoolTotal | 判断是否需要扩容 |
| 有效容量 | 池内最大连续可用资源（扣除碎片） | Sizing 决策输入 |
| 碎片率 | 1 - (有效容量 / 理论剩余容量) | 触发 Rescheduler 阈值 |
| PoolScore | 加权(1-碎片率, utilization balance, 历史迁移成功率) | 调度权重基准 |

### 2.4 池定义的数据结构

```go
type PoolInfo struct {
    Name       string              // 节点池名称（从 node label 提取）
    Labels     map[string]string   // 匹配该池的 node labels
    Nodes      []*NodeInfo         // 池内节点列表
    CPU        PoolResource        // CPU 聚合
    Memory     PoolResource        // Memory 聚合
    Score      float64             // 池健康度（0-1）
    UpdatedAt  time.Time           // 最后计算时间
}

type PoolResource struct {
    Total        int64   // 总可分配资源
    Used         int64   // 已使用
    Util         float64 // 利用率
    FragmentRate float64 // 碎片率
    Effective    int64   // 有效容量（最大连续可用，扣除碎片）
}
```

---

## 三、In-memory + Annotation 持久化方案

### 3.1 持久化分层

| 状态类型 | 存储位置 | 重建方式 |
|----------|----------|----------|
| 池定义 | Node Labels | `ListAllNodes` 秒级重建 |
| 聚合容量/利用率/碎片率/PoolScore | 内存 | `computeNodeUtilization` 按池聚合，秒级 |
| Jitter 历史/降级层级 | 内存 | 丢了从头观察（分钟级） |
| In-flight 迁移状态 | Pod Annotations | Controller 重启后扫 annotations 恢复 |

状态随 K8s 对象走，KubePivot 不引入 etcd/MySQL 等外部存储。

### 3.2 迁移 Annotation Bundle

每个迁移中的 Pod 携带以下 annotations：

```
kubepivot.io/migration-id:             "mig-20260505-001"     // 唯一 ID，幂等校验
kubepivot.io/migration-source-node:    "node3"                // 源节点
kubepivot.io/migration-target-node:    "node5"                // 目标节点
kubepivot.io/migration-phase:          "WaitingForReady"      // 当前阶段
kubepivot.io/migration-start-timestamp:"2026-05-05T14:30:00Z" // 起始时间，僵尸检测
kubepivot.io/migration-cell-type:      "stateless"            // stateless / stateful
kubepivot.io/fencing-ready:            "true"                 // (Stateful 专用) 排空完成
```

### 3.3 僵尸迁移清理

Controller 重启后扫描所有带 `kubepivot.io/migration-id` 的 Pod：
- Pod 已不存在 → 移除 annotation（已完成但未收尾）
- `migration-phase: Evicting` 且超过 10 分钟 → 判定为僵尸，清 annotation，允许重调度
- Pod Running + Ready → 清 annotation 收尾

### 3.4 并发安全

Annotation 写入用 K8s `ResourceVersion` 乐观锁，冲突时重试。进程内 MigrationManager + Rescheduler 共享 `sync.RWMutex` 保护的池视图。

---

## 四、Rescheduler + MigrationManager 分离

### 4.1 职责划分

```
                      ┌─────────────────────┐
                      │    池视图（内存）      │
                      │  PoolInfo map + RWMutex│
                      └──────┬──────┬───────┘
                             │      │
              ┌──────────────┘      └──────────────┐
              ▼                                    ▼
   ┌──────────────────────┐          ┌──────────────────────────┐
   │    Rescheduler        │          │   MigrationManager       │
   │    (决策引擎)          │          │   (执行引擎)              │
   ├──────────────────────┤          ├──────────────────────────┤
   │ 输入: 池利用率数据      │          │ 输入: Pod Watch 事件       │
   │ 做出: 迁哪个 Pod       │          │ 做出: 生命周期推进          │
   │ 输出: evict + annotation│         │ 输出: 清 annotation       │
   │ 运行: 周期性 ticker     │          │ 运行: Watch + Reconcile   │
   └──────────────────────┘          └──────────────────────────┘
```

两组件不互相调用，通过池状态 + Pod annotations 松耦合：
- Rescheduler 打 `migration-phase: Evicting` 后不再跟踪该迁移
- MigrationManager 接管后续所有阶段推进
- 并发迁移数通过池视图中的 `activeMigrations` 计数控制

### 4.2 Rescheduler 决策流程

```
ticker 触发 (每 5min)
  │
  ├─ computeNodeUtilization(pods, nodes)
  │   └─ 按 Node Label 分组 → 计算各池 PoolInfo
  │
  ├─ detectJitter(nodeUtil)  → 调整降级层级
  │
  ├─ detectPoolImbalance(pools)
  │   ├─ 池间：高负载池 vs 低负载池 → 配对
  │   └─ 池内：碎片率 > 阈值 → 触发碎片整理
  │
  ├─ 对每个不平衡对：
  │   ├─ 从高负载池找可迁移 Pod（isMigratable + 在 suggested 资源范围内）
  │   ├─ AssignPod() 确认目标节点
  │   ├─ 打 migration annotations（phase: Evicting）
  │   └─ evictPod()
  │
  └─ 返回本次迁移数
```

### 4.3 MigrationManager 状态机

```
                        Rescheduler
                        evict + 打 annotation
                             │
                             ▼
                    ┌─────────────────┐
                    │    Evicting     │
                    │ 旧 Pod 驱逐中     │
                    └────────┬────────┘
                             │ 旧 Pod 消失，新 Pod 出现
                             ▼
                    ┌─────────────────┐
                    │ WaitingForReady │
                    │ 等待新 Pod Ready  │
                    └────┬──────┬─────┘
                         │      │
                Ready    │      │  超时 (5min)
                         │      ▼
                         │  ┌──────────┐
                         │  │ Rollback │───── 删新 Pod，清 annotation
                         │  └──────────┘
                         ▼
              ┌──────────────────┐
              │    Verifying     │  ← Stateful Cell 专属阶段
              │  Fencing + 排空   │
              └────────┬─────────┘
                       │ fencing-ready: true
                       ▼
              ┌──────────────────┐
              │    Complete      │
              │  清 annotation   │
              └──────────────────┘
```

**阶段详解**：

| 阶段 | 动作 | 超时 | 超时行为 |
|------|------|------|----------|
| Evicting | 等旧 Pod 消失 | 10min | 僵尸清理，清 annotation 重试 |
| WaitingForReady | 等新 Pod Ready | 5min | 删新 Pod，清 annotation → 回滚 |
| Verifying | 等 Fencing 完成（Stateful 专属） | 15min | 不可自动回滚 → 人工介入 |
| Complete | 清 annotation，更新迁移计数 | - | - |

### 4.4 Point of No Return（不可逆点）

Stateful Cell 迁移一旦进入 `Verifying`（Fencing）阶段，**原则上不再允许自动回滚**。

原因：旧 Pod 的数据可能已部分失效或状态已锁定（例如数据库已开始 WAL flush，共享存储的锁状态已变更）。此时自动回滚可能导致 split-brain 或数据不一致。

进入 Verifying 后发生超时 → 转入 `Manual-Intervention` 状态，触发高警报，等待运维介入。

---

## 五、Fencing 协议（Stateful Cell 迁移隔离）

### 5.1 架构

```
┌───────────────────────────────────────────────────────────┐
│                       K8s API Server                       │
│  Pod Annotation: migration-phase: "Verifying" → Watch 事件  │
└────────────┬──────────────────────────────┬───────────────┘
             │                              │
             ▼                              ▼
┌─────────────────────────┐   ┌─────────────────────────────┐
│  MigrationManager       │   │  Sidecar (etcd Learner)      │
│  (Controller 进程内)     │   │  Host Pod: postgres-0        │
│                         │   │                             │
│  1. 打 annotation       │   │  1. Informer Watch 自身 Pod  │
│  2. 等待 fencing-ready   │   │  2. 感知 Verifying → 发信号   │
│  3. 推进 Complete       │   │  3. 等业务容器排空            │
│                         │   │  4. 打 fencing-ready: true    │
└─────────────────────────┘   └──────────┬──────────────────┘
                                         │ SIGUSR1 / gRPC
                                         ▼
                              ┌─────────────────────────────┐
                              │  业务容器 (e.g. postgres)     │
                              │  1. 停止接受新连接              │
                              │  2. 排空现有连接               │
                              │  3. Flush WAL / 状态          │
                              └─────────────────────────────┘
```

### 5.2 时序

```
T0  MigrationManager.Patch(pod, phase="Verifying")
T1  Sidecar Informer 收到 Watch 事件（<1s 延迟）
T2  Sidecar 发 SIGUSR1/gRPC → 业务容器
T3  业务容器停止接受新连接，排空现有连接
T4  业务容器 flush WAL / 持久化状态
T5  业务容器返回 "done" → Sidecar
T6  Sidecar.Patch(pod, fencing-ready="true")
T7  MigrationManager 收到 Watch → 推进到 Complete
T8  MigrationManager 清 annotation，迁移完成
```

### 5.3 信号方式选择

| 方式 | 延迟 | 可靠性 | 实现复杂度 |
|------|------|--------|-----------|
| Sidecar 轮询 Annotation | 秒级 | 中（API 压力） | 低 |
| Sidecar Informer Watch | <1s | 高 | 中（复用现有 Informer） |
| Sidecar → 业务容器 SIGUSR1 | 进程级瞬时 | 中（信号丢失无重试） | 低 |
| Sidecar → 业务容器 gRPC | 毫秒级 | 高（ACK + 重试） | 中 |

推荐：**Informer Watch + gRPC**。Informer 复用 controller 已有机制，gRPC 提供 ACK 语义，保证业务容器确实收到了 fencing 信号。

---

## 六、与 CBA 的联动

### 6.1 Stateless Cell vs Stateful Cell 迁移策略

| | Stateless Cell | Stateful Cell |
|---|---|---|
| 迁移模型 | 接管（v2.5 takeover） | Handover 协议（§5 Fencing） |
| 故障可见性 | 不可见（秒级接管） | 可见但数据完整 |
| 不可逆点 | 无（随时可回滚） | Verifying 阶段（人工介入） |
| 迁移注解 | `cell-type: stateless` | `cell-type: stateful` |
| Fencing 阶段 | 跳过 | 必须 |

### 6.2 2D Matrix-orchestration 的池化切片

CBA 的 `Shard × Workload-class` 2D 矩阵在池化架构下的映射：
- 每个 Cell 属于一个 Node Pool（Shard 维度）
- 每个 Cell 的 Workload-class 决定迁移策略（Stateless vs Stateful）
- MigrationManager 通过 `cell-type` annotation 选择对应分支

---

## 七、工程实施

### 7.1 组件位置

MigrationManager 作为 controller 进程内的普通 package：

```
internal/scheduler/
  ├── pool.go                  ← PoolInfo / PoolResource 定义 + 聚合计算
  ├── pool_test.go
  ├── migration_manager.go     ← MigrationManager 状态机
  ├── migration_manager_test.go
  ├── rescheduler.go           ← 已有，增加池化逻辑
  └── rescheduler_test.go

controller 进程内：
  main loop 启动 MigrationManager goroutine
  → 共享 Informer pool + 池视图
```

**不做独立二进制**：MigrationManager 无独立资源需求，与 Controller 共享 Informer 连接 + 池视图，故障隔离不成立（其最严重 bug 不会带崩集群）。拆分反而引入 dual-write 并发冲突和部署复杂度。

### 7.2 实施路线

| 阶段 | 内容 | 依赖 |
|------|------|------|
| v3.2 | PoolInfo 结构体 + computePoolUtilization | 当前 NodeInfo 加 PoolLabel |
| v3.2 | 池间不平衡检测 + 池内碎片率 | PoolInfo |
| v3.2 | Annotation Bundle 定义 + 僵尸清理逻辑 | - |
| v3.3 | MigrationManager 状态机（Stateless 路径） | Annotation Bundle |
| v3.3 | Fencing 协议（Stateful 路径） | Sidecar gRPC 接口 |
| v3.3 | Dry-run 模式（只打 annotation，不执行 evict） | MigrationManager |

### 7.3 Dry-run 测试模式

MigrationManager 支持 `DryRun` 模式：Rescheduler 正常打 annotation，但 MigrationManager 只记录日志不执行 evict 和 annotation 更新。用于验证碎片率算法是否会引起大规模非必要震荡。

---

## 八、设计 Q

```
Q1: 池定义来源
    A. 纯 Node Label 推导（用户标注 "pool=gpu"）
    B. Label + 自动聚类（KubePivot 通过 resource profile 发现逻辑池）
    C. A 为主，B 作为 opt-in feature flag
    建议: C — v3.2 先用 A 落地，自动聚类放后续迭代

Q2: PoolScore 计算权重
    A. 固定权重（碎片率 0.5, balance 0.3, 迁移成功率 0.2）
    B. workload-class 自适应权重
    C. 先用 A 简单落地，后续演进
    建议: C

Q3: MigrationManager 最大并发迁移数
    A. 固定上限（如 3）
    B. 按池健康度动态调整（PoolScore < 0.5 时降为 1）
    C. 按池规模百分比（池内 Pod 数的 5%）
    建议: B — 与 degradeLevel 机制一致
```

---

## 编辑记录

```
2026-05-05  创建
            基于 qc + Claude 对话：
            - 池化分层拓扑讨论
            - In-memory + Annotation 持久化方案
            - Rescheduler / MigrationManager 拆分 + 状态机
            - Fencing 协议（Stateful Cell）
            - 外部独立调度器路线确认
```
