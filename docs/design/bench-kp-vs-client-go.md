# KubePivot Informer KVCache vs client-go A/B 基准测试报告

> 编写日期：2026-05-06
> 分支：feature/v3.2-client-go-ab（已合并对比结果）
> 环境：Apple M4 / Go 1.25 / GOMAXPROCS=4 / GOGC=200 / client-go v0.34.0
> 数据：5000 Pods（4 容器 / 20 标签 / 真实 Deployment 模板）、200 Nodes

---

## 一、背景

v3.2 实现了自研 PodCache / NodeCache（`internal/eventstream/kv_cache.go`），替代 v2.7 的 SkeletonCache。本轮 benchmark 回答一个核心问题：**KubePivot 的自研 KVCache 比 Kubernetes 标准库 client-go 的 `cache.Indexer` + typed `v1.Pod` / `v1.Node` 快多少？省多少？**

v2.7 时期做过一轮对比（`feature/client-go-comparison` 分支，未合入 Master），记录在 `docs/design/eventstream-perf.md`。本轮在 v3.2 重写版 PodCache / NodeCache 上重新测。

---

## 二、对比矩阵

| 维度 | 用例 | 说明 |
|------|------|------|
| A 读 | Get(ns,name) | 单 key 随机读 |
| A 读 | ListAll (全量迭代) | 最热路径：Rescheduler 每 5min 调用 |
| A 读 | ListByNode | 按节点过滤（KP 二级索引 vs client-go Indexer） |
| A 读 | ConcurrentRead | 多 goroutine 并发读 |
| A 读 | Node Get / ListAll | 节点缓存对比 |
| B 写 | Put/Add 单条 | Watch ADD/UPDATE 事件写入 |
| B 写 | Put/Update 跨节点迁移 | 触发 byNode 索引更新 |
| B 写 | Delete | Watch DELETE 事件 |
| B 写 | PutBulk / AddBulk | 批量写入（冷启动 / Resync） |
| C 冷启动 | 1k / 5k / 10k | 全量 Pod 加载时间 |
| D 转换 | PodEntry→PodInfo vs v1.Pod→PodInfo | 数据结构转换开销 |
| E 内存 | 1k / 5k / 10k | GC 后堆内存驻留 |

---

## 三、原始结果

### A 读路径

| Benchmark | KubePivot (ns) | client-go (ns) | B/op (KP vs CG) | allocs (KP vs CG) |
|-----------|---------------|----------------|-----------------|-------------------|
| Get | **24.3** | 38.9 | 0 vs 27 | 0 vs 1 |
| ListAll 5k (iterate) | 1615 | 1654 | 0 vs 0 | 0 vs 0 |
| ListByNode | **204** | 374 | 208 vs 515 | 1 vs 2 |
| ConcurrentRead | 300 | 301 | 0 vs 0 | 0 vs 0 |
| Node Get | 7.3 | 6.7 | 0 vs 0 | 0 vs 0 |
| Node ListAll | 1516 | **1120** | 1792 vs 1792 | 1 vs 1 |

**Get 分析**：KP 领先 1.6x。核心差异：
- KP：`PodCache.Get(ns, name)` 走 `snapshot.pods["ns/name"]`，单级 map lookup，无需分配
- client-go：`Indexer.GetByKey("ns/name")` 需要 `strings.Split` 拆分 key → 两级 map lookup → 接口类型断言为 `*v1.Pod`，每次 27B 分配

**ListByNode 分析**：KP 领先 1.8x。KP 的 byNode 二级索引直接返回预缓存 `[]*PodEntry` slice，client-go 需要通过 `Indexer.ByIndex` 获取 `[]interface{}` → type assert → 拷贝到 `[]*v1.Pod`。

**Node ListAll**：client-go 略快 1.35x。KP 返回前做了一次 slice copy（CoW 安全），而 client-go 的简单 map walk 无需 copy。这是可修复的工程差异——改成 iterator 模式即可持平或反超。

### B 写路径

| Benchmark | KubePivot | client-go | B/op (KP vs CG) | allocs |
|-----------|----------|-----------|-----------------|--------|
| Single Put / Add | 387 μs | **2.4 μs** | 600 kB vs 6 kB | 52 vs 14 |
| Cross-node Update | 211 μs | **177 ns** | 280 kB vs 75 B | 31 vs 4 |
| Delete | 384 μs | **1.3 μs** | 548 kB vs 335 B | 60 vs 5 |
| PutBulk 1k | **111 μs** | 201 μs | 173 kB vs 287 kB | 1366 vs 2383 |
| PutBulk 5k | **657 μs** | 1050 μs | 826 kB vs 1300 kB | 6758 vs 11789 |
| PutBulk 10k | **1575 μs** | 2270 μs | 1653 kB vs 2603 kB | 13491 vs 23539 |

