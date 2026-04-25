# KubePivot v2.5.0 Controller 分片设计

> 编写日期：2026-04-25
> 适用版本：v2.5.0+
> 状态：✅ 已实现并真实集群验证（10 项目 + 50 项目）

---

## 摘要

v2.5.0 引入 Controller 分片机制，把"全局唯一 leader 干所有活"改造成
"N 副本各干 1/N"。**不是为了让小规模更快——是为了让大规模能扛住**。

```
v2.4.0  1 leader 干 100% reconcile      集中式瓶颈，10 项目时 leader CPU 16.93%
v2.5.0  N 副本各干 1/N，按 hash 分片     水平扩展，单 pod 不再是天花板
```

设计灵感来自 qc 21 岁时复现的 C++ 负载均衡项目（Lars）的"hash 取模分流 + 节点抽象"思路。23 岁时把这套思路移植到 K8s controller 层，KubePivot 不引入 client-go，所有分片机制基于 K8s Lease + kubectl exec。

---

## 一、为什么需要分片

### 1.1 v2.4.0 揭示的真相

v2.4.0 完成 K8s Lease 选举 + 状态机缓存后，第一次拿到了"真实单 leader 成本"数据：

```
v2.3.0 看似的 13.96% / pod   ← 假象（3 副本全跑 reconcile，错峰摊薄）
v2.4.0 真实的 16.93% / pod    ← 真相（1 leader 承担 100% 负载）
```

虽然集群总 CPU 减半（41.88% → 18.92%），但**单 pod 16.93% 在 50/100 项目场景下会打满 CPU limits**。

### 1.2 分片是合理路径

所有水平扩展系统都用过类似机制：
- Kafka：partition + consumer group
- Cassandra：consistent hashing
- Argo CD：sharding by application hash

KubePivot 的特殊性在于 **不引入 client-go**——分片机制必须用 K8s Lease + kubectl exec 实现。

---

## 二、设计原则

```
1. 不引入 client-go             贯穿整个 KubePivot 项目的原则
2. 复用 K8s Lease 机制          v2.4.0 已有，对称 leader 选举
3. 拒绝分布式状态机的深坑        不做跨 pod 的状态协调
4. drop + 幂等                  task 失主时直接丢弃，依赖 reconcile 幂等性
5. 配置驱动                     KUBEPIVOT_SHARDS 环境变量
6. Lars 思路血缘                hash 取模 + 节点抽象 + 配额限制
```

---

## 三、架构总览

```
                       ┌─────────────────────────┐
                       │  K8s API Server          │
                       │                          │
                       │  10 个 shard lease：      │
                       │  shard-0, shard-1, ...   │
                       │  shard-9                 │
                       │                          │
                       │  1 个 leader lease       │
                       └─────────────────────────┘
                              ↑↓ (kubectl)
              ┌───────────────┼────────────────┐
              ↓               ↓                ↓
    ┌─────────────────┐ ┌─────────────────┐ ┌─────────────────┐
    │ Pod A (leader)  │ │ Pod B           │ │ Pod C           │
    │                 │ │                 │ │                 │
    │ Shards: 0,3,6,9 │ │ Shards: 1,4,7   │ │ Shards: 2,5,8   │
    │                 │ │                 │ │                 │
    │ + Sweeper       │ │ + (no sweeper)  │ │ + (no sweeper)  │
    │   (leader-only) │ │                 │ │                 │
    │                 │ │                 │ │                 │
    │ Watcher × 2     │ │ Watcher × 2     │ │ Watcher × 2     │
    │ Reconcile loop  │ │ Reconcile loop  │ │ Reconcile loop  │
    │ Worker pool 20  │ │ Worker pool 20  │ │ Worker pool 20  │
    └─────────────────┘ └─────────────────┘ └─────────────────┘
              ↑              ↑                ↑
              └──────────────┼────────────────┘
                             ↓
              project ns 通过 hash(ns) % 10 落到一个 shard
              eg. kp-auth-service → fnv32("kp-auth-service") % 10 = 3
                  → 由持有 shard-3 的 Pod A 负责
```

