# SNAPSHOT — kubepivot

**里程碑**：引入 slog 结构化日志
**日期**：2026-03-23

---

## 本次完成

### 新增
- `internal/logger/logger.go`（24 行）
  - `LOG_LEVEL=debug` 开启 Debug 级别，默认 Info
  - 默认 **Text 格式写 stderr**（CLI 特化，不污染 stdout 用户输出）
  - `LOG_FORMAT=json` 切换 JSON，用于接入日志收集

### 修改
- `cmd/dtk/main.go`
  - 加 `logger.Init()` 到 `main()` 最顶部
  - import 加 `github.com/Ixecd/kubepivot/internal/logger`

- `internal/scaffold/init.go`
  - import 加 `"log/slog"`
  - `runInDir` 重写：原来 `cmd.Stderr = io.Discard` 导致 git/go 失败时完全看不到原因
  - 现在 stderr 接到 `strings.Builder`，失败时通过 `slog.Debug` 输出原始错误

---

## 设计决策

| 项目 | web3-blitz | kubepivot（本次）|
|------|-----------|-------------------|
| 默认格式 | JSON | Text |
| 写入目标 | stdout | **stderr** |
| 默认开启 JSON | 是 | 否（`LOG_FORMAT=json` 切换）|

**原因**：dtk 是 CLI 工具，stdout 留给用户真正需要看的内容（`✅ 项目已生成`、部署进度）；slog 的内部轨迹全部走 stderr，不干扰脚本管道。

---

## 调试用法

```bash
# 开启 debug，排查 git init / go get 失败原因
LOG_LEVEL=debug dtk init --name demo-svc --module github.com/you/demo-svc

# 输出示例（stderr）：
# time=... level=DEBUG msg=执行命令 cmd=git args=[init] dir=~/demo-svc
# time=... level=DEBUG msg=执行命令 cmd=go args=[get github.com/...] dir=~/demo-svc
# time=... level=DEBUG msg=命令执行失败 cmd=go stderr="dial tcp: connection refused" err="exit status 1"
```

---

## 未改动（有意保留）

- `cmd/dtk/main.go` 里所有 `fmt.Fprintln(os.Stderr, ...)` 错误输出 — CLI 标准做法，不换成 slog
- `internal/scaffold/init.go` 里所有 `fmt.Fprintf(opts.Stdout, ...)` 用户提示 — 同上
- `internal/ai/ai.go` — 无任何日志，不需要改

---

## 文件变动清单

```
新增：
- internal/logger/logger.go

修改：
- cmd/dtk/main.go          ← logger.Init() + import
- internal/scaffold/init.go ← import slog + runInDir 重写
```

---

## 历史快照

```
snapshots/
├── SNAPSHOT-dtk-2026-03-18-scaffold-complete.md
├── SNAPSHOT-dtk-2026-03-18.md
├── SNAPSHOT-dtk-2026-03-19-1.md
├── SNAPSHOT-dtk-2026-03-19-A.md
├── SNAPSHOT-dtk-2026-03-19-B.md
├── SNAPSHOT-dtk-2026-03-19.md
├── SNAPSHOT-dtk-2026-03-20-deploy-e2e(!!!).md
├── SNAPSHOT-dtk-2026-03-20-frontend-skeleton-generic.md
├── SNAPSHOT-dtk-2026-03-20-monitoring.md
├── SNAPSHOT-dtk-2026-03-20-with-frontend.md
└── SNAPSHOT-dtk-2026-03-23-slog.md          ← 本次
```
