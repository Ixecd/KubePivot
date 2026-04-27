# v2.7 Event Stream Benchmark

> 自研 Cache vs client-go cache 性能对比基准
> 目标：Day 1 数据驱动 v2.7 自研路径决策

---

## 测试环境要求

```bash
# 必须显式设置环境变量（公平比较的关键）
export GOGC=200
export GOMEMLIMIT=4GiB
export GOMAXPROCS=4
```

**为什么**：
- 默认 GOGC=100 下 client-go 因临时对象多会触发更多 GC
- 测出来 "client-go CPU 高" 实际是 GC 暂停，不公平
- GOMAXPROCS 锁定避免不同环境差异

---

## 5 个 Benchmark

| Bench | 测什么 | 文件 | 期望对比 |
|-------|--------|------|---------|
| 1 | Cache 单条读延迟 | `cache_get_test.go` | KubePivot < 50ns vs client-go ~200-500ns |
| 2 | Cache List 全量 | `cache_list_test.go` | KubePivot 5-10x 提升 |
| 3 | Watch 稳态吞吐 | `watch_throughput_test.go` | 持平或微胜 |
| 4 | 启动时间 cold start | `cold_start_test.go` | 持平（瓶颈在 K8s API） |
| 5 | **内存放大率** ⭐ | `memory_amplification_test.go` | KubePivot 1.1-1.3x vs client-go 2-3x |

---

## 运行

### Bench 1-4（标准 go bench）

```bash
cd benchmark/eventstream/

go test -bench=. -benchmem -run=^$ -count=10 -timeout=30m \
    -cpuprofile=cpu.prof -memprofile=mem.prof \
    | tee bench-results-$(date +%Y%m%d_%H%M%S).txt
```

### Bench 5（内存放大率，单独跑）

```bash
go test -run=TestMemoryAmplification -v -timeout=10m \
    | tee memory-results-$(date +%Y%m%d_%H%M%S).txt
```

### 数据归档

```bash
DATE=$(date +%Y%m%d)
mkdir -p ../../docs/design/eventstream-perf/$DATE/
mv bench-results-* memory-results-* cpu.prof mem.prof \
   ../../docs/design/eventstream-perf/$DATE/
```

---

## 公平性约定

1. **同一个 JSON 库**：encoding/json 标准库
   - 不引入 json-iterator / sonic / gjson
   - 性能差异来自"少读字段" + "数据结构"，不是"换库"
2. **零热点**：每个 Bench 重置 cache，验证冷启动
3. **同样的测试数据**：generateComplexDeployment 共享，1w 个复杂对象

---

## 决策门

数据出来后，按 day1-checklist 判定规则决定 v2.7 路径。

数据归档到 `docs/design/eventstream-perf.md`，工程伦理：不藏数据。