### 3.1 核心组件

| 组件 | 位置 | 职责 |
|------|------|------|
| `ShardSet` | `internal/sharding/shard.go` | 线程安全的分片集合（Add/Remove/Contains） |
| `ShardOf(ns, N)` | 同上 | FNV-1a 32-bit hash 决定项目归属 |
| `QuotaPerPod(N, replicas)` | 同上 | 每 pod 持有上限 = ceil(N/replicas) |
| `MultiLeaseManager` | `internal/sharding/multi_lease.go` | N 个 lease 抢占 + 续约主循环 |
| `RemoveOrphanProjects` | `internal/controller/global_state.go` | 分片切换时清理孤儿状态 |
| `runSweeperLoop` | `internal/controller/sweeper.go` | Leader-only 周期清理孤儿 lease |

---

## 四、关键设计决策

### Q1：分片策略（StatefulSet vs Deployment + Lease）

**选：N 个 K8s Lease 选举**

```
方案 A：StatefulSet 改造
  pod 0/1/2 用稳定 ordinal，分片 key = ordinal
  ✗ 缺点：滚动升级慢；身份机制和 v2.4.0 lease 不对称

方案 B：Deployment + pod label
  pod 启动时 hash(pod-name) % N 算自己的分片索引
  ✗ 缺点：pod 重启换名字 → 索引变化 → 分片不稳定

方案 C：N 个 K8s Lease 选举（选用）
  每个分片一个 lease，pod 通过抢占 lease 决定归属
  ✓ 复用 v2.4.0 已有的 lease 机制（架构对称）
  ✓ 分片切换天然 HA（pod 死了其他 pod 抢它的 lease）
  ✓ 配额限制避免独吞（参考 Lars 的过载队列思路）
```

### Q2：分片粒度（多少个分片？）

**选：环境变量 KUBEPIVOT_SHARDS，默认 10**

```
方案 A：固定 N（比如 N=6 或 N=9）
  ✗ 不灵活，不同规模场景需要不同 N

方案 B：动态 N = 项目数
  ✗ 项目越多分片越多，分片机制开销爆炸

方案 C：环境变量 KUBEPIVOT_SHARDS=N（选用）
  ✓ 部署时配置，运行时不变
  ✓ deploy.yaml 通过 ConfigMap 注入，运维友好
```

注意：N 必须 ≥ 副本数，否则部分 pod 无 shard 可拿。建议 `N = replicas × 3~10`。

### Q3：hash input（hash 什么？）

**选：namespace 名字 + FNV-1a 32-bit**

```
hash 算法：
  - FNV-1a 32-bit（标准库 hash/fnv）
  - 稳定（同一 ns 永远 hash 到同一 shard）
  - 计算 ~30 ns，零成本

hash input：
  - namespace 名字（v2.3.0 起 project ≡ namespace）
  - 不用 project 名（避免未来 project 跨 ns 时破坏分片稳定性）
  - 不用 namespace + resource_key（粒度太细，无意义）

⚠ FNV 不是密码学 hash——对相似 input（如 kp-bench-001..050）
  分布不完美。50 项目实测分布是 4-7 个 / shard，不均度 ±40%。
  详见第 6.3 节。
```

### Q4：启动决策（pod 怎么决定持有哪些 shard）

**选：lease 抢占动态决定**

```
方案 A：ordinal-based（StatefulSet 配套）  ✗ 不灵活
方案 B：lease 抢占（选用）
       pod 启动后周期扫所有 N 个 lease
       未持有且配额未满 → 尝试抢占
       已持有 → 续约
       
       优点：
       ✓ 死/活/重启都自然处理
       ✓ 无需协调，K8s API 原子保证
方案 C：启动时 hash(pod-name) % N 一次性  ✗ 重启换名 = 分片重洗
```

