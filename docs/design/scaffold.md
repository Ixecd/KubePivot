# 脚手架设计

`dtk init` 的核心是 `internal/scaffold/init.go`，本文说明生成逻辑和关键设计决策。

---

## 整体流程

```
dtk init --name myapp --module github.com/me/myapp
  │
  ├── 1. 参数校验（name 必须匹配 ^[a-z0-9-]+$）
  ├── 2. resolveTemplateRoot（DTK_TEMPLATE_ROOT 或当前目录）
  ├── 3. resolveOutputDir（默认 ./<name>）
  ├── 4. ensureOutputDir（非空目录需要 --force）
  │
  ├── 5. copyEntries（复制模板静态文件）
  │     .editorconfig / .gitignore / .golangci.yaml
  │     build/ configs/ deployments/ docs/ scripts/ ...
  │
  ├── 6. 写入动态生成文件
  │     writeGoMod        go.mod，替换 module 和 go 版本
  │     writeServiceMain  cmd/<name>/main.go
  │     writeInternalSkeleton  internal/api + auth + pkg/code
  │     writeAuthPackage  JWT + middleware + RBAC 骨架
  │     writeMetricsSkeleton   Prometheus 指标
  │     writeMonitoringSkeleton  prometheus.yml + alertmanager + grafana
  │     writeTestSkeleton  e2e + integration + smoke
  │     writeSnapshotSkeleton   snapshots/README.md
  │     writeSwaggerSpec   docs/swagger.yaml
  │     writeDockerfile    build/docker/<name>/Dockerfile
  │     writeBuildSh       build/docker/<name>/build.sh
  │     writeComponentsConfig   configs/components.yaml
  │     writeTestScript    scripts/test_api.sh
  │
  ├── 7. 目录重命名
  │     build/docker/helloworld → build/docker/<name>
  │     deployments/project    → deployments/<name>
  │
  ├── 8. replaceInDir（全文替换）
  │     "github.com/Ixecd/dev-toolkit" → module
  │     "dev-toolkit"                  → name
  │     "demo-svc"                     → name
  │     "helloworld"                   → name
  │
  ├── 9. replaceInDir deployments/（修正 helm 模板）
  │     "project" → name
  │
  ├── 10. fixChartYAMLs（正则修正 Chart.yaml）
  │      name: project    → name: <name>
  │      appVersion: 1.16.0 → appVersion: 1.0.0
  │      dependencies: ... → dependencies: []
  │
  ├── 11. 再次重命名（捕捉 replaceInDir 之后漏掉的）
  │      deployments/dev-toolkit → deployments/<name>
  │      build/docker/dev-toolkit → build/docker/<name>
  │
  ├── 12. 自动执行
  │      git init + git add + git commit
  │      go get testify / jwt / bcrypt / prometheus
  │      go mod tidy
  │
  └── 13. 如果 --with-frontend：writeFrontendSkeleton
```

---

## replaceInDir 的顺序问题

`replaceInDir` 是纯字符串替换，顺序很重要：

- **步骤 8** 先做全局替换，把 `dev-toolkit` → name，`demo-svc` → name
- **步骤 9** 专门处理 `deployments/` 下的 `project` → name
- **步骤 10** `fixChartYAMLs` 用正则兜底，确保 `Chart.yaml` 的 `name` 字段一定被替换

为什么要 `fixChartYAMLs` 单独处理？因为 `Chart.yaml` 里的 `name: project` 和 `appVersion: "1.16.0"` 是 `helm create` 的默认值，纯字符串替换容易误伤其他字段（如 description 里也可能包含 "project"），正则 `^name:.*$` 更精确。

---

## 为什么 `go mod tidy` 而不是 `go mod download`

生成的项目里有 `internal/` 下的本地包。`go mod download` 只下载外部依赖，
本地包的 import 路径需要 `go mod tidy` 才能正确解析。

Dockerfile 里同样用 `go mod tidy`：

```dockerfile
RUN go env -w GOPROXY=https://goproxy.cn,direct
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -o myapp ./cmd/myapp
```

---

## frontend 骨架设计约束

`--with-frontend` 生成的骨架遵循严格约束：

1. **零业务逻辑**：不出现任何领域字段（金额、交易、链、权限点等）
2. **页面仅两个**：`Login.tsx`（通用登录表单）+ `Home.tsx`（占位欢迎页）
3. **nav 为空数组**：`Layout.tsx` 中 `nav = []`，由业务层追加
4. **反引号安全**：前端模板中 TypeScript 模板字符串全部改为字符串拼接

第 4 条原因：Go raw string 用反引号包裹，内部出现反引号会提前终止，
导致 `illegal character U+003F '?'` 编译错误。

错误写法：
```go
// TS 代码里的模板字符串
`/api/v1/users?id=${userID}`
// 在 Go raw string 里，第一个反引号之后的 ? 是非法字符
```

正确写法：
```go
'/api/v1/users?id=' + userID
```

---

## ensureOutputDir 的保护逻辑

```go
// 目录存在且非空，且没有 --force → 报错
if len(entries) > 0 && !force {
    return fmt.Errorf("输出目录已存在：%s\n请加 --force 强制覆盖", path)
}
```

即使目录为空，也要求用户明确加 `--force`，避免意外覆盖已有项目。

---

## 模板根目录查找顺序

```
1. --template flag 显式指定
2. DTK_TEMPLATE_ROOT 环境变量
3. 当前工作目录（检查是否包含 Makefile + scripts/make-rules/common.mk + githooks/pre-commit.sh）
4. 以上都不满足 → 报错
```
