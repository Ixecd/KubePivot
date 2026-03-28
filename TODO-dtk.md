# TODO — dev-toolkit 路线图

> 从"自用脚手架"走向"真正可推广的 Go 云原生工具"。
> 按优先级排列，持续更新。当前：v0.9.0

---

## 🔴 P0 — 全部完成 ✅

---

## 🟡 v1.0.0 — 封神

### 多服务支持（最大改动）

设计文档：`docs/design/multi-service.md`

- [ ] `planner`：解析 `type`/`depends_on`，构建依赖 DAG，拓扑排序（含循环检测）
- [ ] `scaffold`：每个服务生成独立 helm chart（`deployments/{project}/{service}/`）
- [ ] `deploy`：按拓扑层级部署，同层并行，层间串行
- [ ] `deploy`：单服务失败 → 最多重试 3 次 → 级联 rollback（失败服务 + 下游）
- [ ] `deploy`：级联 rollback 失败 → 整组 rollback → 失败则 dtk down
- [ ] `status`：展示每个 helm release 独立状态
- [ ] `rollback`：按拓扑逆序整组回滚，打印每步进度
- [ ] e2e 验证：web3-blitz 双服务场景

### 稳定性
- [ ] web3-blitz NOTES.txt 同步 dtk 最新版本
- [ ] web3-blitz deploy.mk 同步 dtk 最新版本（镜像存在检查）
- [ ] controller 单元测试补充（heal/reconcile 边界 case）

### 文档
- [ ] 最终文档审查，确保与代码一致
- [ ] 外部用户能独立跑通的 quickstart 验证

---

## 🟢 P2 — 体验（可选）

- [ ] `REGISTRY_PREFIX` 支持阿里云 ACR 格式
- [ ] `dtk init --dry-run`
- [ ] 统一进度输出带颜色（终端支持时）

---

## ✅ 已完成

- [x] `dtk init` 端到端生成可编译项目（含 ARCH 自动检测）
- [x] `dtk deploy` build → push → helm → rollout → 状态追踪
- [x] `dtk deploy` 四步拆分，独立计时，统一进度输出
- [x] `dtk deploy` SSA 冲突自动处理、pending-rollback/install/failed 前置处理
- [x] `dtk deploy` 镜像拉取失败友好提示
- [x] `dtk resume` / `dtk rollback` / `dtk release` / `dtk down`
- [x] `dtk doctor` 环境依赖检查
- [x] `dtk status` 三层状态 + `--history` + 时区修复
- [x] `dtk history` 查看部署历史，支持 `-n` 限制
- [x] `dtk diff` 对比两个 revision 的 helm values
- [x] `dtk ai-plan` AI 扫描仓库，自动生成 components.yaml
  - 支持 Grok / Claude / OpenAI / 豆包四个 provider
  - `--suggest-only` 只看建议，`--desc` 补充描述
  - `DTK_LLM_ENDPOINT` 支持私有化部署
- [x] 部署状态机完整实现，57 个单元测试
- [x] A2 Reconciliation Controller（etcd Watch 指数退避重连）
- [x] controller Detector/HelmClient 接口重构，20 个单元测试
- [x] state 包零 K8s 依赖，本地→etcd 自动迁移
- [x] scaffold 完整生成（34 个单元测试，修复 .git 跳过和 regex panic）
- [x] planner 100% 覆盖（20 个单元测试）
- [x] CI 加 -race 检测
- [x] README 重写 + quickstart + gotchas + AI 使用手册
- [x] 设计文档全量更新（architecture / controller / deploy / helm / scaffold / release / multi-service）
- [x] 端到端全流程验证（e2e + web3-blitz 大测试）
- [x] controller 自愈 e2e 验证（10s 内恢复）
- [x] etcd 自动迁移验证通过

---

> 路线：v0.9.0（当前）→ v1.0.0（封神）
>
> v1.0.0 的核心是多服务支持，这是 dtk 和其他脚手架拉开差距的关键。
> 每完成一项，移到 ✅ 已完成，并更新 SNAPSHOT。
