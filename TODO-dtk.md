# TODO — dev-toolkit 路线图

> 从"自用脚手架"走向"真正可推广的 Go 云原生工具"。
> 按优先级排列，持续更新。当前：115 commits。

---

## 🔴 P0 — 核心，推广前必须完成

### 状态机 + etcd 持久化
- [ ] 设计状态机：`IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING → ROLLING_BACK → CLEANING → TERMINATED`
- [ ] 状态持久化到 etcd，key 按 `dtk/<project>/<ns>/state`
- [ ] 本地文件降级方案（无 etcd 时退化到 `~/.dtk/state/<project>/<ns>.json`）
- [ ] `dtk resume` 命令，从上次中断的状态继续
- [ ] 状态转换日志，每次转换记录时间、版本、原因

### 错误处理与回滚
- [ ] 区分首次部署和更新部署（检查 ns 是否已存在）
- [ ] 首次部署失败 → 状态机进入 CLEANING → 删除 ns → 回到 IDLE
- [ ] 更新失败 → 状态机进入 ROLLING_BACK → `helm rollback` → 回到 RUNNING
- [ ] VALIDATING 超时失败 → 自动触发回滚
- [ ] 网络 / 权限问题 → 提示清楚，状态不变，不动任何资源
- [ ] `dtk rollback` 命令，手动触发回滚到指定版本

### 集成测试
- [ ] 完整流程：init → deploy → success
- [ ] 首次部署失败 → 验证 ns 被清理，状态回 IDLE
- [ ] 更新失败 → 验证自动回滚，状态回 RUNNING
- [ ] 网络断开 → 验证不破坏现有资源
- [ ] resume → 验证从中断点继续
- [ ] 用 web3-blitz 作为真实 demo 跑通完整流程

### dry-run 模式
- [ ] `dtk deploy --dry-run` 打印将要执行的操作，不真正执行
- [ ] `dtk init --dry-run` 预览生成的文件结构

---

## 🟡 P1 — 健壮性

### 多服务支持
- [ ] 支持一个项目多个服务（前端、后端、worker）
- [ ] 定义服务依赖顺序
- [ ] 一条命令部署整组服务，按依赖顺序执行
- [ ] 任意一个服务失败，整组回滚

### 版本管理
- [ ] `dtk history` 查看版本历史
- [ ] `dtk diff` 对比两个版本的配置差异
- [ ] VERSION 文件冲突检测
- [ ] `dtk release` 命令：从 git tag 读版本、自动更新 `project.env` 中的 VERSION、打 tag、可选触发 deploy
- [ ] VERSION 变更校验：`dtk deploy` 时如果本地代码有未提交改动，警告用户

### 边界 case 加固
- [ ] init 时目标目录已存在的处理
- [ ] deploy 时镜像不存在的处理
- [ ] KUBE_CONTEXT 为空或无效的处理
- [ ] helm release 状态异常时的处理（pending-install / failed 等）
- [ ] etcd 连接断开时的降级处理

### 测试覆盖
- [ ] `internal/scaffold/` 核心逻辑单元测试：replaceInDir、fixChartYAMLs、writeGoMod
- [ ] `internal/ai/` LoadComponents 解析测试：覆盖空 image、带引号、缺字段等边界情况
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
- [ ] 状态机设计文档（这块值得单独写）
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
- [x] VERSION 不变自动跳过 build/push
- [x] Helm `--force-conflicts` + `--wait` 防冲突
- [x] `--with-frontend` 生成通用 React + Vite + Tailwind 骨架
- [x] monitoring 骨架（prometheus + alertmanager + grafana）默认生成
- [x] CI（GitHub Actions）backend + frontend 分 job
- [x] 完整文档（guide / design / reference）
- [x] deploy e2e 12 个 bug 记录归档
- [x] slog 结构化日志（CLI 特化：text/stderr，LOG_LEVEL=debug）
- [x] `dtk deploy` 前置检查：检测 docker / kubectl / helm，不可用时给出安装链接
- [x] `dtk init` 生成失败时自动清理半成品目录
- [x] `LoadComponents` 换用 `gopkg.in/yaml.v3`，消灭 `image: ""` 引号坑
- [x] `deploy.mk` kubectl / helm 失败时打印 context / namespace / image + hint
- [x] 生成项目默认包含 golang-migrate 骨架，启动自动执行迁移

---

> 两个项目的交汇点：web3-blitz 是 dtk 的活体验证，部署过程中发现的问题直接反哺 DTK P0/P1。web3-blitz 跑通之后就是 dtk 最好的推广 demo。

> 每完成一项，移到 ✅ 已完成，并更新 SNAPSHOT。