### Q5：切换处理（task 入队后 shard 失主了怎么办）

**选：drop + 幂等保证**

```
方案 A：drop（选用）
  失主时 task 直接丢弃
  reconcile 本来就是幂等的：检测到资源缺失 → 修复，不缺失 → return
  最坏情况：两个 pod 同时跑同一个 reconcile（极小概率）
  实测代价：极小

方案 B：转交给新 owner
  ✗ 需要任务队列持久化 + 跨 pod 协调
  ✗ 复杂度跳一档，引入分布式状态机的深坑

方案 C：等当前 task 完成
  ✗ 优雅但慢，且需要协调"什么时候完成"
```

---

## 五、实现层次（Step 1 → Step 3）

按"边界清晰、单 commit 解决一件事"原则，分三步实施：

### Step 1：sharding 包基础设施（commit 721ae8d）

新建 `internal/sharding/` 子包：

```
shard.go              FNV-1a hash + ShardSet 数据结构
shard_test.go         单测（4 个）
multi_lease.go        N 个 lease 抢占主循环
multi_lease_test.go   单测（5 个）
lease_helpers.go      本地 lease 工具函数（破 import cycle）
```

**为什么 sharding 包要本地实现 lease 函数**——避免 import cycle：

```
controller    →  imports sharding   （global.go 启动 manager）
sharding      →  imports controller （早期版本调用 controller.GenerateIdentity）
              ↓
              import cycle
              ↓
解法：sharding 包内本地实现 generateIdentity / tryAcquireOrRenew
      让 sharding 真正自包含
```

### Step 2：业务路径接入（commit f3c3291）

`StartGlobal` 重构（`internal/controller/global.go`）：

```
v2.4.0 时态：
  StartGlobal → runGlobalLeaderElection → runAsLeader
                                          (赢 leader 的 pod 跑业务)

v2.5.0 时态：
  StartGlobal → 启动 sharding manager（每个 pod 都启动）
              → 每个 pod 各自跑 watchers / reconcile loop / worker pool
              → 业务路径加 shard 过滤
              → leader lease 选举仅决定"谁跑 sweeper"
              → leader pod 也持有 shard，正常工作
```

5 处加 shard 过滤：

| 位置 | 过滤逻辑 |
|------|---------|
| `watchNamespaces` 回调 | `if !shardMgr.Shards().OwnsNamespace(meta.Name, N)` |
| `watchConfigMaps` 回调 | 同上（用 `meta.Namespace`） |
| `enqueueProjectResources` 入队前 | 第一道过滤 |
| `OnShardChanged` 回调 | `RemoveOrphanProjects` 用 |
| `OrphanSweeper` 周期任务 | 同上 |

### Step 3：孤儿清理（commit 99f316d）

分片切换时（pod 失去某 shard）触发：

```
1. OnShardChanged(added, removed) 回调
   if len(removed) > 0:
     time.Sleep(5 * time.Second)        ← grace period 让 reconcile 完成
     gs.RemoveOrphanProjects(isOwned)    ← 清理 projects + machines

2. orphanSweeper（每 30 秒兜底）
   gs.RemoveOrphanProjects(isOwned)
   防 OnShardChanged 漏触发
```

---

## 六、性能数据

### 6.1 测试环境

```
硬件        Apple Silicon, 16 GB RAM
集群        orbstack K8s, 单节点 (Pod 容量 110)
副本        3
limits      CPU 500m / Memory 512MiB
分片数 N    10
配额       ceil(10/3) = 4 / pod
```

### 6.2 10 项目场景（小规模 baseline）

| 指标 | v2.4.0 | v2.5.0 | 变化 |
|------|--------|--------|------|
| 集群总 CPU | 18.92% | 31.24% | **+65%** |
| avg CPU / pod | 6.30% | 10.41% | +65% |
| avg memory / pod | 36.62 MiB | 52.40 MiB | +43% |
| peak CPU | 54.25% | 87.85% | +62% |

