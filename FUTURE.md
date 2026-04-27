# FUTURE.md — KubePivot 架构演化实验室

> 创建日期：2026-04-27
> 作用：存放高价值、高风险、需要"重新评估时机"的架构种子
> 关联：[ROADMAP.md](ROADMAP.md)（确定性工程交付计划）

---

## 这份文档是什么 / 不是什么

`ROADMAP.md` 是 **军令状** — 写的是未来 3-6 个月内要交付的确定性工程。
`FUTURE.md` 是 **实验室** — 存放需要在合适时机重新评估的远景种子。

**这份文档不是承诺**：里面的每一条都不保证会实施。
**这份文档不是 backlog**：不是"想做但没时间"的待办池。
**这份文档是种子库**：当条件成熟时（集群规模 / 业务复杂度 / 工程容量），重新评估、决定是否提升到 ROADMAP。

每个种子必须包含：
- **动机**：为什么这个想法值得记录
- **核心想法**：一段话讲清楚是什么
- **工程税**：诚实列出实施成本和风险
- **适用前提**：什么条件下重新评估才有意义
- **重新评估时机**：与 ROADMAP 的交点

---

## 种子库

| 编号 | 种子 | 状态 | 重新评估时机 |
|---|---|---|---|
| F1 | Cell-based Architecture (CBA) | 🌱 探索中 | v2.8 引入有状态资源保护时 |

（更多种子会在未来记录到此处。空表不是问题，FUTURE.md 应该谨慎扩张。）

---

## F1: Cell-based Architecture (CBA)

> 从 1D Hash-sharding 演化到 2D Matrix-orchestration（Shard × Workload-class），实现极致故障隔离。

### 动机

v2.5 / v2.6 / v2.7 是基于 **hash-sharding** 的容错模型：

- 任一 controller pod 挂，其他 pod 接管其 shard
- 优点：单点故障对外不可见
- 局限：故障扩散到接管者；接管者承担 1/(N-1) 额外负载

这个模型在 **无状态资源** 上工作良好：
- Deployment / Service / ConfigMap 接管成本低
- 数据无需迁移，仅 reconcile 状态收敛即可

但 v2.8 计划引入 **有状态资源保护**（StatefulSet / PVC / Database / ETCD），接管模型不再适用：

- 数据迁移成本极高（GB-TB 级数据搬运）
- 接管时数据不一致风险大（split-brain）
- 跨 namespace 状态依赖难以追踪
- 接管延迟从秒级 → 分钟级 → 业务感知中断

需要一个新的故障模型：**故障可控边界 > 故障不可见**。

### 核心想法

从 1D hash-sharding 演化到 2D matrix-orchestration：

```
v2.5 模型 (1D)：
  Shard 0 │ Shard 1 │ Shard 2 │ ... │ Shard 9
  
  接管时：Shard 5 挂 → 其他 shard 接管这些 ns
  问题：有状态资源接管成本爆炸

CBA 模型 (2D)：
  
              ┌──────────────┬──────────────┐
              │   Stateless  │   Stateful   │
  ┌───────────┼──────────────┼──────────────┤
  │ Shard 0   │  Cell[0][SL] │  Cell[0][SF] │
  │ Shard 1   │  Cell[1][SL] │  Cell[1][SF] │
  │ ...       │      ...     │      ...     │
  │ Shard 9   │  Cell[9][SL] │  Cell[9][SF] │
  └───────────┴──────────────┴──────────────┘
  
  Cell[5][SL] 挂 → 走 v2.5 快速接管路径
  Cell[5][SF] 挂 → 就地隔离 / 自愈，不触发数据迁移
```

**核心 trade-off**：

> 用 "故障可见 + 数据安全" 换 "故障不可见 + 数据风险"

- 无状态部分：保留 v2.5 的"接管掩盖故障"模式
- 有状态部分：转向"隔离暴露故障"模式（用户感知降级，但数据完整）

### 维度选择：Shard × Workload-class

考虑过 3 种 2D 维度：

| 维度 | 含义 | 适用场景 | 当前判断 |
|---|---|---|---|
| Shard × Zone | 地理隔离（AZ/region） | 跨地域容灾 | v4.x 多集群阶段再考虑 |
| Shard × Tenant | 多租户隔离 | SaaS 多租户 | v3.x+ 商业化时考虑 |
| **Shard × Workload-class** | 状态/无状态隔离 | **v2.8 有状态资源保护** | **首选** |

选择理由：
- v2.8 的核心痛点是"有状态资源接管成本"
- "业务特征隔离"的收益 >> "物理位置隔离"
- Zone 是 v4.x 多集群阶段的核武器，现在打不出去

### 故障处理策略（按 Cell 类型差异化）

