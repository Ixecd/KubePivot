# KubePivot Benchmark 套件

## kp bench CLI

```bash
kp bench              # 查看所有子命令
kp bench scale        # 规模化基准 (1k/10k/100k pods)
kp bench sim          # 生产模拟器 (Watch jitter + 410 Gone)
kp bench gen          # 生成测试数据
kp bench chaos        # 混沌测试
```

### kp bench scale

```bash
kp bench scale
# 碎片率 1k/10k/100k + O(1) 计数器 + 采样误差

# 等价于
go test -bench=BenchmarkFragmentRate -benchmem ./internal/scheduler/
go test -bench=BenchmarkPoolUtil_O1_100k -benchmem ./internal/scheduler/
go test -run=TestFragSamplingError -v ./internal/scheduler/
```

四路对比 (100k Pods, 2k Nodes, M4):
| 方法 | 延迟 | 加速 |
|------|------|------|
| Exact | 1805 ms | 1x |
| Sampled 10% | 1099 ms | 1.6x |
| PoolIndex | 1860 ms | 1x |
| O(1) Counters | 75.5 ms | 23.9x |

### kp bench sim

```bash
kp bench sim                          # 默认: 10k pods, 10ms jitter, 5% gone
kp bench sim --jitter=5 --gone=3      # 5ms jitter, 3% gone
kp bench sim --pods=50000             # 50k pods delta storm
```

本地模拟生产条件（无需 EKS）:
| 场景 | 延迟 | 说明 |
|------|------|------|
| WatchLoad 10k (0 jitter) | 20.8 μs | 纯处理零开销 |
| WatchJitter 10k (5ms) | 22.4 s | 全部网络延迟 |
| Storm 50k (10ms, 8%) | 252.6 s | jitter + gone 叠加 |

### kp bench gen

```bash
kp bench gen --pods=100000 --nodes=2000 --output=benchmark/fixtures/100k.json
```

生成确定性伪随机 Pod fixture，用于 benchmark 输入。
分布：70% web / 20% batch / 10% GPU，4 容器 + 真实 label 模式。

### kp bench chaos

```bash
kp bench chaos --nodes 3 --pods 500 --chaos-duration 120 --watch-jitter 50 --gone-rate 5
```

kind 集群 + Pod 驱逐 + 节点 taint + Watch jitter + 410 Gone storm。
120 秒内持续注入故障，验证 MigrationManager p99 tail latency。

## Makefile 目标

```bash
make bench.kp          # KP KVCache 全部 benchmark
make bench.scale       # 规模化基准 (1k/10k/100k)
make bench.baseline    # 保存基线
make bench.regression  # 回归检查 (vs baseline)
```

## Prune Gate

每个 feature PR 应附带 benchmark 对比。性能退步阈值：
- PodCache_Get: >20% → block
- PodCache_ColdStart (5k): >30% → block
- PodCache_Memory (5k): RSS >20% → block
- FragmentRate (100k): >50% → block

## 文件结构

```
benchmark/
├── README.md                 本文件
├── sim/
│   └── prod_sim_test.go      生产模拟器 (Watch jitter + 410 Gone)
├── chaos/
│   └── kind-chaos-lite.sh    Chaos 脚本
├── fixtures/
│   └── gen_real.go           测试数据生成器
└── results/                  基准结果归档
```
