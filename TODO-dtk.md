# TODO — dev-toolkit 路线图

> 从"自用脚手架"走向"真正可推广的 Go 云原生工具"。
> 按优先级排列，持续更新。当前：v0.6.0

---

## 🔴 P0 — 核心，推广前必须完成

### 全部完成 ✅

---

## 🟡 P1 — 健壮性（v0.6.0 → v0.8.0）

### 稳定性
- [ ] controller 单元测试（heal / reconciler）
- [ ] web3-blitz `deploy.mk` 同步 dtk 最新版本（镜像存在检查）

### 多服务支持
- [ ] 支持一个项目多个服务（前端、后端、worker）
- [ ] 定义服务依赖顺序
- [ ] 任意一个服务失败，整组回滚

### 版本管理
- [ ] `dtk diff` 对比两个版本配置差异

### 边界 case 加固
- [ ] helm release 状态异常处理（pending-install / failed）
- [ ] deploy 时镜像不存在的处理
- [ ] `resources.yaml` 为空时 DetectActualState 降级策略

### 测试覆盖
- [ ] `internal/scaffold/` 核心逻辑单元测试
- [ ] `internal/planner/` 解析测试
- [ ] `dtk init` e2e 测试：生成物可编译验证
- [ ] CI 加 `go test -race -cover`，覆盖率 ≥ 60%

---

## 🟢 P2 — 体验与推广（v0.9.0）

- [ ] `ARCH` 自动检测（`go env GOARCH`）
- [ ] 统一进度输出格式，带时间戳
- [ ] 关键步骤耗时打印
- [ ] `REGISTRY_PREFIX` 支持阿里云 ACR 格式
- [ ] `dtk init --dry-run`
- [ ] 外部用户能独立跑通的 quickstart 验证

---

## ✅ 已完成

- [x] `dtk init` 端到端生成可编译项目
- [x] `dtk deploy` build → push → helm → rollout → 状态追踪
- [x] `dtk deploy` SSA managedFields 冲突自动处理（清除 + 重试）
- [x] `dtk deploy` pending-rollback 前置检查 + 自动处理
- [x] `dtk resume` 从中断点恢复
- [x] `dtk rollback` 手动回滚
- [x] `dtk release` 打版本 tag，可选触发部署
- [x] `dtk down` 彻底下线，二次确认
- [x] `dtk doctor` 环境依赖检查
- [x] `dtk status` 三层状态 + `--history` + 时区修复
- [x] `dtk history` 查看部署历史，支持 `-n` 限制条数
- [x] 部署状态机完整实现，47 个单元测试全绿
- [x] `RUNNING → ROLLING_BACK` 转换表修复
- [x] `ForceState()` 异常恢复专用方法
- [x] A2 Reconciliation Controller 完整实现
- [x] etcd Watch 断线指数退避重连（1s → 30s）
- [x] state 包零 K8s 依赖
- [x] 本地状态→etcd 自动迁移（state.New() 内检测）
- [x] scaffold 完整生成（helm chart + controller 骨架 + handoff + AI 指南）
- [x] README 重写 + quickstart + gotchas 文档
- [x] 端到端全流程验证（v0.5.1）
- [x] web3-blitz 大测试通过（v0.6.0，含 controller 自愈 + etcd 迁移）
- [x] helmRollback 全面修复
- [x] `runCmd` 捕获 stderr 供关键词检测

---

> 路线：v0.6.0（当前）→ v0.8.0（稳到无坑）→ v0.9.0（体验拉满）→ v1.0.0（封神）
>
> 每完成一项，移到 ✅ 已完成，并更新 SNAPSHOT。
