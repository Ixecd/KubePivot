# kp sandbox

带迁移原子性和蓝绿流量切换的安全部署。五阶段状态机：LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING。

## 用法

```
kp sandbox <子命令>
```

## 子命令

### start

启动沙盒会话。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--namespace` | (project.env) | K8s namespace |
| `--context` | (project.env) | kube context |
| `--kubeconfig` | (project.env) | kubeconfig 路径 |
| `--dry-run` | `false` | 只打印执行计划 |
| `--from-env` | | 从指定 env 读取已验证 traffic 配置（v2.6.1） |

**阶段流转**：

```
1. LOCKED       → 获取分布式锁，阻止其他 kp deploy
2. SNAPSHOTTING → 触发 PVC 快照（有 CSI 才执行，无 CSI 跳过）
3. SIMULATING   → 预跑 DB 迁移（postgres 事务 DDL，失败自动回滚）
4. COMMITTING   → 真实 migrate + helm upgrade + 蓝绿流量切换
5. RUNNING      → 成功，释放锁
```

**失败处理**：任何阶段失败 → RESTORING → PVC 快照恢复 + helm rollback → IDLE。

**COMMITTING 阶段禁止 force-unlock**。

### status

查看当前沙盒状态。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--namespace` | (project.env) | K8s namespace |

### unlock

强制解锁（COMMITTING 阶段仍然禁止）。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--namespace` | (project.env) | K8s namespace |
| `--reason` | (必填) | 解锁原因 |
| `--force` | `false` | 强制解锁确认 |

## RBAC

`PermSandbox`

## 相关命令

- `kp deploy` — 普通部署（不走 sandbox）
- `kp pvc backup/restore` — 手动 PVC 快照
- `kp migrate run` — 手动 DB 迁移
- `kp rollback` — 手动回滚
