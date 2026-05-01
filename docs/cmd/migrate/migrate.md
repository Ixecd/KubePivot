# kp migrate

数据库迁移管理。支持 golang-migrate 和 atlas。

## 用法

```
kp migrate <子命令>
```

## 子命令

### status

查看迁移状态。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--database-url` | (env `KP_DATABASE_URL` 或 `.env`) | 数据库连接串 |
| `--service` | | 只检查指定服务 |
| `--migration-tool` | `auto` | `golang-migrate` / `atlas` / `auto` |

### plan

预览待执行的迁移。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--database-url` | (同上) | 数据库连接串 |
| `--migration-tool` | `auto` | 迁移工具 |

### run

执行迁移。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--database-url` | (同上) | 数据库连接串 |
| `--migration-tool` | `auto` | 迁移工具 |
| `--migrations-dir` | (自动探测) | 迁移文件目录 |
| `--target` | `-1`（最新） | 目标版本号 |
| `--dry-run` | `false` | 预览不执行 |
| `--full-sql` | `false` | dry-run 时打印完整 SQL |

### fix-dirty

修复脏状态。

## RBAC

`PermMigrate`（run）。status / plan / fix-dirty 是读/修复操作，不加 RBAC。

## 相关命令

- `kp sandbox start` — 带迁移原子性的安全部署
- `kp compat check` — API 兼容性检测
