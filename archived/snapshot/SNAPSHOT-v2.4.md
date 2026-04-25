# SNAPSHOT — KubePivot

> 当前版本：**v2.4.0** ✅
> 上次更新：2026-04-25
> 上一版本归档：[v2.3.0 全局架构换血](snapshots/SNAPSHOT-kubepivot-2026-04-24-v2.3.0.md)
> 本版本归档：[v2.4.0 稳固版](snapshots/SNAPSHOT-kubepivot-2026-04-25-v2.4.0.md)

---

## 当前状态

```
✅ 全局单一 HA Controller（v2.3.0 架构跃迁）
✅ K8s Lease 选举（v2.4.0，21.7ms 故障转移）
✅ 状态机缓存（v2.4.0，集群总 CPU -55%）
✅ Dockerfile 容错（v2.4.0，可重现 build）
✅ 蓝绿 helm-release 显式声明（v2.4.0）
✅ 性能基准首版（v2.3.0/v2.4.0 对比数据已落地）
✅ TODO 路线图 v2.4 → v2.7（清晰）
```

---

## 版本线

```
v1.0.0  多服务 DAG + A2 Controller + 安全合规基线
v1.4.0  跨版本迁移（KubePivot 改名）
v1.5.x  StatefulSet + etcd 健康监控 + 蓝绿 e2e
v1.6.0  Controller HA（Leader Election + WorkQueue）
v1.7.0  状态漂移治理 + HPA + on-missing 全策略
v1.8.0  Operation Sandbox + Header Preview + Warmup
v1.9.0  多集群联邦 + 企业合规（audit + OPA + Vault）
v2.0.0  插件平台 + Chaos Mesh + GitOps Manifesto（commit #329）
v2.1.0  脚手架适配性 + 扩展性
v2.2.0  真实集群自愈闭环 + scratch 容器化
v2.3.0  🌐 全局单一 HA Controller 架构级跃迁
v2.4.0  ⚙  稳固边缘 + 性能基线（消除冗余 -55% CPU）   ← 你在这
v2.5.0  🚀 A 演进 + B 调研（待开始）
v2.6.0  🌊 流量层落地（Lars 思路）
v2.7.0  ✨ 自研 Handwritten Informer
v3.0.0  🌟 KubePivot 完整体（推迟到 A+B 都成熟）
```

---

## 核心成就（v2.4.0 角度看）

```
✅ Controller 架构纯粹性
   全程不引入 client-go，全部走 kubectl exec
   架构差异化：KubePivot 不是"又一个 K8s controller"，是"绕开 K8s 抽象的工具链"

✅ 全局接入协议（双层）
   内核层：namespace label kubepivot.io/managed=true
   交互层：kp controller enroll → 自动 label + ConfigMap 分发
   分发：每个 ns 一个 kubepivot-resources ConfigMap（含 sha256 annotation）

✅ Leader-Dispatch-Worker 并发模型
   Leader 只做分发（非阻塞 enqueue）
   Worker Pool 20 goroutine 消费
   v2.4.0 加了 K8s Lease 选举，3 副本真 HA（之前是 3 leader 冗余）

✅ 状态机缓存 + 并发安全
   GlobalState 持有 map[ns]*machineEntry（machine + per-instance mutex）
   reconcile task 复用，不再每次 state.New
   集群总 CPU -55%

✅ 双层接入协议向后兼容
   resources.yaml 加可选 helm-release 字段（蓝绿 / 金丝雀场景）
   不声明则保持 v2.3.0 推断行为

✅ 性能基线 + benchmark 工具链
   benchmark/scripts/{setup,steady-state}.sh
   docker stats + docker top 实时采样
   v2.3.0/v2.4.0 数据已落入 docs/design/performance.md

✅ 可重现 build
   Dockerfile ARG KUBECTL_VERSION=v1.32.0（默认）
   --build-arg 可覆盖；网络抖动时 fallback + retry
```

---

## 核心组件（v2.4.0 时态）

```
internal/controller/
├── lease.go                    K8s Lease 选举（v2.4.0 新增）
├── leader.go                   etcd 分布式 Leader（v2.2.0）
├── global.go                   StartGlobal + runAsLeader 主循环
├── global_state.go             多项目内存状态 + 状态机缓存（v2.4.0 扩展）
├── worker_pool.go              固定大小 goroutine 池
├── watcher.go                  KubectlWatcher（kubectl --watch）
├── namespace_blacklist.go      5 个系统 ns 黑名单
├── controller.go               Start(args) 分支
├── reconciler.go               Reconciler struct
├── heal.go                     healRecreate / healRollback
├── drift_sync.go               30s 漂移扫描（v2.4.0 改造 helm-release）
├── resources.go                Resource struct（v2.4.0 +HelmRelease）
└── ...

internal/controller_installer/   独立包（v2.3.0 起）
├── installer.go
└── templates/
    ├── namespace.yaml
    ├── rbac.yaml
    └── deployment.yaml

cmd/kp/
├── controller.go                kp controller 子命令分发
└── controller_enroll.go         enroll / unenroll / projects

benchmark/                       性能测试（v2.4.0 落地）
├── scripts/
├── manifests/
└── results/  (.gitignore)
```

---

## 性能数据（10 项目稳态）

| 指标 | v2.3.0 | v2.4.0 | 变化 |
|------|--------|--------|------|
| 集群总 CPU | 41.88% | 18.92% | **-55%** |
| avg memory / pod | 59 MiB | 36.62 MiB | -38% |
| peak CPU | 89.95% | 54.25% | -40% |
| Leader 故障转移延迟 | N/A | 21.7 ms | ✅ |

详见 `docs/design/performance.md`。

---

## 下一步：v2.5.0

读 [TODO.md](TODO.md) 的 v2.5.0 章节。简版：

```
P0  Controller 分片（A.1 — Lars 思路）
P0  Backoff 队列（A.1.5 — Lars 过载队列 + Probe）
P0  client-go 对比基准（A.2 — 给 v2.7.0 决策提供数据）
P1  流量层调研（B.1 — 设计文档不写代码）
+   v2.4.0 P2 性能基准补全（自愈延迟 / Watcher 鲁棒 / sha256 验证）
```

---

## 项目边界

**做**：应用层（脚手架 / 部署 / 自愈 / 流量层）
**不做**：基础设施层（集群生命周期 / NodePool / 裸金属——见未来 Cloud 项目）

详见 [README.md](README.md) "项目边界（Out of Scope）"节。

---

## 开发哲学

```
1. 设计先对齐，再动手
2. 小步快跑，每步 make dev
3. 不搞技术债
4. 真实集群验证不可跳过
5. 不为做而做（"经核查不需要"也是工程产出）
6. 只保护，不越权
```
