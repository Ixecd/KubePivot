# 设计文档 — Operation Sandbox（v1.8.0）

> 作者：qc（Ixecd）
> 日期：2026-04-04
> 版本：v1.8.0

---

## 一、为什么需要 Sandbox

传统的 K8s 部署流程在处理 **DB 迁移 + 服务升级** 时存在一个隐患：

```
kp migrate run  →  成功  →  kp deploy  →  失败
                                            ↓
                              DB 已变更，服务还是旧版本
                              → 数据不一致，手动处理
```

这个窗口期是无保护的。如果迁移本身有风险（DROP COLUMN、数据格式变更），一旦出问题，回滚代价极高。

**Sandbox 的核心价值**：在真正执行之前，先在一个受保护的沙盒环境里模拟整个流程，确认安全后再 commit。

---

## 二、状态机设计

### 新增状态

```
LOCKED       → 持有分布式锁，阻止其他 kp deploy
SNAPSHOTTING → 正在创建 PVC 快照（有 CSI 才执行）
SIMULATING   → 临时 Job 预跑 DB 迁移（postgres 事务 DDL）
COMMITTING   → 真实 migrate + helm upgrade（禁止 force-unlock）
RESTORING    → 失败后恢复 PVC 快照 + helm rollback
```

### 转换表

```
IDLE/RUNNING    → LOCKED
LOCKED          → SNAPSHOTTING | IDLE（force-unlock）
SNAPSHOTTING    → SIMULATING | RESTORING | IDLE
SIMULATING      → COMMITTING | RESTORING | IDLE
COMMITTING      → RUNNING | RESTORING    ← 禁止直接到 IDLE
RESTORING       → IDLE
```

### 关键约束

**COMMITTING 阶段永远禁止 force-unlock**。原因：此时 DB 迁移正在执行，如果强制解锁，状态机回到 IDLE 但 DB 已部分变更，会造成不可恢复的数据不一致。

---

## 三、执行流程

```
kp sandbox start
       │
       ▼
Step 1: LOCKED
  - 生成 sandbox-id（UUID 前 8 位）
  - 状态机 → LOCKED
  - 写 .kp/sandbox/<id>.json（Controller GC 用）
  - 阻止其他 kp deploy 入口
       │
       ▼
Step 2: SNAPSHOTTING
  - 检查 CSI VolumeSnapshot CRD
  - 有 CSI → kp pvc backup（异步，等待 readyToUse）
  - 无 CSI → 跳过，告警，继续
       │
       ▼
Step 3: SIMULATING
  - 检查 DATABASE_URL 和迁移目录
  - 无迁移 → 跳过
  - 有迁移 → 创建 K8s Job：
      * 打包迁移文件到 ConfigMap
      * 使用 golang-migrate 镜像
      * 所有资源打 kubepivot.io/sandbox-id: <id> label
      * 等待 Job 完成（timeout=300s）
      * Job 失败 → RESTORING
  - Job 创建失败 → 降级为 kp migrate run --dry-run
       │
       ▼
Step 4: COMMITTING
  - 真实执行 kp migrate run
  - 迁移失败 → RESTORING（双层回滚）
  - 迁移成功 → kp deploy
  - 部署失败 → RESTORING（双层回滚）
       │
       ▼
Step 5: RUNNING
  - 清理 SandboxSession 文件
  - 释放锁
```

### 失败回滚（双层）

```
RESTORING:
  Layer 1：kp pvc restore（有快照才执行）
  Layer 2：kp rollback（helm rollback）
  → IDLE
```

---

## 四、Controller GC

**问题**：如果 `kp sandbox start` 进程被 kill，或者 pod 崩溃，Session 文件会遗留，状态机卡在 Sandbox 状态，阻止后续 `kp deploy`。

**解法**：Controller 每 5 分钟扫描 `.kp/sandbox/` 目录：
- 读取 Session 文件里的 `started_at` 和 `ttl`（默认 3600s）
- 超期 → 清理带 `kubepivot.io/sandbox-id` label 的 Job/Pod/ConfigMap
- 强制把状态机 ForceState → IDLE

```go
// 超期判断
if now.Sub(session.StartedAt) > ttl {
    cleanExpiredSandbox(session, path)
}
```

---

## 五、force-unlock 边界

```bash
# 允许
kp sandbox unlock --force --reason "xxx"  # LOCKED/SNAPSHOTTING/SIMULATING

# 禁止（永远）
kp sandbox unlock --force --reason "xxx"  # COMMITTING → 报错退出
```

---

## 六、"只保护，不越权" 原则的体现

- **不自动 apply PVC 快照恢复**：有 CSI 才触发，没有只告警
- **不强制删除 DB 数据**：kp migrate fix-dirty 只打印命令，让用户确认执行
- **Job 清理只删 kp 创建的资源**（带 sandbox-id label）
- **COMMITTING 禁止 force-unlock**：不是 kp 越权，是保护用户不伤自己

---

## 七、命令速查

```bash
# 查看执行计划
kp sandbox start --dry-run

# 启动沙盒
kp sandbox start

# 查看当前状态
kp sandbox status

# 强制解锁（COMMITTING 禁止）
kp sandbox unlock --force --reason "原因说明"

# dirty 迁移修复指引
kp migrate fix-dirty
```

---

## 八、待验证项（TODO）

| 项 | 验证条件 | 计划 |
|----|---------|------|
| SIMULATING Job 真实执行 | K8s 集群 + postgres + golang-migrate 镜像 | v1.9.0 之前 |
| PVC 快照联动 | CSI 支持 VolumeSnapshot | 有 CSI 环境时 |
| Controller GC 超期清理 | 长时间运行的集群 | v1.8.1 |
