# KubePivot Controller 设计文档

> 适用：KubePivot v2.3.0+（global 架构）
> 前代架构（v2.2.0 per-project）见文末附录

---

## 一、设计动机

### v2.2.0 per-project 架构的问题

| 问题 | 表现 |
|------|------|
| 资源浪费 | 每个项目都部署 3 副本 controller，10 项目 = 30 pod |
| 认知负担 | 每个项目 components.yaml 里都有 controller 段，用户要维护 |
| 镜像管理 | controller chart 在每个项目的 `deployments/*/kubepivot-controller/` |
| 语义混乱 | `kp deploy` 既部署业务又部署运维组件 |

### v2.3.0 global 架构目标

**一个集群，一个 controller**——像 kube-proxy、coredns 那样作为集群基础设施存在，不依附于任何具体项目。

```
v2.2.0（per-project）          v2.3.0（global）
───────────────────           ───────────────────
每项目 ns 3 副本 controller    kubepivot-system 3 副本 controller
资源模型：N × 3                 资源模型：3（不随项目数增长）
接入方式：helm chart           接入方式：ns label + kp enroll
```

---

## 二、架构总览

```
                             ┌─────────────────────────────┐
                             │      kubepivot-system       │
                             │                             │
                             │  ┌────────────────────────┐ │
                             │  │ kubepivot-controller   │ │
                             │  │    3 副本 HA           │ │
                             │  └────────────────────────┘ │
                             │            │                │
                             │   Leader Election           │
                             │   /kubepivot/global/leader  │
                             └────────────┬────────────────┘
                                          │
                 ┌────────────────────────┼────────────────────────┐
                 │                        │                        │
          watch namespace           watch configmap          reconcile loop
    label=managed=true       label=managed=true, all-ns         8s tick
                 │                        │                        │
                 ▼                        ▼                        ▼
                              ┌─────────────────────────┐
                              │      Worker Pool        │
                              │    20 goroutine (可调)   │
                              └────────────┬────────────┘
                                           │ handleTask
                                           ▼
                                  检测资源是否存在
                                   ├── 存在 → 跳过
                                   └── 缺失 → healRecreate
                                              healRollback
                                              healScaleDown

           ┌──────────────────────────────────────────────────┐
           │                                                  │
           ▼                                                  ▼
┌─────────────────────┐                         ┌─────────────────────┐
│  project-a ns       │                         │  project-b ns       │
│  label: managed=true│                         │  label: managed=true│
│                     │                         │                     │
│  ConfigMap          │                         │  ConfigMap          │
│  kubepivot-resources│                         │  kubepivot-resources│
│  ├─ sha256 (annot.) │                         │  ├─ sha256 (annot.) │
│  └─ resources.yaml  │                         │  └─ resources.yaml  │
│                     │                         │                     │
│  业务 Deployment    │                         │  业务 Deployment    │
│  业务 Service       │                         │  业务 Service       │
└─────────────────────┘                         └─────────────────────┘
```

---

## 三、接入协议（双层）

### 3.1 内核层：namespace label

```
kubectl label ns <n> kubepivot.io/managed=true
```

这是 **ground truth**。Controller 通过 `-l kubepivot.io/managed=true` selector watch 所有 managed namespace。用户可以绕过 `kp controller enroll` 直接手工打 label，对 controller 而言没有区别。

### 3.2 交互层：kp controller enroll

封装三步操作，消除"步骤遗漏"的人为失误：

```
kp controller enroll
  1. ensureNamespace（幂等创建）
  2. kubectl label ns <n> kubepivot.io/managed=true --overwrite
  3. 读 configs/resources.yaml → 写 ConfigMap kubepivot-resources
     - labels.kubepivot.io/managed: "true"
     - annotations.kubepivot.io/sha256: <hex>
     - data.resources.yaml: 原始内容
```

### 3.3 分发协议：ConfigMap

每个 managed namespace 维护一个固定名字的 ConfigMap：

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: kubepivot-resources
  namespace: feelings-server
  labels:
    kubepivot.io/managed: "true"         # controller watcher 选中依据
    app.kubernetes.io/managed-by: kp
  annotations:
    kubepivot.io/sha256: "ec7753b4..."   # 热加载快速 skip 凭证
data:
  resources.yaml: |                       # 纯 YAML，便于 kubectl edit
    resources:
      - kind: Deployment
        name: feelings-server
        on-missing: auto-heal
        max-retry: 3
