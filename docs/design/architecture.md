# kp 整体架构设计

> 版本：v2.3.0

---

## 定位

kp 是一个 **Go 云原生项目脚手架**，解决两个核心问题：

1. **从零搭建**：`kp init` 生成完整可部署的骨架，含多服务 Helm chart、认证、迁移、监控
2. **持续交付**：`kp deploy` 一键 OPA 检查 + 迁移兼容 + CVE 扫描 + AI 规划 + build + push + helm upgrade + 状态追踪 + 自动自愈

目标用户是 **Go 后端开发者**，不要求熟悉 K8s 运维细节。

---

## 整体架构

```
开发者本机                              K8s 集群
──────────────────────────              ──────────────────────────────────
kp CLI
  ├── kp init                → 生成项目骨架（多 chart，不含 controller）
  ├── kp ai-plan             → 扫描仓库 + LLM 规划 → components.yaml
  │
  ├── kp controller install  → 集群级一次性安装                kubepivot-system ns
  │                               ↓                             ┌──────────────────┐
  │                                                             │ kubepivot-       │
  │                                                             │   controller     │
  │                                                             │  （3 副本 HA）    │
  │                                                             │                  │
  │                                                             │  Leader Election │
  │                                                             │  /kubepivot/     │
  │                                                             │   global/leader  │
  │                                                             │         ↓        │
  │                                                             │ ┌──────────────┐ │
  │                                                             │ │ Namespace    │ │
  │                                                             │ │ Watcher      │ │
  │                                                             │ ├──────────────┤ │
  │                                                             │ │ ConfigMap    │ │
  │                                                             │ │ Watcher      │ │
  │                                                             │ ├──────────────┤ │
  │                                                             │ │ Reconcile    │ │
  │                                                             │ │ Loop (8s)    │ │
  │                                                             │ ├──────────────┤ │
  │                                                             │ │ Worker Pool  │ │
  │                                                             │ │ (20 g)       │ │
  │                                                             │ └──────────────┘ │
  │                                                             └──────────────────┘
  │                                                                      ▲
  ├── kp controller enroll   → label ns + 写 ConfigMap    ──────────────┘
  │                            （触发 ConfigMap Watcher 热加载）
  │
  ├── kp deploy              → OPA 策略检查
  │       ↓                    迁移兼容性检查（oasdiff）
  │       ↓                    CVE 扫描（trivy）
  │       ↓                    AI 规划（可选）
  │       ↓                    DAG 拓扑排序 → 逐层并行 build/push/helm upgrade
  │       ↓ 写状态到 etcd  ──────────────────────→ etcd pod
  │       ↓ 顺带同步 resources.yaml → ConfigMap（触发热加载）
  │       ↓ 状态机（本地/etcd）
  │
  ├── kp status    ← 读状态机 + helm release 信息
  ├── kp diff      ← helm history / drift 检测 / 环境对比
  ├── kp rollback  → helm rollback（整组逆序）
  ├── kp sandbox   → LOCKED→SNAPSHOTTING→SIMULATING→COMMITTING→RUNNING
  ├── kp audit     → 统一审计日志（deploy + secret + drift）
  ├── kp policy    → OPA 策略引擎
  ├── kp chaos     → Chaos Mesh API 混沌注入
  ├── kp doctor    → 环境检查（含 drift 告警）
  └── kp plugin    → 插件市场（~/.kp/plugins/）
```

---

## 核心设计决策

### 1. 状态机是中枢

所有部署操作通过状态机协调，不允许并发部署：

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING

RUNNING → LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
```

**为什么**：helm upgrade 是有状态操作，并发执行会产生 pending-rollback 死锁。状态机强制串行，并提供完整操作历史。

COMMITTING 阶段永远禁止 force-unlock——DB 正在迁移，强制解锁会导致数据不一致。

### 2. state 包零 K8s 依赖

`internal/state` 只负责 FSM 逻辑，不知道如何查询 K8s 资源。K8s 操作全部在 `internal/controller` 和 `cmd/kp` 里通过 kubectl CLI 实现。

**为什么**：零依赖意味着可以独立测试，不需要 K8s 集群。

### 3. kubectl CLI 而不是 client-go

所有 K8s 操作（包括 v2.3.0 新引入的 watch 逻辑）通过 `kubectl` 命令行实现，不引入 `k8s.io/client-go`。

**为什么**：
- kubectl 行为经过充分验证
- client-go 依赖庞大，增加二进制体积和编译时间
- 架构差异化：KubePivot 不是"又一个 K8s controller"，而是"能绕开 K8s 抽象直接操作集群的工具链"
- 50 项目规模下 exec kubectl 峰值仅 ~31 次/s，完全扛得住
- Watcher 接口抽象，未来规模上来可平替 client-go 实现

### 4. 每个服务独立 helm release

不再是一个大 chart 管所有资源，每个服务有独立 release：`{project}-{service}`。

**为什么**：独立回滚不影响其他服务，按依赖顺序部署，出问题容易定位。

### 5. 配置驱动

- `configs/components.yaml`：服务列表、类型、依赖关系、HPA 配置，驱动 planner 和 deploy
- `configs/resources.yaml`：controller 监控的资源，含 force-sync/no-sync-fields/on-missing 策略

### 6. 只保护，不越权

kp 只对自己声明所有权的字段（image/env/ports/resources）执行 force-sync，通过 `--force-conflicts` 解决 SSA 冲突，不干预 Istio/HPA/云厂商注入的字段。

### 7. 降级不阻断

可选组件缺失不阻止核心流程：Trivy 未安装跳过扫描，OPA 未安装跳过策略检查，Prometheus 不可达跳过 error rate 监控，CSI 未安装跳过 PVC 快照，Istio/Nginx 未安装时 Preview 降级生成 README。

### 8. Controller 全局化（v2.3.0）

一个集群，一个 controller——像 kube-proxy、coredns 那样作为集群基础设施存在：

```
v2.2.0（per-project）          v2.3.0（global）
───────────────────           ───────────────────
每项目 ns 3 副本 controller    kubepivot-system 3 副本 controller
资源模型：N × 3                 资源模型：3（不随项目数增长）
接入方式：helm chart           接入方式：ns label + kp enroll
```

**为什么**：
- 资源浪费（10 项目 30 pod → 3 pod）
- 认知负担（每项目都要维护一套 controller chart）
- 语义清晰（`kp deploy` 只管业务，`kp controller install` 管运维基础设施）

**双层接入协议**：
- 内核层：`kubectl label ns <n> kubepivot.io/managed=true`（ground truth）
- 交互层：`kp controller enroll`（自动封装 label + ConfigMap 分发）

**分发协议**：每个 managed namespace 维护一个 ConfigMap `kubepivot-resources`，内含 `sha256` annotation 做热加载去重。

详见 [`docs/design/controller.md`](controller.md)。

---

### 9. 流量层抽象（v2.6.0）

v2.6 引入 **流量层 Provider 抽象**，解决"如何在 GitOps 框架下管理流量切换"的问题。

```
TrafficProvider 接口 (internal/route/provider.go):
  ┌─────────────────────────────────────────────┐
  │  Provider                                   │
  │    Name() / Validate() / GetCurrentRoutes() │
  │    ApplyRoutes() / SetWeight()              │
  └─────────────────────────────────────────────┘
       │
       ├─ IngressProvider     (networking.k8s.io/v1)
       ├─ GatewayAPIProvider  (gateway.networking.k8s.io/v1)
       └─ 未来：LinkerdProvider / IstioProvider
```

**关键设计**：

- 不锁死特定流量后端
- 加新 Provider 不改 reconcile 逻辑
- 路由抽象（`Route` / `Match`）与具体后端解耦
- 自动检测（Gateway API 优先 → Ingress fallback）+ 显式覆盖

**与状态机的协同**：

- 不引入新状态，复用 v2.4.0 Sandbox 状态机
- COMMITTING 阶段内部分两步执行（helm upgrade → 流量切换）
- 失败统一走 RESTORING

**资源所有权标记**（"只保护，不越权"）：

- `metadata.annotations[kubepivot.io/managed-fields]` 声明 KubePivot 管哪些字段
- IngressProvider 只动 `spec.rules[].http.paths[].backend.service.name`
- 完整保留用户字段（`spec.tls` / `IngressClassName` / cert-manager annotation 等）

详见 [流量层设计文档](traffic-layer.md)。

---

## 包结构与职责

```
internal/
├── ai/         LLM 客户端（Grok/Claude/OpenAI/豆包）+ 仓库扫描 + prompt
├── planner/    AI 规划：读 components.yaml → DAG → 拓扑排序 → Plan
│               Component: Name/Image/Type/Deps/Namespace/HPA 字段
├── scaffold/   项目生成：模板复制 + 动态文件 + 多 chart 骨架
├── state/      状态机：FSM + etcd/本地持久化（零 K8s 依赖）
│               13 个状态（含 v1.8.0 Sandbox 链）
│               validTransitions 转换表严格约束
│
├── controller/ Reconciliation Controller（v2.3.0 global 架构）
│   ├── controller.go          Start(args) 分支：per-project / global
│   ├── global.go              StartGlobal + runAsLeader 主循环
│   ├── global_state.go        多项目状态缓存 + sha256 指纹
│   ├── worker_pool.go         固定大小 goroutine 池（默认 20）
│   ├── watcher.go             kubectl --watch + 心跳守卫 + 指数退避
│   ├── namespace_blacklist.go 5 个系统 ns 黑名单 + env 扩展
│   ├── leader.go              etcd 分布式 Leader Election（TTL=15s）
│   ├── workqueue.go           三集合 WorkQueue（queue/dirty/processing）
│   ├── reconciler.go          Reconciler struct（加 project 字段）
│   ├── heal.go                on-missing 全策略 + OOMKilled + CrashLoopBackOff
│   ├── drift_sync.go          30s 漂移扫描 + etcd 审计日志
│   └── sandbox_gc.go          5m Sandbox Session 超期清理
│
└── controller_installer/ （v2.3.0 新增独立包）
    ├── installer.go           Install / Uninstall / Status
    └── templates/             embed.FS：namespace.yaml / rbac.yaml / deployment.yaml