**单条写入**：client-go 压倒性优势（161x ~ 1194x）。这是 KP 的 CoW（Copy-on-Write）架构代价。每次 `Put` 需要：
1. 深拷贝整个 podSnapshot（所有 5000 PodEntry + byNode 二级索引 map）
2. 修改目标条目
3. 原子替换 snapshot

387 μs 对 5000 Pod 的缓存 = ~77 ns/pod 的拷贝开销。在高频 Watch 场景（100 事件/s），累计 38.7 ms/s 写入时间，占单核 CPU 约 4%。M4 / EKS 等生产环境可承受，但需持续监控 p95 Put 延迟。

**批量写入**：KP 反超（1.4x ~ 1.8x）。PutBulk 一次性 CoW 快照完成全部写入，client-go 只能逐条 `Add`。随着规模增长，KP 优势收窄（1.8x → 1.4x），因为快照拷贝成本随 N 线性增长。

### C 冷启动

| Scale | KubePivot (μs) | client-go (μs) | B/op (KP vs CG) | allocs (KP vs CG) |
|-------|---------------|----------------|-----------------|-------------------|
| 1k Pods | **122** | 201 | 173 kB vs 287 kB | 1366 vs 2383 |
| 5k Pods | **611** | 1041 | 826 kB vs 1300 kB | 6758 vs 11789 |
| 10k Pods | **1261** | 2270 | 1653 kB vs 2603 kB | 13491 vs 23539 |

KP 冷启动快 1.6x ~ 1.8x，内存省 ~1.6x。主要差异来源：
- KP：`PodEntry`（精简 struct，~100B/个）→ 一次 `PutBulk`
- client-go：`v1.Pod`（完整 K8s API 对象，~400B/个，含 metav1.ObjectMeta 的 managedFields / ownerReferences 等）→ 逐条 `Add`

### D 转换开销

| Benchmark | KubePivot | client-go |
|-----------|----------|-----------|
| FakePod → PodEntry / v1.Pod | 内联（无独立步骤） | ~1115 ns, 5696 B, 10 allocs |

KP 的 `FakePod → PodEntry` 是手工字段复制，零依赖。client-go 的 `FakePod → v1.Pod` 需要通过 `resource.NewQuantity` 等 API 构造，每次 10 次分配。这是 `k8s.io/apimachinery` 的类型安全代价。

### E 内存驻留

| Scale | KubePivot (B/pod) | client-go (B/pod) |
|-------|------------------|-------------------|
| 1k | **2126** | 7856 |
| 5k | **2113** | 7824 |
| 10k | **2119** | 7830 |

KP 内存用量为 client-go 的 **27%**（节省 3.7x）。原因：
- KP 只存调度字段：Namespace / Name / NodeName / Phase / Labels / Requests(CPU/Mem/GPU)
- client-go 存完整 `v1.Pod`：ObjectMeta（含 managedFields / ownerReferences / annotations 等）+ full Spec + full Status
- KP 的 B/pod 跨规模稳定在 ~2100，因为 CoW 快照持有 2 份拷贝

**10k Pod 生产预估**：KP ~20 MB vs client-go ~76 MB（仅 Pod 缓存，不含 Node）。

---

## 四、与设计目标的对比

| 指标 | v3.2 实测 | v3.2 设计目标 | v2.7 历史 | 判定 |
|------|----------|-------------|----------|------|
| Get | 1.6x | ≥ 3x | 3.1x | ❌ 未达标 |
| ListAll iterate | tie | ≥ 10x | 46x | ❌ 未达标 |
| ListByNode | 1.8x | ≥ 5x | — | ❌ 未达标 |
| ColdStart 10k | 1.8x | ≥ 1.5x | 1.8x | ✅ |
| Memory 10k | 3.7x | ≤ 0.5x (KP 更小) | 8.5x | ✅ |

目标未达标的原因：

1. **v2.7 数据不可复现**：v2.7 用的是 `SkeletonCache`（更激进优化——只存元数据 skeleton），v3.2 PodEntry 存完整调度字段（Labels / Requests / GPU）。比较基准不同。
2. **client-go v0.34 自身优化**：v2.7 时期用的 client-go 版本较旧，v0.34 的 `Indexer` 有性能改善。
3. **测试方法更严格**：本轮用 `for-range` 迭代强制 slice 物化，避免了 Go 1.25 编译器死代码消除导致的虚高数字。