```

**三个字段三个用途**：
- `label` 是 watcher selector 的 key
- `annotation.sha256` 是热加载去重的依据（内容变则 hash 变）
- `data.resources.yaml` 是业务语义，纯 YAML

---

## 四、关键架构决策

### 4.1 不引入 client-go（架构纯粹性）

v2.3.0 最重要的一次决策。

**选项 A：引入 client-go（业界标准）**
- 获得 SharedInformerFactory、WorkQueue 等成熟组件
- 镜像体积从 ~35MB 膨胀到 ~45MB
- 破坏"只用 CLI 不吃 K8s SDK"的架构连贯性

**选项 B：exec kubectl --watch（KubePivot 路线）**
- 架构一致：整个项目只依赖 kubectl/helm 二进制
- 镜像体积不变（scratch + 3 个静态二进制）
- watch 断线、缓存、event 回调都要自己实现

**选择 B**，理由：

1. **差异化**：ArgoCD/Flux/Karmada 都是 client-go。KubePivot 的定位不是"又一个 K8s controller"，而是"能绕开 K8s 抽象直接操作集群的工具链"。这个定位要求不吃 K8s SDK。

2. **规模够用**：50 项目 × 5 资源 / 8s reconcile ≈ 31 次 kubectl/s。单次 kubectl 约 50ms，峰值 1.5s/s CPU，完全扛得住。client-go informer 的优势在 1000+ 项目的规模。

3. **可逆**：Watcher 接口抽象，未来规模上来了可以加一个 client-go 实现的 `InformerWatcher`，保留 KubectlWatcher 作为轻量备选。

### 4.2 Leader-Dispatch-Worker 并发模型

Leader 只做分发，Worker Pool 做实际执行。这是为了应对"50 个项目同时炸"的场景：

```
Leader（Namespace/ConfigMap Watcher + Reconcile Loop）
       │
       │  enqueue 是非阻塞操作
       ▼
channel（buffer = poolSize × 4）
       │
       │  20 个 worker 并发消费
       ▼
Worker Pool（healRecreate / healRollback / healScaleDown）
```

单任务 90s timeout，panic recovery。channel 满了丢弃并告警（不阻塞 Leader）。

### 4.3 sha256 指纹热加载

ConfigMap Watcher 收到事件后，**不直接触发 reconcile**，而是先比对 sha256：

```
ConfigMap MODIFIED
  ↓
读 data.resources.yaml
  ↓
计算 sha256
  ↓
对比 GlobalState 里缓存的 sha256
  ├── 相同 → 跳过（内容未变，忽略非语义事件）
  └── 不同 → UpsertProject → 立即入队全量 reconcile
```

这样 `kp deploy` 顺带同步的 ConfigMap 如果内容没变，就是幂等操作，不会触发无意义的 reconcile 风暴。

### 4.4 三道 Namespace 黑名单护栏

即使用户给 `kube-system` 打上 `managed=true` label，controller 在三处代码层面物理拒绝：

```
1. Worker Pool Enqueue 入队前      worker_pool.go    Enqueue()
2. handleTask 执行前（二次）        global.go        handleTask()
3. GlobalState UpsertProject 前    global_state.go  UpsertProject()
```

硬编码黑名单：

```
kube-system / kube-public / kube-node-lease / kubepivot-system / default
```

可通过环境变量 `KUBEPIVOT_EXTRA_PROTECTED_NS=istio-system,monitoring` 运行时扩展。

---

## 五、核心组件

### 5.1 Watcher

`internal/controller/watcher.go`

```go
type Watcher interface {
    Watch(ctx context.Context, onEvent func(WatchEvent)) error
}
```

**KubectlWatcher 实现**：

- `exec.CommandContext(ctx, ...)` 绑定子进程生命周期：ctx 取消 → kubectl 子进程被 SIGKILL，不产生僵尸
- `json.NewDecoder(stdout).Decode()` 流式解析 `kubectl get --watch --output-watch-events=true -o json` 的单行 JSON
- stderr 丢弃不污染 JSON 流
- **心跳守卫**：30s 无事件 → 主动 `kubectl get` 探活 → 失败则 `cancelStream()` 触发重连
- **指数退避重连**：1s → 2s → 4s → 8s → 30s 封顶

### 5.2 Worker Pool

`internal/controller/worker_pool.go`

- 固定大小 goroutine 池，默认 20，`KUBEPIVOT_WORKER_POOL_SIZE` 可覆盖
- channel buffer = size × 4，允许短暂突发
- 单任务独立 ctx + 90s timeout
- panic recovery，单任务崩溃不影响其他 worker
- 优雅关闭：`close(tasks)` + `wg.Wait()`
- 观测统计：enqueued / done / failed

### 5.3 GlobalState

`internal/controller/global_state.go`

多项目内存状态缓存：

```go
type GlobalState struct {
    mu       sync.RWMutex
    projects map[string]*projectState   // key = namespace
}

