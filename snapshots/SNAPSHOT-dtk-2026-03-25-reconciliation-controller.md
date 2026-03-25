# SNAPSHOT — dev-toolkit

**里程碑**：A2 Reconciliation Controller 完成，状态机架构全面升级
**日期**：2026-03-25
**版本**：v0.4.1

---

## 本次完成

### 核心：独立控制器 Pod

```
internal/controller/
├── controller.go     # 主入口，dtk controller start
├── reconciler.go     # Reconciliation Loop（etcd Watch + 8s 周期）
├── resources.go      # 加载 configs/resources.yaml
├── etcd_watcher.go   # etcd 实时 Watch
└── heal.go           # 自动自愈 + 回滚兜底
```

Controller 作为独立 Deployment 运行在 K8s 里，生命周期由 K8s 管理，不再依赖 CLI 进程。

### 配置驱动资源监控

新增 `configs/resources.yaml`，声明需要监控的 K8s 资源：

```yaml
resources:
  - kind: Deployment
    name: wallet-service
    on_missing: recreate
    max_retry: 3
    fallback: rollback
```

新增监控资源只改配置，不改代码。

### 状态机核心增强

- `EtcdKey()` 导出函数，供 Controller 读写状态
- `DetectResourceExists(kind, name)` 支持 Deployment / StatefulSet 存在性检查
- SSA 冲突自动处理（升级前自动清除 managedFields）

### 文档

- `docs/design/state-machine.md` 重写（A2 完整方案）
- `docs/design/reconciliation-controller.md` 新增

---

## A1 → A2 架构对比

| 维度 | A1 | A2 |
|------|----|----|
| Loop 生命周期 | 和 CLI 进程绑定 | K8s 管理，自动重启 |
| dtk deploy | 阻塞终端 | 立即返回 |
| SSA 冲突 | 手动清除 | 自动处理 |
| 故障恢复 | dtk resume | 自动自愈 |
| 扩展性 | 硬编码 | 配置驱动 |

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
└── SNAPSHOT-dtk-2026-03-25-reconciliation-controller.md ← 本次
```