**Stateless Cell**（保留 v2.5 接管模式）：
```
Cell[5][SL] 挂
  ↓
其他 SL Cell 接管这些 ns 的无状态资源
  ↓
快速 reconcile（秒级）
  ↓
对外表现：单点故障不可见 ✓
```

**Stateful Cell**（新引入：就地隔离/自愈）：
```
Cell[5][SF] 挂
  ↓
不触发数据迁移
  ↓
就地重启 / 自愈
  ↓
若长时间不恢复：报警 + 暴露给用户（业务降级）
  ↓
对外表现：故障可见但可控 ⚠️
```

### 可视化设想：Matrix Grid Debug View

每个 Cell 显示为 grid 单元：
- 亮度 = 健康度
- 颜色 = 状态（绿/黄/红）

正常时：grid 均匀色温
故障时：某区域出现"红斑"
扩散时：红斑沿 cell 邻接关系蔓延，直到隔离边界（cell 边界）即停

> 故障的视觉呈现像生物炎症 — 比 log/metrics 直观 10x

但有适用前提：
- cell 数 < 20：grid 视图意义不大，list 视图够用
- cell 数 > 50：grid 视图变成核心 debug 工具
- KubePivot 早期不需要

### 工程税（诚实列出）

#### 1. Cell 粒度选择困难

- 太细：cell 数爆炸（1000+）→ 管理复杂度高
- 太粗：故障半径仍大 → 失去 CBA 意义
- 工业实践：通常 10-100 之间
- KubePivot v2.8 时集群规模？大概率 < 100 ns，cell 数 < 20 → 收益边际有限

#### 2. Cell 间通信开销

"完全隔离"意味着 cross-cell 调用走外部边界：
- gRPC over network 而非进程内 channel
- 延迟从 μs → ms
- 是性能税

#### 3. 测试矩阵爆炸

CBA 隔离要测：
- 单 cell 内正常
- 单 cell 故障不影响他者
- cross-cell 通信
- cell 拓扑变化（添加/删除）
- workload-class 边界判定准确

测试矩阵 = O(N²)
v2.8 阶段团队就一人，测试成本可能压垮其他工作。

#### 4. workload-class 自动判定

CBA 假设可以自动识别"这个资源是 stateful 还是 stateless"。
但实际：
- StatefulSet → 明确 stateful
- Deployment → 多数 stateless，但若挂载 PVC 也是 stateful
- Service → 通常 stateless，但 headless service 可能服务 stateful 工作负载
- Pod 的 ephemeral storage vs PVC

需要一套"workload classifier"。
误判成本：stateless 资源被错放进 stateful cell → 接管路径错误。

### 适用前提

CBA 重新评估的条件（必须**同时满足**）：

- [ ] 集群规模 ≥ 100 ns（cell 数足够才有意义）
- [ ] v2.8 有状态资源保护已引入
- [ ] 团队 / 工程容量能承担 O(N²) 测试矩阵
- [ ] 已有真实生产场景遇到"接管时数据迁移"痛点
- [ ] workload-class 自动判定算法已设计

任一条件不满足 → 暂不提升到 ROADMAP，继续作为种子。

### 重新评估时机

**v2.8 设计阶段（约 2026-Q3）** 重新评估：
- 那时候 v2.7 已上线，集群规模数据可见
- 有状态资源保护进入设计阶段
- 决定 CBA 是 v2.8 内置 / v2.9 / 还是继续延后

如果 v2.8 决定不引入 CBA：
- 检查 v3.x+ 多租户阶段是否需要
- 检查 v4.x 多集群阶段是否合并到 Shard × Zone

如果 v2.8 决定引入 CBA：
- 提升到 ROADMAP.md
- 拆解到具体 Step 计划
- 此 F1 标记为"已毕业"，保留在文档中作为决策依据

### 现状（2026-04-27）

- 想法记录：✓
- 代码：暂不写
- 团队对话：v2.7 实施中讨论生成（commit 419-420 期间）
- 触发因素：从工业 Matrix 工具的"细胞 grid"可视化得到启发，延伸到爆炸半径设计

---

## 编辑约定

### 添加新种子

新种子必须满足：
- **高价值**：影响架构演化方向，不只是一个 feature
- **高风险**：盲目实施可能产生大量返工
- **可暂缓**：当前阶段不实施，未来某个时机重新评估更合适

不满足的想法应该去：
- 即时实施 → ROADMAP.md
- 短期 backlog → TODO.md
- 想法记录 → personal notes / decision-stack.md

### 种子状态流转

```
🌱 探索中 → ✅ 已毕业（提升到 ROADMAP）
         → ❌ 已弃用（评估后确定不做，保留决策记录）
         → ⏸️ 长期搁置（条件不成熟）
```

种子被弃用时**不删除**，保留作为"历史决策依据"。

### 编辑记录

- 2026-04-27 16:05  创建文档 + 加入 F1 种子（CBA）