cmd/kp/         CLI 入口（26 个子命令，v2.3.0 新增 kp controller 家族）
examples/
└── policies/   OPA 策略示例（no-latest-tag / require-resource-limits）
```

**依赖方向**（严格单向）：

```
cmd/kp → internal/{ai,planner,state,controller,controller_installer,scaffold}
internal/controller → internal/state（只读状态，不写）
internal/controller → internal/executor（kubectl/helm 路径）
internal/{scaffold,planner,ai,state}（各自独立）
```

---

## Controller 详细设计

### v2.3.0 全局架构

Controller 以 **集群唯一单实例** 的形式运行在 `kubepivot-system` namespace，3 副本 HA。通过双层接入协议（ns label + ConfigMap）watch 所有 managed 项目。

```
StartGlobal(ctx)
  └── Leader Election（/kubepivot/global/leader）
      └── runAsLeader
          ├── Namespace Watcher   label=kubepivot.io/managed=true
          ├── ConfigMap Watcher   name=kubepivot-resources, all-ns
          ├── Reconcile Loop      8s 周期全量对账
          └── Worker Pool         20 goroutine 消费 task channel
```

### Watcher 层（不引入 client-go）

自实现 `Watcher` 接口，基于 `exec kubectl --watch`：

- `exec.CommandContext` 绑进程生命周期（ctx 取消 → SIGKILL，无僵尸）
- `json.NewDecoder` 流式解析 `--output-watch-events=true -o json` 输出
- 30s 无事件 → 心跳守卫主动 `kubectl get` 探活 → 失败强制 cancelStream 重连
- 指数退避 1s → 2s → 4s → 8s → 30s 封顶

接口抽象，未来可平替 client-go informer 实现，架构不破。

### Leader-Dispatch-Worker 模型

```
Leader（Watcher + Reconcile Loop）
       │
       │  非阻塞入队
       ▼
channel（buffer = poolSize × 4）
       │
       │  20 个 worker 并发消费
       ▼
Worker Pool
  ├── 每任务独立 ctx + 90s timeout
  ├── panic recovery（单任务崩溃不影响其他）
  └── Stats: enqueued / done / failed
```

Leader 只做分发不做执行。50 项目同时炸时，Leader 仍然流畅 enqueue，Worker Pool 按池大小串行消费。

### ConfigMap 热加载（sha256 指纹）

```
ConfigMap MODIFIED 事件
  ↓
读 data.resources.yaml → 计算 sha256
  ↓
对比 GlobalState 缓存 sha256
  ├── 相同 → 跳过（幂等 apply 不触发 reconcile）
  └── 不同 → UpsertProject → 立即入队全量 reconcile
