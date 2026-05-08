# TODO — KubePivot v3.3+

> 编写日期：2026-05-08
> Last release: v3.2.0
> Total commits: 537
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

## v3.2 已完成

### KVCache 性能冲刺

```
 Delta buffer — Put O(1) delta write (~120ns), CoW 推迟到 FlushDelta
 merge-on-read — ListAll/ListByNode 非阻塞，delta 超阈异步 flush
 RV CAS — PodEntry/NodeEntry.RV，Put 拒绝旧事件覆盖
 ShardedPodCache — 16 路 power-of-2 namespace hash 分片
 NodeCache zero-copy — nodeSnapshot.list 预缓存
 PoolUtilCache — Generation 计数器，无变化 scan 跳过
 poolFragmentRate 复用 usageMap — 7×O(Np) → 1×O(Np)
 内存放大率 1.13x（v2.7 1.65x → v3.2.1 反降）
 并发 64g Put 160ns（16 shards, +33% vs 单线）
 seenPool + mergeListPool — sync.Pool 复用 merge-on-read 临时 map/slice
 client-go A/B 基准 — 17 项 benchmark, 5 维度, 5000 Pod
 Labels 压缩 — CommonLabels[10] + LabelHash，生产 Pod 368B (-74%)
 内存 vs client-go: 3.7x 省 (1691 B/pod vs 7824 B/pod)
```

### 迁移引擎 + CBA

```
 MigrationManager — Evicting → WaitingForReady → Complete / Failed
 Dual-Path — Stateful 超时 Paused+Retry, Stateless 超时 Failed
 Rehydrate — PodCache 重启时从 Pod annotation 恢复状态
 DryRun 真隔离 — clone map 模式，真实状态不触碰
 CBA — CellClass (Stateless/Stateful) + ClassifyWorkload
 HashRing — 40 vnodes/pod 一致性哈希, 4→3 pod 仅 ~25% 漂移
 Weight-aware HashRing — NewWeightedHashRing(pods, weights, baseVnodes)
 MigrationLabel — kubepivot.io/migration-active 真 K8s label
 MigrationTargetHint — Rescheduler evict 前写 hint, webhook 读
 FencingConfig — FallbackChain + HardTimeout + SignalProtocol
```

### 池化调度层

```
 PoolInfo + computePoolUtilization — 池级利用率 + 碎片率 + PoolScore
 PoolImbalancePair — 池间不平衡检测（CPU/Mem/GPU 三维）
 Generation + PoolUtilCache — 无变化 scan 跳过
 PoolUtilTracker — O(1) 原子计数器，碎片率 23.9x 加速
```

### Controller 接线 + 调度器

```
 Pod Informer → PodCacheBridge → PodCache
 Node Informer → NodeCacheBridge → NodeCache
 InformerDetector: KVCache 优先 → kubectl 兜底
 OOM 自动调优: bumpMemory 25% + patch Deployment
 CrashLoop >=5 次 → 自动 rollback
 kp controller update --apply — P→S→C 三维 sizing 自动回写 system.yaml
 kp self-update — CLI 自更新
```

### Config + 构建

```
 system.yaml 6组31字段全中文注释
 kp controller update --apply 自动回写
 kp sync 同步框架文件
 etcd 12字段: heartbeat/election/corrupt-check/quota/snapshot
 buildx --push 一条命令
 Dockerfile controller/kp 双镜像
```

### 基础设施

```
 碳感知: CarbonIntensityProvider + CarbonSDKClient (15min TTL) + kp scheduler status --carbon-region
 KinK: tools/kink/ FakeGPUCluster + apiserver + Prometheus endpoint
 Route: IngressProvider + GatewayAPIProvider + AutoDetect + kp sandbox start --from-env
```

### 文档

```
 kvcache-perf-analysis.md — 性能分析报告（八节, 含已知局限）
 bench-kp-vs-client-go.md — A/B 基准对比报告
 informer-kv-cache-impl-notes.md — 实施日志 v4（十二节）
 pooling-migration.md — 更新实施路线 + 偏差
 docs/cmd/ — controller / scheduler / sizing 命令文档重写
 docs/reference/ — deployment / eventstream / route 参考文档
 FORGET.md — 全量扫描 94→19 项 P0+P1
 ROADMAP.md + SNAPSHOT.md + HANDOFF.md + TODO.md — v3.2 收尾
```

---

## v3.3 P0 — 上生产前必修

```
[ ] Shard 切换 10s gap — lease handoff 预通知 + jump consistent hash
[ ] Quota 分配不均 (4:4:2) — jump consistent hash 天然均匀
[ ] Webhook TLS 证书 — 生成 /etc/kubepivot/tls.crt + caBundle 注入
[ ] Prometheus HTTP server — 暴露 scheduler 7 + informer 9 指标
[ ] WorkerPool 背压机制 — reconcile 堆积保护
[ ] 自愈死循环保护 — 连续 rollback > N 次暂停
[ ] etcdmanager compact/defrag 接入 config — 走 system.yaml
```

## v3.3 P1 — 规模化前必做

```
[ ] OOM 事件自动接线 — ReportOOM() → controller pod status watcher
[ ] Jitter 阈值生产校准 — jitterThreshold/jitterSpikeCount 需生产数据
[ ] Fencer OOB 隔离确认 — ConfirmIsolated 显式闭环（网络/GPU reset）
[ ] Watch 接线 Phase 4 — RV 自动传递 + KVCache 默认启用
[ ] Pod affinity/anti-affinity 拓扑约束 — ConstraintChecker
[ ] Deployment Pod 迁移 label-based 匹配 — webhook 改名匹配
[ ] 跨 namespace 依赖部署 — planner 已支持 / 分隔，执行层补齐
[ ] Per-service rollback — 项目级 → 单 service 粒度
[ ] Controller chart 一键部署 — 去掉手动 build 镜像门槛
[ ] Etcd emptyDir → PVC — 生产持久化
[ ] GPU fields in resources.yaml — gpu/gpuCount/migProfile
[ ] FNV hash → jump consistent hash — namespace 分片负载均衡
```

---

## 远期（v3.3+ / v4.0）

```
[ ] GPU 3D DP — 等 DCGM 数据流就绪后升级
[ ] GPU sharing MPS/TimeSlicing
[ ] Multi-cluster 支持正式化
[ ] kp explain Layer 3 决策溯源
[ ] 多项目 sizing 批量报告
```

---

## 已废弃 / 不做

```
 单 leader 模式回退
 引入 client-go 作为运行时依赖
 手动分片调优
 kubectl top pod -o json
```

---

## 编辑记录

```
2026-05-08  v3.2 收尾定稿
            - 已实现项归入 v3.2：Labels 压缩 / WeightHashRing / mergeListPool / CarbonSDK / kp controller update
            - v3.3 对齐 FORGET.md P0+P1：P0 7 项 / P1 12 项
            - P2/P3/远期精简，与 FORGET 一致
```
