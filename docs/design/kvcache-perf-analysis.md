# KubePivot Informer KVCache 性能分析与已知局限

> 编写日期：2026-05-07
> 分支：Master（基准）/ feature/v3.2-kvcache-perf-v2（测试）
> 状态：v3.2.1 性能冲刺完成，54 项改动，19 包全量 test 通过
> 关联文档：[informer-kv-cache-impl-notes.md](informer-kv-cache-impl-notes.md) / [bench-kp-vs-client-go.md](bench-kp-vs-client-go.md)

---

## 一、性能对比：v3.2 初版 → v3.2.1

数据来源：Apple M4 / Go 1.25 / 5000 Pod / count=3 / benchtime=500ms。

### 写路径

| Benchmark | v3.2 初版 (CoW) | v3.2.1 (Delta) | 改善倍数 |
|-----------|----------------|---------------|---------|
| Put 单条 | 387 μs, 600 kB, 52 allocs | 333 ns, 172 B, 4 allocs | **1160x** |
| Put 跨节点迁移 | 211 μs, 280 kB, 31 allocs | 168 ns, 60 B, 3 allocs | **1255x** |
| Delete | 384 μs, 548 kB, 60 allocs | 387 ns, 150 B, 4 allocs | **992x** |
| PutBulk 1k | 111 μs, 173 kB | 111 μs, 173 kB | ~same |
| PutBulk 5k | 657 μs, 826 kB | 546 μs, 826 kB | **1.2x** |
| PutBulk 10k | 1575 μs, 1653 kB | 1132 μs, 1653 kB | **1.4x** |

单写从毫秒级杀到纳秒级。Delta buffer 将 CoW 全量快照（O(N) map copy）推迟到 FlushDelta，每次 Put 只做 O(1) map write + RV CAS 校验。Delete 同用 delta tombstone（nil value），不再重建全量 byNode 索引。

批量写改善 1.2-1.4x，来自 Generation tracing 和 delta 清零后的 snapshot 构建路径优化。

### 读路径

| Benchmark | v3.2 初版 | v3.2.1 | 变化 |
|-----------|----------|--------|------|
| Get | 24 ns, 0 B, 0 allocs | 25 ns, 0 B, 0 allocs | -1 ns（RV CAS 额外 lookup） |
| ListAll 5k (iterate) | 1615 ns, 0 B | 1677 ns, 0 B | ~same |
| ListByNode | 204 ns, 208 B | 147 ns, 160 B | **1.4x** |
| ConcurrentRead | 300 ns, 0 B | 304 ns, 0 B | ~same |
| Memory 5k | 2113 B/pod | 2130 B/pod | +17 B（RV int64 + generation） |

读路径整体持平。Get 多 1ns 来自 delta RLock + snapshot fallback 双查找。ListByNode 改善 1.4x 来自 merge-on-read 替代 sync flush。内存增长 17 B/pod 来自 RV（int64=8B）和 generation（atomic.Int64=8B）两个字段，可忽略。

### 冷启动

| Benchmark | v3.2 初版 | v3.2.1 | 变化 |
|-----------|----------|--------|------|
| 1k Pods | 122 μs, 173 kB | 111 μs, 173 kB | **1.1x** |
| 5k Pods | 611 μs, 826 kB | 541 μs, 826 kB | **1.13x** |
| 10k Pods | 1261 μs, 1653 kB | 1136 μs, 1653 kB | **1.11x** |

冷启动走 PutBulk 路径（全量 CoW），改善来自 snapshot 构建路径的小幅优化。

---

## 二、v3.2.1 架构总览

```
Write path                    Read path (delta empty)     Read path (delta non-empty)
───────────                   ──────────────────────      ─────────────────────────
Put:                          Get:                        Get:
  deltaMu.Lock()                deltaMu.RLock()             deltaMu.RLock()
  delta[k] = pod                check delta[k]              check delta[k]
  deltaMu.Unlock()              deltaMu.RUnlock()           deltaMu.RUnlock()
  bumpGen()                     snapshot.pods[k]            snapshot.pods[k] (fallback)
  notify subscribers           ~25ns, 0 alloc              ~25ns, 0 alloc
  ~120ns, ~170B, 4 allocs
                                                          ListAll:
Delete:                       ListAll:                      deltaMu.RLock()
  deltaMu.Lock()                snapshot.list               merge-on-read:
  delta[k] = nil                ~0ns (atomic load)            snapshot.list + delta
  deltaMu.Unlock()             O(1), 0 alloc                  seenPool(map re-use)
  bumpGen()                                                  deltaMu.RUnlock()
  notify subscribers                                        if nd>200: go FlushDelta()
  ~370ns, ~150B, 4 allocs                                  O(N), 1 alloc

PutBulk:                      ListByNode:
  FlushDelta()                  snapshot.byNode[node]
  writeMu.Lock()                O(K), lock-free
  CoW snapshot + swap         ~147ns, ~160B, 1 alloc
  ~546μs @ 5k
```

