# kubepivot 项目快照

> 用途：新会话开始时直接把这个文件扔给 Claude，5秒对齐，继续工作。
> 最后更新：2026-03-19

---

## 项目是什么

Go 云原生脚手架工具。一行命令生成完整项目骨架，一键 AI 规划 + Helm 部署。
目标：开发者用 dtk init 得到一个带完整认证、权限、API 规范的生产级项目骨架。

```bash
dtk init --name my-svc --module github.com/me/my-svc
dtk deploy
```

---

## 当前版本状态

### `dtk init` 生成的完整项目结构

```
<n>/
├── cmd/<n>/main.go              # HTTP 服务入口（:8080，从环境变量读端口）
├── internal/
│   ├── api/
│   │   ├── handler.go           # Handler 骨架（Healthz, Home）
│   │   └── server.go            # NewMux，路由注册
│   ├── auth/
│   │   ├── auth.go              # HashPassword, CheckPassword
│   │   │                        # GenerateToken（JWT HS256，1h）
│   │   │                        # ParseToken, GenerateRefreshToken（32字节随机hex）
│   │   ├── middleware.go        # JWTMiddleware(secret, next)
│   │   │                        # GetClaims(r) → *Claims
│   │   └── rbac.go              # PermissionChecker 接口
│   │                            # RBACMiddleware(checker, permission, next)
│   └── pkg/
│       └── code/
│           └── code.go          # ErrorCode 类型 + 6个基础错误码
│                                # ErrUnknown/ErrInvalidArg/ErrUnauthorized
│                                # ErrForbidden/ErrNotFound/ErrInternal/ErrDeadlineExceeded
├── test/
│   ├── e2e/e2e_test.go          # //go:build e2e，TestE2E_<Name>，需服务启动
│   ├── integration/api_test.go  # TestAPI_Healthz，httptest，无需外部服务
│   └── smoke/smoke_test.go      # //go:build e2e，TestSmoke_Health，需服务启动
├── scripts/
│   └── test_api.sh              # curl 冒烟测试（healthz + auth验证）
├── build/docker/<n>/
│   ├── Dockerfile               # golang:1.25-alpine AS builder → alpine:3.20
│   └── build.sh                 # docker build 脚本
├── configs/
│   ├── components.yaml          # dtk deploy 组件配置（name/port/image）
│   └── project.env              # PROJECT_NAME/KUBE_NAMESPACE/REGISTRY_PREFIX等
├── docs/
│   └── swagger.yaml             # Swagger 2.0 骨架（/healthz + / 两个接口）
├── deployments/<n>/             # Helm Chart（Chart.yaml/values.yaml/templates/）
├── snapshots/
│   └── README.md                # 快照归档说明
├── .github/workflows/
│   └── ci.yml                   # GitHub Actions CI
├── go.mod                       # go 1.25，module 路径已替换
└── go.sum                       # testify + jwt/v5 + crypto/bcrypt 已安装
```

---

## `dtk init` 执行流程（按顺序）

1. **copyEntries** — 复制模板文件（build/configs/deployments/docs/scripts/tools等，不含test/）
2. **writeGoMod** — 替换 module 路径，固定 go 1.25
3. **writeServiceMain** — 生成 `cmd/<n>/main.go`
4. **writeComponentsConfig** — 生成 `configs/components.yaml`
5. **writeTestScript** — 生成 `scripts/test_api.sh`
6. **writeInternalSkeleton** — 生成 `internal/api/` + `internal/pkg/code/`
7. **writeAuthPackage** — 生成 `internal/auth/`（auth.go + middleware.go + rbac.go）
8. **writeDockerfile** — 生成 `build/docker/<n>/Dockerfile`（在所有 renameDir 之后）
9. **writeBuildSh** — 生成 `build/docker/<n>/build.sh`
10. **writeSwaggerSpec** — 生成 `docs/swagger.yaml`
11. **writeTestSkeleton** — 生成 `test/`（e2e/integration/smoke，e2e+smoke带build tag）
12. **writeSnapshotSkeleton** — 生成 `snapshots/README.md`
13. **writeComponentsConfig** — 生成 `configs/project.env`
14. **renameDir** — helloworld→name，dtk→name，deployments/project→name
15. **replaceInDir** — 全局替换模块路径+项目名
16. **replaceInDir(deployments/)** — 单独替换 "project"→name（修复 Helm chart）
17. **fixChartYAMLs** — 清理 Chart.yaml 的 dependencies 字段
18. **renameDir** — kubepivot→name，（二次兜底）
19. **git init + add + commit** — chore: init project by dtk
20. **go get** — testify@latest + jwt/v5 + crypto/bcrypt（固定版本避免go版本冲突）
21. **go mod tidy**

---

## `dtk deploy` 执行流程

