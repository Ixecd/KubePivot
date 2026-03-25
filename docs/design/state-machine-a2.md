# dtk 状态机 A2 方案 — Reconciliation Controller 设计文档

> 版本：2026-03-25
> 适用：dev-toolkit v0.4.1+

---

## 背景：A1 方案的问题

v0.3.x 的状态机（A1 方案）把 Reconciliation Loop 跑在 `dtk` CLI 进程里：

```
用户终端
  └── dtk deploy
        └── Reconciliation Loop（进程内）
```

**根本缺陷**：Loop 的生命周期和 CLI 进程绑定。用户 Ctrl+C、终端断开、网络抖动，Loop 就死了，状态卡在中间态，只能靠 `dtk resume` 手动恢复。

---

## A2 方案：独立控制器 Pod

把 Reconciliation Loop 从 CLI 进程里拆出来，作为独立的 K8s Deployment 运行：

```
用户终端                    K8s Cluster
  └── dtk deploy              └── web3-blitz-controller (Deployment)
        │                           └── Reconciliation Loop（永久运行）
        │                                 ├── etcd Watch（事件驱动）
        └──  写状态 etcd  ──────────────→  └── 8s 周期 Reconcile（兜底）
```

**核心优势**：
- Controller Pod 崩溃由 K8s 自动重启，Loop 不会永久消失
- `dtk` CLI 执行完就退出，不再阻塞终端
- 多个 `dtk deploy` 并发时，Controller 天然串行处理

---

## 目录结构

```
internal/controller/
├── controller.go     # 主入口，dtk controller start 子命令
├── reconciler.go     # Reconciliation Loop 核心（etcd Watch + 周期 Reconcile）
├── resources.go      # 加载 configs/resources.yaml
├── etcd_watcher.go   # etcd 实时 Watch（事件驱动触发）
└── heal.go           # 自动自愈 + 回滚兜底逻辑
```

---

## 配置驱动：resources.yaml

Controller 监控的资源通过配置文件声明，不需要改 Go 代码：

```yaml
# configs/resources.yaml
resources:
  - kind: Deployment
    name: wallet-service
    namespace: web3-blitz
    on_missing: recreate      # 缺失时：重建
    max_retry: 3              # 最多重试 3 次
    fallback: rollback        # 超过重试次数：helm rollback

  - kind: StatefulSet
    name: postgres
    namespace: web3-blitz
    on_missing: alert         # 缺失时：只告警，不自动处理
    max_retry: 0
```

新增监控资源只需改配置，扩展性极强。

---

## 核心机制

### 双保险触发

```
etcd Watch（事件驱动）
    │ 状态变更立即触发
    ▼
Reconcile()
    │
    ├── 检查资源实际状态
    ├── 与 etcd 期望状态对比
    └── 执行自愈操作

8s 周期 Reconcile（兜底）
    │ 防止 Watch 事件丢失
    └── 同上
```

### 自愈策略

```
资源缺失 / 状态异常
    │
    ├── 尝试 helm upgrade --install --force-conflicts
    │       ↓ 失败（重试 N 次）
    └── helm rollback（回退到上一个稳定版本）
```

### SSA 冲突自动处理

A1 方案的痛点：`kubectl set image` 产生的 managedFields 和 helm 的 field manager 冲突，导致 `helm upgrade` 失败。

A2 方案在 `heal.go` 里加了自动清除：

```go
// upgrade 前先清除 managedFields
kubectl patch deployment <n> --type=merge -p '{"metadata":{"managedFields":null}}'
helm upgrade --install --force-conflicts ...
```

---

## 状态机新增能力

### EtcdKey()

暴露为导出函数，供 Controller 读写状态：

```go
key := state.EtcdKey(project, namespace)
// → "dtk/<project>/<namespace>/state"
```

### DetectResourceExists()

支持 Deployment 和 StatefulSet 的存在性检查：

```go
exists := sm.DetectResourceExists("Deployment", "wallet-service")
exists := sm.DetectResourceExists("StatefulSet", "postgres")
```

---

## 部署架构

`dtk deploy` 现在会同时部署两个组件：

```
web3-blitz namespace
├── wallet-service (Deployment)      # 业务服务
└── web3-blitz-controller (Deployment) # 状态机控制器
```

Controller 和 wallet-service 通过 etcd 通信，完全解耦。

---

## 与 A1 方案对比

| 维度 | A1（CLI 内进程）| A2（独立 Controller Pod）|
|------|----------------|--------------------------|
| Loop 生命周期 | 和 CLI 进程绑定 | 由 K8s 管理，自动重启 |
| 用户体验 | dtk deploy 阻塞终端 | dtk deploy 立即返回 |
| 并发安全 | 多个 deploy 可能竞争 | Controller 天然串行 |
| SSA 冲突 | 需要手动清除 | 自动处理 |
| 扩展性 | 硬编码 | 配置驱动 |
| 故障恢复 | 需要 dtk resume | 自动自愈 |

---

## 验证方法

```bash
# 1. 部署
dtk deploy

# 2. 确认 controller pod 在运行
kubectl get pods -n web3-blitz | grep controller

# 3. 手动删除 wallet-service deployment，观察自愈
kubectl delete deployment wallet-service -n web3-blitz

# 4. 查看 controller 日志，确认自动重建
kubectl logs -n web3-blitz deployment/web3-blitz-controller -f
```

---

## 遗留 TODO

- [ ] Controller 多副本 Leader 选举（当前单副本）
- [ ] 自愈事件写入 etcd history，供 `dtk status` 查询
- [ ] `dtk status` 命令：实时查看 Controller 当前状态
- [ ] 告警通知（Slack / 邮件）
