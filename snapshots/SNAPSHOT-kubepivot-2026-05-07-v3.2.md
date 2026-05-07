# KubePivot v3.2 — 调度系统基础设施交付

> 编写日期：2026-05-07
> 分支：Master
> HEAD: c4f176c
> Total commits: 523
> Co-Authored-By: DeepSeek

---

## 背景

v3.2 是 KubePivot 从"工程"到"智能调度系统"的关键里程碑。
v2.7 奠定了 Event Stream Infrastructure（自研 Informer + SkeletonCache）。
v3.0-3.1 补齐了调度器骨架 + GPU 感知 + Rescheduler。
v3.2 完成三件大事：

1. **池化调度层**：PoolInfo + 池级聚合 + 碎片率 + 不平衡检测
2. **迁移引擎 + CBA**：MigrationManager + Cell-based Architecture + Fencing 协议
3. **KVCache 性能冲刺**：Delta buffer + Sharded + merge-on-read + RV CAS

---

## 核心交付

### KVCache 性能

```
Put:        387μs → 120ns (1160x)     Delta buffer, O(1) write
Delete:     384μs → 387ns (992x)      Delta tombstone
Concurrent: 64g × 16 shards = 160ns   ShardedPodCache
放大率:     1.13x                     v2.7 1.65x → v3.2.1 反降
内存:       1691 B/pod                client-go 7824 B/pod (3.7x)
```

### 迁移引擎

```
MigrationManager: Evicting→WaitingForReady→Complete/Failed/Paused
Dual-Path: Stateful Paused+Retry, Stateless Failed
Rehydrate: 重启恢复
DryRun: clone map 隔离
```

### CBA

```
HashRing: 40 vnodes/pod, 4→3 pod 仅 25% 漂移
Fencing: FallbackChain + HardTimeout + SignalProtocol
MigrationTargetHint: webhook 路由防回弹
```

### 池化

```
PoolInfo + fragmentRate (7×O(Np)→1×) + PoolUtilCache (无变化跳过)
```

---

## 两轮审查

Round 1: MigrationLabel / Dual-Path / SignalProtocol fallback
Round 2: HashRing / Rehydrate / DryRun / time.Parse / HardTimeout
Round 3: Delta buffer / Sharded / RV CAS / merge-on-read / ready gate / seenPool

---

## 测试

19 包全量 test + race 通过。
7 项 KVCache benchmark + 内存 breakdown + 并发压测。

---

## 关联

- [FORGET.md](../FORGET.md) — v3.3 待办
- [TODO.md](../TODO.md) — 路线图
- [docs/design/kvcache-perf-analysis.md](../docs/design/kvcache-perf-analysis.md) — 性能分析
- [docs/design/informer-kv-cache-impl-notes.md](../docs/design/informer-kv-cache-impl-notes.md) — 实施日志
- [docs/design/bench-kp-vs-client-go.md](../docs/design/bench-kp-vs-client-go.md) — A/B 基准
- [docs/design/pooling-migration.md](../docs/design/pooling-migration.md) — 池化+迁移