type projectState struct {
    Namespace string
    Sha256    string       // resources.yaml 内容的 sha256
    Resources []Resource   // 解析后的资源清单
}
```

- RWMutex 读多写少（watcher 写，worker pool 读）
- `UpsertProject` 返回 `changed bool`（sha256 比对，内容未变返回 false）
- Protected namespace 拒绝建立状态
- 坏 YAML 保留旧状态，降级不阻断
- `GetProject` 返回副本防外部污染

### 5.4 Main Loop（global.go）

```
StartGlobal(ctx)
  └── runGlobalLeaderElection
      │   etcd 配置 → 选举竞争
      │   无 etcd → 单机模式（降级但不致命）
      └── 成为 Leader → runAsLeader(ctx)
          │
          ├── go watchNamespaces         label=managed=true
          ├── go watchConfigMaps         name=kubepivot-resources, all-ns
          ├── go reconcileLoop           8s 周期全量对账
          └── go pool.Start              20 worker 消费 channel
```

**handleTask 职责**：

```
1. 二次 ns 黑名单护栏
2. DetectResourceExists(kind, name, namespace)
3. 存在 → return nil（当前版本仅处理缺失；OOM/CrashLoop 计划 v2.5.0）
4. 缺失 → 构造 Reconciler（注入 project=task.Project）
         → 复用 v2.2.0 的 checkAndHeal（自愈逻辑零重写）
```

---

## 六、CLI 命令家族

```
集群级管理（一次性 / 运维）：
  kp controller install    [--namespace kubepivot-system] [--image xxx:tag]
  kp controller uninstall  [--force]
  kp controller status
  kp controller projects

项目接入（在项目目录运行）：
  kp controller enroll     [--namespace <ns>] [--resources configs/resources.yaml]
  kp controller unenroll

Pod 内部（deployment.yaml 自动调用）：
  kp controller start [--global]
```

### kp deploy 顺带同步

在 `executeDeploy` 的末尾（状态机转 `RUNNING` 之后）调用 `autoSyncResourcesIfEnrolled`：

```
kp deploy
  └── ...正常部署流程...
      └── 状态机: RUNNING
          └── autoSyncResourcesIfEnrolled
              ├── 读 ns label → 非 managed 静默跳过
              ├── 读 configs/resources.yaml
              └── 写 ConfigMap kubepivot-resources（含 sha256）
```

**降级不阻断**：同步失败只 warn，不影响部署成功状态。

---

## 七、资源声明（configs/resources.yaml）

controller 监控的资源由配置文件驱动，**新增资源只改配置，不改代码**：

```yaml
resources:
  - kind: Deployment
    name: myapp
    on-missing: auto-heal
    max-retry: 3
    fallback: rollback

  - kind: StatefulSet
    name: myapp-postgres
    on-missing: auto-heal
    max-retry: 2

  - kind: PersistentVolumeClaim
    name: postgres-data
    on-missing: alert
