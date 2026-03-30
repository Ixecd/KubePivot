# SNAPSHOT — kubepivot

**里程碑**：kubeconfig 支持 + scaffold 拆分 + internal 目录优化
**日期**：2026-03-24

---

## 本次完成

### 1. `--kubeconfig` 支持
- `dtk deploy --kubeconfig ~/.kube/prod.yaml` 指定 kubeconfig 文件
- 优先级：命令行 `--kubeconfig` > `project.env` 的 `KUBE_CONFIG` > 默认 `~/.kube/config`
- `deploy.mk` 的 `KUBECTL_FLAGS` / `HELM_FLAGS` 同步加 `--kubeconfig`
- `scaffold.go` 生成的 `project.env` 模板加 `KUBE_CONFIG=`
- `kubectlArgs()` 辅助函数统一构建 kubectl 参数，消除重复

### 2. scaffold.go 拆分（2136 行 → 5 个文件）
```
internal/scaffold/
├── scaffold.go      # InitProject 主流程 + 工具函数
├── skeleton.go      # write*Skeleton 系列小函数
├── helm.go          # writeHelmTemplateSkeleton
├── migration.go     # writeMigrationSkeleton
├── monitoring.go    # writeMonitoringSkeleton
└── frontend.go      # writeFrontendSkeleton
```

### 3. internal 目录优化
```
删除：
- internal/auth/     空目录
- internal/api/      三个空文件（package api）
- internal/pkg/codegen/  空文件

重命名：
- internal/ai/ → internal/planner/
- internal/ai/ai.go → internal/planner/planner.go
```

---

## 文件变动清单

```
新增：
- internal/scaffold/skeleton.go
- internal/scaffold/frontend.go
- internal/scaffold/monitoring.go
- docs/guide/zh-CN/kubeconfig.md

修改：
- cmd/dtk/main.go         ← --kubeconfig flag + kubectlArgs()
- scripts/make-rules/deploy.mk  ← KUBE_CONFIG + --kubeconfig flags
- internal/scaffold/scaffold.go ← project.env 模板加 KUBE_CONFIG=

删除：
- internal/auth/
- internal/api/
- internal/pkg/codegen/
- internal/scaffold/* 中已迁移的函数
```

---

## 历史快照

```
snapshots/
├── SNAPSHOT-dtk-2026-03-23-slog.md
├── SNAPSHOT-dtk-2026-03-23-error-handling.md
├── SNAPSHOT-dtk-2026-03-23-migrate.md
├── SNAPSHOT-dtk-2026-03-24-helm-self-contained.md
└── SNAPSHOT-dtk-2026-03-24-kubeconfig.md   ← 本次
```