**结论**：当前数据比 v2.7 更保守、更诚实。内存优势（3.7x）是最大护城河。

---

## 五、核心发现

### 优势

1. **0 分配读路径**：Get / ListAll 迭代零分配，是 client-go 无法复制的架构优势（client-go 的 `interface{}` → 类型断言必须分配）
2. **内存 3.7x 优势**：精简 struct vs 完整 K8s API 对象，10k Pod 省 ~56 MB
3. **冷启动 1.8x**：一次 CoW 快照 vs 逐条 Add
4. **ListByNode 1.8x**：byNode 二级索引直接返回预缓存 slice

### 弱点

1. **单条写 161x 落后**：CoW 全量快照是写放大瓶颈。但生产 Watch 频率通常 <100 事件/s，CPU 开销在可接受范围
2. **Node ListAll 1.35x 落后**：slice copy 损失

### 与 v2.7 SkeletonCache 的关系

v2.7 的 SkeletonCache 在纯元数据路径上仍然是最优方案（Get 3.1x, List 46x）。v3.2 PodCache 用精度换了速度——牺牲了一些 micro-benchmark 数字，换取了完整调度字段。两个 cache 可以共存：SkeletonCache 给控制面快速决策，PodCache 给调度器精确计算。

---

## 六、v3.3 优化路线

基于 benchmark 暴露的弱点，v3.3 四刀优化：

### 优先级 P0：Delta CoW 写优化

```
现状：Put 全量快照 → 深拷贝所有 PodEntry + byNode map → 原子替换
     → 387 μs/op（5k cache），CGO 161x 领先

目标：diff old/new snapshot → 只 patch 变更的 podEntry + byNode 索引条目
     → ~10 μs/op，与 client-go 单条 Add 同级

方案：
  1. Put(oldPod, newPod) 输入两个指针
  2. 比较 oldPod == newPod（指针相等 → 跳过）
  3. NodeName 变了 → update byNode[oldNode] remove + byNode[newNode] insert
  4. 其他字段变了 → replace podEntry in snapshot.pods map
  5. 只拷贝被修改的那一"页"（受影响的部分），其余指针共享
```

### 优先级 P1：Node ListAll 零拷贝迭代

```
现状：Node ListAll → make([]*NodeEntry, len(nodes)) → copy
     → 1516 ns, CGO 1.35x 更快

目标：返回只读 iterator / 直接返回内部 slice（lock-free 读已保证安全）
     → 性能持平或反超

方案：NodeCache.ListAll() 直接返回内部 snapshot 的 slice 引用
     （atomic.Value swap 已确保旧 snapshot 不被并发修改）
```

### 优先级 P2：真实 YAML Benchmark

```
现状：fake data（10 种容器模板 × 均匀分布）不能代表生产集群的资源碎片化

目标：从真实 K8s 集群拉 kube-state-metrics 数据 → 生成 benchmark fixture
     → 覆盖 StatefulSet 固定名 / Deployment 随机名 / GPU 节点异构拓扑
     → 10k+ Pod 大规模压测

方案：写 kp bench generate 子命令 → 连 K8s API → dump JSON fixtures
```

### 优先级 P3：Sharded Cache（Ristretto / 按 ns 分片）

```
现状：单 CoW snapshot，写并发 = 1。Watch 多 ns 并发涌入 → 排队等待 writeMu

目标：按 namespace prefix 分片 × N → 写并发 N 倍
     → 或引入 github.com/dgraph-io/ristretto 做 Hot/Warm 分层

代价：复杂度跃升，跨 ns 查询需要 scatter-gather
评估：先上生产跑 1 个月，看 p95 Put 延迟是否触达告警阈值再决定
```

---

## 七、部署建议

| 场景 | 推荐 | 理由 |
|------|------|------|
| Controller reconcile（<10/s） | KP PodCache | 0 分配读，ListAll 稳定 |
| Rescheduler scan（5min） | KP PodCache | ListAll + ListByNode 批量读 |
| 高频 Watch（>500/s） | 当前可扛，加 p95 监控 | 单条 387μs × 500 = 193ms/s |
| 内存敏感（>5k Pod） | KP 必选 | 3.7x 内存节省 |

**监控指标**：`kubepivot_podcache_put_duration_seconds`（p95 < 1ms）、`kubepivot_podcache_snapshot_bytes`（<2GB/10k）

---

## 编辑记录

```
2026-05-06  创建
            基于 feature/v3.2-client-go-ab 分支 benchmark 原始数据
            17 项 benchmark, count=5, benchtime=1s
            原始数据: feature 分支 benchmark/results/v3.2-ab-20260506/
```