```

### 支持的 kind

Deployment / StatefulSet / Service / PVC / Ingress / CronJob

### on-missing 策略

| 策略 | 行为 |
|------|------|
| `auto-heal` | 执行 `helm rollback` 到上一个 revision，`--history-max=10` 防 secret 堆积 |
| `rollback` | 回滚 n→n-1 |
| `scale-down` | 缩容到 0 |
| `alert` | 只打 slog.Error 日志，不自动处理 |
| `custom` | 用户自定义（需配 fallback 字段） |

---

## 八、RBAC

Controller 使用 ClusterRole（cross-namespace 权限）：

```
apps:                deployments / statefulsets / replicasets / daemonsets  完整 CRUD
core:                pods / services / pvc / configmaps / endpoints          完整 CRUD
namespaces:          get / list / watch                                      （发现 managed ns）
secrets:             完整 CRUD                                                （helm release state）
coordination/leases: 完整 CRUD                                                （Leader Election）
events:              create / patch                                          （事件记录）
autoscaling:         horizontalpodautoscalers                                 完整 CRUD
networking:          networkpolicies / ingresses                              完整 CRUD
rbac:                roles / rolebindings                                     完整 CRUD
apiextensions:       customresourcedefinitions                                get / list / watch
```

详见 `internal/controller_installer/templates/rbac.yaml`。

---

## 九、可观测性

### 日志（slog 结构化）

```
🌐 controller 启动（global 模式 v2.3.0）
👑 已成为全局 Leader，启动 watchers + worker pool
👀 Namespace Watcher 启动          label=kubepivot.io/managed=true
👀 ConfigMap Watcher 启动          scope=all namespaces
🧵 Worker Pool 启动                size=20 buffer=80
🔁 Reconcile Loop 启动             interval=8s
📥 发现 managed namespace           ns=feelings-server action=ADDED
📋 项目状态已更新                    namespace=feelings-server resources=1 sha256=ec7753b4...
🔄 ConfigMap 变化触发全量 reconcile   namespace=feelings-server
🛡 拒绝对 protected namespace 入队    namespace=kube-system kind=Deployment
```

### 关键指标（Stats）

Worker Pool 暴露三个计数：

```go
enqueued, done, failed := pool.Stats()
```

用于 `kp controller status` 输出和未来的 Prometheus exporter。

---

## 十、故障与降级

| 场景 | 当前行为 | 计划 |
|------|---------|------|
| etcd 未配置 | 3 副本降级单机模式（各自 runAsLeader，冗余但不致命） | v2.4.0 改 K8s Lease API 原生选举 |
| kubectl --watch 断线 | 心跳守卫探活 → 指数退避重连 | ✅ |
| ConfigMap YAML 坏 | 保留旧状态，warn 日志 | ✅ |
| Worker 任务 panic | recovery，计入 failed，不影响其他 worker | ✅ |
| Channel 满 | 丢弃入队并告警（保护 Leader 不阻塞） | ✅ |
| Protected namespace 误打 label | 三道护栏物理拒绝 | ✅ |

---

## 十一、从 v2.2.0 迁移

```
1. helm uninstall <project>-kubepivot-controller     # 卸老 controller
2. kp controller install                              # 装全局 controller
3. cd <project> && kp controller enroll               # 接入项目
4. 后续 kp deploy 自动同步 resources.yaml
```

**向后兼容**：`kp controller start`（不带 `--global`）仍然走 v2.2.0 per-project 路径，旧项目升级 kp 二进制后可以选择不迁移。`kp init` 生成的新项目骨架默认不再包含 per-project controller chart。

---

## 附录 A：v2.2.0 per-project 架构（历史）

> 保留此节仅作为历史参考。新部署一律走 v2.3.0 global 架构。

v2.2.0 架构下，每个项目独立部署一个 controller：

```
project-a namespace                 project-b namespace
  ├── 业务 Pod                        ├── 业务 Pod
  └── kubepivot-controller（3 副本）   └── kubepivot-controller（3 副本）
      ├── Leader Election              ├── Leader Election
      │   /kubepivot/<proj>/<ns>/leader│   /kubepivot/<proj>/<ns>/leader
      └── 只管自己 namespace            └── 只管自己 namespace
```

每个项目 `deployments/<project>/kubepivot-controller/` 下有独立 Helm chart，
`components.yaml` 里有 `kubepivot-controller` 段，用户通过 values.yaml 的
`enabled: true/false` 控制是否部署。

**迁移原因**：资源浪费（N × 3）+ 认知负担（每项目都要维护一套 controller）。

---

## 附录 B：环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `KUBEPIVOT_MODE` | `global` / `per-project`（auto 兼容） | 由 `--global` flag 和 env 组合决定 |
| `KUBEPIVOT_WORKER_POOL_SIZE` | Worker Pool 大小 | 20 |
| `KUBEPIVOT_EXTRA_PROTECTED_NS` | 追加 ns 黑名单（逗号分隔） | 空 |
| `ETCD_ENDPOINTS` | etcd 地址，空则降级单机 | 空 |
| `LEADER_KEY` | Leader Election 路径 | `/kubepivot/global/leader` |
| `KUBE_CONFIG` | kubeconfig 路径 | 空（使用 ServiceAccount） |

---

## 附录 C：文件地图

```
internal/controller/
├── global.go                  StartGlobal + runAsLeader 主循环
├── global_state.go            多项目状态缓存 + sha256 指纹
├── worker_pool.go             固定大小 goroutine 池
├── watcher.go                 kubectl --watch + 心跳 + 退避
├── namespace_blacklist.go     5 个系统 ns 黑名单
├── controller.go              Start(args) 分支（per-project / global）
├── reconciler.go              Reconciler struct（加 project 字段）
├── heal.go                    healRecreate / healRollback（r.project）
└── drift_sync.go              forceSync（--history-max=10）

internal/controller_installer/（独立包）
├── installer.go               Install / Uninstall / Status
└── templates/
    ├── namespace.yaml
    ├── rbac.yaml
    └── deployment.yaml

cmd/kp/
├── controller.go              kp controller 子命令分发
└── controller_enroll.go       enroll / unenroll / projects 实现
```
