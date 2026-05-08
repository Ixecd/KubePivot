# TODO — KubePivot v3.2+

> 编写日期：2026-05-07
> Last release: v3.2.0
> Total commits: 523
> Co-Authored-By: DeepSeek

---

## 版本号约定

```
v{major}.{minor}

major  架构变更（v2 → v3）
minor  新功能落地（v3.1 → v3.2）
不打 patch 版本

发布节奏：tag → 实现 → tag（详见 commits/README.md）
```

---

## ✅ v3.2 已完成

### KVCache 性能冲刺

```
✅ Delta buffer — Put O(1) delta write (~120ns), CoW 推迟到 FlushDelta
✅ merge-on-read — ListAll/ListByNode 非阻塞，delta 超阈异步 flush
✅ RV CAS — PodEntry/NodeEntry.RV，Put 拒绝旧事件覆盖
✅ ShardedPodCache — 16 路 power-of-2 namespace hash 分片
✅ NodeCache zero-copy — nodeSnapshot.list 预缓存
✅ PoolUtilCache — Generation 计数器，无变化 scan 跳过
✅ poolFragmentRate 复用 usageMap — 7×O(Np) → 1×O(Np)
✅ 内存放大率 1.13x（v2.7 1.65x → v3.2.1 反降）
✅ 并发 64g Put 160ns（16 shards, +33% vs 单线）
✅ seenPool — sync.Pool 复用 merge-on-read 临时 map
✅ client-go A/B 基准 — 17 项 benchmark, 5 维度, 5000 Pod
✅ Labels 黑洞分析 — Labels map 占 98% 分配 (1400B/1432B)
✅ 内存 vs client-go: 3.7x 省 (1691 B/pod vs 7824 B/pod)
```

### 迁移引擎 + CBA

```
✅ MigrationManager — Evicting → WaitingForReady → Complete / Failed
✅ Dual-Path — Stateful 超时 Paused+Retry, Stateless 超时 Failed
✅ Rehydrate — PodCache 重启时从 Pod annotation 恢复状态
✅ DryRun 真隔离 — clone map 模式，真实状态不触碰
✅ CBA — CellClass (Stateless/Stateful) + ClassifyWorkload
✅ HashRing — 40 vnodes/pod 一致性哈希, 4→3 pod 仅 ~25% 漂移
✅ MigrationLabel — kubepivot.io/migration-active 真 K8s label
✅ MigrationTargetHint — Rescheduler evict 前写 hint, webhook 读
✅ FencingConfig — FallbackChain + HardTimeout + SignalProtocol
```

### 池化调度层

```
✅ PoolInfo + computePoolUtilization — 池级利用率 + 碎片率 + PoolScore
✅ PoolImbalancePair — 池间不平衡检测（CPU/Mem/GPU 三维）
✅ Generation + PoolUtilCache — 无变化 scan 跳过
```

### 文档

```
✅ kvcache-perf-analysis.md — 性能分析报告（八节, 含已知局限）
✅ bench-kp-vs-client-go.md — A/B 基准对比报告
✅ informer-kv-cache-impl-notes.md — 实施日志 v4（十二节）
✅ pooling-migration.md — 更新实施路线 + 偏差
✅ FORGET.md — v3.2 状态更新 + v3.3 补齐
```

---

## ⏳ v3.3 进行中

### P0 — 生命线

```
[ ] Watch 接线 (Phase 2/4) — PodCache 自动填充, RV 自动传递
    当前 PodCache 只通过 Put/PutBulk 手动填充
    依赖: Informer Resource → PodEntry 转换链路

[ ] Fencer 接口 (OOB 隔离确认)
    Stateful Cell 迁移需要 Fencer.ConfirmIsolated
    gRPC stub + etcd Learner sidecar
```

### P1 — 性能

```
[ ] Real YAML benchmark — kp bench dump 从真实集群拉 fixture
[ ] Delta storm p99 监控 — pprof merge-on-read >5% → 阈值降到 100
[ ] Cold→Warm e2e — 真实 Watch sync 模拟 + p99 fallback hit <1%
```

### P2 — 增强

```
[ ] Weight-aware HashRing — GPU 池按迁移代价加权 vnodes
[ ] GPU 迁移可行性校验 — NVIDIA_DRIVER_VERSION / CUDA_CAPABILITY
[ ] 池定义自动聚类 — resources.requests profile hash
[ ] Deployment Pod 迁移 target label-based 匹配
[ ] ListAll merge slice sync.Pool
```

### P3 — 后补

```
[ ] Ristretto / sharded cache — Hot/Warm 分层
[ ] buf_pool slab allocator — Go GC >10% CPU 时启用
[ ] Chaos 测试 — litmus/chaos-monkey
```

---

## 📋 远期（v3.3+ / v4.0）

```
[ ] Enterprise Governance (v2.8 遗留) — SSO / RBAC / 加密 / 签名
[ ] Resource Sizing Engine (v2.9 遗留) — 二维 DP
[ ] Intelligent Scheduling System (v3.0 范围) — 维度 A+B 协同
[ ] Multi-cluster 支持正式化
[ ] kp explain Layer 3 决策溯源
[ ] 多项目 sizing 批量报告
```

---

## ✗ 已废弃 / 不做

```
✗ 单 leader 模式回退
✗ 引入 client-go 作为运行时依赖
✗ 手动分片调优
✗ kubectl top pod -o json
```

---

## 编辑记录

```
2026-05-07  v3.2 刷新
            - 归档 v3.2 版到 archived/todo/TODO-v3.2.md
            - 全量重写：反映 v3.2 KVCache 性能冲刺 + 迁移引擎 + CBA 完成内容
            - v3.3 四优先级排列 (P0-Fencer/Watch, P1-Real YAML, P2-GPU, P3-buf_pool)
```
