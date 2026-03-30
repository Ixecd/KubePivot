# kubepivot 项目快照

> 用途：新会话开始时直接把这个文件扔给 Claude，5秒对齐，继续工作。
> 最后更新：2026-03-19

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
<n>/
├── cmd/<n>/main.go
├── internal/
│   ├── api/
│   │   ├── handler.go          # Handler 骨架（Healthz, Home）
│   │   └── server.go           # NewMux，路由注册
│   ├── auth/
│   │   ├── auth.go             # HashPassword, CheckPassword, GenerateToken, ParseToken, GenerateRefreshToken
│   │   └── middleware.go       # JWTMiddleware, GetClaims
│   └── pkg/
│       └── code/
│           └── code.go         # 错误码（ErrUnknown ~ ErrInternal）
├── test/
│   ├── e2e/e2e_test.go         # //go:build e2e
│   ├── integration/api_test.go # httptest，无需外部服务
│   └── smoke/smoke_test.go     # //go:build e2e
├── scripts/
│   └── test_api.sh
├── build/docker/<n>/
│   ├── Dockerfile              # golang:1.25-alpine 多阶段构建
│   └── build.sh
├── configs/
│   ├── components.yaml
│   └── project.env
├── docs/
│   └── swagger.yaml            # Swagger 2.0 API 骨架
├── deployments/<n>/            # Helm Chart
├── snapshots/
│   └── README.md
├── .github/workflows/
│   └── ci.yml                  # GitHub Actions CI
├── go.mod                      # go 1.25
└── go.sum
```

### `dtk init` 自动完成的事

1. 复制模板文件
2. 生成 `cmd/<n>/main.go`
3. 生成 `internal/api/handler.go` + `server.go`
4. 生成 `internal/auth/auth.go` + `middleware.go`（JWT + bcrypt + refresh token）
5. 生成 `internal/pkg/code/code.go`
6. 生成 `test/` 骨架（e2e/smoke 带 build tag）
7. 生成 `scripts/test_api.sh`
8. 生成 `build/docker/<n>/Dockerfile`（golang:1.25-alpine）+ `build.sh`
9. 生成 `docs/swagger.yaml` 骨架
10. 生成 `configs/project.env` + `components.yaml`
11. 生成 `snapshots/README.md`
12. renameDir + replaceInDir（全局 + deployments/ 精准替换）
13. git init + git add + git commit
14. go get testify + jwt/v5 + crypto/bcrypt + go mod tidy

### `dtk deploy` 流程

```
AI 规划资源 → docker build + push → helm upgrade → kubectl rollout
```

### GitHub Actions CI

```
触发：push/PR to main(Master)
步骤：checkout → setup-go 1.25 → build → vet → test → docker build
```

---

## 核心文件

```
internal/scaffold/scaffold.go   # 所有 write* 函数 + InitProject 流程
cmd/dtk/main.go                 # CLI 入口
scripts/make-rules/swagger.mk   # swagger.validate + swagger.serve
```

---

## 关键设计决策

- `copyEntries` 不包含 `test/`（writeTestSkeleton 生成）
- `"project"` 不在全局 replacements，单独对 `deployments/` 精准替换
- `writeDockerfile/writeBuildSh` 在所有 `renameDir` 之后执行
- `git init/add/commit` 在 `replaceInDir` 之后执行
- e2e/smoke 测试加 `//go:build e2e` tag，CI 只跑 integration
- Dockerfile 用 golang:1.25-alpine，go.mod 固定 go 1.25
- auth 包放 `internal/auth/`（不放 api/），便于复用和脚手架迁移
- swagger.yaml 放 `docs/`，swagger.mk 路径对应

---

## 待实现

- [ ] GitHub Actions workflow 加入 `dtk init` 生成的 `.github/` 目录
- [ ] `dtk init --template` 自定义模板完整测试
- [ ] `dtk deploy --dry-run` 输出优化
- [ ] 多服务支持（components.yaml 多个 name）

---

## 历史快照

```
snapshots/
└── SNAPSHOT-dtk-2026-03-18-scaffold-complete.md
```
