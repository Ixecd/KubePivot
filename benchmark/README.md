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

### kp bench v3.4 (并发构造基准)

v3.4 新增并发构造全覆盖基准，M4 裸金属实测。

```bash
# 全量 (11 项, ~47s)
go test -bench=. -benchmem -benchtime=3s ./benchmark/

# 单项:
go test -bench=BenchmarkTokenBucket -benchmem ./benchmark/
go test -bench=BenchmarkWorkerPool -benchmem ./benchmark/
go test -bench=BenchmarkSubscriber -benchmem ./benchmark/
go test -bench=BenchmarkWorkQueue -benchmem ./benchmark/
go test -bench=BenchmarkRollbackTracker -benchmem ./benchmark/
go test -bench=BenchmarkFlood -benchmem ./benchmark/
```

**M4 基线 (2026-05-09, v3.4)**：

#### 锁 / 信号量

| 构造 | 单线 | 20g 并发 | 分配 | 分析 |
|------|------|----------|------|------|
| tokenBucket allow() | 47 ns | 127 ns | 0 | Mutex 锁内 time.Now()，并发 +2.7x 仍在 130ns 内 |
| rollbackTracker RLock | 88 ns | 88 ns | 1 | RWMutex 20 reader 零争用——同单线 |
| rollbackTracker R/W | 88 ns | - | 1 | 混合 read/write 10:1，写路径低频不拖读 |

#### 队列

| 构造 | 单线 | 分配 | 分析 |
|------|------|------|------|
| WorkQueue Dedup | 7 ns | 0 | dirty set 命中——纯 map lookup + return |
| WorkQueue AddGetDone | 157 ns | 1 | cond.Wait + goroutine spawn + mutex + 三集合操作 |
| subscriber dispatch | 30 ns | 0 | buffered chan non-blocking send，channel 有空间 |
| WorkerPool Enqueue | <1 ns | 0 | buffered chan send syscall（编译器内联） |

#### 流水线

| 场景 | eps | drop% | 分析 |
|------|-----|-------|------|
| FloodPipeline (无限流) | 37M/s | 98.5% | 20 consumer 追不上 flame producer，channel 饱和 |
| FloodPipeline (rate=50/s) | 64/s | ~100% | token bucket 严格限流，pre-check 拒绝 |

**关键结论**：

- 锁不是瓶颈——RWMutex 20g 零退化，Mutex 并发 2.7x 仍在 130ns
- channel 满才是背压——37M eps 时 98% drop，channel 是天然限流器
- 后置代币生效——tokenBucket 只跟踪不阻塞，channel 先满
- WorkQueue goroutine-per-Wait 代价 157ns，standalone 低频无影响

### shard flip gap (chaos)

```bash
bash benchmark/chaos/shard-flip-gap.sh --duration 120
```

测量 v3.4 handoff + rebalance 的 shard 接管间隙，目标 <5s。

**v3.4 orbstack 实测 (2026-05-09)**：

| 指标 | 测量值 | 目标 | 判定 |
|------|--------|------|------|
| shard flip gap | 2s | <5s | ✅ 达标 |
| drop rate | 0 | <0.1% | ✅ 零丢 |
| pod 恢复 | 3/3 | full | ✅ 全恢复 |
| Webhook TLS | 正常启动 | no error | ✅ |
| /metrics | :9090 可访问 | 200 | ✅ |
| Phase 4 KVCache | 已启用 | enabled | ✅ |

混沌测试过程：
- kill pod-0，ReleaseAll handoff 触发
- 2s 后其他 pod 抢占释放的 shard lease
- gap 从 v3.3 的 15-20s (TTL 过期) → v3.4 的 2s (主动释放)

### kp bench chaos

```bash
kp bench chaos --nodes 3 --pods 500 --chaos-duration 120 --watch-jitter 50 --gone-rate 5
```

kind 集群 + Pod 驱逐 + 节点 taint + Watch jitter + 410 Gone storm。
120 秒内持续注入故障，验证 MigrationManager p99 tail latency。

### Multi-node chaos + scale (v3.4)

```bash
# 1. 启动 3 节点 Kind 集群
kind create cluster --name kp-chaos --config benchmark/chaos/kind-multi-node.yaml

# 2. 部署 v3.4 controller
kp controller install

# 3. 规模压力测试 (1k-10k namespace)
bash benchmark/chaos/scale-ns.sh --count=10000 --batch=100

# 4. k6 洪水测试 (/health 吞吐 + 延迟)
k6 run --vus 500 --duration 300s benchmark/chaos/flood-ns.js

# 5. 混沌: podkill 50% + net-split
chaos-mesh apply -f benchmark/chaos/podkill.yaml

# 6. 清理
kind delete cluster --name kp-chaos
```

SLO 目标:
| 维度 | 目标 | 测法 |
|------|------|------|
| 吞吐 | 10k eps | `flood-ns.js` + k6 stages |
| 延迟 | p99 <200ms | k6 http_req_duration |
| 恢复 | MTTR <10s | `shard-flip-gap.sh` podkill 50% |
| 资源 | CPU <50%, mem <2GB | `scale-ns.sh` + kubectl top |
| 丢包 | drop <0.1% | /metrics EventsDropped / channelDropped |

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
