# kp bench

KubePivot 性能基准工具。覆盖 KVCache / 池化调度 / 迁移引擎 / 生产模拟全链路。

## 用法

```
kp bench <subcommand> [flags]
```

## 子命令

| 子命令 | 说明 |
|--------|------|
| `kvcache` | KVCache 基准 (Get / ListAll / Put / ColdStart / Memory / Subscribe) |
| `pool` | 池化调度基准 (FragmentRate / O(1)Counters / Sampling / PoolIndex) |
| `memory` | 内存分析 (Labels 压缩 / 放大率 / Breakdown) |
| `concurrent` | 并发基准 (ShardedPodCache 64g Put) |
| `storm` | Delta Storm 基准 (merge-on-read p99) |
| `all` | 全量基准（上面五组一口气） |
| `scale` | 规模化基准 (1k / 10k / 100k FragmentRate) |
| `gen` | 生成测试数据 |
| `chaos` | 混沌测试 (kind + jitter + gone) |
| `sim` | 生产模拟器 |

## 示例

```bash
# KVCache 全量
kp bench kvcache

# 池化调度
kp bench pool

# 内存分析
kp bench memory

# 全量 + CPU profile
kp bench all --cpuprofile

# 规模化
kp bench scale

# 生成 100k Pod fixture
kp bench gen --pods=100000 --output=benchmark/fixtures/100k.json

# 混沌测试
kp bench chaos --nodes 3 --pods 500 --chaos-duration 120 --watch-jitter 50 --gone-rate 5

# 生产模拟
kp bench sim
```

## kp bench kvcache

13 组 benchmark，涵盖读/写/冷启动/内存/订阅全路径。

| Benchmark | 维度 | 说明 |
|-----------|------|------|
| PodCache_Get | 读 | 单 key 随机读，5k Pod |
| PodCache_ListAll | 读 | 全量列出 (1k/5k/10k) |
| PodCache_ListByNode | 读 | 按节点过滤，byNode 二级索引 |
| PodCache_ListByNS | 读 | 按 namespace 过滤 |
| PodCache_ConcurrentRead | 读 | 多 goroutine 并发读 |
| NodeCache_Get | 读 | 单节点读 |
| NodeCache_ListAll | 读 | 全量节点列出 |
| PodCache_Put | 写 | 单条 delta write |
| PodCache_PutCrossNode | 写 | 跨节点迁移，byNode 索引更新 |
| PodCache_Delete | 写 | 单条 delta tombstone |
| PodCache_PutBulk | 写 | 批量写入 (1k/5k/10k) |
| PodCache_ColdStart | 冷启动 | 全量加载 (1k/5k/10k) |
| PodCache_SubscribeNotify | 订阅 | Subscribe 通知吞吐 |

## kp bench pool

5 组 benchmark，池化调度计算路径。

| Benchmark | 说明 |
|-----------|------|
| FragmentRate | 精确碎片率 (1k/10k/100k) |
| PoolUtil_O1 | O(1) 原子计数器 (10k/100k) |
| FragmentRateSampled | 采样碎片率 (10k/100k) |
| PoolIndex_Compute | Pre-group 索引计算 (10k/100k) |
| FragSamplingError | 采样误差验证测试 |

**四路对比 (100k Pods, 2k Nodes, M4)**：

| 方法 | 延迟 | 加速 |
|------|------|------|
| Exact | 1805 ms | 1x |
| Sampled 10% | 1099 ms | 1.6x |
| PoolIndex | 1860 ms | 1x (单池等价) |
| O(1) Counters | 75.5 ms | 23.9x |

## kp bench memory

6 组分析，Labels 压缩效果 + 内存放大率。

| Benchmark | 说明 |
|-----------|------|
| PodEntry_Alloc | 20 labels 分配 (984 B) |
| PodEntry_AllocCommon | 8 common labels (368 B, -74%) |
| Labels_Alloc | 单独 Labels map 开销 |
| PodCache_Memory | 内存驻留 (1k/5k/10k) |
| PodCache_MemoryAmp | 内存放大率 1.13x |
| MemoryBreakdown | 5000 PodEntry 明细 |

## kp bench concurrent

ShardedPodCache (16 路 ns-hash 分片) 64 goroutine 并发 Put。

```
BenchmarkPodCache_ParallelPut  160 ns/op, 170 B, 5 allocs
```

## kp bench storm

Delta Storm — 500 delta 写入后 merge-on-read。

```
BenchmarkPodCache_DeltaStorm         500 put + ListAll
BenchmarkPodCache_DeltaStorm_ListByNode  500 put + ListByNode
```

## kp bench scale

规模化专用：FragmentRate (1k/10k/100k) + O(1) Counters (100k)。

## kp bench gen

```bash
kp bench gen --pods=100000 --nodes=2000 --output=benchmark/fixtures/100k.json
```

生成确定性伪随机 Pod fixture。分布：70% web / 20% batch / 10% GPU。

## kp bench chaos

```bash
kp bench chaos --nodes 3 --pods 500 --chaos-duration 120 --watch-jitter 50 --gone-rate 5
```

kind 集群 + Pod 驱逐 + 节点 taint + Watch jitter + 410 Gone storm。

## kp bench sim

本地生产模拟器。注入 Watch 网络延迟 + 410 Gone 重同步，无需 EKS 集群。

| 场景 | 延迟 | 说明 |
|------|------|------|
| WatchLoad 10k (0 jitter) | 20.8 μs | 纯处理零开销 |
| WatchJitter 10k (5ms) | 22.4 s | 全部模拟网络延迟 |
| Storm 50k (10ms, 8%) | 252.6 s | jitter + gone 叠加 |

## 相关文档

- [KVCache 性能分析](../design/kvcache-perf-analysis.md)
- [KVCache 实施日志](../design/informer-kv-cache-impl-notes.md)
- [client-go A/B 基准](../design/bench-kp-vs-client-go.md)
- [benchmark README](../../benchmark/README.md)
