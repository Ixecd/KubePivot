# SNAPSHOT — KubePivot v3.2

> 编写日期：2026-05-07
> Last commit: c4f176c
> Total commits: 523
> Co-Authored-By: DeepSeek

---

## 一、版本与 commit

```
当前 branch:        Master
当前 commit:        c4f176c
upstream:           https://github.com/Ixecd/KubePivot
total commits:      523
```

**v3.2 核心 commit 链**：

| Commit | 内容 |
|--------|------|
| `4c41b9b` | feat(eventstream): Informer KV Cache — PodCache + NodeCache |
| `ec744a4` | feat(scheduler): 池化调度层 — PoolInfo + 碎片率 |
| `def2b55` | feat(scheduler): 迁移引擎+CBA — 8项修复 + HashRing + target node |
| `0645e4d` | docs(design): client-go A/B benchmark report |
| `bd3dcef` | perf(eventstream): Delta buffer + Sharded + merge-on-read + RV CAS |
| `c4f176c` | perf(eventstream): 内存放大率 1.13x + 并发压测 + Labels 黑洞 |

---

## 二、代码结构

```
KubePivot/
├── internal/
│   ├── eventstream/               v3.2 KVCache 核心 (~1100 行)
│   │   ├── kv_cache.go            PodCache/NodeCache/ShardedPodCache + delta buffer
│   │   ├── cache.go               SkeletonCache (v2.7 已有)
│   │   ├── informer.go            Informer 接口
│   │   └── ...
│   │
│   ├── scheduler/                 v3.2 调度层 (~3500 行)
│   │   ├── scheduler.go           调度器入口
│   │   ├── pool.go                池化 (PoolInfo/PoolUtilCache/fragmentRate)
│   │   ├── rescheduler.go         运行时重调度
│   │   ├── migration_manager.go   迁移引擎 (Delta/Dual-Path/Rehydrate)
│   │   ├── cba.go                 CBA (HashRing/Fencing/CellClass)
│   │   ├── informer_adapter.go   PodLister/NodeLister 适配
│   │   ├── webhook.go             Admission Webhook + MigrationTargetHint
│   │   └── *_test.go              ~2000 行测试
│   │
│   ├── metrics/                   v2.7 已有 (~1119 行)
│   ├── controller/                Controller (v2.7 双保险)
│   ├── route/                     v2.6 流量层
│   └── ...
│
├── docs/design/
│   ├── kvcache-perf-analysis.md       ← v3.2 新增 (八节，含已知局限)
│   ├── bench-kp-vs-client-go.md       ← v3.2 新增 (A/B 对比报告)
│   ├── informer-kv-cache-impl-notes.md ← v3.2 重写 (v4, 十二节)
│   ├── pooling-migration.md           ← v3.2 更新 (实施路线 + 偏差)
│   └── bench-client-go-ab.md          ← v3.2 新增 (benchmark 设计)
│
├── commits/                       工程档案 (v3.2 新增 ~5 个)
├── benchmark/
│   └── README.md                   ← v3.2 新增 (Real YAML 工具链 + Prune Gate)
├── archived/                      历史归档 (含 v3.2 三文档)
└── snapshots/                     永久档案
```

---

## 三、测试状态

```
19 包全量 test + race 通过:
  cmd/kp / ai / audit / auth / controller / controller_installer
  etcdmanager / eventstream / metrics / planner / rbac / route
  scheduler / sealed / sharding / sizing / state / supplychain
  test/integration
```

---

## 四、性能数据 (v3.2.1, Apple M4, 5000 Pod)

### vs v3.2 初版

| Benchmark | v3.2 初版 | v3.2.1 | 改善 |
|-----------|----------|--------|------|
| Put 单条 | 387 μs | 333 ns | 1160x |
| Put 跨节点 | 211 μs | 168 ns | 1255x |
| Delete | 384 μs | 387 ns | 992x |
| Concurrent 64g Put | — | 160 ns | — |
| Get | 24 ns | 25 ns | ~same |
| ListByNode | 204 ns | 147 ns | 1.4x |

### vs client-go

| 维度 | KP v3.2.1 | client-go v0.34 | KP 优势 |
|------|----------|----------------|---------|
| Get | 25 ns, 0B | 39 ns, 27B | 1.6x |
| Put 单条 | 333 ns | 2.4 μs | 7.2x |
| Memory 5k | 1691 B/pod | 7824 B/pod | 3.7x |
| 放大率 | 1.13x | 4.69x | 4.1x |
| 10k Pod 堆 | 15.85 MB | 76 MB | 4.8x |

---

## 五、关联文档

- [TODO.md](TODO.md) — 路线图 v3.3
- [HANDOFF.md](HANDOFF.md) — 接手指南
- [docs/design/kvcache-perf-analysis.md](docs/design/kvcache-perf-analysis.md) — 性能分析
- [docs/design/bench-kp-vs-client-go.md](docs/design/bench-kp-vs-client-go.md) — A/B 基准
- [docs/design/informer-kv-cache-impl-notes.md](docs/design/informer-kv-cache-impl-notes.md) — 实施日志
- [docs/design/pooling-migration.md](docs/design/pooling-migration.md) — 池化+迁移
- [FORGET.md](FORGET.md) — 未实现/遗忘条目

---

## 编辑记录

```
2026-05-07  v3.2 刷新
            归档 v3.2 版到 archived/snapshot/SNAPSHOT-v3.2.md
            全量重写：523 commits, c4f176c HEAD
```
