# SNAPSHOT — dev-toolkit

**里程碑**：A2 Reconciliation Controller 方案完整落地
**日期**：2026-03-25
**版本**：v0.4.1

---

## 本次完成

- 独立 `web3-blitz-controller` Deployment（同一个 Helm Chart）
- `configs/resources.yaml` 配置化资源监控
- Reconciliation Loop（etcd Watch + 8秒定期 Reconcile）
- 自动自愈机制（helm upgrade → 失败后自动 rollback）
- `internal/controller/` 完整包实现
- `DetectResourceExists` 方法支持 Deployment/StatefulSet
- 状态机设计文档更新

---

## 测试验证

- 手动删除 deployment 后，controller 自动检测并自愈
- controller pod 与 wallet-service 完全解耦
- etcd 通信稳定

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
└── SNAPSHOT-dtk-2026-03-25-reconciliation-controller   ← 本次
```