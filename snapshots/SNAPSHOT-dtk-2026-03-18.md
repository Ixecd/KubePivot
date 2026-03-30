# kubepivot 项目快照

> 用途：新会话开始时直接把这个文件扔给 Claude，5秒对齐，继续工作。
> 最后更新：2026-03-18

---

## 项目是什么

Go 云原生脚手架工具。一行命令生成完整项目骨架，一键 AI 规划 + Helm 部署。

```bash
dtk init --name my-svc --module github.com/me/my-svc
dtk deploy
```

---

## 当前版本状态

### `dtk init` 生成的项目结构

```
<name>/
├── cmd/<name>/main.go          # HTTP 服务入口（:8080 /healthz /）
├── internal/
│   ├── api/
│   │   ├── handler.go          # Handler 骨架（Healthz, Home）
│   │   └── server.go           # NewMux，路由注册
│   └── pkg/
│       └── code/
│           └── code.go         # 错误码（ErrUnknown ~ ErrInternal）
├── test/
│   ├── e2e/e2e_test.go         # 完整流程测试骨架（Testify）
│   ├── integration/api_test.go # Handler 集成测试（httptest）
│   └── smoke/smoke_test.go     # 服务存活冒烟测试
├── scripts/
│   └── test_api.sh             # curl 冒烟测试脚本
├── build/docker/<name>/
│   ├── Dockerfile              # 多阶段构建（golang:1.24-alpine → alpine:3.20）
│   └── build.sh                # docker build 脚本
├── configs/
│   ├── components.yaml         # dtk deploy 组件配置
│   └── project.env             # 部署环境变量
├── deployments/<name>/         # Helm Chart
├── go.mod                      # module 路径已替换
└── go.sum                      # testify 已自动安装
```

### `dtk init` 自动完成的事

1. 复制模板文件（build / configs / deployments / docs / scripts / tools 等）
2. 生成 `cmd/<name>/main.go`
3. 生成 `internal/api/handler.go` + `server.go`
4. 生成 `internal/pkg/code/code.go`
5. 生成 `test/e2e` + `integration` + `smoke` 骨架
6. 生成 `scripts/test_api.sh`
7. 生成 `build/docker/<name>/Dockerfile` + `build.sh`
8. 生成 `configs/project.env` + `components.yaml`
9. renameDir：helloworld → name，dtk → name，deployments/project → name
10. replaceInDir 全局替换模块路径 + 项目名
11. replaceInDir deployments/ 单独替换 "project" → name（修复 Helm chart）
12. git init + git add + git commit（chore: init project by dtk）
13. go get testify@latest + go mod tidy

### `dtk deploy` 流程

```
AI 规划资源（replicas/cpu/memory）
→ docker build + push
→ helm upgrade --install
→ kubectl set image + rollout
```

---

## 核心文件

```
internal/scaffold/scaffold.go   # 核心逻辑，所有 write* 和 init 流程
cmd/dtk/main.go                 # CLI 入口
configs/project.env             # REGISTRY_PREFIX / ARCH / VERSION
configs/components.yaml         # 部署组件列表
```

---

## 关键设计决策

- `copyEntries` 不包含 `test/`（用 `writeTestSkeleton` 生成，避免复制脚手架自己的测试）
- `"project": name` 不在全局 replacements（避免污染 docs/README），单独对 `deployments/` 做精准替换
- `writeDockerfile` / `writeBuildSh` 在所有 `renameDir` 之后执行（避免被 RemoveAll 覆盖）
- `git init/add/commit` 在 `replaceInDir` 之后执行（保证 index 内容是最终状态）
- `go get testify` 在 git commit 之后执行（go.sum 变化不影响初始 commit）

---

## 待实现

- [ ] `dtk init` 支持 `--template` 自定义模板目录完整测试
- [ ] `dtk deploy --dry-run` 输出优化
- [ ] CI/CD 模板生成（GitHub Actions workflow）
- [ ] 多服务支持（components.yaml 多个 name）