```

### 三道 Namespace 黑名单护栏

即使 RBAC 授权，代码层仍然物理拒绝 `kube-system / kube-public / kube-node-lease / kubepivot-system / default`：

```
1. Worker Pool Enqueue()        入队前护栏
2. global.go handleTask()       执行前二次护栏
3. global_state.go UpsertProject() 建立状态前三次护栏
```

可通过 `KUBEPIVOT_EXTRA_PROTECTED_NS=istio-system,monitoring` 运行时扩展。

### Drift Sync Loop

```
每 30s 扫描所有 force-sync=true 的资源
对比 helm chart 期望值 vs live 集群值
硬冲突 → helm upgrade --force-conflicts --history-max=10 强制对齐
no-sync-fields 豁免字段跳过
审计日志写入 etcd /kubepivot/<project>/<ns>/drift/<ts>
```

### Sandbox GC Loop

```
每 5m 扫描 .kp/sandbox/*.json
超过 TTL（默认 3600s）→ 清理 sandbox-id label 资源 + ForceState → IDLE
```

---

## 数据流

### kp deploy（多服务路径）

```
project.env → deployConfig
components.yaml → planner.BuildLayers → []Layer（拓扑分层）
                                            ↓
OPA 策略检查（有 opa 命令才跑）      isMultiService?
迁移兼容性检查（有 oasdiff 才跑）         ↓ yes
镜像安全扫描（有 trivy 才跑）      deployLayers：
                                     同层 goroutine + semaphore 并行
state.New(etcd/local) → Machine      层间串行
    ↓                                build/push（只做一次）
Transition(INIT→DEPLOY→VALIDATE→RUN) helm upgrade --force-conflicts --history-max=10
                                     kubectl rollout status
                                         ↓ 失败
                                     级联 rollback → 整组 rollback
    ↓
autoSyncResourcesIfEnrolled（v2.3.0）
  ├── ns label=managed=true? → 是 → 同步 resources.yaml 到 ConfigMap
  └── 否 → 静默跳过（用户未 enroll）
```

### kp controller enroll（v2.3.0）

```
kp controller enroll
  ├── ensureNamespace（幂等创建）
  ├── kubectl label ns <n> kubepivot.io/managed=true --overwrite
  └── 读 configs/resources.yaml
      ├── 计算 sha256
      └── 写 ConfigMap kubepivot-resources
          ├── labels.kubepivot.io/managed: "true"
          ├── annotations.kubepivot.io/sha256: <hex>
          └── data.resources.yaml: 原始内容
                │
                ▼
          controller ConfigMap Watcher 接收事件
                │
          sha256 变化 → UpsertProject → enqueue 全量 reconcile
```

### kp sandbox（原子性迁移）

```
IDLE/RUNNING → LOCKED（阻止其他 kp deploy）
LOCKED → SNAPSHOTTING（kp pvc backup，有 CSI 才执行）
SNAPSHOTTING → SIMULATING（K8s Job 预跑迁移，降级 dry-run）
SIMULATING → COMMITTING（真实迁移 + kp deploy）
COMMITTING → RUNNING（成功）
任意失败 → RESTORING（pvc restore + helm rollback）→ IDLE
```

---

## 测试策略

| 包 | 测试方式 | 测试数 |
|---|---|---|
| internal/planner | 纯逻辑，直接测 | 32 |
| internal/state | 纯逻辑 + localStore（TempDir）含 Sandbox 转换 | 62 |
| internal/scaffold | 纯逻辑 + 文件操作（TempDir） | 34 |
| internal/controller | Detector/HelmClient mock 注入 + v2.3.0 新增 Watcher/Pool/State 测试 | 57 |
| **合计** | | **185+** |

etcdStore 和 validator.go 依赖外部（etcd/kubectl），不做单元测试。
所有测试通过 `go test ./... -race`。

---

## 真实集群验证

| 版本 | 场景 | 结果 |
|------|------|------|
| v2.2.0（per-project） | 删除 web3-blitz Deployment | < 10 秒自愈 |
| v2.3.0（global） | 删除 feelings-server Deployment（跨 namespace 管理） | ~12 秒自愈 |

---

## 路线图

| 版本 | 主题 | 状态 |
|------|------|------|
| v1.0.0 | 多服务 DAG + A2 Controller + 安全合规基线 | ✅ |
| v1.4.0 | 跨版本迁移（KubePivot 改名） | ✅ |
| v1.5.x | StatefulSet + etcd 健康监控 + 蓝绿 e2e + Secret 轮转 | ✅ |
| v1.6.0 | Controller HA（Leader Election + WorkQueue）+ 可观测性 | ✅ |
| v1.7.0 | 状态漂移治理（终态强权）+ HPA + on-missing 全策略 | ✅ |
| v1.8.0 | Operation Sandbox + Header Preview + Warmup | ✅ |
| v1.9.0 | 多集群联邦 + 企业合规（audit + OPA + Vault）| ✅ |
| v2.0.0 | 插件平台 + self-update + Chaos Mesh | ✅ |
| v2.1.0 | 脚手架适配性 + 扩展性 + GitOps 愿景落地 | ✅ |
| v2.2.0 | 真实集群自愈闭环 + scratch 容器化全量改造 | ✅ |
| v2.3.0 | 全局单一 HA Controller 架构级跃迁 | ✅ |
| v2.4.0 | Leader Election 无 etcd 降级 + etcd 状态恢复 + rbac 模板化 | 🚧 |