**单 pod 拆解（v2.5.0）**：

```
Pod A (leader)   avg CPU 10.84%   shards: 0,1,4,5 (4 个)
Pod B            avg CPU 11.81%   shards: 2,3,6,7 (4 个)
Pod C            avg CPU  8.59%   shards: 8,9     (2 个)

三 pod CPU 趋同 → 没有 v2.4.0 的"集中式瓶颈"
```

**诚实结论：10 项目下 v2.5.0 是过度工程**

总 CPU 上升 65% 来源：

```
框架开销 ×N：
  3 倍 kubectl --watch 子进程   ~5%
  3 倍 reconcile loop 8s tick   ~3%
  3 倍 worker pool idle          ~4%
  ────────────────────────────
  合计                          ~12% ≈ 实测 +12.32%

分摊收益（reconcile 本身）：
  10 项目工作量平摊到 3 副本 ≈ 3.3 项目 / pod
  CPU 节省：约 2/3 × 16.93% = ~11%
  
净效果：+12% 框架开销 - 11% 分摊收益 ≈ +1%（理论）
        实测 +65% 因为 framework overhead 在小规模下边际占比极大
```

→ **10 项目下 v2.5.0 不是优化**，分片机制的固定开销 > 分摊收益。

### 6.3 50 项目场景（大规模真实价值）

50 项目 / 3 副本 / N=10 / 5 分钟稳态：

| 指标 | v2.5.0 50 项目 | 备注 |
|------|---------------|------|
| 集群总 CPU | 138.27% | 占用 1.38 核（CPU limits 总额 1.5 核） |
| avg CPU / pod | 46.09% | 健康范围 |
| peak CPU | 100.53% | 短暂顶到 limits |
| avg memory / pod | 84.16 MiB | |

**单 pod 拆解（v2.5.0）**：

```
Pod              avg_CPU   max_CPU   avg_MEM   shards
─────────────────────────────────────────────────────
f7n9x            49.04%    100.53%   87.08 MiB  0,3,4,7  (4 个)
ztg2z            50.75%     79.17%   93.38 MiB  6,8      (2 个)
tktpg            38.49%     80.04%   72.02 MiB  1,2,5,9  (4 个)
```

**Shard 项目分布（FNV32 实测）**：

```
shard 0: 5 projects   shard 5: 7 projects
shard 1: 4 projects   shard 6: 7 projects
shard 2: 4 projects   shard 7: 5 projects
shard 3: 5 projects   shard 8: 4 projects
shard 4: 4 projects   shard 9: 5 projects
total: 50

不均匀度：4-7 个 / shard，± 40%
```

**FNV 在连号短字符串（kp-bench-001..050）上分布不完美**——这是
非密码学 hash 的固有特征。真实生产环境项目名通常是有意义的字符串，
分布会更均匀。这点在第 8 节"未做事项"详细讨论。

**实际工作量分布**：

```
f7n9x  4 shards = 19 projects   CPU 49.04%   单项目 ≈ 2.58%/CPU
ztg2z  2 shards = 11 projects   CPU 50.75%   单项目 ≈ 4.61%/CPU  ← 异常
tktpg  4 shards = 20 projects   CPU 38.49%   单项目 ≈ 1.92%/CPU
```

**ztg2z 单项目 CPU 异常高的可能原因**：

```
1. ztg2z 是 leader pod，多跑 sweeper goroutine（每 60s 一次 lease 列举）
2. ztg2z 之前重启过（Step 3 集成测试时杀过 pod），watcher 恢复期开销
3. 单次 5 分钟样本的随机抖动
4. 持有 shard 6+8 恰好包含某些"reconcile 路径稍长"的项目（mock 项目应该不存在差异，但极小概率）

需要"性能立方体"测试方法论才能说清——见第 8.1 节
```