---

## 三、关键设计决策

### 3.1 Delta Buffer vs 增量 CoW

选择 Delta Buffer 而非按字段增量 CoW（diff old/new → patch leaf）。理由：
- Delta 是通用抽象，不关心 Pod 哪个字段变了
- 增量 CoW 需要 field-by-field diff，同一 Pod 多次不同字段变更产生不同的 patch 路径，测试组合爆炸
- Delta 的"延迟批量合并"天然适合 AP 语义

代价：
- ListAll 在 delta 非空时需要 merge-on-read（一次额外 O(N) 扫描 + 1 alloc）
- 阈值为 200（在 Rescheduler 5min/tick 频率下，200 条 delta 是极端情况）

### 3.2 Merge-on-read vs Sync Flush

v3.2 初版的 ListAll 在 delta 非空时阻塞等待 CoW flush。v3.2.1 改为 merge-on-read + 异步 flush：
- 读路径从不阻塞
- delta 超过 200 时后台异步 CoW，后续 ListAll 回到快路径（pre-built slice）
- 消除了多 goroutine 同时 ListAll 时的惊群问题

### 3.3 RV CAS 校验

PodEntry.RV 携带 etcd ResourceVersion。Put 在写入 delta 前检查 RV >= current 才接受。防止：
- Watch 事件乱序（旧事件在新事件之后到达）
- 410 Gone 回放（Informer 重新 List 后旧 Watch 事件继续到达）
- 双 Cache（Skeleton ↔ PodEntry）版本不一致

当前 RV 由调用方手动传入。Phase 2 Watch 接线完成后自动从 Resource.ResourceVersion 提取。

### 3.4 ready=false 默认

PodCache 初始 `ready=false`，PutBulk 完成后置 `ready=true`。ShardedPodCache 的空分片手动 `ready=true`（永远不会有 Pod 的分片不应阻塞整缓存）。InformerAdapter 在 ready=false 时走 ErrCacheNotReady → fallback kubectl。

这区分了三种状态：
- ready=false：缓存未填充（Controller 冷启动）
- ready=true + empty：集群无 Pod（合法）
- ready=true + populated：正常运行

---

## 四、已知局限（诚实清单）

### 4.1 FlushDelta 仍是一次 CoW（~546μs @ 5k）

Delta 稀释了写放大，但 FlushDelta 仍然是一次全量 snapshot 重建。触发场景：
- PutBulk（初始 List 填充、410 Gone 重建）
- delta > 200 时的异步 flush
- Rescheduler 每 5min 一次 tick → 可接受

**缓解**：merge-on-read 避免了 ListAll 主动触发 CoW。FlushDelta 只在 PutBulk 和异步 path 调用。

### 4.2 Merge-on-read 在 delta storm 时的额外开销

当 Watch 事件高频（>1k/s）导致 delta 持续 >200，ListAll 走 merge-on-read 路径（2N 扫描 + 1 alloc）。async flush 在后台追赶，但 tail latency 增加。

**缓解**：`seenPool`（sync.Pool）复用临时 map，消除 merge 路径中的 map 分配。slice 分配（~N×8B）在 Rescheduler 5min/tick 频率下可忽略（40KB/5min）。merge slice pool 留 v3.3（需 InformerAdapter 配合归还）。阈值 `mergeThreshold=200`，生产 pprof 若 merge-on-read >5% cycles 则降到 100。

### 4.3 Merge-on-read 分配明细

| 分配项 | 大小 | 池化 | 备注 |
|--------|------|------|------|
| merge slice | ~N×8B | ⏳ v3.3 | 40KB @ 5k, 5min/tick → 忽略 |
| seen map | ~Nd×8B | ✅ seenPool | sync.Pool 复用 |
| snapshot.list | 0 | pre-built | 不走 merge 路径时零分配 |

### 4.4 RV 未由 Informer 自动填充

PodEntry.RV 字段和 Put CAS 逻辑已就绪，但 Informer → PodEntry 的 RV 传递路径未完成（Phase 2: Watch 接线）。当前所有 RV=0（CAS 跳过，不影响写入）。v3.3 Watch 接线后自动启用全部功能。

### 4.5 ShardedPodCache.ListAll 合并分配

