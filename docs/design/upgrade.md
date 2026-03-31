# 跨版本升级设计 — kp upgrade

## 概述

`kp upgrade` 是 KubePivot 的跨版本全链路升级器，专门解决多服务版本协同升级中的兼容性、数据迁移和安全回滚问题。

**不是 `kp deploy` 的替代品**：

- `kp deploy`：日常高频发版，轻量快速，不做版本兼容检查
- `kp upgrade`：跨大版本跃迁，全链路检查，低频高安全

---

## 核心原则

**只保护，不越权**：检测风险、阻断部署、清晰提示，最终决策权交给用户。

- 发现破坏性 DB 变更 → 阻断，不自动 rollback DB
- 服务部署失败 → 提示 `kp rollback`，不自动执行
- 健康校验失败 → 提示 `kp rollback`，不自动执行

---

## 升级流程

```
Step 1: 全链路兼容性检查
    1a. DB 迁移风险      → 复用 scanMigrationFiles + analyzeSQLFile
    1b. API 兼容性提示   → 引导 kp compat check
    1c. Values 兼容提示  → 引导 kp diff --migrate
    ↓ 有阻断项 → 终止（--force 可绕过）

Step 2: 执行 DB 迁移
    → 复用 executeMigrationFile（事务执行，失败自动回滚事务）
    → 提示 kp rollback（服务层回滚）

Step 3: 部署服务
    → 复用 executeDeploy（完整状态机流程）
    → 失败提示 kp rollback

Step 4: 健康校验
    → kubectl rollout status --timeout=60s
    → 失败提示 kp rollback
```

---

## 代码复用

| 功能 | 复用模块 |
|------|---------|
| DB 迁移风险检查 | `scanMigrationFiles` + `analyzeSQLFile`（migrate_plan.go）|
| DB 迁移执行 | `executeMigrationFile`（migrate_run.go）|
| 服务部署 | `executeDeploy`（deploy.go）|
| 健康校验 | `kubectl rollout status`（multi_deploy.go 同款）|
| swagger 探测 | `findSwaggerFile`（compat.go）|

零冗余，全复用。

---

## 失败处理

### DB 迁移失败

```
❌ 数据库迁移失败！
  版本: 4 / 文件: 000004_xxx.up.sql / 错误: ...

💡 建议操作：
  1. 检查并修复迁移 SQL
  2. 如需回滚整个部署，请执行：kp rollback
```

服务未升级，用户可以：
- 修复 SQL 后重新 `kp upgrade`
- 或 `kp rollback` 回滚服务到上一版本

### 服务部署失败

DB 迁移已执行，需要用户手动判断：
- 如果 DB 变更是向后兼容的（ADD COLUMN 等）→ 修复代码重新部署
- 如果 DB 变更是破坏性的 → `kp rollback` 回滚服务，再执行 DB down migration

---

## v1.4.0 范围

**已实现**：
- 全链路兼容性检查（三步）
- DB 迁移执行
- 服务部署
- 健康校验
- dry-run 预览

**v1.5.0 计划**：
- 自动配置迁移（废弃字段检测 + 迁移建议，不自动替换）
- 升级前自动备份 Values 快照

**v2.0.0 计划**：
- `kp self-update`（CLI 自身版本升级）

---

## 与其他命令的关系

```
kp migrate plan   → 单独分析风险（不执行）
kp migrate run    → 单独执行迁移（不部署）
kp diff --migrate → values diff + 迁移建议（不执行）
kp compat check   → API 兼容性检测（不执行）
kp upgrade        → 以上所有 + 执行 + 健康校验的完整编排
```