### 6.4 10x → 50x 缩放对比（v2.5.0 内部）

| 维度 | 10 项目 | 50 项目 | 缩放比 |
|------|--------|---------|--------|
| 项目数 | 10 | 50 | 5.0x |
| 集群总 CPU | 31.24% | 138.27% | **4.4x** ← 接近线性 |
| avg CPU / pod | 10.41% | 46.09% | 4.4x |
| peak CPU | 87.85% | 100.53% | **1.14x** ← 几乎不变 |
| avg memory / pod | 52.40 MiB | 84.16 MiB | 1.6x ← 亚线性（缓存复用） |

**关键解读**：

```
✅ CPU 接近线性扩展（4.4x vs 5x 项目）→ 分片机制有效
✅ peak CPU 几乎不变 → v2.5.0 解决了 v2.4.0 的"单点瓶颈"
✅ 内存缓慢增长 → 状态机缓存命中率高
```

**v2.5.0 真正的胜利不是"小规模也快"，是"大规模仍可控"**：

```
v2.4.0 假设外推到 50 项目：
  leader 单 pod = 16.93% × 5 = 84.65%（接近 limits 500m 100%）
  reconcile burst 期间会顶满 limits
  
v2.5.0 实测 50 项目：
  单 pod 平均 46.09%
  peak 100.53%（短暂）
  仍然在 limits 内健康运行
```

---

## 七、Lars 设计血缘

KubePivot 的分片机制不是凭空设计——它继承自 qc 21 岁时复现的 C++ 负载均衡项目 Lars 的核心思想：

```
Lars (2024)                       KubePivot v2.5.0 (2026)
─────────────────────────────────────────────────────────────
3 UDP Server                  →   3 副本 controller
modid+cmdid % 3 分流          →   fnv32(namespace) % N
DNS Service 双 Map 模型       →   GlobalState + machineEntry
host_info 节点抽象            →   shard lease 持有者
idle/overload 双队列          →   配额限制 ceil(N/replicas)
Probe 探测                    →   Lease 续约（5s 周期）
Reporter 状态汇报             →   slog.Info 结构化日志
```

但有一处**主动拒绝 Lars 思路**：

```
Lars：跨进程的"集群协调者"做全局状态机管理
KubePivot v2.5.0：拒绝分布式状态机
                  → 每 pod 自治，自扫孤儿
                  → drop + 幂等代替"精确传输"
                  → 简单 > 完美
```

这是 23 岁的工程成熟——**知道哪些 21 岁会做的事不该做**。

---

## 八、未做事项与未来演进

### 8.1 性能立方体测试方法论（v2.5.1 任务）

当前性能数据**只覆盖了一个组合**：50 项目 / 3 副本 / N=10。

但 KubePivot 的可配置性意味着系统是个三维空间：

```
项目数 P    {10, 50, 100, 200, 500}
副本数 R    {3, 5, 10}
分片数 N    {R, R*2, R*5, R*10}
```

单点测试无法回答关键问题：

```
- N 应该多大才合理？(N=R 时每 pod 1 shard，N=R*10 时每 pod 多 shard)
- 100 项目下需要多少副本？
- 分片机制在多大项目数后才划算？(从"过度工程"翻转到"水平扩展胜利")
- shard 项目分布不均的影响有多大？(FNV 在连号字符串下 ±40% 不均)
```

**v2.5.1 计划**（不打 tag，作为持续改进任务）：

