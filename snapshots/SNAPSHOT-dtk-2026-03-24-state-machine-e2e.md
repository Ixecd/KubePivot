# SNAPSHOT — kubepivot

**里程碑**：部署状态机 e2e 全链路验证通过
**日期**：2026-03-24
**版本**：v0.3.3

---

## 本次完成

### 状态机核心

```
internal/state/
├── state.go      # FSM、转换表、DeployRecord、历史记录
├── store.go      # etcd 优先 + 本地文件降级
├── validator.go  # 所有 pod Ready + kubectl exec healthz 200
└── state_test.go # 15 个单元测试，全绿
```

### 新增命令

```
cmd/dtk/
├── deploy.go   # runDeploy / runResume / runRollback（接入状态机）
└── runner.go   # kubectl/helm 辅助函数
```

### web3-blitz 配合改动

- `mux.go` 加 `/healthz` 路由（wallet-service 原来没有，validator 无法检查）
- `wallet-service-deployment.yaml` probe 改为 `/healthz`（原来是 `/metrics`）
- Dockerfile 加 `wget`（alpine 镜像默认没有，kubectl exec 检查需要用）

---

## e2e 验证记录

```json
// ~/.dtk/state/web3-blitz/web3-blitz.json 关键节点
v0.1.4: VALIDATING → ROLLING_BACK（3次，healthz 不存在导致超时）
v0.1.5: VALIDATING → RUNNING（加了 /healthz 后一次通过）
```

完整状态转换链：
```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → ROLLING_BACK → RUNNING
     → INITIALIZING → DEPLOYING → VALIDATING → ROLLING_BACK → RUNNING
     → INITIALIZING → DEPLOYING → VALIDATING → ROLLING_BACK → RUNNING
     → INITIALIZING → DEPLOYING → VALIDATING → RUNNING ✅ (v0.1.5)
     → INITIALIZING → DEPLOYING → VALIDATING → RUNNING ✅ (再次部署)
```

---

## 已修复问题

| 问题 | 根因 | 修复 |
|------|------|------|
| healthz 404 | wallet-service 没有 `/healthz` 路由 | mux.go 加路由 |
| healthz 连不上 | pod IP 是集群内部地址，本机无法直连 | 改用 `kubectl exec` |
| scale/resource 报错 | `kubectlArgs` 返回不含 kubectl，但又传给 `runCmd("kubectl", ...)` 导致重复 | 改用 `runOutput` 直接执行 |
| validator 卡死 | port 写死 8080，wallet-service 是 2113 | `ValidateDeployment` 加 port 参数 |

---

## 文件变动清单

```
新增（kubepivot）：
- internal/state/state.go
- internal/state/store.go
- internal/state/validator.go
- internal/state/state_test.go
- cmd/dtk/deploy.go
- cmd/dtk/runner.go
- docs/design/state-machine.md

修改（kubepivot）：
- cmd/dtk/main.go      ← 加 resume/rollback case，printUsage 更新
- cmd/dtk/main.go      ← scaleDeployment/setDeploymentResources 改用 runOutput

新增（web3-blitz）：
- /healthz 路由（mux.go）

修改（web3-blitz）：
- deployments/web3-blitz/templates/wallet-service-deployment.yaml ← probe 改 /healthz
- build/docker/wallet-service/Dockerfile ← 加 wget
```

---

## 历史快照

```
snapshots/
├── SNAPSHOT-dtk-2026-03-23-slog.md
├── SNAPSHOT-dtk-2026-03-23-error-handling.md
├── SNAPSHOT-dtk-2026-03-23-migrate.md
├── SNAPSHOT-dtk-2026-03-24-helm-self-contained.md
├── SNAPSHOT-dtk-2026-03-24-kubeconfig.md
├── SNAPSHOT-dtk-2026-03-24-state-machine.md     ← 本次
└── SNAPSHOT-dtk-2026-03-24-state-machine-e2e.md ← 本次
```