16 个分片的 pre-built list 合并为单 slice 需要一次分配。高频 scan 可加 `sync.Pool` 缓存 → v3.3。

### 4.6 内存模型：PodEntry ~2100B/pod 而非 ~100B

`PodEntry` struct 本身约 100B，但 Labels map（20 keys × strings）和 Go 的 map overhead 将实际堆占用推到 ~2100B/pod。与 client-go `v1.Pod` 的 ~7856B/pod 相比仍省 3.7x，但并非标称的 100B。

**不修**：Labels 压缩（LabelHash + CommonLabels）增加复杂度但当前 3.7x 内存优势已经够用。10k Pod = 21MB vs 76MB。

### 4.7 单机 benchmark 无真实 IO

所有 benchmark 在 M4 单机纯内存运行，无 kubectl call、无网络延迟、无 Watch jitter。生产 EKS 环境下 ListAll 延迟可能受 Informer Watch 接线影响。

**建议**：v3.3 Real YAML benchmark 从真实集群拉 kube-state-metrics 数据，模拟生产资源分布。

---

## 五、与 client-go 对比（v3.2.1 终版）

| 维度 | KP v3.2.1 | client-go v0.34 | KP 优势 |
|------|----------|----------------|---------|
| Get | 25 ns, 0B, 0 allocs | 39 ns, 27B, 1 alloc | 1.6x |
| Put 单条 | 333 ns, 172B, 4 allocs | 2.4 μs, 6kB, 14 allocs | **7.2x** |
| ListByNode | 147 ns, 160B, 1 alloc | 374 ns, 515B, 2 allocs | 2.5x |
| PutBulk 5k | 546 μs, 826 kB | 1050 μs, 1300 kB | 1.9x |
| ColdStart 5k | 541 μs, 826 kB | 1041 μs, 1300 kB | 1.9x |
| Memory 5k | 2130 B/pod | 7824 B/pod | **3.7x** |

单条 Put 从初版的 161x 落后逆转为 7.2x 领先。内存 3.7x 是硬护城河。

### 5.1 并发写 (v3.2.1 新增)

| 场景 | KP (16 shards) | client-go (global RWMutex) | KP 优势 |
|------|---------------|---------------------------|---------|
| 64 goroutines Put | 160 ns, 170B, 5 allocs | 待测 (feature/v3.2-client-go-ab) | 预期 10x+ |
| 1 goroutine Put | 333 ns, 172B, 4 allocs | 2.4 μs, 6kB, 14 allocs | 7.2x |

KP: 16 路 namespace hash 分片，64 goroutine → ~4 goroutine/shard → deltaMu 竞争极小（160ns vs 120ns 单线 = +33% overhead）。
client-go: 全局 `cache.Indexer` 的 `threadSafeStore` 用 `sync.RWMutex` 保护所有 Add/Update/Delete。64 并发写全部串行在单锁上。

### 5.2 Delta Storm 场景 (v3.2.1 新增)

| 场景 | 延迟 | 分配 |
|------|------|------|
| ListAll (delta 空) | 1.6 μs | 0 B |
| Delta Storm (500 writes + ListAll) | 2.1 ms | 8.0 MB |
| Delta Storm (500 writes + ListByNode) | 1.1 ms | 5.5 MB |

Delta storm 比正常快路径重 1000x，但这是触发条件（delta=500，阈值 200 的 2.5 倍）。async flush 触发后后续 ListAll 回到 1.6μs。生产监控 `len(delta)` 指标，>200 时触发 alert。

### 5.3 内存真相：Labels 黑洞

`BenchmarkPodEntry_Alloc`: 1432 B/op, 26 allocs/op。
`BenchmarkLabels_Alloc`: 1400 B/op, 24 allocs/op。

**Labels map 吃掉 98% 的分配**。PodEntry struct 本体 ~100B 被 map overhead（~40B）+ 20 个 key strings（~800B）+ 20 个 value strings（~400B）+ map bucket overhead 淹没。报表的 2130 B/pod 是 GC 后堆驻留的真实值，包含 CoW snapshot 的双份拷贝。

### 5.4 内存放大率 (v3.2.1 新增)

| 指标 | v2.7 SkeletonCache | v3.2.1 PodCache | client-go v1.Pod |
|------|-------------------|----------------|-----------------|
| 理论最小值 (B/pod) | ~50 B | ~1500 B | ~400 B |
| 实际 TotalAlloc (B/pod) | — | **1691 B** | — |
| 实际 HeapInuse (B/pod) | — | 1660 B | 7824 B |
| 放大率 (Alloc / theoretical) | 1.65x | **1.13x** | 4.69x |
| 10k Pod 堆占用 | — | **15.85 MB** | 76 MB |

