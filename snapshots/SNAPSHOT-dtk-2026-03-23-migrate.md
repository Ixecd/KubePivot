# SNAPSHOT — kubepivot

**里程碑**：golang-migrate 支持
**日期**：2026-03-23

---

## 本次完成

### 新增 `internal/scaffold/migration.go`
- `writeMigrationSkeleton(outputDir, name)` — 生成项目时自动创建：
  - `internal/db/migrations/000001_<n>_init.up.sql` 骨架
  - `internal/db/migrations/000001_<n>_init.down.sql` 骨架
  - `internal/db/connect.go` — 内置 golang-migrate 自动迁移
- `InitProject` 里 `writeInternalSkeleton` 之后调用

### 生成的 connect.go 特性
- 用 `golang-migrate/migrate/v4` 替代手写 DDL
- 启动时自动执行所有未执行的迁移
- `MIGRATIONS_PATH` 环境变量可覆盖迁移文件路径（默认 `internal/db/migrations`）
- 迁移版本通过 `schema_migrations` 表追踪，幂等安全

---

## 背景：为什么要加这个

web3-blitz 开发中踩坑：
- pgx v5 默认 `search_path` 为空，`docker exec psql` 建的表 Go 程序找不到
- 手动建表容易漏、容易建错库
- 上线时需要手动执行 SQL，不可靠

golang-migrate 解决所有这些问题：启动自动跑，版本化管理，回滚有保障。

---

## 使用方式（生成的项目）

```bash
# 新增一张表
# 1. 创建迁移文件
touch internal/db/migrations/000002_add_orders.up.sql
touch internal/db/migrations/000002_add_orders.down.sql

# 2. 写 SQL，重启服务自动执行
# 不需要手动跑任何命令
```

---

## go get 列表更新

新增：
- `github.com/golang-migrate/migrate/v4`
- `github.com/golang-migrate/migrate/v4/database/postgres`
- `github.com/golang-migrate/migrate/v4/source/file`

---

## 文件变动清单

```
新增：
- internal/scaffold/migration.go
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
├── SNAPSHOT-dtk-2026-03-23-slog.md
├── SNAPSHOT-dtk-2026-03-23-error-handling.md
└── SNAPSHOT-dtk-2026-03-23-migrate.md   ← 本次
```
