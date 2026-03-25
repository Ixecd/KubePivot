# dtk 状态机设计文档

> 版本：v0.4.1
> 最后更新：2026-03-25

---

## 一、为什么需要状态机

`dtk deploy` 是一个多步骤、有副作用的长流程操作：

```
build image → push image → helm upgrade → rollout → validate
```

任意一步失败，或者进程中途被 Ctrl+C 杀掉，都会留下不确定的中间状态。没有状态机的情况下：

- 首次部署失败后 namespace 残留，下次部署会误判为"更新"而非"首次"
- 更新失败后没有自动回滚，服务处于损坏状态
- 进程中断后不知道从哪里继续，只能重头跑
- 并发 `dtk deploy` 可能产生竞争，导致资源混乱

**状态机的价值**：把部署过程的每一个阶段显式化，记录历史，支持从任意中断点恢复，并在失败时自动触发兜底操作。

---

## 二、状态定义

```
IDLE          初始状态。无部署记录，或者首次部署失败后清理完成。
INITIALIZING  部署流程已启动，正在准备（读取配置、检查依赖）。
DEPLOYING     正在执行 helm upgrade + kubectl rollout。
VALIDATING    rollout 完成，正在验证服务健康（pod Ready + healthz 200）。
RUNNING       部署成功，服务正常运行。
ROLLING_BACK  验证失败或手动触发，正在执行 helm rollback。
CLEANING      首次部署失败，正在清理 namespace。
TERMINATED    服务已下线，终态，不可再转换。
```

---

## 三、状态转换表

```
当前状态         可转换到
─────────────────────────────────────────────────────────
IDLE           → INITIALIZING
INITIALIZING   → DEPLOYING, CLEANING
DEPLOYING      → VALIDATING, ROLLING_BACK, CLEANING
VALIDATING     → RUNNING, ROLLING_BACK, CLEANING
RUNNING        → INITIALIZING, CLEANING, TERMINATED
ROLLING_BACK   → RUNNING, CLEANING
CLEANING       → IDLE, TERMINATED
TERMINATED     → （终态，不可转换）
```

所有不在上表中的转换都会被拒绝并返回错误，状态机会拒绝执行并保持当前状态不变。

---

## 四、完整流程图

### 4.1 正常部署

```
IDLE
  │  dtk deploy
  ▼
INITIALIZING
  │  helm upgrade --install --wait
  ▼
DEPLOYING
  │  rollout 完成
  ▼
VALIDATING ── 所有 pod Ready + kubectl exec healthz 200 ──▶ RUNNING
```

### 4.2 首次部署失败（namespace 不存在）

```
INITIALIZING → DEPLOYING
                  │  helm upgrade 失败
                  ▼
              CLEANING ── kubectl delete namespace ──▶ IDLE
```

首次部署判断依据：`helmReleaseExists()` 返回 false（查 helm history，release 不存在）。

> ⚠️ 之前用 `namespaceExists()` 判断首次部署，但 helm `--create-namespace` 会自动创建 namespace，
> 导致首次失败后 `namespaceExists()` 返回 true，误判为更新。改用 `helmReleaseExists()` 更准确。

### 4.3 更新失败（已有 RUNNING 状态）

```
RUNNING → INITIALIZING → DEPLOYING
                              │  helm upgrade 失败 / VALIDATING 超时
                              ▼
                         ROLLING_BACK ── helm rollback ──▶ RUNNING
```

### 4.4 手动回滚

```
任意状态
  │  dtk rollback
  ▼
ROLLING_BACK ── helm rollback ──▶ RUNNING
```

---

## 五、持久化

### 5.1 etcd 优先

状态持久化到 etcd，key 格式：

```
dtk/<project>/<namespace>/state
```

value 是 JSON 序列化的 `DeployRecord`。

### 5.2 本地文件降级

`ETCD_ENDPOINTS` 为空时，自动降级到本地文件：

```
~/.dtk/state/<project>/<namespace>.json
```

