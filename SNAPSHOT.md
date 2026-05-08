# SNAPSHOT — KubePivot v3.2

> 编写日期：2026-05-08
> Last commit: 294ec8f
> Total commits: 537
> Co-Authored-By: DeepSeek

---

## 一、版本与 commit

```
当前 branch:        Master
当前 commit:        294ec8f
upstream:           https://github.com/Ixecd/KubePivot
total commits:      537
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
| `fc11baf` | perf(scheduler): v3.2 规模化落地 — O(1)池利用率 + 生产模拟器 + EKS pilot |
| `f14c211` | feat(cmd-test-docs): kp bench CLI + 单测补全26个 + 文档补齐11个命令 |
| `e3e5128` | docs(readme): v3.2 乾枢 — AI 可编程的确定性 K8s 操作引擎 |
| `ecd6945` | feat(config-controller): v3.2 系统配置化 + Controller KVCache 接线 |
| `fd5556d` | feat(config-etcd-informer): etcd 12字段全配置 + Watch Bookmark |
| `294ec8f` | fix(controller-build): Node Informer 接线 + buildx 构建重构 + controller 配置回写 |

---

## 二、代码结构

```
KubePivot/
├── internal/
│   ├── eventstream/               v3.2 KVCache 核心
│   │   ├── kv_cache.go            PodCache/NodeCache/ShardedPodCache + delta buffer
│   │   ├── pod_bridge.go          PodCacheBridge + NodeCacheBridge
│   │   ├── cache.go               SkeletonCache (v2.7 已有)
│   │   ├── informer.go            Informer 接口
│   │   └── ...
│   │
│   ├── scheduler/                 v3.0-v3.2 调度层
│   │   ├── scheduler.go           调度器入口
│   │   ├── coordinator.go         双 DP 协同
│   │   ├── bin_pack.go            0-1 背包 DP + FFD
│   │   ├── pool.go                池化 (PoolInfo/PoolUtilCache/PoolUtilTracker)
│   │   ├── rescheduler.go         运行时重调度 + 抖动降级
│   │   ├── migration_manager.go   迁移引擎 (Delta/Dual-Path/Rehydrate)
│   │   ├── cba.go                 CBA (HashRing/Fencing/CellClass)
│   │   ├── informer_adapter.go   PodLister/NodeLister KVCache 适配
│   │   ├── webhook.go             Admission Webhook + MigrationTargetHint
│   │   └── *_test.go              ~2500 行测试
│   │
│   ├── sizing/                    v2.9 2D DP 资源优化
│   │   ├── dp.go                  核心 DP 算法 (80×128 状态空间)
│   │   ├── intelligence.go        智能 Profile 推荐 + 自适应权重
│   │   ├── gpu_policy.go          MIG 分区量化
│   │   └── vpa.go                 VPA YAML 生成
│   │
│   ├── controller/                Controller (v2.7 双保险 + v3.2 KVCache 接线)
│   ├── route/                     v2.6 流量层 Provider 抽象
│   ├── planner/                   拓扑分层 + components.yaml 解析
│   ├── state/                     状态机 (12 部署状态 + 5 沙盒状态)
│   ├── metrics/                   业务指标层
│   ├── supplychain/               供应链安全策略
│   └── ...
│
├── docs/
│   ├── cmd/                       命令文档 (controller/scheduler/sizing + 48个子命令)
│   ├── reference/                 参考文档 (deployment/eventstream/route)
│   ├── design/                    设计文档
│   └── guide/                     用户指南
│
├── configs/
│   ├── system.yaml               6组31字段 Controller 配置
│   └── resources.yaml            自愈资源声明
│
├── commits/                       工程档案
├── archived/                      历史归档 (含 v3.2 三文档)
└── snapshots/                     永久档案
```

---

## 三、测试状态

```
20 包全量 test + race 通过:
  cmd/kp / ai / audit / auth / controller / controller_installer
  etcdmanager / eventstream / executor / metrics / planner / rbac / route
  scheduler / sealed / sharding / sizing / state / supplychain
  test/integration

单测 959 + benchmark 39
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

- [TODO.md](TODO.md) — 路线图 v3.3+
- [HANDOFF.md](HANDOFF.md) — 接手指南
- [ROADMAP.md](ROADMAP.md) — 完整版本路线
- [FORGET.md](FORGET.md) — 未实现/遗忘条目
- [docs/reference/eventstream.md](docs/reference/eventstream.md) — Informer + KVCache 参考
- [docs/reference/deployment.md](docs/reference/deployment.md) — 部署引擎参考
- [docs/reference/route.md](docs/reference/route.md) — 流量层参考
- [docs/cmd/controller/controller.md](docs/cmd/controller/controller.md) — Controller + resources.yaml
- [docs/cmd/scheduler/scheduler.md](docs/cmd/scheduler/scheduler.md) — 乾枢调度器
- [docs/cmd/sizing/sizing.md](docs/cmd/sizing/sizing.md) — 资源优化引擎
- [docs/design/kvcache-perf-analysis.md](docs/design/kvcache-perf-analysis.md) — 性能分析
- [docs/design/bench-kp-vs-client-go.md](docs/design/bench-kp-vs-client-go.md) — A/B 基准

---

## 编辑记录

```
2026-05-08  v3.2 收尾
            归档 v3.2 版到 archived/snapshot/SNAPSHOT-v3.2.md
            537 commits, 294ec8f HEAD, 文档地图更新
2026-05-07  v3.2 刷新 — 523 commits, c4f176c HEAD
```
