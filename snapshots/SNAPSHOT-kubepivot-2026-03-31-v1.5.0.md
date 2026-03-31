# SNAPSHOT — KubePivot

**里程碑**：v1.5.0 StatefulSet 状态同步 🏆
**日期**：2026-03-31
**版本**：v1.5.0

---

## 本轮完成（v1.4.0 → v1.5.0）

### etcd 健康监控（kp doctor）

| 检查项 | 说明 |
|--------|------|
| etcdctl 安装 | 版本检测，未安装时给出安装命令 |
| etcd 连通性 | `etcdctl endpoint health`，过滤 ETCDCTL_* 环境变量冲突 |
| raft index 差值 | diff > 1000 warn，diff > 10000 critical，预示脑裂或 IO 压力 |
| DB 磁盘使用率 | 超 80% warn，超 90% error，给出 defrag 命令 |
| VolumeSnapshot CRD | 未安装时 warn，给出安装命令 |
| VolumeSnapshotClass | 无可用 class 时 warn，给出配置引导 |

### StatefulSet 部署支持（kp deploy）

- `deployService` rollout：根据 `plan.Type` 路由到 `statefulset/` 或 `deployment/`
- `buildHelmArgs`：StatefulSet 类型跳过 `replicaCount --set`，由 StatefulSet spec 管理
- 修复：replicaCount 被设置两次的 bug

### StatefulSet 状态展示（kp status）

- 展示 StatefulSet 整体状态（期望/就绪/已更新副本数）
- 展示每个 Pod 的 ordinal/ready/version/phase
- version 优先从 Pod label 读，其次从镜像 tag 提取
- 按 ordinal 升序排列
- 修复：ANSI 颜色码导致列对齐错乱（手动计算 padding）

### PVC 备份骨架（kp pvc）

- `kp pvc backup/restore/list` 命令骨架
- 完整设计 TODO 注释（发现/命名/恢复流程/展示字段）
- 标注需要 CSI VolumeSnapshot 环境，v1.5.1 完整实现

---

## 验证记录

| 功能 | 验证结果 |
|------|---------|
| `kp doctor` etcd 检查 | raftIndex=48161, diff=0, 0.0MB ✅ |
| `kp doctor` VolumeSnapshot | local-path 正确识别为不支持 ✅ |
| `kp status` StatefulSet | postgres-0 (16-alpine) + etcd-0 (v3.5.14) ✅ |
| `kp pvc` 骨架 | 正确提示 CSI 不可用 ✅ |

---

## 快照归档

```
snapshots/
├── SNAPSHOT-dtk-2026-03-29-v1.0.0-final.md
├── SNAPSHOT-dtk-2026-03-30-v1.1.0.md
├── SNAPSHOT-dtk-2026-03-30-v1.2.0.md
├── SNAPSHOT-dtk-2026-03-30-v1.3.0.md
├── SNAPSHOT-kubepivot-2026-03-31-v1.4.0-wip.md
├── SNAPSHOT-kubepivot-2026-03-31-v1.4.0.md
└── SNAPSHOT-kubepivot-2026-03-31-v1.5.0.md  ← 本次
```
