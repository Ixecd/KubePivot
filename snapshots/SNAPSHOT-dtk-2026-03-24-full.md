# SNAPSHOT — kubepivot

**里程碑**：今日全部 P0 收官 + CI 修复
**日期**：2026-03-24
**版本**：v0.4.0

---

## 今日完成汇总

### v0.3.1 — scaffold 拆分
- 2136 行 `scaffold.go` 拆成 6 个文件：`scaffold.go` / `skeleton.go` / `helm.go` / `migration.go` / `monitoring.go` / `frontend.go`
- `internal/` 目录清理：删除空目录 `auth/` `api/` `pkg/codegen/`，`ai/` → `planner/`

### v0.3.2 — kubeconfig 多集群支持
- `--kubeconfig` flag，优先级：CLI > project.env `KUBE_CONFIG` > `~/.kube/config`
- `deploy.mk` 加 `KUBE_CONFIG` → `--kubeconfig` 注入
- `project.env` 模板加 `KUBE_CONFIG=`
- `docs/guide/zh-CN/kubeconfig.md`

### v0.3.3 — 部署状态机 e2e 验证
- `internal/state/`：FSM + etcd/本地文件持久化 + VALIDATING 验证
- `cmd/dtk/deploy.go`：runDeploy / runResume / runRollback
- `cmd/dtk/runner.go`：kubectl/helm 辅助函数
- 15 个单元测试全绿
- e2e：v0.1.4 三次自动回滚，v0.1.5 一次通过
- `docs/design/state-machine.md`

### v0.3.4 — Bug 修复 + 集成测试
- `is_first` 判断：`namespaceExists` → `helmReleaseExists`（helm 自动创建 namespace 导致误判）
- `--dry-run` 输出完整 make 命令和环境变量
- `cmd/dtk/deploy_test.go`：10 个集成测试

### v0.4.0 — dtk release
- `cmd/dtk/release.go`：semver 校验、工作区检查、tag 重复检查、更新 project.env、git commit + tag + push、`--deploy` 可选触发部署
- `cmd/dtk/release_test.go`：8 个测试全绿
- `docs/design/release.md`

### CI 修复
- `release_test.go`：加 `setupGitRepo()` 辅助函数，在临时仓库里配置 git user，解决 CI 环境无全局 git config 导致测试失败
- `.github/workflows/ci.yml`：加 Configure git、Install kubectl、Install helm

---

## 已修复 Bug 清单

| Bug | 根因 | 修复 |
|-----|------|------|
| healthz 404 | wallet-service 没有 `/healthz` 路由 | mux.go 加路由 |
| healthz 连不上 | pod IP 是集群内部地址 | 改用 `kubectl exec` |
| scale/resource 报错 | kubectl 参数重复 | 改用 `runOutput` |
| validator port 写死 8080 | wallet-service 是 2113 | 加 port 参数 |
| is_first 误判 | helm `--create-namespace` 自动建 ns | 改用 `helmReleaseExists` |
| CI git commit 失败 | CI 无全局 git user config | 测试里配置临时 git user |

---

## 测试覆盖总览

```
go test ./...  全绿

cmd/dtk              18 个测试（deploy + release）
internal/state       15 个状态机测试
internal/scaffold    scaffold 单元测试
test/integration     e2e 集成测试
```

---

## 文档更新

```
docs/
├── design/
│   ├── state-machine.md   ← 新增
│   └── release.md         ← 新增
└── guide/zh-CN/
    ├── helm.md            ← 新增
    └── kubeconfig.md      ← 新增
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
├── SNAPSHOT-dtk-2026-03-24-release.md
└── SNAPSHOT-dtk-2026-03-24-full.md   ← 本次
```
