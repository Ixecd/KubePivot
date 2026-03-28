# A2 Reconciliation Controller 设计文档

> 适用：dev-toolkit v0.4.1+

---

## 设计动机

A1 方案（Loop 和 CLI 进程绑定）的问题：

| 问题 | 表现 |
|------|------|
| 进程绑定 | `dtk deploy` 阻塞终端，进程退出则自愈停止 |
| 无法处理运行时故障 | 部署成功后服务挂掉，无人感知 |
| 扩展性差 | 每次加新资源都要改代码 |

A2 方案：controller 作为独立 Deployment 运行在 K8s 里，生命周期由 K8s 管理。

---

## 架构

```
dtk deploy（CLI）
    │ 写状态到 etcd
    ↓
etcd: dtk/{project}/{ns}/state

    ↓ Watch / 8s 定时
{n}-controller（K8s Deployment，常驻）
    ├── etcd Watcher（事件驱动）
    │     断线 → 指数退避重连（1s → 2s → 4s ... 最大 30s）
    │     重连成功 → delay 重置为 1s
    └── 8s 周期 Reconcile（兜底）
            │
            ↓ 遍历 configs/resources.yaml
        检测资源是否存在
            │
     ┌──────┴──────┐
   存在           缺失
     │              │
   跳过        on-missing 策略
                ├── auto-heal → helm rollback
                └── alert    → 只记日志
```

---

## 职责边界

| 包 | 职责 |
|---|---|
| `internal/state` | 纯 FSM，零 K8s 依赖 |
| `internal/controller` | K8s 检测 + 自愈逻辑 |
| `cmd/dtk` | CLI 入口，引用两个包 |

---

## 接口设计

两个核心接口，方便 mock 测试：

```go
// Detector K8s 资源检测
type Detector interface {
    ResourceExists(kind, name, namespace string) (bool, error)
}

// HelmClient helm 操作
type HelmClient interface {
    History(release, namespace string) ([]HelmRelease, error)
    Rollback(release, namespace string, revision int) error
}
```

生产实现：`KubectlDetector`（kubectl CLI）和 `RealHelmClient`（helm CLI）。

---

## configs/resources.yaml

controller 监控的资源由配置文件驱动，**新增资源只改配置，不改代码**：

```yaml
resources:
  - kind: Deployment
    name: myapp
    on-missing: auto-heal   # 缺失时自动 helm rollback
    max-retry: 3
    fallback: rollback

  - kind: StatefulSet
    name: myapp-postgres
    on-missing: auto-heal
    max-retry: 2
    fallback: rollback

  # 只告警，不自动处理
  - kind: PersistentVolumeClaim
    name: postgres-data
    on-missing: alert
```

**支持的 kind**：Deployment / StatefulSet / Service / PVC / Ingress / CronJob

**on-missing 策略**：
- `auto-heal`：执行 `helm rollback` 到上一个 revision
- `alert`：只打 slog.Error 日志，不自动处理

---

## 自愈流程

```
检测到资源缺失
    ↓
helm history → 取最新 revision（latest）
    ↓
helm rollback {release} {latest-1} --wait
    ↓ 成功
r.sm.Transition(StateRunning, "controller: rollback 自愈成功")
    ↓ 失败
slog.Error，返回 error（下次 reconcile 重试）
```

自愈时间线（实测 web3-blitz）：

```
0s  手动 kubectl delete deployment wallet-service
1s  新 pod Pending → Init:0/2（wait-postgres）
2s  Init:1/2（wait-etcd）
4s  Running
10s 1/1 Ready ✅
```

---

## graceful shutdown

```go
var wg sync.WaitGroup
wg.Add(1)
go reconciler.Start(ctx, &wg)

<-sig   // 等待 SIGTERM/SIGINT
cancel()
wg.Wait()  // 等当前 reconcile 跑完再退出
```

收到信号后等待当前 reconcile 完成（包括可能正在跑的 `helm rollback --wait`），不强杀。

---

## 镜像构建

controller 镜像需要包含三个二进制：`dtk`、`kubectl`、`helm`。

```bash
cd ~/dev-toolkit
docker build --no-cache \
  -f build/docker/controller/Dockerfile \
  -t your-registry/myapp-controller:v1.0.0 .
docker push your-registry/myapp-controller:v1.0.0
```

⚠️ 必须加 `--no-cache`，否则代码改动不会进镜像。

---

## 启用步骤

1. 构建 controller 镜像（见上）
2. 编辑 `deployments/<n>/values.yaml`：
   ```yaml
   controller:
     enabled: true
     image:
       repository: your-registry/myapp-controller
       tag: v1.0.0
   ```
3. `dtk deploy`

---

## 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `PROJECT_NAME` | helm release 名 | 同项目名 |
| `KUBE_NAMESPACE` | namespace | 同 PROJECT_NAME |
| `ETCD_ENDPOINTS` | etcd 地址 | `etcd:2379` |
| `RESOURCES_CONFIG` | resources.yaml 路径 | `/etc/controller/resources.yaml` |
| `KUBE_CONFIG` | kubeconfig 路径 | 空（使用 pod ServiceAccount） |
| `VERSION` | 当前版本号 | `latest` |

---

## RBAC 权限

默认全量权限，上线前按需收紧：

```yaml
# 当前为全量权限，上线前请按实际需要收紧
rules:
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["*"]
```

最小权限参考：
```yaml
rules:
  - apiGroups: ["apps"]
    resources: ["deployments", "statefulsets"]
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources: ["secrets"]  # helm release secrets
    verbs: ["get", "list", "delete"]
```

---

## 已知限制

| # | 问题 | 状态 |
|---|------|------|
| 1 | etcd 断线恢复后重连 | ✅ 已实现指数退避重连 |
| 2 | controller 和 dtk deploy 并发触发 pending-rollback | ✅ dtk deploy 前置检查自动处理 |
| 3 | controller 层 helm rollback 受 SSA 冲突影响 | 🚧 待补 |
