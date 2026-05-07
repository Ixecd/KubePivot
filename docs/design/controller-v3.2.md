# KubePivot Controller v3.2 设计与实施文档

> 编写日期：2026-05-08
> 状态：实施文档（基于 v3.2 现状）
> 关联：[controller.md](controller.md) (v2.x) / [sharding.md](sharding.md) / [pooling-migration.md](pooling-migration.md)

---

## 一、架构全景

Controller 是 KubePivot 的常驻进程，运行在 K8s 集群内作为 Deployment。它负责项目状态追踪、资源对账、自愈、分片、调度。

```
┌─────────────────────────────────────────────────────────┐
│                    Controller Pod                       │
│                                                         │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │
│  │ Namespace    │  │ ConfigMap    │  │ Reconcile    │  │
│  │ Watcher      │  │ Watcher      │  │ Loop (8s)    │  │
│  │ (kubectl)    │  │ (kubectl)    │  │              │  │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘  │
│         │                 │                 │           │
│         └─────────┬───────┘                 │           │
│                   ▼                         ▼           │
│  ┌──────────────────────────────────────────────────┐   │
│  │              GlobalState                         │   │
│  │   map[namespace] → []Resource (内存项目目录)      │   │
│  └──────────────────┬───────────────────────────────┘   │
│                     │                                    │
│                     ▼                                    │
│  ┌──────────────────────────────────────────────────┐   │
│  │           WorkerPool ×20                         │   │
│  │   handleTask → checkAndHeal → helm install       │   │
│  └──────────────────────────────────────────────────┘   │
│                                                         │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │
│  │ InformerPool│  │ Scheduler    │  │ Rescheduler   │  │
│  │(deployments)│  │(kubectlAdapt)│  │(kubectlAdapt) │  │
│  └──────────────┘  └──────────────┘  └──────────────┘  │
│                                                         │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │
│  │ Webhook      │  │ OrphanSweeper│  │ VerifiedTrfc │  │
│  │(:443)        │  │ (30s)        │  │ Writer       │  │
│  └──────────────┘  └──────────────┘  └──────────────┘  │
└─────────────────────────────────────────────────────────┘
```

## 二、核心组件

### 2.1 GlobalState（global_state.go）

内存项目目录。`map[string][]Resource`，key = namespace。

- `UpsertProject(ns, resourcesYAML)` — 解析并更新/新增项目
- `GetProject(ns)` — 获取项目资源列表
- `RemoveProject(ns)` — 删除项目
- `RemoveOrphanProjects(filter)` — 清理不再属于本 shard 的项目
- `GetOrCreateMachine(ns, version)` — 获取或创建状态机（带锁）
- `ListProjects()` — 列出所有项目 namespace

线程安全：RWMutex 保护。

### 2.2 WorkerPool（worker_pool.go）

固定大小（20 workers）的 goroutine pool。
- `Enqueue(task)` — 投递 reconcile 任务
- 每个 task 绑定项目 namespace、资源 Kind/Name、触发原因
- 无背压机制（队列满时丢弃，v3.2 已知局限）

### 2.3 Watchers（global.go）

两个 kubectl 驱动的 Watcher：

**Namespace Watcher**：监听 `kubepivot.io/managed=true` label 的 namespace。
- ADDED/MODIFIED → `loadResourcesConfigMap` 读取 `kubepivot-resources` ConfigMap
- DELETED → `RemoveProject`

**ConfigMap Watcher**：监听 `kubepivot-resources` ConfigMap 变化。
- ADDED/MODIFIED → `UpsertProject` + `enqueueProjectResources` 触发全量 reconcile
- DELETED → `RemoveProject`

两者都做 shard 过滤（`OwnsNamespace`），只处理本 Pod 持有的 shard。

### 2.4 Reconcile Loop（global.go）

8 秒固定间隔的周期性对账循环。
- 遍历 `ListProjects()` 的所有 namespace
- 每个 namespace 调用 `enqueueProjectResources` 投递到 WorkerPool
- 单层 shard 过滤（入队时），不二次检测
- 幂等设计：多 Pod 重复 reconcile 无害

### 2.5 handleTask（global.go）

每个 reconcile task 的处理逻辑：
1. 跳过受保护 namespace（kube-system 等）
2. `DetectResourceExists` 检查资源是否存在于 K8s
3. 存在 → return nil（正常）
4. 不存在 → 自愈：从 GlobalState 拿状态机 → helm install/rollback
5. v2.7 Step 2b-1：用 InformerDetector（cache hit）+ KubectlDetector（fallback）

**已知问题**：每次 handleTask 新建 `kubectlDetector` 和 `InformerDetector`，应改为复用。

### 2.6 Sharding（global.go + sharding 包）

- 10 个 shard，每 Pod 通过 Lease API 抢占
- `OnShardChanged` 回调：失分片 → 清理孤儿项目（5s grace） / 得分片 → 主动同步已存在项目
- `OrphanSweeper` 30s 兜底
- Leader Election：etcd 优先 → K8s Lease API → 降级单机

### 2.7 InformerPool（informer_pool.go）

管理 `eventstream.Informer` 实例。
- 当前只启动了 `deployments` informer（`apps/v1`）
- InformerDetector 用于资源存在性快速检测（cache hit → return true，miss → kubectl fallback）
- Prometheus 指标注册已实现（`RegisterMetrics`）但未调用（缺 HTTP server）
- 可注入 `newInformerFunc` 函数变量（测试用）

