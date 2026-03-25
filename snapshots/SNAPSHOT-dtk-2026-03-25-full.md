# SNAPSHOT — dev-toolkit

**里程碑**：今日全部工作汇总
**日期**：2026-03-25
**版本**：v0.4.1

---

## 今日完成

### 1. A2 Controller 代码审查 + state 包解耦（v0.4.1）

Grok 写的初版 controller 代码有 9 个问题，全部修复：

**最严重**：`internal/state` 包引入了 `k8s.io/client-go`，职责越界。
state 包只负责状态机逻辑，不应该知道如何查询 K8s 资源。

**修复清单**：
- `state.go`：删除 `DetectResourceExists`、`NewForDetect`，删除所有 k8s.io import
- `state/detect.go`：整个删除
- `state/resources.go`：整个删除（硬编码了 web3-blitz 服务名，不应存在于通用工具）
- `controller/heal.go`：`resourceExists` 改用 `kubectl` 命令行，保持风格一致
- `controller/controller.go`：项目名/namespace 从环境变量读，不再硬编码
- `controller/resources.go`：加 `Namespace` 字段，路径支持环境变量覆盖
- `controller/reconciler.go`：log → slog，LoadResources 失败降级而非 Fatal
- `controller/etcd_watcher.go`：endpoints Split，加 DialTimeout，log → slog
- `deploy.go`：`detectActualState` 从 plan 读服务名，不再硬编码

### 2. Controller 部署到 K8s 并验证自愈

- `build/docker/controller/Dockerfile`：基于 dtk 二进制，内含 kubectl + helm
- `cmd/dtk/main.go`：加 `controller start` 子命令
- `deployments/web3-blitz/templates/`：controller-deployment + rbac + resources-configmap
- `heal.go`：自愈逻辑改用 `helm rollback revision=latest`
- `deploy.mk`：`--set global.version=$(VERSION) --set global.arch=$(ARCH)` 让 helm 存正确版本

**验证结果**：手动删除 wallet-service，controller 8 秒内检测到，自动 rollback 恢复，
wallet-service v0.1.10 正常启动，`1/1 Running`。

### 3. scaffold 模板更新

`project.env` 模板加 `ARCH=arm64` 和 `VERSION=v0.1.0`、`ETCD_ENDPOINTS=`。

---

## 文件变动清单

```
dev-toolkit 修改：
- internal/state/state.go          ← 删 k8s 相关，加 EtcdKey/ResumeFromValidating
- internal/state/detect.go         ← 删除
- internal/state/resources.go      ← 删除
- internal/controller/controller.go ← 环境变量驱动
- internal/controller/heal.go      ← kubectl CLI 检测，helm rollback 自愈
- internal/controller/reconciler.go ← slog，降级处理
- internal/controller/etcd_watcher.go ← Split endpoints，DialTimeout
- internal/controller/resources.go ← 加 Namespace 字段
- cmd/dtk/main.go                  ← 加 controller start case
- cmd/dtk/deploy.go                ← detectActualState 从 plan 读
- internal/scaffold/scaffold.go    ← project.env 模板加 ARCH/VERSION/ETCD_ENDPOINTS
- build/docker/controller/Dockerfile ← 新增

web3-blitz 修改：
- deployments/web3-blitz/templates/controller-deployment.yaml ← 新增
- deployments/web3-blitz/templates/controller-rbac.yaml       ← 新增
- deployments/web3-blitz/templates/resources-configmap.yaml   ← 新增
- deployments/web3-blitz/values.yaml                         ← 加 global + controller
- deployments/web3-blitz/templates/wallet-service-deployment.yaml ← 镜像用 global.version
- configs/resources.yaml                                      ← 新增
- scripts/make-rules/deploy.mk                                ← 传 global.version/arch
```

---

## 遗留 Bug

| # | 问题 | 项目 | 优先级 |
|---|------|------|--------|
| 1 | controller rollback 后状态机不同步 | dtk + web3-blitz | P0 |
| 2 | resumeFromValidating 不检查转换合法性 | dtk | P0 |
| 3 | detectActualState deployment 被删后返回 DEPLOYING | dtk | P0 |
| 4 | helm pending-rollback 死锁需手动清理 | web3-blitz | P1 |
| 5 | controller RBAC 权限过宽 | web3-blitz | P1 |
| 6 | controller 镜像未和 dtk release 联动 | dtk | P1 |
| 7 | ADMIN_EMAIL 只在启动时检查，注册后需重启 | web3-blitz | P1 |

---

## 历史快照

```
snapshots/
├── SNAPSHOT-dtk-2026-03-23-slog.md
├── SNAPSHOT-dtk-2026-03-23-error-handling.md
├── SNAPSHOT-dtk-2026-03-23-migrate.md
├── SNAPSHOT-dtk-2026-03-24-helm-self-contained.md
├── SNAPSHOT-dtk-2026-03-24-kubeconfig.md
├── SNAPSHOT-dtk-2026-03-24-state-machine.md
├── SNAPSHOT-dtk-2026-03-24-state-machine-e2e.md
├── SNAPSHOT-dtk-2026-03-24-release.md
├── SNAPSHOT-dtk-2026-03-24-full.md
├── SNAPSHOT-dtk-2026-03-25-reconciliation-controller.md
├── SNAPSHOT-dtk-2026-03-25-a2-refactor.md
└── SNAPSHOT-dtk-2026-03-25-full.md   ← 本次
```
