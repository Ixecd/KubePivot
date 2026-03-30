# TODO — dev-toolkit 路线图

> 从"自用脚手架"走向"真正可推广的 Go 云原生工具"。
> 按优先级排列，持续更新。当前：v1.1.0（进行中）

---

## 🟡 v1.1.0（剩余）

- [ ] `REGISTRY_PREFIX` 支持阿里云 ACR 格式
- [ ] 统一进度输出带颜色（终端支持时）
- [ ] 灰度发布支持（豆包建议）

---

## 🟢 P2 — 长期

- [ ] 服务级 FSM（v2.0，目前是项目级）
- [ ] 跨 namespace 依赖支持
- [ ] `dtk ai-plan` 接入私有化 LLM 最佳实践文档
- [ ] 安全扫描 + RBAC 越权检测（豆包建议，平台级方向）

---

## ✅ 已完成

### v1.1.0（进行中）

- [x] 构建 `dev-toolkit-controller` 镜像（`qingchun22/dev-toolkit-controller:v1.0.0`）并推送
- [x] web3-blitz controller chart 迁移到独立 chart（`web3-blitz-controller/`）
- [x] controller 自愈 e2e 验证（web3-blitz，wallet-service 删除后 ~13s 恢复）
- [x] controller SSA 冲突处理（检测冲突 → 清除 managedFields → 重试 rollback）
- [x] `dtk status` 展示每个 helm release 独立状态和 revision
- [x] `dtk rollback` 按拓扑逆序逐层并行打印进度
- [x] `dtk init --dry-run` 打印目录结构，不执行文件写入
- [x] gotchas.md 补充 controller 章节（on-missing 格式、nil pointer、release 命名）
- [x] 修复 controller 三个 bug（RealHelmClient 未注入、release 命名错误、策略字段不匹配）

### v1.0.0 封神 🏆

- [x] 多服务独立 helm release（每个服务 `{project}-{service}`）
- [x] `components.yaml` 支持 `type`（deployment/statefulset）和 `depends_on`
- [x] `planner`：DAG 依赖图 + Kahn 拓扑排序 + Downstream 级联下游（32个单测）
- [x] `scaffold`：四个独立 chart（postgres/etcd/业务服务/controller）
- [x] `deploy`：同层 goroutine 并行，层间串行
- [x] `deploy`：build/push 只做一次，helm+rollout 最多重试 3 次
- [x] `deploy`：级联 rollback → 整组 rollback → dtk down
- [x] `helmReleaseExists` 防止 rollback 未安装的 release 误触发 dtk down
- [x] controller 统一命名为 `dev-toolkit-controller`，所有项目共用同一镜像
- [x] 清理旧 templates/ 里的 controller 文件，修复 helm --wait 超时
- [x] e2e 验证：e2e 项目（3层拓扑）+ web3-blitz（2层拓扑）全部跑通
- [x] 全量文档更新（architecture/controller/deploy/helm/scaffold/state-machine/multi-service）
- [x] gotchas 补充老项目迁移、chart 不存在、service name 冲突等坑

### v0.9.0

- [x] `dtk ai-plan` AI 扫描仓库，自动生成 components.yaml
  - 支持 Grok / Claude / OpenAI / 豆包四个 provider
  - `--suggest-only` 只看建议，`--desc` 补充描述
  - `DTK_LLM_ENDPOINT` 支持私有化部署
- [x] 统一进度输出（P.Start/Done/Fail/Info，带时间戳和耗时）
- [x] `make deploy.full` 拆成四步独立计时

### v0.8.x 及之前

- [x] `dtk init` 端到端生成可编译项目（含 ARCH 自动检测）
- [x] `dtk deploy` build → push → helm → rollout → 状态追踪
- [x] `dtk deploy` SSA 冲突自动处理、pending-rollback/install/failed 前置处理
- [x] `dtk deploy` 镜像拉取失败友好提示
- [x] `dtk resume` / `dtk rollback` / `dtk release` / `dtk down`
- [x] `dtk doctor` 环境依赖检查
- [x] `dtk status` 三层状态 + `--history`
- [x] `dtk history` 查看部署历史，支持 `-n` 限制
- [x] `dtk diff` 对比两个 revision 的 helm values
- [x] 部署状态机完整实现（57个单测）
- [x] A2 Reconciliation Controller（etcd Watch 指数退避重连，20个单测）
- [x] state 包零 K8s 依赖，本地→etcd 自动迁移
- [x] scaffold 完整生成（34个单测）
- [x] planner（32个单测，100% 覆盖）
- [x] CI 加 -race 检测，143个单测全绿
- [x] 全量文档 + quickstart + gotchas + AI 使用手册

---

> v1.0.0 已封神 🏆
> v1.1.0 主体完成，剩余：ACR 格式 / 颜色输出 / 灰度发布
