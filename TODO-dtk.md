# TODO — dev-toolkit 路线图

> 从"自用脚手架"走向"真正可推广的 Go 云原生工具"。
> 按优先级排列，持续更新。当前：v0.5.1

---

## 🔴 P0 — 核心，推广前必须完成

### 状态机 & Reconciliation Controller

- [x] 状态机升级为 A2 独立 Controller Pod 方案
- [x] 新增 `configs/resources.yaml` 配置化资源监控
- [x] Reconciliation Loop（etcd Watch + 定期 Reconcile）
- [x] 自动自愈机制（helm rollback 恢复缺失资源）
- [x] state 包彻底解耦（零 K8s 依赖）
- [x] controller graceful shutdown（sync.WaitGroup）
- [x] 状态机单元测试 47 个，全转换表覆盖
- [x] `RUNNING → ROLLING_BACK` 补入转换表
- [x] `ForceState()` 方法，专用异常恢复场景
- [ ] `startEtcdWatcher` 断线重连机制
- [ ] SSA 冲突自动清除 managedFields 重试
- [ ] controller 自愈流程端到端验证

### scaffold（dtk init）

- [x] 生成 `handoff/HANDOFF.md` + `handoff/AI-CODING-GUIDE.md`
- [x] 生成 `configs/resources.yaml`
- [x] 生成 controller yaml 骨架，带 enabled 开关
- [x] NOTES.txt 显示组件状态
- [x] postgres / etcd 加 enabled 开关，initContainers 联动
- [x] 基础设施组件命名加项目名前缀
- [x] `controller.enabled: false` 默认关闭
- [ ] `dtk init --dry-run` 预览生成的文件结构

### CLI 命令

- [x] `dtk deploy` 完整状态机流程
- [x] `dtk deploy` pending-rollback 前置检查 + 自动处理
- [x] `dtk resume` 从中断点恢复，含 pending-rollback 检查
- [x] `dtk rollback` 手动回滚（revision/CLEANING bug 修复）
- [x] `dtk release` 打版本 tag，可选触发部署
- [x] `dtk down` 彻底下线，二次确认
- [x] `dtk doctor` 环境依赖检查
- [x] `dtk status` 三层状态 + `--history`

### 集成测试

- [x] 端到端全流程验证（9 步，除 controller 自愈）
- [ ] controller 自愈端到端验证
- [ ] 首次部署失败 → ns 清理，状态回 IDLE
- [ ] 更新失败 → 自动回滚，状态回 RUNNING
- [ ] `internal/controller/` 单元测试

---

## 🟡 P1 — 健壮性（v0.6.0 → v0.8.0）

### 稳定性
- [ ] `startEtcdWatcher` 断线重连
- [ ] SSA 冲突自动清除
- [ ] controller 单元测试（heal / reconciler）

### 多服务支持
- [ ] 支持一个项目多个服务
- [ ] 定义服务依赖顺序
- [ ] 任意一个服务失败，整组回滚

### 版本管理
- [ ] `dtk history` 查看版本历史
- [ ] `dtk diff` 对比两个版本配置差异

### 边界 case 加固
- [ ] deploy 时镜像不存在的处理
- [ ] helm release 状态异常处理（pending-install / failed）
- [ ] etcd 连接断开时的降级处理
- [ ] `resources.yaml` 为空时 DetectActualState 策略

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

---

## ✅ 已完成

- [x] `dtk init` 端到端生成可编译项目
- [x] `dtk deploy` build → push → helm → rollout → 状态追踪
- [x] `dtk resume` / `dtk rollback` / `dtk release` / `dtk down`
- [x] `dtk doctor` 环境检查
- [x] `dtk status` + `--history`
- [x] helm pending-rollback 自动检测和处理
- [x] 部署状态机完整实现，47 个单元测试全绿
- [x] A2 Reconciliation Controller 完整实现
- [x] state 包零 K8s 依赖
- [x] scaffold 完整生成（helm chart + controller 骨架 + handoff + AI 指南）
- [x] README 重写 + quickstart + gotchas 文档
- [x] 端到端全流程验证通过（v0.5.1）
- [x] helmRollback 全面修复
- [x] `RUNNING → ROLLING_BACK` 转换表修复
- [x] `dtk status` Helm Status 字段修复（json 解析）
- [x] `dtk status` pod 对齐修复

---

> 路线：v0.5.1（当前）→ v0.8.0（稳到无坑）→ v0.9.0（体验拉满）→ v1.0.0（封神）
>
> 每完成一项，移到 ✅ 已完成，并更新 SNAPSHOT。
