# TODO — dev-toolkit 路线图

> 从"自用脚手架"走向"真正可推广的 Go 云原生工具"。
> 按优先级排列，持续更新。当前：v0.4.1

---

## 🔴 P0 — 核心，推广前必须完成

### 状态机 & Reconciliation Controller（已完成 A2 方案）

- [x] 状态机升级为 A2 独立 Controller Pod 方案
- [x] 新增 `configs/resources.yaml` 配置化资源监控
- [x] Reconciliation Loop（etcd Watch + 定期 Reconcile）
- [x] 自动自愈机制（helm upgrade → 失败后自动 rollback）
- [ ] controller pod 集成到同一个 Helm Chart
- [ ] `DetectResourceExists` 支持更多资源类型（Service/PVC/Ingress）
- [ ] SSA 冲突自动清除 managedFields 重试
- [ ] helm rollback 同样受 SSA 冲突影响的处理

### 集成测试（剩余）

- [ ] 首次部署失败 → 验证 ns 被清理，状态回 IDLE（端到端）
- [ ] 更新失败 → 验证自动回滚，状态回 RUNNING（端到端）
- [ ] 手动删除 deployment → controller 自动自愈（核心验证）
- [ ] controller pod 挂掉后重启仍能继续对账
- [ ] 用 web3-blitz 作为真实 demo 完整跑通 A2 流程

### dry-run 模式（剩余）

- [ ] `dtk init --dry-run` 预览生成的文件结构

## 🟡 P1 — 健壮性

### 多服务支持
- [ ] 支持一个项目多个服务（前端、后端、worker）
- [ ] 定义服务依赖顺序
- [ ] 一条命令部署整组服务，按依赖顺序执行
- [ ] 任意一个服务失败，整组回滚

### 版本管理（剩余）
- [ ] `dtk history` 查看版本历史
- [ ] `dtk diff` 对比两个版本的配置差异
- [ ] VERSION 变更校验：`dtk deploy` 时如果本地代码有未提交改动，警告用户

### 边界 case 加固
- [ ] deploy 时镜像不存在的处理
- [ ] KUBE_CONTEXT 为空或无效的处理
- [ ] helm release 状态异常时的处理（pending-install / failed 等）
- [ ] etcd 连接断开时的降级处理

### 测试覆盖
- [ ] `internal/scaffold/` 核心逻辑单元测试：replaceInDir、fixChartYAMLs、writeGoMod
- [ ] `internal/planner/` LoadComponents 解析测试：覆盖空 image、带引号、缺字段等边界情况
- [ ] `dtk init` e2e 测试：生成项目后执行 `go build ./...`，验证生成物可编译
- [ ] CI 加 `go test -race -cover`，覆盖率不低于 60%

---

## 🟢 P2 — 体验与推广

### CLI 体验
- [ ] 统一的进度输出格式，带状态机当前状态
- [ ] 关键步骤耗时打印
- [ ] `dtk doctor` 检查环境依赖（go / docker / kubectl / helm 版本）
- [ ] `dtk status` 查看当前部署状态
- [ ] `ARCH` 自动检测（`go env GOARCH`），不再需要用户手动填
- [ ] `REGISTRY_PREFIX` 支持阿里云 ACR 格式，`dtk init` 时交互式询问仓库类型

### 文档重写
- [ ] 从用户视角重写，回答「为什么这么设计」
- [ ] 每个命令的 error 和解决方案
- [ ] 外部用户能独立跑通的 quickstart

### 前端骨架增强
- [ ] `--with-frontend` 根据 `docs/swagger.yaml` 自动生成对应的 API client
- [ ] 支持多套前端风格切换（theme 系统）
- [ ] `dtk init --with-frontend` 后自动运行 `npm install`

### AI-native
- [ ] `EstimateResources` 接入 LLM API，根据代码仓库内容推断资源配置
- [ ] `dtk plan`：独立命令，只做 AI 规划，输出建议的 `components.yaml` 和 `values.yaml`
- [ ] AI 分析 `cmd/` 下的服务依赖关系，自动生成 `components.yaml`

---

## ✅ 已完成

- [x] `dtk init` 端到端生成可编译项目
- [x] `dtk deploy` 端到端 build → push → helm install → rollout（`1/1 Running`）
- [x] `dtk resume` 命令，检查 K8s 实际状态后从中断点恢复
- [x] `dtk rollback` 命令，手动触发 helm rollback
- [x] `dtk release` 命令：semver 校验、工作区检查、更新 VERSION、git commit + tag + push、--deploy 可选触发部署
- [x] 部署状态机（IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING → ROLLING_BACK → CLEANING → TERMINATED）
- [x] 状态持久化到 etcd，降级到 `~/.dtk/state/<project>/<ns>.json`
- [x] 首次部署失败 → CLEANING → 删除 ns → IDLE
- [x] 更新失败 → ROLLING_BACK → helm rollback → RUNNING
- [x] VALIDATING 超时 → 自动回滚
- [x] `is_first` 判断：改用 `helmReleaseExists`（修复 helm --create-namespace 导致的误判）
- [x] `dtk deploy --dry-run` 打印完整 make 命令和环境变量
- [x] VERSION 不变自动跳过 build/push
- [x] Helm `--force-conflicts` + `--wait` 防冲突
- [x] `--kubeconfig` flag + `KUBE_CONFIG` 支持多集群部署
- [x] `--with-frontend` 生成通用 React + Vite + Tailwind 骨架
- [x] 自包含 Helm chart（postgres + etcd + 业务服务，零外部依赖）
- [x] initContainers 启动顺序（wait-postgres + wait-etcd）
- [x] golang-migrate 骨架，启动自动执行迁移
- [x] monitoring 骨架（prometheus + alertmanager + grafana）默认生成
- [x] `dtk deploy` 前置检查：检测 docker / kubectl / helm
- [x] `dtk init` 生成失败时自动清理半成品目录
- [x] slog 结构化日志（CLI 特化：text/stderr，LOG_LEVEL=debug）
- [x] `LoadComponents` 换用 `gopkg.in/yaml.v3`
- [x] `deploy.mk` 失败时打印 context / namespace / image + hint
- [x] scaffold.go 拆分（2136 行 → 6 个文件）
- [x] internal/ 目录清理（ai → planner，删空目录）
- [x] 15 个状态机单元测试 + 18 个集成测试
- [x] CI 修复（git user config、kubectl/helm 安装）
- [x] 完整文档（state-machine / release / helm / kubeconfig）
- [x] A2 方案决策：独立 controller pod + etcd 通信
- [x] `internal/controller/` 包完整实现（reconciler、etcd_watcher、heal）
- [x] `DetectResourceExists` 方法实现
- [x] 状态机设计文档更新
- [x] 所有之前的状态机 Bug 已解决（resumeFromValidating 合法性检查、detectActualState 资源缺失判断等）

---

> 两个项目的交汇点：web3-blitz 是 dtk 的活体验证，部署过程中发现的问题直接反哺 DTK P0/P1。

> 每完成一项，移到 ✅ 已完成，并更新 SNAPSHOT。
