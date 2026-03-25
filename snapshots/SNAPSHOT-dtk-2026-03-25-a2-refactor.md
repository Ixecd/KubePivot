# SNAPSHOT — dev-toolkit

**里程碑**：A2 Controller 代码审查 + state 包解耦重构
**日期**：2026-03-25
**版本**：v0.4.1

---

## 背景

v0.4.0 中由 Grok 实现了 A2 Reconciliation Controller 的初版代码。
经过 code review，发现若干设计问题，本次全部修正。

---

## Grok 初版代码问题清单（批评环节）

**严重问题：**

1. **`internal/state` 包引入了 `k8s.io/client-go`**
   这是本次重构中最严重的问题。`state` 包的职责是状态机逻辑，不应该知道 K8s 怎么查资源。引入 `client-go` 意味着用户安装 dtk CLI 要拉取整个 K8s 客户端依赖，这是完全不必要的。**职责边界不清是架构腐化的根源。**

2. **`state/resources.go` 硬编码了 `wallet-service`、`postgres`**
   dtk 是通用工具，web3-blitz 的服务名出现在 dtk 的核心包里，这是严重的关注点污染。这个文件整个就不应该存在。

3. **`heal.go` 里 namespace 硬编码 `"web3-blitz"`**
   `Resource` 结构体里明明有 `namespace` 字段的设计空间，却在实现里绕过直接硬编码，导致 Controller 只能给 web3-blitz 用。

4. **`heal.go` 里 helm chart 路径硬编码 `"./deployments/web3-blitz"`**
   Controller 跑在 pod 里，相对路径根本不存在，这个代码在生产环境里会直接崩。

**工程质量问题：**

5. **`log` 和 `slog` 混用**
   项目统一用 `slog`，Grok 在 controller 包里全部用了 `log.Println`，风格不一致。

6. **`etcd_watcher.go` 重复创建 etcd 客户端**
   `store.go` 里已经有 etcd 客户端了，`etcd_watcher.go` 又新建了一个，浪费连接资源。

7. **`etcd_watcher.go` endpoints 未做 Split**
   `ETCD_ENDPOINTS` 可能是 `"etcd:2379,etcd2:2379"` 多个地址，直接塞进 `[]string{}` 是错误的，`store.go` 里早就做了 `strings.Split`，同样的问题写了两遍。

8. **`NewForDetect` 是 workaround，不是设计**
   为了绕开 `store` 字段创造了一个残缺的 `Machine`，说明检测逻辑本来就不应该挂在 `Machine` 上，这是设计问题被代码技巧掩盖的典型。

9. **`deploy.go` 里 `deploymentName := "wallet-service"` 依然硬编码**
   `components.yaml` 里有完整的服务列表，从 plan 读一行代码的事，却写死了服务名。

**总结**：Grok 有一个显著的工作习惯问题——**上来就写代码，不先对齐设计**。在没有充分理解项目架构和职责边界的情况下堆代码，导致职责越界、硬编码、重复逻辑同时出现。代码能跑，但可维护性差，扩展性差，生产可用性存疑。

---

## 本次修复内容

### `internal/state/state.go` — 彻底清洁

- 删除 `DetectResourceExists`（职责不属于状态机）
- 删除 `NewForDetect`（workaround，不是设计）
- 删除所有 `k8s.io` import
- 保留 `ResumeFromValidating`（带合法性检查）
- 保留 `EtcdKey`（供 controller 使用）

### `internal/state/detect.go` — 整个删除

K8s 检测逻辑不属于 state 包。

### `internal/state/resources.go` — 整个删除

硬编码 web3-blitz 服务名的文件不应该存在于通用工具里。

### `internal/controller/heal.go` — 重写

- `resourceExists` 改用 `kubectl get` 命令行，和项目风格一致，不引入 client-go
- namespace 从 `res.Namespace` 读，不再硬编码
- helm chart 路径用 `CHART_DIR` 环境变量，无配置时降级直接 rollback
- `log` → `slog`

### `internal/controller/` 其他文件

- `controller.go`：项目名/namespace 从环境变量读，不再硬编码 `"web3-blitz"`
- `resources.go`：加 `Namespace` 字段，路径支持 `RESOURCES_CONFIG` 环境变量
- `reconciler.go`：`log` → `slog`，LoadResources 失败降级而非 Fatal
- `etcd_watcher.go`：`endpoints` 加 `strings.Split`，加 `DialTimeout`，`log` → `slog`

### `cmd/dtk/deploy.go`

- `detectActualState` 从 plan 读服务名，用 kubectl 命令行检测，删掉 `NewForDetect` 和 `DetectResourcesState` 调用
- `runResume` 的 switch 简化为 Running / Idle 两分支

---

## 修复后架构

```
internal/state/       → 纯状态机逻辑，零 K8s 依赖
  state.go            → FSM + 转换表 + EtcdKey
  store.go            → etcd / 本地文件持久化
  validator.go        → pod healthz 验证（kubectl exec）
  state_test.go       → 15 个单元测试

internal/controller/  → 资源检测 + 自愈，K8s 操作全在这里
  controller.go       → 入口，从环境变量读配置
  reconciler.go       → etcd Watch + 8s 周期对账
  etcd_watcher.go     → etcd 实时 Watch
  resources.go        → 配置驱动，支持任意 K8s 资源
  heal.go             → kubectl 检测 + helm 自愈 + rollback 兜底
```

---

## 测试

```
go test ./...  全绿
- cmd/dtk              ✅
- internal/state       ✅（15 个状态机测试）
- internal/scaffold    ✅
- test/integration     ✅
- internal/controller  （待补测试）
```

---

## 历史快照

```
snapshots/
├── ...
├── SNAPSHOT-dtk-2026-03-25-reconciliation-controller.md
└── SNAPSHOT-dtk-2026-03-25-a2-refactor.md  ← 本次
```
