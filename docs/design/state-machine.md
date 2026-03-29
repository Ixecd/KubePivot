# dtk 状态机设计文档

> 适用：dev-toolkit v0.4.0+
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

## 架构（A2 方案）

**核心**：独立 `*-controller` Deployment，运行在集群内部，不依赖 dtk CLI（dtk CLI 执行完即退出）。通过 etcd Watch + 定期 Reconcile 驱动，配置化资源列表。

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

## Reconciliation Loop

**运行位置**：`{project}-controller` Deployment（独立 pod）

**工作机制**：
- **etcd Watch**：实时监听状态变化，指数退避重连（1s → 30s）
- **定期 Reconcile**：每 8 秒全面对账一次（兜底）
- **资源检查**：根据 `configs/resources.yaml` 配置检查所有核心资源
- **自动自愈**：资源缺失 → 执行 `helm rollback` 到上一个版本

---

## configs/resources.yaml

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
```

**on-missing 策略**：`auto-heal`（自动 rollback）/ `alert`（只记日志）

---

## 完整流程图

### 正常部署

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
```

### 首次部署失败

```
DEPLOYING/INITIALIZING → CLEANING → IDLE
```

### 更新失败

```
DEPLOYING/VALIDATING → ROLLING_BACK → RUNNING
```

---

## 持久化

**etcd 优先**，key 格式：`dtk/<project>/<namespace>/state`

**本地文件降级**：无 etcd 时自动降级到 `~/.dtk/state/<project>/<namespace>.json`

**自动选择**：

```go
store := state.NewAutoStore(env["ETCD_ENDPOINTS"])
// ETCD_ENDPOINTS 留空 → 使用本地文件
```

---

## DeployRecord 数据结构

```json
{
  "project":    "web3-blitz",
  "namespace":  "web3-blitz",
  "state":      "RUNNING",
  "version":    "v0.1.10",
  "is_first":   false,
  "reason":     "部署成功",
  "updated_at": "2026-03-28T17:00:00+08:00",
  "history": [
    {
      "from":      "DEPLOYING",
      "to":        "VALIDATING",
      "reason":    "验证部署结果",
      "version":   "v0.1.10",
      "timestamp": "2026-03-28T17:00:00+08:00"
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

业务服务必须实现 `/healthz` 路由：

```go
mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
})
```

---

## 命令说明

### dtk deploy

状态必须为 `IDLE`、`RUNNING` 或 `TERMINATED` 才能发起新部署。内部按拓扑排序多 helm release 部署（多服务），或走 make deploy.full（单服务）。

### dtk resume

先检查 K8s 实际状态，再决定从哪里继续：服务正常 → 同步 RUNNING；服务不存在 → 从头部署。

### dtk rollback

手动触发整组 helm rollback，按拓扑逆序回滚所有 release，状态回到 RUNNING。

---

## 并发保护

当前状态为 `INITIALIZING`、`DEPLOYING`、`VALIDATING`、`ROLLING_BACK`、`CLEANING` 时，`dtk deploy` 会拒绝执行：

```
当前部署状态为 DEPLOYING，不能发起新部署
如需继续，请运行: dtk resume
```
