# 数据库迁移感知设计

## 概述

`kp migrate` 提供两个能力：
1. **status** — 查看当前 DB 迁移版本和 K8s 部署版本是否对齐
2. **plan** — 分析待执行迁移文件中的破坏性变更，阻断高风险升级

轻量无依赖：不需要两个 DB 连接做 schema diff，只用正则扫描迁移文件 + 一条 SQL 查当前版本。

---

## kp migrate status

```
服务名          迁移工具          DB 迁移版本    K8s 版本    状态
─────────────────────────────────────────────────────────
wallet-service  golang-migrate    2             v0.1.12     ✓ 已迁移
```

**实现**：

1. 连接 postgres（DATABASE_URL 优先级链）
2. 探测迁移工具：先查 `schema_migrations`（golang-migrate），再查 `atlas_schema_revisions`（Atlas）
3. 读 `configs/project.env` 里的 VERSION
4. 输出对比结果

---

## kp migrate plan

**核心流程**：

```
读当前 DB 版本
    ↓
扫描 db/migrations/*.up.sql（版本 > 当前 DB 版本）
    ↓
逐条解析 SQL：stripComments → splitStatements → 正则匹配
    ↓
风险分级输出
```

**风险规则**：

```go
DROP TABLE / DROP COLUMN       → RiskDestructive（❌ 阻断）
ALTER COLUMN TYPE（缩容）       → RiskDestructive
ALTER COLUMN SET NOT NULL       → RiskPotential（⚠️ 告警）
CREATE INDEX（无 CONCURRENTLY） → RiskSafe + 建议
CREATE TABLE / ADD COLUMN       → RiskSafe
```

**类型变更判断**：

```go
// 已知安全扩容
int     → bigint / int8
varchar → text
char    → varchar / text
smallint → int / integer / bigint

// 其他类型变更 → 破坏性
```

**SQL 预处理**：

```go
// 1. 去掉单行注释（-- ...）
// 2. 去掉块注释（/* ... */）
// 3. 按分号拆分语句
// 4. 逐句正则匹配
```

---

## 部署集成

`deployLayers` 在 `checkRequiredSecrets` 之后自动调用 `checkMigrationCompatibility`：

```
kp deploy
    ↓
checkRequiredSecrets（Secret 存在性）
    ↓
checkMigrationCompatibility（迁移兼容性）
    ↓ 有破坏性变更
阻断 + 提示 --force-migrate
    ↓ 无破坏性变更 / --force-migrate
继续部署
```

**降级处理**：
- DATABASE_URL 未配置 → 静默跳过
- DB 不可达 → 静默跳过
- 无迁移目录 → 静默跳过

不强制依赖，不影响未使用 postgres 的项目。

---

## 文件格式支持

| 工具 | 文件格式 | 版本提取 |
|------|---------|---------|
| golang-migrate | `000001_add_users.up.sql` | 前缀数字 |
| Atlas | `20240330120000_add_users.sql` | 时间戳 |

通用正则：`^(\d+)_`，同时兼容两种格式。

---

## DATABASE_URL 优先级

```
1. --database-url flag（最高优先级）
2. KP_DATABASE_URL 环境变量
3. .env 文件里的 DATABASE_URL（本地开发）
4. configs/project.env 里的 DATABASE_URL
```

K8s 生产环境用 `KP_DATABASE_URL` 环境变量注入，不依赖 .env 文件。