```
读取 configs/components.yaml
    │
    ▼
AI 规划资源（replicas/cpu/memory/storage）
    │
    ▼
docker build（golang:1.25-alpine 多阶段）
    │
    ▼
docker push（REGISTRY_PREFIX/image-ARCH:VERSION）
    │
    ▼
helm upgrade --install（KUBE_NAMESPACE）
    │
    ▼
kubectl set image + rollout status
```

---

## GitHub Actions CI 流程

```yaml
触发：push/PR to main(Master)
环境：ubuntu-latest，Go 1.25
步骤：
  1. Checkout
  2. Set up Go 1.25（带缓存）
  3. go build ./...
  4. go vet ./...
  5. go test ./...（只跑 integration，e2e/smoke 有 build tag 跳过）
  6. docker build（验证镜像能构建）
```

---

## auth 包设计

**auth.go：**
```go
// JWT Claims
type Claims struct {
    UserID   int64  `json:"user_id"`
    Username string `json:"username"`
    jwt.RegisteredClaims      // exp, iat
}

const tokenExpiry = 24 * time.Hour        // access token
const RefreshTokenExpiry = 7 * 24 * time.Hour  // refresh token

func HashPassword(password string) (string, error)       // bcrypt DefaultCost
func CheckPassword(password, hash string) bool            // bcrypt.CompareHashAndPassword
func GenerateToken(userID int64, username, secret string) (string, error)  // HS256
func ParseToken(tokenStr, secret string) (*Claims, error)
func GenerateRefreshToken() (string, error)              // crypto/rand 32字节hex
```

**middleware.go：**
```go
// 从 Authorization: Bearer <token> 提取并验证 JWT
func JWTMiddleware(secret string, next http.HandlerFunc) http.HandlerFunc

// 从 context 取出 claims（JWTMiddleware 注入的）
func GetClaims(r *http.Request) *Claims
```

**rbac.go：**
```go
// 业务层实现此接口
type PermissionChecker interface {
    HasPermission(ctx context.Context, userID int64, permission string) (bool, error)
}

// 链式：JWTMiddleware → RBACMiddleware → handler
func RBACMiddleware(checker PermissionChecker, permission string, next http.HandlerFunc) http.HandlerFunc
```

---

## Swagger 骨架

生成的 `docs/swagger.yaml` 包含：
- `/healthz` GET — 健康检查
- `/` GET — 服务信息（需要 Bearer token）
- `securityDefinitions.Bearer` — JWT 认证定义
- 基础 `Response` 定义（code/message/data）

业务接口在此基础上扩展。

验证：`swagger validate docs/swagger.yaml`
启动：`swagger serve docs/swagger.yaml`

---

## 关键设计决策

| 决策 | 原因 |
|------|------|
| `copyEntries` 不含 `test/` | 避免复制脚手架自己的测试，用 writeTestSkeleton 生成 |
| `"project"` 不在全局 replacements | 避免污染 docs/README，单独对 deployments/ 精准替换 |
| `writeDockerfile/writeBuildSh` 在 renameDir 之后 | 避免被 RemoveAll 覆盖 |
| `git init/add/commit` 在 replaceInDir 之后 | 保证 index 内容是最终状态 |
| e2e/smoke 加 `//go:build e2e` | CI 只跑不依赖外部服务的 integration 测试 |
| Dockerfile 用 golang:1.25-alpine | 和 go.mod 版本一致，避免版本冲突 |
| auth 包用接口设计（PermissionChecker） | 不依赖具体 DB 实现，业务层自由选择 |
| swagger.yaml 放 `docs/` | 语义清晰，文档归文档 |
| go get 固定版本 | 避免最新版依赖要求更高 go 版本 |

---

## 待实现

- [ ] GitHub Actions workflow 加入 `dtk init` 生成的 `.github/` 目录（已有 CI，待加入脚手架模板）
- [ ] `dtk init --template` 自定义模板目录完整测试
- [ ] `dtk deploy --dry-run` 输出优化
- [ ] 多服务支持（components.yaml 多个 name，批量 build + deploy）

---

## 常见问题

**dtk deploy 报 FROM BASE_IMAGE**
build/docker/dtk/Dockerfile 是脚手架自己的，新项目的 Dockerfile 由 writeDockerfile 生成，不会有这个问题。

**go.mod requires go >= 1.25**
writeGoMod 已固定 go 1.25，旧项目手动改：`sed -i '' 's/^go .*/go 1.25/' go.mod`

**git commit 失败**
可能 git 没有配置 user.name/user.email，手动执行：
```bash
git config user.email "you@example.com"
git config user.name "Your Name"
```

**e2e 测试在 CI 失败**
e2e/smoke 需要服务运行，已加 `//go:build e2e` tag，CI 自动跳过，本地跑用 `-tags e2e`。

---

## 历史快照

```
snapshots/
└── SNAPSHOT-dtk-2026-03-18-scaffold-complete.md  # 脚手架初版完成
```
