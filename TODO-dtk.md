# TODO — dev-toolkit 路线图

> 从"自用脚手架"走向"真正可推广的 Go 云原生工具"。
> 按优先级排列，持续更新。当前：v0.8.0

---

## 🔴 P0 — 全部完成 ✅

---

## 🟡 P1 — 全部完成 ✅

---

## 🟢 P2 — 体验与推广（v0.9.0 → v1.0.0）

### AI 功能
- [ ] `EstimateResources` 接入真实 LLM，可配置三种模式：
  - 默认：AI 扫描代码仓库，直接生成 components.yaml 并部署
  - 建议模式：只输出建议，用户确认后部署
  - 估算模式：只估算资源规格
- [ ] `dtk plan`：独立命令，只做 AI 规划

### 多服务支持
- [ ] 支持一个项目多个服务（前端、后端、worker）
- [ ] 定义服务依赖顺序
- [ ] 任意一个服务失败，整组回滚

### CLI 体验
- [ ] 统一进度输出格式，带时间戳
- [ ] 关键步骤耗时打印
- [ ] `dtk init --dry-run`
- [ ] `REGISTRY_PREFIX` 支持阿里云 ACR 格式

### 文档与推广
- [ ] web3-blitz deploy.mk 同步 dtk 最新版本
- [ ] 外部用户能独立跑通的 quickstart 验证

---

## ✅ 已完成

### 命令
- [x] `dtk init` 端到端生成可编译项目（ARCH 自动检测）
- [x] `dtk deploy` build → push → helm → rollout → 状态追踪
- [x] `dtk deploy` SSA managedFields 冲突自动处理
- [x] `dtk deploy` pending-rollback/install/failed 前置检查
- [x] `dtk deploy` 镜像拉取失败友好报错
- [x] `dtk resume` 从中断点恢复
- [x] `dtk rollback` 手动回滚
- [x] `dtk release` 打版本 tag，可选触发部署
- [x] `dtk down` 彻底下线，二次确认
- [x] `dtk doctor` 环境依赖检查
- [x] `dtk status` 三层状态 + `--history`
- [x] `dtk history` 查看部署历史，支持 `-n` 限制
- [x] `dtk diff` 对比两个 revision 的 values 差异

### 状态机
- [x] 完整 FSM，57+ 单元测试全绿
- [x] etcd 优先 + 本地文件降级 + 自动迁移
- [x] `ForceState()` 异常恢复
- [x] `RUNNING → ROLLING_BACK` 转换表修复

### Controller
- [x] A2 独立 Controller Pod
- [x] etcd Watch + 指数退避重连（1s→30s）
- [x] Detector / HelmClient 接口隔离
- [x] 20 个单元测试

### scaffold
- [x] helm chart 骨架（postgres/etcd/controller，enabled 开关）
- [x] NOTES.txt 组件状态展示
- [x] handoff/HANDOFF.md + AI-CODING-GUIDE.md
- [x] ARCH 自动检测
- [x] 34 个单元测试，修复 .git 跳过和正则 panic

### 稳定性
- [x] helmRollback 全面修复
- [x] SSA / pending-rollback/install/failed 自动处理
- [x] 镜像拉取失败友好提示
- [x] etcd 断线指数退避重连
- [x] CI `-race` 检测

### 文档
- [x] README 重写 + quickstart + gotchas + AI-CODING-GUIDE
- [x] 端到端验证报告（v0.5.1）
- [x] web3-blitz 大测试（v0.6.0）

---

> 路线：v0.8.0（当前，稳到无坑）→ v0.9.0（AI + 体验）→ v1.0.0（封神）
>
> 每完成一项，移到 ✅ 已完成，并更新 SNAPSHOT。