### 2.8 自愈（heal.go）

`checkAndHeal(res)`：
1. `loadResourceLabels` — 从 kubectl 获取资源当前标签
2. 类型路由（Deployment/StatefulSet/Service/ConfigMap 等）
3. 检测资源缺失 → `helm install` 或 `helm rollback`
4. 检测标签漂移 → `kubectl patch`

v2.7 优化：`loadResourceLabels` 优先走 InformerDetector cache（LabelGetter 接口），miss 走 kubectl。

### 2.9 Scheduler + Rescheduler（global.go）

- 使用 `kubectlAdapter` 获取 Pod/Node 列表（**不是 PodCache**）
- Rescheduler 每 5min 扫描集群利用率，检测不平衡并迁移
- Webhook 部署在 Admission Controller（:443），Pod 创建时实时分配 nodeSelector

### 2.10 etcd Learner Sidecar（etcdmanager）

Controller 以 StatefulSet 部署，每个 Pod 携带 etcd Learner sidecar：
- **Bootstrap**：pod-0 冷启动 → 其他 Pod 以 Learner 加入 → Learner 追平数据 → MemberPromote 转 Voting
- **TLS**：per-node 证书（client/server/peer），CA 自主签发
- **Compact**：每小时 `etcdctl compact <revision>`（compactLoop）
- **Defrag**：Follower 轮询制——Leader 跳过 / Follower 执行 → 转移 Leader → 下轮覆盖 / 集群健康预检（节点不全则跳过） / Pod 序号分布防全集群同时阻塞（24h 周期，各 Pod 不同 UTC 时段）
- **优雅退出**：Learner 降级 + MemberRemove + 非 Leader 优先退

### 2.11 其他组件

| 组件 | 文件 | 作用 |
|------|------|------|
| Leader Election | leader.go / lease.go | etcd lease / K8s Lease API / 降级单机 |
| Drift Sync | drift_sync.go | 检测 ConfigMap 漂移，自动同步 |
| Sandbox GC | sandbox_gc.go | 清理过期 Sandbox |
| VerifiedTrafficWriter | verified_traffic_writer.go | 周期写入 verified-traffic ConfigMap |
| RBAC Hook | rbac_hook.go | Controller 启动时校验 RBAC 权限 |

## 三、当前状态

### 3.1 已实现

- ✅ GlobalState + WorkerPool 并发 reconcile
- ✅ Namespace/ConfigMap kubectl Watcher
- ✅ 8s reconcile loop + 分片过滤
- ✅ 自愈（helm install/rollback + kubectl patch）
- ✅ Sharding（MultiLeaseManager, 10 shards）
- ✅ OrphanSweeper 30s 兜底
- ✅ InformerPool（deployments informer + InformerDetector fast path）
- ✅ Leader Election（etcd / K8s Lease / 降级）
- ✅ Scheduler + Rescheduler + Webhook（kubectlAdapter）
- ✅ VerifiedTrafficWriter / DriftSync / SandboxGC

### 3.2 已实现但未激活

- ⚠️ PodCache（PodCacheBridge 代码就绪，Controller 未接线）
- ⚠️ Prometheus 指标（RegisterMetrics 就绪，HTTP server 未实施）
- ⚠️ Pod Informer（InformerPool 只启动 deployments，未启动 pods）

### 3.3 已知局限

| # | 局限 | 影响 | 计划 |
|---|------|------|------|
| 1 | Scheduler 用 kubectlAdapter，非 PodCache | 每次 scan 走 kubectl fork 100ms+。KVCache 性能工作未生效 | v3.2 接线 |
| 2 | 无 Pod Informer | PodCacheBridge 从未激活，RV 自动填充从未触发 | v3.2 接线 |
| 3 | handleTask 每次新建 detector | 不必要的 alloc | v3.2 |
| 4 | Reconcile 固定 8s | 多 namespace 下无效扫描 | v3.3 |
| 5 | WorkerPool 无背压 | 队列满时静默丢弃 | v3.3 |
| 6 | 无 Prometheus 指标 | 可观测性为零 | v3.3 |
| 7 | 无自适应间隔 | 空闲时也 8s tick | v3.3 |

## 四、v3.2 接线计划

核心目标：让 KVCache 性能工作在 Controller 端生效。

### 4.1 P0：Scheduler 切换 InformerAdapter

```
现状: Scheduler(kubectlAdapter, kubectlAdapter, ...)
      → kubectl get pods -A (100ms+)

目标: Scheduler(InformerAdapter(podCache, nodeCache), ...)
      → podCache.ListAll() (1.6μs)
```

### 4.2 P0：启动 Pod Informer + PodCacheBridge

```
informerPool.Start(ctx, "pods", "v1")
podBridge := eventstream.NewPodCacheBridge(podInformer, podCache)
// PodCache 自动填充，RV 自动，调度器读缓存
```

### 4.3 P1：Detector 复用

```
// 在 StartGlobal 中创建一次
detector := NewInformerDetector(informerPool, NewKubectlDetector(kubeconfig))
// 传入 handleTask 闭包
```

---

## 五、编辑记录

```
2026-05-08  创建
            基于 v3.2 Master 代码全量 review
            31 文件，959 单测，完整架构拆解
```
