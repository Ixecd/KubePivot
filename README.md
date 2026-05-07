# KubePivot (kp) — 乾枢

> AI 时代的云抽象。确定性的 K8s 操作引擎，AI 的可编程接口。

[![Go Version](https://img.shields.io/badge/go-1.25+-blue.svg)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![Tests](https://img.shields.io/badge/tests-959-brightgreen.svg)]()

---

## 哲学

KubePivot 回答一个问题：**如何让 AI 可靠地操作云？**

今天的主流答案是 AIOps——让 AI 直接调 K8s API，边猜边学。结果是不确定的：同样的 prompt，两次执行结果不同。

KubePivot 的答案不同：**把云抽象成一个命令行**。`kp` 是一个确定性系统——状态机、调度器、迁移引擎、Controller 分片自愈——每个操作都有确定的结果。AI 只需要像人一样敲 `kp deploy`、`kp scheduler reschedule`、`kp bench`——KubePivot 保证执行。

```
AI 决定           ← 不确定（概率空间）→  LLM / Agent
KubePivot 执行    ← 确定（状态机）    →  kp CLI
K8s 生效          ← 物理（API Server）→  kubectl
```

CI 归 Git，CD 归 KubePivot。AI 通过操作 `kp` 来控制服务——这是真正的 AIOps。

---

## 全链路

```
kp init → configs/ + Makefile + Dockerfile + Helm chart + deployments/
kp deploy → supply-chain → AI plan → sizing → topology sort → sandbox → state machine → K8s
```

一条命令。项目骨架、多服务编排、蓝绿切换、状态追踪、自愈——全在 `kp deploy` 里。

---

## 确定性引擎

### 状态机

项目从 `Idle → Initializing → Deploying → Validating → Running` 五阶段。蓝绿走 `LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING`。每一步都有确定的下一状态。

### Controller（分片 + 自愈）

多副本 Controller，每 Pod 持有部分 namespace shard。故障 Pod 的 shard ~10s 被其他 Pod 抢占。孤儿 namespace 周期清理。

### KVCache（自研 Informer）

0 client-go。Delta buffer（Put O(1)）、ShardedPodCache（16 路并发写）、merge-on-read（非阻塞读）。内存 3.7x 省于 client-go，放大率 1.13x。

### 调度器

池化视角（PoolInfo + FragmentRate），迁移引擎（Dual-Path stateful/stateless），HashRing 一致性哈希，O(1) 原子计数器（100k Pods 碎片率 75ms, 23.9x）。

---

## 快速开始

```bash
go install github.com/Ixecd/kubepivot/cmd/kp@latest
kp init --name myapp && cd myapp && kp deploy
```

---

## CLI

```
kp init         项目脚手架（12 语言）
kp deploy       全链路部署
kp sandbox      安全部署（五阶段状态机）
kp scheduler    调度器（status / reschedule）
kp bench        性能基准（kvcache / pool / memory / storm / all）
kp status       部署状态
kp rollback     回滚
kp promote      蓝绿切换
kp migrate      DB 迁移
kp secret       Secret 轮转/密封
kp sizing       资源优化
kp controller   全局控制器
kp chaos        混沌工程
kp supply-chain 供应链安全
kp team         RBAC 多团队
kp login        SSO（Google/GitHub/Dex）
...
```

## AI 可编程

所有命令都有确定的行为——AI 不需要理解 K8s，只需要理解 `kp`。

```bash
# AI 调度一个服务
kp deploy --env prod --sizing-mode auto

# AI 查询集群健康
kp scheduler status

# AI 触发重调度
kp scheduler reschedule

# AI 跑基准
kp bench all

# AI 回滚
kp rollback --env prod
```

KubePivot 是 AI 和 K8s 之间的确定性翻译层。AI 不需要学 kubectl、不需要理解 YAML、不需要担心 side effect——KubePivot 保证执行结果的可预测性。

---

## 为什么自研 KVCache 而不是用 client-go

5.7 KB/pod 的绝对差值不值一提——10000 Pod 才差 57MB，不到一张 GPU 显存。

真正的价值不在"省了多少内存"，在"**省了什么**"：

| | client-go v1.Pod | KubePivot PodEntry |
|---|---|---|
| 存什么 | 完整 K8s 对象（managedFields + ownerReferences + full Spec/Status） | 6 个调度字段（Namespace/Name/NodeName/Phase/Labels/Requests） |
| ListAll 5k | 122 KB 分配 + 5000 allocs | **0 分配** |
| GC 影响 | Rescheduler 每 5min 触发 GC 扫描 35MB+/天 | GC 不受调度器影响 |
| 1M Pod | 7.8 GB | 2.1 GB（但架构不退化） |
| 碎片率 100k | O(Np) 全扫，秒级 | **O(1) 计数器 75ms** |
| 放大率 | 4.69x | **1.13x** |

这不是资源节省，是架构正确性。调度器只需要 6 个字段——那就只存 6 个字段。零分配读路径让 GC 完全不受调度器影响，p99 抖动归零。O(1) 计数器让 100k Pod 碎片率计算从秒级降到 75ms。

## 设计原则

**只保护，不越权**：只对自己声明所有权的字段 force-sync，不干预 Istio/HPA/云厂商注入。

**0 外部依赖**：仅 6 个直接依赖。不引入 client-go、cobra、viper、gin。自研 Informer 替代 client-go。

**降级不阻断**：Trivy/OPA/Prometheus/CSI 缺失，核心流程继续。

**CAP AP 优先**：最终一致 + 高可用 + 分区容错。Delta buffer + atomic.Value snapshot。

## 统计

| 指标 | 数值 |
|------|------|
| 单测 | 959 |
| Benchmark | 39 (KVCache 30 + Scheduler 9) |
| 直接依赖 | 6 |
| CLI 命令 | 40+ |

## 文档

| 文档 | 内容 |
|------|------|
| [kvcache-perf-analysis.md](docs/design/kvcache-perf-analysis.md) | KVCache 性能分析（十一节） |
| [informer-kv-cache-impl-notes.md](docs/design/informer-kv-cache-impl-notes.md) | KVCache 实施日志（v5） |
| [bench-kp-vs-client-go.md](docs/design/bench-kp-vs-client-go.md) | client-go A/B 基准报告 |
| [pooling-migration.md](docs/design/pooling-migration.md) | 池化调度 + 迁移引擎设计 |
| [eventstream-draft.md](docs/design/eventstream-draft.md) | 自研 Informer 设计 |
| [gpu-scheduling-draft.md](docs/design/gpu-scheduling-draft.md) | GPU 调度设计 |
| [iam-draft.md](docs/design/iam-draft.md) | IAM 系统设计 |

## License

MIT © 2026 qc（Ixecd）
