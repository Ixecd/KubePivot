# SNAPSHOT — kubepivot

**里程碑**：部署状态机完成，15 个单元测试全绿
**日期**：2026-03-24

---

## 本次完成

### 状态机设计

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                          ↓              ↓
                    ROLLING_BACK ←───────┘
                          ↓
                       RUNNING（回滚成功）

首次部署失败：DEPLOYING → CLEANING → IDLE
下线：RUNNING → TERMINATED
```

### 新增文件

```
internal/state/
├── state.go      # 状态类型、转换表、DeployRecord、Machine
├── store.go      # Store 接口、etcdStore、localStore、NewAutoStore
├── validator.go  # VALIDATING 验证：所有 pod Ready + healthz 200
└── state_test.go # 15 个单元测试，全部通过

cmd/dtk/
├── deploy.go     # runDeploy/runResume/runRollback（接入状态机）
└── runner.go     # kubectl/helm 辅助函数（原 internal/state/runner.go）
```

### 状态机特性

**持久化**：
- etcd 优先（key: `dtk/<project>/<ns>/state`）
- 无 etcd 自动降级到 `~/.dtk/state/<project>/<ns>.json`

**新增命令**：
- `dtk resume` — 检查 K8s 实际状态后决定从哪个阶段恢复
- `dtk rollback` — 手动触发 helm rollback，状态回到 RUNNING

**错误处理**：
- 首次部署失败 → 自动清理 namespace → 状态回 IDLE
- 更新失败 → 自动 helm rollback → 状态回 RUNNING
- VALIDATING 超时 → 自动回滚
- 部署中被中断 → `dtk resume` 恢复

### 测试覆盖（15/15）

| 测试名 | 验证点 |
|--------|--------|
| TestTransition_HappyPath | 正常部署全链路 |
| TestTransition_IllegalTransition | 非法转换被拒绝，状态不变 |
| TestTransition_FirstDeployFailure | 首次失败 → CLEANING → IDLE |
| TestTransition_UpdateFailure | 更新失败 → ROLLING_BACK → RUNNING |
| TestTransition_ValidatingTimeout | 验证超时 → ROLLING_BACK → RUNNING |
| TestTransition_History | 每次转换追加历史记录 |
| TestTransition_TerminatedIsTerminal | TERMINATED 是终态 |
| TestTransition_DuplicateDeploy | 重复部署被拒绝 |
| TestLocalStore_SaveAndLoad | 本地文件存储读写 |
| TestLocalStore_LoadNotFound | 不存在时返回错误 |
| TestLocalStore_Delete | 删除记录 |
| TestLocalStore_DeleteNotFound | 删除不存在记录不报错 |
| TestMachine_PersistsAcrossRestart | 重启后状态恢复 |
| TestMachine_FirstDeployFlag | 首次部署标志持久化 |
| TestMachine_HistoryPersists | 历史记录持久化 |

---

## 历史快照

```
snapshots/
├── SNAPSHOT-dtk-2026-03-23-slog.md
├── SNAPSHOT-dtk-2026-03-23-error-handling.md
├── SNAPSHOT-dtk-2026-03-23-migrate.md
├── SNAPSHOT-dtk-2026-03-24-helm-self-contained.md
├── SNAPSHOT-dtk-2026-03-24-kubeconfig.md
└── SNAPSHOT-dtk-2026-03-24-state-machine.md   ← 本次
```