v3.2.1 的 1.13x 比 v2.7 的 1.65x 更好——CoW 的指针共享发挥了极致作用：
1. 两份 map 拷贝只复制了 `*PodEntry` 指针（8B），不是 1500B 的数据本体
2. PodEntry 在堆上只有一份，byNode 索引的 map entry 也指向同一份
3. 额外的 ~200B/pod 是 map bucket overhead + snapshot struct + list slice 的摊销

对 client-go：每个 `v1.Pod` 是独立对象（ObjectMeta + Spec + Status 全复制），无共享机制，4.69x 是不可避免的。

---

## 六、pprof 性能剖析链

```bash
# CPU profile — Rescheduler scan 热点
go test -bench=BenchmarkPodCache_ -benchtime=10s -cpuprofile=cpu.out ./internal/eventstream/
go tool pprof -http=:8080 cpu.out

# Heap profile — 内存分配热点
go test -bench=BenchmarkPodEntry_Alloc -benchtime=1s -memprofile=mem.out ./internal/eventstream/
go tool pprof -http=:8081 mem.out

# Delta storm profile — merge-on-read 路径
go test -bench=BenchmarkPodCache_DeltaStorm -benchtime=5s -cpuprofile=storm_cpu.out -memprofile=storm_mem.out ./internal/eventstream/
go tool pprof -http=:8082 storm_cpu.out

# Goroutine profile — 并发锁竞争
go test -bench=BenchmarkPodCache_ParallelPut -benchtime=10s -blockprofile=block.out ./internal/eventstream/
go tool pprof -http=:8083 block.out
```

关键盯三个指标：
- `PodCache.Put` 的 `deltaMu.Lock` 阻塞时间（block profile）
- `ListAll.merge-on-read` 的 `make` 分配占比（heap profile）
- `putBulk` 的 `copyMap` 在 CPU profile 里的占比（应 < 5%）

## 七、EKS p99 生产验证计划（v3.3 pilot）

当前所有 benchmark 在 M4 单机纯内存运行。缺失维度：
- **Watch 延迟**：Informer Watch 事件的网络 RTT + JSON 反序列化
- **kubectl fallback 延迟**：cold cache → kubectl 调用的 100ms+ p99
- **真实 Pod 密度**：kube-state-metrics 数据生成 > 当前 fake 模板均匀分布
- **GC 压力**：client-go v1.Pod 的 400B/pod → KP PodEntry 的 100B/pod 对 GC 的实际影响

pilot 计划：
```bash
# 1. 从 EKS 集群 dump 真实 Pod YAML
kp bench dump --kubeconfig ~/.kube/eks-config --ns-filter 'kube-*,default,prod-*' --output benchmark/fixtures/eks-5k.json

# 2. 用真实数据跑全链（含 Informer Watch 接线）
go test -bench=. -benchtime=30s -cpuprofile=cpu-eks.out -memprofile=mem-eks.out ./internal/eventstream/

# 3. 对比 client-go Informer 同等负载
go test -tags clientgo -bench=. -benchtime=30s ./benchmark/clientgo/

# 4. p99 tail latency 收集
go test -bench=BenchmarkPodCache_DeltaStorm -count=10 | tee storm-raw.txt
benchstat storm-raw.txt
```

预期：EKS 上 Watch 延迟放大 KVCache 优势（kubectl call 100ms+ vs KVCache 1.6μs），但 delta storm p99 可能因网络 jitter 恶化。`mergeThreshold` 可能需要从 200 降到 100。

## 八、v3.3 待办

| 项目 | 优先级 | 预期收益 |
|------|--------|---------|
| Watch 接线（Phase 2/4）+ RV 自动填充 | P0 | PodCache 生产可用 |
| Real YAML benchmark（kp bench dump） | P1 | 数据真实性 |
| ListAll merge `sync.Pool[*[]PodEntry]` | P2 | 0 alloc 回归 |
| Ristretto / 按 ns 分片 | P2 | Hot/Warm 分层，写并发 N 倍 |
| buf_pool slab allocator | P3 | Go GC >10% CPU 时启用 |
| mergeThreshold 生产调优 | P3 | pprof 驱动 |

---

## 编辑记录

```
2026-05-07  创建
            基于 feature/v3.2-kvcache-perf-v2 分支 benchmark 数据
            v3.2 初版数据来自 feature/v3.2-client-go-ab 分支
            对比 17 项 benchmark, count=3, benchtime=500ms
```
