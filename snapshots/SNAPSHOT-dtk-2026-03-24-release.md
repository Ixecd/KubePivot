# SNAPSHOT — dev-toolkit

**里程碑**：dtk release 命令完成，今日全部 P0 收官
**日期**：2026-03-24
**版本**：v0.4.0

---

## 本次完成

### dtk release

```bash
dtk release --version v1.0.0           # 打 tag 发布
dtk release --version v1.0.0 --deploy  # 打完 tag 直接部署
dtk release --version v1.0.0 --no-push # 只打本地 tag 不推送
```

完整流程：
1. 校验 version 格式（必须符合 `v{major}.{minor}.{patch}`）
2. 检查工作区干净（有未提交改动直接报错）
3. 检查 tag 是否已存在（防止重复打）
4. 更新 `configs/project.env` 里的 `VERSION=`
5. `git add configs/project.env`
6. `git commit -m "chore: release v1.0.0"`
7. `git tag -a v1.0.0 -m "release v1.0.0"`
8. `git push` + `git push --tags`
9. 可选：触发 `dtk deploy`

### 测试

```
cmd/dtk/release_test.go — 8 个测试全绿
  TestSemverPattern              版本号格式校验
  TestUpdateVersion_ExistingKey  已有 VERSION 行时更新
  TestUpdateVersion_NoExistingKey 无 VERSION 行时追加
  TestUpdateVersion_PreservesComments 注释行保留
  TestTagExists_NotFound         不存在的 tag 返回 false
  TestTagExists_Found            已存在的 tag 返回 true
  TestCheckCleanWorkspace_Clean  干净工作区通过
  TestCheckCleanWorkspace_Dirty  有未提交改动报错
```

---

## 今日完成汇总（2026-03-24）

### 状态机（v0.3.3）
- `internal/state/` — FSM + etcd/本地文件持久化 + VALIDATING 验证
- `cmd/dtk/deploy.go` — runDeploy/runResume/runRollback
- 15 个单元测试全绿
- e2e 验证：v0.1.4 三次自动回滚，v0.1.5 一次通过

### Bug 修复（v0.3.4）
- `is_first` 判断：改用 `helmReleaseExists` 替代 `namespaceExists`
- `--dry-run` 输出完整 make 命令和环境变量
- 集成测试：10 个测试覆盖核心逻辑

### dtk release（v0.4.0）
- semver 格式强制校验
- 工作区干净检查
- 自动更新 project.env VERSION
- git commit + tag + push 一体化
- `--deploy` 可选触发部署

### 多集群支持（v0.3.2）
- `--kubeconfig` flag
- `KUBE_CONFIG` 写入 project.env 模板
- deploy.mk 加 `--kubeconfig` 注入

### scaffold 拆分（v0.3.1）
- 2136 行 scaffold.go 拆成 6 个文件
- internal 目录清理：删空目录，ai → planner

---

## 测试覆盖汇总

```
go test ./...
ok  cmd/dtk              — deploy/release/state 集成测试
ok  internal/scaffold    — scaffold 单元测试
ok  internal/state       — 15 个状态机测试
ok  test/integration     — e2e 集成测试
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
├── SNAPSHOT-dtk-2026-03-24-state-machine.md
├── SNAPSHOT-dtk-2026-03-24-state-machine-e2e.md
└── SNAPSHOT-dtk-2026-03-24-release.md   ← 本次
```