### 5.3 自动选择

```go
// ETCD_ENDPOINTS 留空 → 使用本地文件
store := state.NewAutoStore(env["ETCD_ENDPOINTS"])
```

etcd 连接超时（3 秒）或失败，自动降级，不会阻塞部署流程。

---

## 六、DeployRecord 数据结构

```json
{
  "project":    "web3-blitz",
  "namespace":  "web3-blitz",
  "state":      "RUNNING",
  "version":    "v0.1.10",
  "is_first":   false,
  "reason":     "部署成功",
  "updated_at": "2026-03-25T17:09:51.114Z",
  "history": [
    {
      "from":      "IDLE",
      "to":        "INITIALIZING",
      "reason":    "开始部署 v0.1.10",
      "version":   "v0.1.10",
      "timestamp": "2026-03-25T17:09:10.123Z"
    },
    {
      "from":      "INITIALIZING",
      "to":        "DEPLOYING",
      "reason":    "执行 helm upgrade",
      "version":   "v0.1.10",
      "timestamp": "2026-03-25T17:09:10.456Z"
    },
    {
      "from":      "DEPLOYING",
      "to":        "VALIDATING",
      "reason":    "验证部署结果",
      "version":   "v0.1.10",
      "timestamp": "2026-03-25T17:09:40.789Z"
    },
    {
      "from":      "VALIDATING",
      "to":        "RUNNING",
      "reason":    "部署成功",
      "version":   "v0.1.10",
      "timestamp": "2026-03-25T17:09:51.114Z"
    }
  ]
}
```

每次状态转换都追加到 `history`，完整记录部署历史，支持审计和问题排查。

---

## 七、VALIDATING 阶段

验证条件：**所有 pod Ready 且 healthz 返回 200**

### 7.1 实现步骤

1. `kubectl get deployment <n> -o jsonpath={.status.readyReplicas}/{.status.replicas}` 轮询 pod 就绪数
2. 就绪后用 `kubectl exec <pod> -- wget -qO- http://localhost:<port>/healthz` 检查健康
3. 默认超时 120 秒，每 5 秒轮询一次
4. 超时 → 转换到 ROLLING_BACK → 自动回滚

### 7.2 为什么用 kubectl exec 而不是直接 HTTP

pod IP 是集群内部地址，本机无法直连。`kubectl exec` 在 pod 内部执行 wget，绕开网络限制。

### 7.3 业务服务必须实现 /healthz

所有通过 dtk 部署的服务都必须实现 `/healthz` 路由，返回 HTTP 200：

```go
mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("ok"))
})
```

port 从 `components.yaml` 的 `port` 字段读取，不再硬编码。

---

## 八、并发保护

当前状态为以下任意一种时，`dtk deploy` 会拒绝执行：

```
INITIALIZING / DEPLOYING / VALIDATING / ROLLING_BACK / CLEANING
```

报错信息：
```
当前部署状态为 DEPLOYING，不能发起新部署
如需继续，请运行: dtk resume
```

---

## 九、dtk resume 行为

`dtk resume` 先检查 K8s 实际状态，再决定从哪里恢复：

```
kubectl get deployment <name> → 存在   → 同步状态为 RUNNING
kubectl get deployment <name> → 不存在 → 从头重新部署
```

> ⚠️ 已知 Bug：`resume` 从 CLEANING 状态直接尝试转换到 VALIDATING，
> 不符合转换表，会失败但没有报错提示。待修复。

---

## 十、A2 架构：独立 Reconciliation Controller

### 10.1 A1 方案的问题

v0.3.x 的 A1 方案把 Reconciliation Loop 跑在 `dtk` CLI 进程里，
Loop 的生命周期和 CLI 进程绑定。用户 Ctrl+C 或终端断开，Loop 就死了。

### 10.2 A2 方案

把 Reconciliation Loop 从 CLI 进程里拆出来，作为独立的 K8s Deployment 运行：

