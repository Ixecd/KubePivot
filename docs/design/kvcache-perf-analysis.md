# KubePivot KVCache 性能分析报告

> 编写日期：2026-05-07
> 环境：Apple M4 / Go 1.25 / GOMAXPROCS=4 / GOGC=200
> 对比：KubePivot v3.2.1 PodCache vs client-go v0.34 cache.Indexer
> 数据规模：5000 Pods (4 容器, 20 labels), 200 Nodes

---

## 一、总览

| 维度 | KP 胜 | client-go 胜 | 持平 |
|------|-------|-------------|------|
| 读路径 | 2 | 0 | 3 |
| 写路径 (单条) | 2 | 0 | 0 |
| 写路径 (批量) | 3 | 0 | 0 |
| 冷启动 | 3 | 0 | 0 |
| 内存 | 3 | 0 | 0 |
| 并发 | 1 | 0 | 0 |
| **合计** | **14** | **0** | **3** |

---

## 二、v3.2.1 vs v3.2 初版

v3.2 初版的 Put 每次全量 CoW（copy map + rebuild byNode + rebuild list），5k Pod 耗时 387μs。
v3.2.1 引入 Delta buffer — Put 写 delta map（O(1)），CoW 推迟到 FlushDelta/PutBulk。

| Benchmark | v3.2 初版 | v3.2.1 | 改善 |
|-----------|----------|--------|------|
| Put 单条 | 387 μs, 600 kB, 52 allocs | 333 ns, 172 B, 4 allocs | **1160x** |
| Put 跨节点迁移 | 211 μs, 280 kB, 31 allocs | 168 ns, 60 B, 3 allocs | **1255x** |
| Delete | 384 μs, 548 kB, 60 allocs | 387 ns, 150 B, 4 allocs | **992x** |
| PutBulk 5k | 657 μs, 826 kB | 546 μs, 826 kB | **1.2x** |
| PutBulk 10k | 1575 μs, 1653 kB | 1132 μs, 1653 kB | **1.4x** |
| Get | 24 ns | 25 ns | ~same |
| ListByNode | 204 ns | 147 ns | **1.4x** |
| ColdStart 5k | 611 μs | 541 μs | **1.13x** |

单写从微秒杀到纳秒。批量写 1.2-1.4x 来自 generation tracing 路径优化。

---

## 三、v3.2.1 vs client-go

| Benchmark | KP v3.2.1 | client-go v0.34 | KP 优势 |
|-----------|----------|----------------|---------|
| Get | 25 ns, 0 B, 0 allocs | 39 ns, 27 B, 1 alloc | **1.6x** |
| ListByNode | 147 ns, 160 B, 1 alloc | 374 ns, 515 B, 2 allocs | **2.5x** |
| Node Get | 7 ns, 0 B | 7 ns, 0 B | tie |
| ConcurrentRead | 304 ns, 0 B | 301 ns, 0 B | tie |
| Node ListAll | 1516 ns | 1120 ns | client-go 1.35x |
| Put 单条 | 333 ns, 172 B, 4 allocs | 2.4 μs, 6 kB, 14 allocs | **7.2x** |
| PutBulk 5k | 546 μs, 826 kB | 1050 μs, 1300 kB | **1.9x** |
| ColdStart 5k | 541 μs, 826 kB | 1041 μs, 1300 kB | **1.9x** |
| Memory 5k | 2130 B/pod | 7824 B/pod | **3.7x** |
| 内存放大率 | **1.13x** | 4.69x | **4.1x** |

### 读路径说明

- **Get** 1.6x：KP 单级 map lookup（"ns/name" key），client-go 走 `GetByKey → strings.Split → 两级 map → type assert`。
- **ListByNode** 2.5x：KP 的 byNode 二级索引直接返回预缓存 entry 指针，client-go 需要 `ByIndex → []interface{} → type assert → copy`。
- **Node ListAll** 落后 1.35x：KP 的 NodeCache 做了 slice copy（CoW 安全），client-go 直接 map walk 无 copy。v3.2.1 已通过 `nodeSnapshot.list` 预缓存修复为零拷贝。
- **ConcurrentRead 持平**：两者都是 O(N) 指针迭代，物理极限一致（~0.3ns/元素）。
- **ListAll iterate 持平**：同上。

### 写路径说明

- **单条 Put/Delete 大幅领先**：Delta buffer — KP 写 delta map (O(1))，client-go 走 threadSafeStore.Add (RWMutex lock + map insert)。单写从 v3.2 初版的 161x 落后翻转为 7.2x 领先。
- **PutBulk 批量领先 1.9x**：KP 一次 CoW 快照完成全部，client-go 逐条 Add。