```
1. 自动化 benchmark matrix 脚本
   bash benchmark/scripts/matrix.sh -p "10,50,100" -r "3,5" -n "auto,2x,5x"
   自动跑所有组合，输出 CSV
   
2. 数据可视化
   matplotlib 渲染热力图：(P, R) → CPU/项目
   找最优 N 公式（基于 P 和 R）

3. 文档产出：docs/design/sharding-tuning.md
   - 配置建议矩阵：项目数 X 用 Y 副本和 Z 分片
   - 性能立方体可视化
   - "什么时候应该升级到 v2.7.0 自研 informer" 的判据

4. 顺手做的小优化（如果有数据支持）：
   - QuotaPerPod ceil → floor 对比（4:4:2 vs 4:3:3 哪个更均衡）
   - Hash 算法对比：FNV vs xxhash vs murmur3
```

### 8.2 v2.5.1 其他持续改进

```
Backoff 队列（A.1.5）
  Lars 的过载队列 + Probe 思想没在 v2.5.0 落地
  独立工程，~200 行
  
client-go 对比基准（A.2）
  fork 一个 client-go 版本 controller，跑同样 benchmark
  数据驱动决定 v2.7.0 自研 informer 的必要性
  
v2.4 P2 移入：concurrent-chaos / watch-reconnect 脚本验证
  脚本已写好，benchmark 命令行实测过
  正式跑出 p50/p95 数据补全
```

### 8.3 v2.6.0：流量层（B.1 + B.2）

下一个真 minor tag。Lars 思路在流量调度层完整落地。
设计草案见 `docs/design/traffic-layer-draft.md`（待补）。

### 8.4 v2.7.0：自研 Informer（watcher 框架开销的根本解法）

```
v2.7.0 自研 Informer 设计目标：
  - 不引入 client-go
  - 自己实现 list/watch 协议（HTTP + JSON）
  - 每个 shard 一个独立 informer，K8s API server 推送的事件
    就只是该 shard 关心的
  - 真正消除"3 倍 watcher 开销"
  
前提：v2.5.1 client-go 对比基准的真实数据出来后判断启动
```

---

## 九、附录

### 9.1 配置参考

```bash
# Controller 部署时通过 ConfigMap 注入
kubectl create configmap kubepivot-controller-config \
  -n kubepivot-system \
  --from-literal=worker_pool_size=20 \
  --from-literal=shards=10
```

```yaml
# deployment.yaml 通过 valueFrom 引用
- name: KUBEPIVOT_SHARDS
  valueFrom:
    configMapKeyRef:
      name: kubepivot-controller-config
      key: shards
```

### 9.2 监控分片状态

```bash
# 查看每个 shard lease 的归属
kubectl get lease -n kubepivot-system | grep shard

# JSON 格式看持有者
kubectl get lease -n kubepivot-system -o json \
  | jq -r '.items[] | select(.metadata.name | startswith("kubepivot-controller-shard-")) | "\(.spec.holderIdentity)\t\(.metadata.name)"' \
  | sort

# 看每个 pod 的日志（含 shard 持有变化）
kubectl logs -n kubepivot-system -l app=kubepivot-controller --tail=100 \
  | grep -E "🧩|🎯|🧹"
```

### 9.3 故障转移演练

```bash
# 杀一个 pod，观察 shard 重洗
victim=$(kubectl get pods -n kubepivot-system -l app=kubepivot-controller -o jsonpath='{.items[0].metadata.name}')
kubectl delete pod -n kubepivot-system "$victim"

# 等 30 秒
sleep 30

# 看新分布（lease 应该被其他 pod 抢占）
kubectl get lease -n kubepivot-system | grep shard
```

实测：~10 秒内某个 pod 抢占空缺 lease，分片重新分布稳定。

### 9.4 算 namespace 落到哪个 shard

```python
def fnv32a(s):
    h = 2166136261
    for c in s.encode():
        h ^= c
        h = (h * 16777619) & 0xFFFFFFFF
    return h

shard = fnv32a("kp-auth-service") % 10  # → 某个固定的 0-9 值
```

---

## 相关文档

- [架构总览](architecture.md)
- [Controller 设计](controller.md)
- [性能基准](performance.md)
- [GitOps 宣言](../../GITOPS-MANIFESTO.md)
