# dtk 状态机设计文档

> 版本：2026-03-25
> 状态：A2 独立 Controller Pod 方案已落地

---

## 设计动机

`dtk deploy` 是一个多步骤、有副作用的长流程操作。任意一步失败或进程被中断，都会留下不确定的中间状态。传统命令式部署难以处理以下场景：

- 首次部署失败后 namespace 残留
- 更新失败后没有自动回滚
- 资源被手动删除、scale、patch 后状态不一致
- 并发部署产生竞争

状态机从**命令式**升级为**声明式 + Reconciliation Loop**，核心目标是：
- 真正实现**自动自愈**
- 高可扩展性（新增任何资源只需改配置）
- 业务逻辑与状态对账完全解耦

---

## 架构升级（A2 方案）

**核心变化**：
- 新增独立 `*-controller` Deployment，专门运行 Reconciliation Loop
- Loop 运行在集群内部，不依赖 dtk CLI（dtk CLI 执行完即退出）
- 配置驱动：`configs/resources.yaml`
- 通信方式：纯 etcd（Watch + 定期 Reconcile）
- 纠正策略：优先自动自愈 → 多次失败后自动 rollback

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

## Reconciliation Loop（核心新增）

**运行位置**：`web3-blitz-controller` Deployment（独立 pod）

**工作机制**：
- **etcd Watch**：实时监听 `dtk/<project>/<namespace>/state` 变化
- **定期 Reconcile**：每 8 秒全面对账一次
- **资源检查**：根据 `configs/resources.yaml` 配置检查所有核心资源
- **自动自愈**：资源缺失 → 尝试 `helm upgrade --install --force-conflicts`
- **失败兜底**：自愈失败 N 次 → 自动 `helm rollback` 到上一个版本

---

## configs/resources.yaml（配置化扩展）

```yaml
resources:
  - kind: Deployment
    name: wallet-service
    on-missing: auto-heal
    max-retry: 2
    fallback: rollback
  - kind: StatefulSet
    name: postgres
    on-missing: auto-heal
    max-retry: 2
    fallback: rollback
  # ... 可随意扩展任何 K8s 资源
```

## Controller与业务解耦

- wallet-service：只负责业务逻辑
- web3-blitz-controller：只负责状态对账和自愈
- 两者通过 etcd 通信

## 文件结构（新增部分）

```text
internal/controller/          # 新增
├── controller.go
├── reconciler.go
├── resources.go
├── etcd_watcher.go
└── heal.go

configs/resources.yaml        # 新增配置化资源列表

deployments/web3-blitz/templates/
└── controller-deployment.yaml   # 新增 controller Deployment
```

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

## 下一步

- controller pod 集成到同一个 Helm Chart
- 完整 e2e 测试（手动删除 deployment → 自动自愈）
- SSA 冲突自动清理
- 多资源类型全面支持
