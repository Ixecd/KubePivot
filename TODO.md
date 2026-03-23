# TODO — dev-toolkit 路线图

> 从"自用脚手架"走向"真正可推广的 Go 云原生工具"。
> 按优先级排列，持续更新。当前：113 commits。

---

## 🔴 高优先级（影响基本可用性）

### 测试覆盖
- [ ] `internal/scaffold/` 核心逻辑单元测试：replaceInDir、fixChartYAMLs、writeGoMod
- [ ] `internal/ai/` LoadComponents 解析测试：覆盖空 image、带引号、缺字段等边界情况
- [ ] `dtk init` e2e 测试：生成项目后执行 `go build ./...`，验证生成物可编译
- [ ] CI 加 `go test -race -cover`，覆盖率不低于 60%

### 版本管理
- [ ] `dtk release` 命令：从 git tag 读版本、自动更新 `project.env` 中的 VERSION、打 tag、可选触发 deploy
- [ ] VERSION 变更校验：`dtk deploy` 时如果本地代码有未提交改动，警告用户

---

## 🟡 中优先级（影响推广和团队使用）

### 去掉隐性环境假设
- [ ] `ARCH` 自动检测（`go env GOARCH`），不再需要用户手动填
- [ ] `REGISTRY_PREFIX` 支持阿里云 ACR 格式，`dtk init` 时交互式询问仓库类型
- [ ] Helm chart 结构可替换：支持用户提供自定义 chart 模板，不强绑定内置模板
- [ ] `deploy.install` 支持 `--set-file` 传入额外 values，不强制用 `project.env`

### dtk deploy 与 make 职责明确化
- [ ] 把 IMAGES 过滤逻辑下沉到 `deploy.mk`，`dtk deploy` 只负责读配置和调用 make
- [ ] 去掉 `deploy.run.all`（`kubectl set image` 冗余步骤），完全由 Helm 管理滚动更新
- [ ] `dtk deploy --dry-run` 输出完整的 make 命令，方便用户调试

### 前端骨架增强
- [ ] `--with-frontend` 根据 `docs/swagger.yaml` 自动生成对应的 API client（`src/api/`）
- [ ] 支持多套前端风格切换（theme 系统），同一套功能，不同视觉风格
- [ ] `dtk init --with-frontend` 后自动运行 `npm install`，不需要用户手动执行

### 多服务支持
- [ ] `components.yaml` 支持多个 service，`dtk deploy` 并行构建多个镜像
- [ ] `dtk deploy --service <name>` 只部署指定服务，不全量更新

---

## 🟢 低优先级（走向真正 AI-native）

### 真正的 AI 规划
- [ ] `EstimateResources` 接入 LLM API（Claude / GPT），根据代码仓库内容推断合理资源配置
- [ ] AI 分析 `cmd/` 下的服务依赖关系，自动生成 `components.yaml`
- [ ] `dtk plan`：独立命令，只做 AI 规划，输出建议的 `components.yaml` 和 `values.yaml`

### 可观测性增强
- [ ] `dtk status`：一键查看所有服务的 pod 状态、最近日志、metrics 摘要
- [ ] `dtk logs <service>`：封装 `kubectl logs`，支持多 pod 聚合
- [ ] Grafana dashboard 根据 `components.yaml` 自动生成，不需要手写 JSON

### 生态扩展
- [ ] `dtk plugin`：插件系统，允许社区扩展 init 模板和 deploy 策略
- [ ] 支持 GitHub Actions / GitLab CI 自动生成，`dtk ci --provider github`
- [ ] `dtk doctor`：环境诊断命令，一键检查所有依赖工具版本和配置是否正确
- [ ] 支持 Kustomize 作为 Helm 的替代部署方式

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
- [x] `dtk init` 生成失败时自动清理半成品目录（--force 时跳过保护已有文件）
- [x] `LoadComponents` 换用 `gopkg.in/yaml.v3`，消灭 `image: ""` 引号坑
- [x] `deploy.mk` kubectl / helm 失败时打印 context / namespace / image + hint

---

> 每完成一项，移到 ✅ 已完成，并更新 SNAPSHOT。
