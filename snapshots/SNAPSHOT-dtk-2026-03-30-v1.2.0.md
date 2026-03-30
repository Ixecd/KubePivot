# SNAPSHOT — kubepivot

**里程碑**：v1.2.0 安全合规基线
**日期**：2026-03-30
**版本**：v1.2.0

---

## 本轮完成（v1.1.0 → v1.2.0）

### dtk doctor 安全检查

新增 `cmd/dtk/doctor_security.go`，四个安全检查项：

```
✓/✗ 明文密码        扫描 deployments/ 下所有 values.yaml
✓/⚠ Pod 安全上下文  检查 deployment/statefulset 是否含 securityContext
✓/⚠ RBAC 权限      检查 Role 是否含 * 通配符
✓/⚠ Network Policy  检查是否存在 NetworkPolicy 模板
```

### dtk init 模板强化

业务服务 chart 天生合规：

- **Pod Security Context**：runAsNonRoot/readOnlyRootFilesystem/allowPrivilegeEscalation/capabilities
- **Network Policy**：默认拒绝入站，只允许同 namespace + 业务端口
- **资源 limits**：requests(100m/128Mi) + limits(500m/512Mi)
- **etcd chart**：从 Deployment+emptyDir 升级到 StatefulSet+PVC

### Secret 全生命周期管理

- `dtk init` 自动生成 `scripts/create-secret.sh`（幂等，从 .env 读）
- `dtk deploy` 前扫描 `deployments/` 下所有 `secretKeyRef`，检查 secret 是否存在，缺失则警告
- controller RBAC 从全权限收紧到最小权限（secrets/deployments/statefulsets/pods）

### bug 修复

- **dtk resume**：检测到 IDLE 后 ForceState(IDLE) 再重新部署，删除多余裸 executeDeploy 调用
- **deploy.mk**：用本地 `docker image inspect` 替代远端 `docker manifest inspect`，网络抖动不再误触发 push

---

## 快照归档

```
snapshots/
├── SNAPSHOT-dtk-2026-03-29-v1.0.0-final.md
├── SNAPSHOT-dtk-2026-03-30-v1.1.0.md
├── SNAPSHOT-dtk-2026-03-30-v1.2.0.md  ← 本次
└── SNAPSHOT-dtk-2026-03-30-v1.3.0.md
```
