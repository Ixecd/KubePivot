# 脚手架设计

`kp init` 的核心是 `internal/scaffold/`，本文说明生成逻辑和关键设计决策。

---

## 整体流程

```
kp init --name myapp --module github.com/me/myapp
  │
  ├── 1. 参数校验（name 必须匹配 ^[a-z0-9-]+$）
  ├── 2. resolveTemplateRoot（DTK_TEMPLATE_ROOT 或当前目录）
  ├── 3. resolveOutputDir（默认 ./<n>）
  ├── 4. ensureOutputDir（非空目录需要 --force）
  │
  ├── 5. copyEntries（复制模板静态文件）
  │     .editorconfig / .gitignore / .golangci.yaml
  │     build/ configs/ deployments/ docs/ scripts/ ...
  │
  ├── 6. 写入动态生成文件
  │     writeGoMod              go.mod，替换 module 和 go 版本
  │     writeServiceMain        cmd/<n>/main.go
  │     writeComponentsConfig   configs/components.yaml
  │     writeResourcesConfig    configs/resources.yaml（controller 监控配置）
  │     writeTestScript         scripts/test_api.sh
  │     writeInternalSkeleton   internal/api + auth + pkg/code
  │     writeAuthPackage        JWT + middleware + RBAC 骨架
  │     writeMetricsSkeleton    Prometheus 指标
  │     writeMonitoringSkeleton prometheus.yml + alertmanager + grafana
  │     writeTestSkeleton       e2e + integration + smoke
  │     writeSnapshotSkeleton   snapshots/README.md
  │     writeSwaggerSpec        docs/swagger.yaml
  │     writeDockerfile         build/docker/<n>/Dockerfile
  │     writeBuildSh            build/docker/<n>/build.sh
  │     writeHandoffSkeleton    handoff/HANDOFF.md（自动填充项目信息）
  │     writeAICodingGuide      handoff/AI-CODING-GUIDE.md（AI 编码约束）
  │
  ├── 7. 生成 project.env（含 ARCH 自动检测）
  │     go env GOARCH → 填入 ARCH 字段
  │
  ├── 8. 目录重命名
  │     build/docker/helloworld → build/docker/<n>
  │     deployments/project    → deployments/<n>
  │
  ├── 9. replaceInDir（全文替换，按 key 长度降序）
  │     "github.com/Ixecd/dev-toolkit" → module
  │     "dev-toolkit"                  → name
  │
  ├── 10. replaceInDir deployments/（修正 helm 模板）
  │       "project" → name
  │
  ├── 11. fixChartYAMLs（逐行解析修正 Chart.yaml）
  │       dependencies: [...] → dependencies: []
  │
  ├── 12. 再次重命名（捕捉 replaceInDir 之后漏掉的）
  │       deployments/dev-toolkit → deployments/<n>
  │       build/docker/dev-toolkit → build/docker/<n>
  │
  ├── 13. writeHelmTemplateSkeleton（生成多服务独立 chart 结构）
  │
  │     writePostgresChart → deployments/<n>/<n>-postgres/
  │       ├── Chart.yaml
  │       ├── values.yaml（storage: 1Gi）
  │       └── templates/
  │           ├── statefulset.yaml
  │           └── service.yaml
  │
  │     writeEtcdChart → deployments/<n>/<n>-etcd/
  │       ├── Chart.yaml
  │       └── templates/
  │           ├── deployment.yaml
  │           └── service.yaml
  │
  │     writeServiceChart → deployments/<n>/<n>/
  │       ├── Chart.yaml
  │       ├── values.yaml（image / service / env / resources）
  │       └── templates/
  │           ├── deployment.yaml（含 initContainers 等待 postgres/etcd）
  │           ├── service.yaml
  │           ├── serviceaccount.yaml
  │           └── NOTES.txt
  │
  │     writeControllerChart → deployments/<n>/<n>-controller/
  │       ├── Chart.yaml
  │       ├── values.yaml（enabled: false，image 待填写）
  │       └── templates/
  │           ├── deployment.yaml（带 enabled guard）
  │           ├── rbac.yaml（带 enabled guard）
  │           └── configmap.yaml（带 enabled guard）
  │
  ├── 14. 自动执行
  │       git init + git add + git commit
  │       go get testify / jwt / bcrypt / prometheus
  │       go mod tidy
  │
  └── 15. 如果 --with-frontend：writeFrontendSkeleton
```

---

## replaceInDir 的顺序问题

`replaceInDir` 是纯字符串替换，**按 key 长度降序排序后执行**，避免短 key 破坏长 key：

```go
sort.Slice(keys, func(i, j int) bool {
    return len(keys[i]) > len(keys[j])
})
```

**反例**（不排序时可能出现）：先替换 `dev-toolkit` → `myapp`，再替换 `github.com/Ixecd/dev-toolkit` → 找不到，已变成 `github.com/Ixecd/myapp`。

此 bug 已在 v0.8.0 修复，通过单元测试覆盖。

---

## fixChartYAMLs 实现

原来用正则 lookahead（`(?=`），Go RE2 不支持会 panic。

现改为逐行解析：找到 `dependencies:` 开头的行，收集后续缩进行，统一替换为 `dependencies: []`。此 bug 已在 v0.8.0 通过单元测试发现并修复。

---

## handoff 目录

`kp init` 会在 `handoff/` 下生成两个文件：

**HANDOFF.md**：写给下一个接手的 Claude，自动填充：当前日期、仓库模块路径、项目名、目录结构、dtk 命令速查、快速访问命令。需要开发者手动填写：架构设计、已知问题、下一步计划。

**AI-CODING-GUIDE.md**：写给协助写业务代码的 Claude，约束该在哪里加 handler / migration / 新模块，不能动哪些文件，代码风格，部署配置同步清单。

---

## ARCH 自动检测

```go
func getArch() (string, error) {
    out, err := exec.Command("go", "env", "GOARCH").Output()
    if err != nil {
        return "", err
    }
    return strings.TrimSpace(string(out)), nil
}
```

生成 `project.env` 时自动填入，不再需要用户手动写 `ARCH=arm64`。

---

## ensureOutputDir 的保护逻辑

```go
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
3. 当前工作目录（检查 Makefile + scripts/make-rules/common.mk + githooks/pre-commit.sh）
4. 以上都不满足 → 报错
```

---

## frontend 骨架设计约束

`--with-frontend` 生成的骨架：零业务逻辑，页面仅两个（Login.tsx + Home.tsx），nav 为空数组，TypeScript 模板字符串全部改为字符串拼接（避免 Go raw string 中反引号冲突）。

---

## go mod tidy 而不是 go mod download

生成的项目有本地包，`go mod download` 只下载外部依赖，本地包的 import 路径需要 `go mod tidy` 才能正确解析。Dockerfile 里同样用 `go mod tidy`。