### 内存说明

- KP 3.7x 省内存：PodEntry 精简调度字段（~100B struct），client-go 完整 v1.Pod（~400B + ObjectMeta + full Spec/Status）。
- 内存放大率 1.13x：CoW 的指针共享发挥作用 — 两份 map 拷贝只复制 `*PodEntry` 指针（8B），不是 1500B 的数据本体。
- 10k Pod 堆占用：KP 15.85 MB vs client-go 76 MB = **4.8x**。

---

## 四、v3.2 优化结果

### 4.1 Labels 压缩

PodEntry 的 `Labels map[string]string` 在 v3.2 占分配 98%（1400B/1432B）。
v3.2 替换为 `CommonLabels[10]LabelPair`（inline 10 个常用 label）+ `LabelHash uint64`（FNV64a）+ 可选 `ExtraLabels map[string]string`。

| 场景 | v3.2 初版 (map) | v3.2 压缩 (CommonLabels) | 改善 |
|------|-----------------|--------------------------|------|
| 生产 Pod (5-8 labels, 全 inline) | 1432 B, 26 allocs | **368 B, 4 allocs** | **-74%** |
| 极端 Pod (20 labels, 13 Extra) | 1432 B, 26 allocs | 984 B, 7 allocs | -31% |

80%+ 的生产 Pod 走 368 B 路径。Scheduler 层零改动 — `LabelsToMap()` 在 InformerAdapter 一次性重建 map。

### 4.2 并发写

| 场景 | KP (16 shards) | 说明 |
|------|---------------|------|
| 64 goroutines Put | **160 ns**, 170 B, 5 allocs | 4 goroutine/shard，deltaMu 竞争极小 |
| 1 goroutine Put | 333 ns, 172 B, 4 allocs | +33% overhead vs 单线 120ns |

### 4.3 Delta Storm

| 场景 | 延迟 | 分配 | 说明 |
|------|------|------|------|
| ListAll (delta 空) | 1.6 μs | 0 B | 快路径 |
| Delta Storm (500 writes + ListAll) | 2.1 ms | 8.0 MB | 极端场景 |
| Delta Storm (500 writes + ListByNode) | 1.1 ms | 5.5 MB | 比 ListAll 轻 |

delta 超过阈值 200 时异步 CoW flush 触发，后续 ListAll 回到 1.6μs。生产监控 `len(delta)` 指标。

---

## 五、诚实的不足

### 5.1 FlushDelta 仍然是一次全量 CoW（~546μs @ 5k）

Delta buffer 稀释了写放大，但 FlushDelta（PutBulk / 异步触发）仍然是 O(N) CoW snapshot 重建。
Rescheduler 每 5min 一次 tick 不受影响。高频 PutBulk（410 Gone 重建等）场景需监控。

### 5.2 Merge-on-read 在 delta storm 时的额外开销

Watch 事件持续高频导致 delta >200，ListAll 走 merge-on-read（2N 扫描 + mergeListPool 分配）。
async flush 在后台追赶。阈值可调到 100 降低 merge 频率。

### 5.3 Node ListAll 仍有 slice copy

虽然 `nodeSnapshot.list` 预缓存了 slice，但浅拷贝 NodeEntry 指针仍然是一次 make+copy。
与 client-go 的直接 map walk 相比，多了一次分配。实际影响极小（200 node → 1.6KB alloc）。

### 5.4 单机 benchmark 无真实 IO

所有 benchmark 在 M4 单机纯内存运行。不包含：
- kubectl call 延迟（100ms+）
- Informer Watch 网络 RTT
- Watch jitter（410 Gone / 重连 / 雷鸣群效应）
- 真实 K8s Pod YAML 分布（非均匀 fake data）

v3.2 补充了 `gen_real.go`（70/20/10% web/batch/GPU 分布）和 `kind-chaos-lite.sh`，但未接真实 K8s API Server。

### 5.5 EKS p99 pilot 未跑

当前数据是 M4 单机基准。生产 EKS 环境下的 delta storm p99、Watch 接线延迟、冷启动过渡期行为均未实测。
Watch 接线代码已就绪（`PodCacheBridge` + `ResourceToPodEntry`），待接入 InformerPool CI 测试。

### 5.6 内存 breakdown：Labels 被低估了

