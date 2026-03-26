# SNAPSHOT — dev-toolkit

**里程碑**：controller 包稳定化 + state 包彻底解耦
**日期**：2026-03-27
**版本**：v0.4.1

---

## 本次完成

### 架构修正

上一个 commit（Grok）将 `DetectResourceExists` / `DetectActualStateFromResources` 重新写入了 `state` 包，虽然换用 kubectl CLI 避开了 client-go，但 state 包仍然知道如何查询 K8s 资源，与 a2-refactor 的核心判断相悖。本次将其彻底移出。

**职责边界最终确认**：

| 包 | 职责 |
|---|---|
| `internal/state` | 纯 FSM：状态、转换表、持久化、`ResumeFromValidating`、`EtcdKey` |
| `internal/controller` | K8s 检测 + 自愈：`DetectResourceExists`、`DetectActualState`、`checkAndHeal` |
| `cmd/dtk` | CLI 入口，引用两个包 |

### 修复清单

**internal/controller/resources.go**
- `DetectResourceExists` / `DetectActualState` 从 state 包移入，正式归属 controller 包
- `LoadResources` 签名修复：接收 `path string` 参数，空字符串时降级到环境变量 `RESOURCES_CONFIG`，再降级到 `configs/resources.yaml`
- `Resource.OnMissing` yaml tag 统一为 `on-missing`（与 resources.yaml 示例对齐）

**internal/controller/heal.go**
- `getLatestRevision` 修复：去掉 `--max 1`，改为取 `history[len(history)-1]`，正确返回最新 revision
- `healRecreate` rollback 目标改为 `revision - 1`（原代码 rollback 到自身，无意义）
- `r.sm.Transition` 返回值不再丢弃，失败时记录 slog.Error
- `OnMissing` case 统一为 `"auto-heal"`（原为 `"recreate"`）
- `alert` case 补全 namespace 日志字段
- `resourceExists` 死代码删除（`checkAndHeal` 已改用包级 `DetectResourceExists`）

**internal/controller/reconciler.go**
- `Start` 接收 `*sync.WaitGroup` 参数，退出前调用 `wg.Done()`

**internal/controller/controller.go**
- `KUBECONFIG` → `KUBE_CONFIG`，与项目其他地方统一
- graceful shutdown：`time.Sleep(2s)` → `wg.Wait()`，等当前 reconcile 跑完再退出
- `NewReconciler` 传入 `kubeconfig` 字段

**cmd/dtk/deploy.go**
- `runResume` 改用 `controller.DetectActualState`，补全错误处理

---

## 已知遗留

| # | 问题 | 优先级 |
|---|------|--------|
| 1 | `startEtcdWatcher` goroutine 未纳入 WaitGroup，强制退出时不受控 | P1 |
| 2 | helm pending-rollback 死锁仍需手动清理 | P1 |
| 3 | `resources.yaml` 为空时 `DetectActualState` 直接报错，策略待定 | P1 |

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
├── SNAPSHOT-dtk-2026-03-25-full.md
└── SNAPSHOT-dtk-2026-03-27-controller-stabilization.md  ← 本次
```