```
用户终端                      K8s Cluster
  └── dtk deploy                └── web3-blitz-controller (Deployment)
        │                             └── Reconciliation Loop（永久运行）
        │                                   ├── etcd Watch（事件驱动）
        └── 写状态到 etcd ────────────────→  └── 8s 周期 Reconcile（兜底）
```

**controller 自愈流程**：

```
8s 定时器触发
    │
    ├── kubectl get deployment wallet-service → 不存在
    │
    ├── helm history web3-blitz → 查最新 deployed revision
    │
    └── helm rollback web3-blitz <revision> --wait
              │
              ├── 成功 → wallet-service 恢复，1/1 Running
              └── 失败 → ERROR 日志，下次周期重试
```

### 10.3 controller 配置

controller 从环境变量读配置，挂载 `resources.yaml` 决定监控哪些资源：

```yaml
# configs/resources.yaml
resources:
  - kind: Deployment
    name: wallet-service
    namespace: web3-blitz
    on_missing: recreate    # 缺失时触发 helm rollback
    max_retry: 3
    fallback: rollback

  - kind: StatefulSet
    name: postgres
    namespace: web3-blitz
    on_missing: alert       # 只告警，不自动处理
    max_retry: 0
    fallback: ""
```

新增监控资源只改配置，不改代码。

### 10.4 A1 vs A2 对比

| 维度 | A1（CLI 进程内）| A2（独立 Controller Pod）|
|------|----------------|--------------------------|
| Loop 生命周期 | 和 CLI 进程绑定 | K8s 管理，自动重启 |
| dtk deploy 体验 | 阻塞终端 | 立即返回 |
| 并发安全 | 可能竞争 | Controller 天然串行 |
| SSA 冲突 | 需要手动清除 | 自动处理（待实现）|
| 故障恢复 | 需要 dtk resume | 自动自愈 |
| 扩展性 | 硬编码 | 配置驱动 |

---

## 十一、文件结构

```
internal/state/
├── state.go       # State 类型、转换表、DeployRecord、Machine、EtcdKey
├── store.go       # Store 接口、etcdStore、localStore、NewAutoStore
├── validator.go   # ValidateDeployment（pod Ready + healthz）
└── state_test.go  # 15 个单元测试

internal/controller/
├── controller.go     # 入口，从环境变量读配置，启动 Reconciler
├── reconciler.go     # Reconciliation Loop（etcd Watch + 8s 周期）
├── etcd_watcher.go   # etcd 实时 Watch，触发 reconcile
├── resources.go      # 加载 configs/resources.yaml
└── heal.go           # resourceExists + healRecreate + fallbackRollback

cmd/dtk/
├── deploy.go      # runDeploy / runResume / runRollback
├── runner.go      # kubectl/helm 辅助函数
└── main.go        # CLI 入口，含 controller start subcommand
```

**职责边界**：
- `internal/state`：纯状态机逻辑，零 K8s 依赖，不知道如何查询资源
- `internal/controller`：资源检测 + 自愈，所有 K8s 操作都在这里
- `cmd/dtk`：CLI 命令入口，调用 state 和 controller

---

## 十二、已知遗留问题

| # | 问题 | 严重程度 |
|---|------|----------|
| 1 | `resumeFromValidating` 不检查转换合法性，从 CLEANING 强转 VALIDATING | P0 |
| 2 | `detectActualState` deployment 被删后仍返回 DEPLOYING | P0 |
| 3 | controller rollback 后没有更新 dtk 状态文件，状态不同步 | P0 |
| 4 | helm rollback 受 SSA 冲突影响，卡在 pending-rollback | P1 |
| 5 | controller RBAC 权限过宽，生产环境需要收紧 | P1 |
| 6 | controller 同时触发 rollback 和 dtk deploy 会产生 helm 并发冲突 | P1 |
| 7 | `is_first` 在历史记录过多后无法准确反映当前是否首次 | P2 |