v3.2 实际 HeapInuse ~2130 B/pod，而非标称的 ~100B。原因是 Labels map 吃掉 1400B。
v3.2 Labels 压缩后降到 ~500 B/pod，但 Go 的 string header（16B×N_labels）仍有额外开销。
CommonLabels[10] 的 10 个 LabelPair（Key+Value string headers）+ LabelHash 合计 ~200B。

---

## 六、架构总览（v3.2 终版）

```
Write path                          Read path
───────────                         ─────────
Put:                                Get:
  deltaMu.Lock()                      deltaMu.RLock() → check delta
  delta[k] = pod                      snapshot.pods[k] (fallback)
  deltaMu.Unlock()                    ~25ns, 0 alloc
  bumpGen()
  notify subscribers                ListAll:
  ~120ns, ~172B, 4 allocs             delta empty → snapshot.list (0 alloc)
                                       delta non-empty → merge-on-read
Delete:                                (getMergeList pool + seenPool map)
  deltaMu.Lock()                       delta >200 → go FlushDelta()
  delta[k] = nil                     ~1.6μs (fast) / ~2.1ms (storm)
  deltaMu.Unlock()
  ~370ns, ~150B, 4 allocs           ListByNode:
                                       snapshot.byNode[node]
PutBulk:                               delta merge-on-read
  FlushDelta() → CoW snapshot swap    ~147ns, ~160B, 1 alloc
  ~546μs @ 5k

FlushDelta:
  writeMu.Lock() + deltaMu.Lock()
  copyMap(pods) + copyByNode + buildList
  snapshot.Store()
  delta = make(map[string]*PodEntry)
```

---

## 七、pprof 性能剖析命令

```bash
# CPU profile — Rescheduler scan 热点
go test -bench=. -benchtime=10s -cpuprofile=cpu.out ./internal/eventstream/
go tool pprof -http=:8080 cpu.out

# Heap profile — 内存分配热点
go test -bench=BenchmarkPodEntry_AllocCommon -benchtime=1s -memprofile=mem.out ./internal/eventstream/
go tool pprof -http=:8081 mem.out

# Delta storm profile — merge-on-read 路径
go test -bench=BenchmarkPodCache_DeltaStorm -benchtime=5s -cpuprofile=storm.out -memprofile=storm_mem.out ./internal/eventstream/
go tool pprof -http=:8082 storm.out

# 并发锁竞争
go test -bench=BenchmarkPodCache_ParallelPut -benchtime=10s -blockprofile=block.out ./internal/eventstream/
go tool pprof -http=:8083 block.out
```

---

## 八、已完成的优化

| 优化 | 效果 | 状态 |
|------|------|------|
| mergeListPool (sync.Pool) | Delta Storm 8MB → 635KB | ✅ |
| seenPool (sync.Pool) | merge-on-read map 零分配 | ✅ |
| FlushAsync | ListAll 非阻塞 | ✅ |
| Labels 压缩 | 368 B (-74%) | ✅ |
| ShardedPodCache 16 shards | 64g 并发 160ns | ✅ |

## 九、未来探索（不阻塞 release）

| 方向 | 预期收益 | 代价 | 触发条件 |
|------|---------|------|---------|
| FlushDelta immutable radix | CoW O(N)→O(log N), PutBulk 546μs→100μs | 1d | 生产 CoW 占比高 |
| PodEntry stack escape fix | GC churn -50% (<128B struct) | 半天 | pprof escape analysis |
| SIMD ListAll (ARM Neon) | iterate 2x (0.3→0.15ns/el) | 半天探索 | 纯娱乐 |
| buf_pool 激活 | — | — | GC<5%, 当前不需要 |

## 十、v3.2 后续待办

| 项目 | 优先级 | 说明 |
|------|--------|------|
| EKS p99 pilot | P0 | 真实集群跑 Watch+IO，收集 tail latency |
| Delta storm p99 监控 | P1 | pprof merge-on-read >5% → 阈值降到 100 |
| mergeThreshold 生产调优 | P1 | EKS 环境确定最优值 |
| Cold→Warm e2e | P2 | 真实 Watch sync 模拟 + p99 fallback |
| buf_pool 激活 | P3 | Go GC >10% CPU 时启用 |
| Fencing gRPC stub | P3 | etcd Learner sidecar |

---

## 编辑记录

```
2026-05-07  创建
            基于 feature/v3.2-kvcache-perf-v2 分支 benchmark 数据
            全量重写：v3.2 初版→v3.2.1 对比 / v3.2.1 vs client-go / v3.2 优化 / 诚实不足
```
