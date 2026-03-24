# dtk 状态机设计文档

> 版本：2026-03-24
> 适用：dev-toolkit v0.3.3+

---

## 设计动机

`dtk deploy` 是一个多步骤、有副作用的长流程操作：

```
build → push → helm upgrade → rollout → validate
```

任意一步失败，或者进程中途被 Ctrl+C 杀掉，都会留下不确定的中间状态。没有状态机的情况下：

- 首次部署失败后 namespace 残留，下次部署会误判为"更新"而非"首次"
- 更新失败后没有自动回滚，服务处于损坏状态
- 进程中断后不知道从哪里继续，只能重头跑
- 并发 `dtk deploy` 可能产生竞争

状态机解决了这些问题。

---

## 状态定义

```
IDLE          初始状态，无部署记录或已清理完成
INITIALIZING  部署流程已启动，正在准备
DEPLOYING     正在执行 helm upgrade + rollout
VALIDATING    rollout 完成，正在验证服务健康
RUNNING       部署成功，服务正常运行
ROLLING_BACK  验证失败或手动触发，正在执行 helm rollback
CLEANING      首次部署失败，正在清理 namespace
TERMINATED    服务已下线，终态
```

---

## 状态转换表

```
当前状态       → 可转换到
─────────────────────────────────────────
IDLE           → INITIALIZING
INITIALIZING   → DEPLOYING, CLEANING
DEPLOYING      → VALIDATING, ROLLING_BACK, CLEANING
VALIDATING     → RUNNING, ROLLING_BACK
RUNNING        → INITIALIZING, TERMINATED
ROLLING_BACK   → RUNNING, CLEANING
CLEANING       → IDLE, TERMINATED
TERMINATED     → （终态，不可转换）
```

---

## 完整流程图

### 正常部署

```
IDLE
  │  dtk deploy
  ▼
INITIALIZING
  │  执行 helm upgrade
  ▼
DEPLOYING
  │  rollout 完成
  ▼
VALIDATING  ──── 所有 pod Ready + healthz 200 ────▶ RUNNING
```

### 首次部署失败

```
DEPLOYING / INITIALIZING
  │  helm upgrade 失败
  ▼
CLEANING  ──── kubectl delete namespace ────▶ IDLE
```

### 更新失败（已有 RUNNING 状态）

```
DEPLOYING / VALIDATING
  │  失败或超时
  ▼
ROLLING_BACK  ──── helm rollback ────▶ RUNNING
```

### 手动回滚

```
任意状态
  │  dtk rollback
  ▼
ROLLING_BACK  ──── helm rollback ────▶ RUNNING
```

---

## 持久化

### etcd 优先

状态持久化到 etcd，key 格式：

```
dtk/<project>/<namespace>/state
```

value 是 JSON 序列化的 `DeployRecord`。

### 本地文件降级

无 etcd 时自动降级到本地文件：

```
~/.dtk/state/<project>/<namespace>.json
```

### 自动选择

```go
// ETCD_ENDPOINTS 留空 → 使用本地文件
store := state.NewAutoStore(env["ETCD_ENDPOINTS"])
```

配置方式：在 `configs/project.env` 里加：

```ini
ETCD_ENDPOINTS=localhost:2379   # 留空=本地文件
```

---

## DeployRecord 数据结构

```json
{
  "project":    "web3-blitz",
  "namespace":  "web3-blitz",
  "state":      "RUNNING",
  "version":    "v0.1.5",
  "is_first":   false,
  "reason":     "部署成功",
  "updated_at": "2026-03-24T15:55:10.632524+08:00",
  "history": [
    {
      "from":      "DEPLOYING",
      "to":        "VALIDATING",
      "reason":    "验证部署结果",
      "version":   "v0.1.5",
      "timestamp": "2026-03-24T15:55:10.431840+08:00"
    }
  ]
}
```

每次状态转换都追加到 `history`，完整记录部署历史。

---

## VALIDATING 阶段

验证条件：**所有 pod Ready 且 healthz 返回 200**

实现方式：

1. `kubectl get deployment <name> -o jsonpath={.status.readyReplicas}/{.status.replicas}` 轮询 pod 就绪数
2. 就绪后用 `kubectl exec <pod> -- wget -qO- http://localhost:<port>/healthz` 检查健康
3. 默认超时 120 秒，每 5 秒轮询一次
4. 超时 → ROLLING_BACK → 自动回滚

为什么用 `kubectl exec` 而不是直接 HTTP：pod IP 是集群内部地址，本机无法直连，`kubectl exec` 在 pod 内部执行，绕开网络问题。

业务服务必须实现 `/healthz` 路由，返回 200：

```go
mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("ok"))
})
```

---

## 命令说明

### dtk deploy

正常部署入口，状态必须为 `IDLE`、`RUNNING` 或 `TERMINATED` 才能发起新部署。

```bash
dtk deploy [--namespace <ns>] [--context <ctx>] [--kubeconfig <path>] [--dry-run]
```

### dtk resume

中断恢复。先检查 K8s 实际状态，再决定从哪里继续：

```
K8s 实际状态      → resume 行为
──────────────────────────────────────────
服务正常运行       → 同步状态为 RUNNING
rollout 未完成    → 重新进入 VALIDATING
namespace 不存在  → 从头部署
```

```bash
dtk resume [--namespace <ns>] [--context <ctx>] [--kubeconfig <path>]
```

### dtk rollback

手动触发回滚，执行 `helm rollback`，状态回到 `RUNNING`。

```bash
dtk rollback [--namespace <ns>] [--context <ctx>] [--kubeconfig <path>]
```

---

## 并发保护

当前状态为 `INITIALIZING`、`DEPLOYING`、`VALIDATING`、`ROLLING_BACK`、`CLEANING` 时，`dtk deploy` 会拒绝执行：

```
当前部署状态为 DEPLOYING，不能发起新部署
如需继续，请运行: dtk resume
```

---

## 文件结构

```
internal/state/
├── state.go      # State 类型、转换表、DeployRecord、Machine
├── store.go      # Store 接口、etcdStore、localStore、NewAutoStore
├── validator.go  # ValidateDeployment、getPodReadiness、checkHealthz
└── state_test.go # 15 个单元测试
```
