# dev-toolkit 项目快照

> 用途：新会话开始时直接把这个文件扔给 Claude，5秒对齐，继续工作。
> 最后更新：2026-03-19

---

## 项目是什么

Go 云原生脚手架工具。一行命令生成完整项目骨架，一键 AI 规划 + Helm 部署。
目标：开发者用 dtk init 得到带完整认证、权限、API 规范、监控的生产级项目骨架。

```bash
dtk init --name my-svc --module github.com/me/my-svc
dtk deploy
```

---

## 当前版本状态

### `dtk init` 生成的完整项目结构

```
<n>/
├── cmd/<n>/main.go
├── internal/
│   ├── api/handler.go, server.go
│   ├── auth/auth.go, middleware.go, rbac.go
│   └── pkg/code/code.go
├── test/e2e/, integration/, smoke/     # e2e/smoke 带 //go:build e2e
├── scripts/test_api.sh
├── build/docker/<n>/Dockerfile, build.sh   # golang:1.25-alpine
├── configs/components.yaml, project.env
├── docs/swagger.yaml                   # Swagger 2.0 骨架
├── deployments/<n>/                    # Helm Chart
├── snapshots/README.md
├── .github/workflows/ci.yml
├── go.mod                              # go 1.25
└── go.sum
```

### `dtk init` 自动完成（21步）

copyEntries → writeGoMod → writeServiceMain → writeComponentsConfig →
writeTestScript → writeInternalSkeleton → writeAuthPackage(含rbac.go) →
writeDockerfile → writeBuildSh → writeSwaggerSpec → writeTestSkeleton →
writeSnapshotSkeleton → writeProjectEnv → renameDir → replaceInDir →
replaceInDir(deployments/) → fixChartYAMLs → renameDir(二次兜底) →
git init+add+commit → go get(testify+jwt+bcrypt) → go mod tidy

### `dtk deploy` 流程

AI 规划资源 → docker build → docker push → helm upgrade → kubectl rollout

---

## auth 包设计

```go
// auth.go
func HashPassword(password string) (string, error)        // bcrypt DefaultCost
func CheckPassword(password, hash string) bool
func GenerateToken(userID int64, username, secret string) // JWT HS256, 1h
func ParseToken(tokenStr, secret string) (*Claims, error)
func GenerateRefreshToken() (string, error)               // crypto/rand 32字节hex, 7天

// middleware.go
func JWTMiddleware(secret string, next http.HandlerFunc) http.HandlerFunc
func GetClaims(r *http.Request) *Claims

// rbac.go
type PermissionChecker interface {
    HasPermission(ctx context.Context, userID int64, permission string) (bool, error)
}
func RBACMiddleware(checker PermissionChecker, permission string, next http.HandlerFunc) http.HandlerFunc
```

---

## 待实现

- [ ] metrics 骨架（internal/metrics/metrics.go，Prometheus Counter/Gauge/Histogram）
- [ ] 错误码三段设计加入骨架（100xxx/101xxx/102xxx）
- [ ] GitHub Actions workflow 加入 dtk init 生成的 .github/ 目录
- [ ] dtk init --template 自定义模板测试
- [ ] dtk deploy --dry-run 输出优化
- [ ] 多服务支持

---

## 关键设计决策

| 决策 | 原因 |
|------|------|
| auth 包用接口（PermissionChecker） | 不依赖具体 DB，业务层实现 |
| swagger.yaml 放 docs/ | 语义清晰，文档归文档 |
| go.mod 固定 go 1.25 | 和 Dockerfile golang:1.25-alpine 一致 |
| e2e/smoke 加 build tag | CI 只跑 integration，不依赖外部服务 |
| copyEntries 不含 test/ | writeTestSkeleton 生成，避免混入脚手架自己的测试 |

---

## 历史快照

```
snapshots/
└── SNAPSHOT-dtk-2026-03-18-scaffold-complete.md
```
