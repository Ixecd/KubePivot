# dev-toolkit 快照

> 用途：新会话开始时直接把这个文件扔给 Claude，5秒对齐，继续工作。
> 最后更新：2026-03-23

---

## 项目是什么

Go 云原生脚手架工具 `dtk`，1 行命令生成完整项目骨架，支持 AI plan + Helm deploy。

```bash
# 纯后端
dtk init --name demo-svc --module github.com/you/demo-svc

# 后端 + 前端骨架
dtk init --name demo-svc --module github.com/you/demo-svc --with-frontend

# 部署
dtk deploy
```

---

## 命令总览

| 命令 | 说明 |
|------|------|
| `dtk init` | 生成项目骨架 |
| `dtk deploy` | AI plan + build + push + Helm deploy |

### `dtk init` flags

| flag | 说明 |
|------|------|
| `--name` | 项目名（lowercase，必填）|
| `--module` | Go module 路径 |
| `--output` | 输出目录（默认 ./<n>）|
| `--template` | 模板根目录（默认 DTK_TEMPLATE_ROOT 或当前目录）|
| `--force` | 允许覆盖非空目录 |
| `--with-frontend` | 同时生成通用 `frontend/` 骨架 |

---

## deploy 完整流程

```
dtk deploy
  │
  ├── 1. 读 configs/components.yaml       # 解析组件列表
  ├── 2. 读 configs/project.env           # VERSION / ARCH / REGISTRY_PREFIX 等
  ├── 3. 过滤 image="" 的组件             # CLI 工具不部署
  ├── 4. 组装 IMAGES 传给 make            # 只含有 image 的服务
  │
  └── make deploy.full
        ├── deploy.build                  # VERSION 不变则跳过
        ├── deploy.push                   # VERSION 不变则跳过
        ├── deploy.install                # helm upgrade --install --wait
        └── deploy.run.all                # kubectl set image + rollout status
```

---

## 关键配置

### `configs/project.env`

```env
PROJECT_NAME=demo-svc
REGISTRY_PREFIX=qingchun22
KUBE_CONTEXT=              # 留空用当前 context，严禁写 ""
KUBE_NAMESPACE=demo-svc
ARCH=arm64
VERSION=v0.1.0             # 改这个触发重新 build+push
```

### `configs/components.yaml`

```yaml
components:
  - name: demo-svc    # 对应 cmd/ 下的 binary 名
    port: 8080
    image: demo-svc   # 非空 = 部署；空或 "" = 跳过（CLI 工具用这个）
```

**CLI 工具不能部署到 K8s**，必须 `image: ""`，否则 CrashLoopBackOff 无限套娃。

---

## `dtk init` 完整生成结构

```
<n>/
├── cmd/<n>/main.go
├── internal/
│   ├── api/handler.go, server.go
│   ├── auth/auth.go, middleware.go, rbac.go
│   ├── metrics/metrics.go
│   └── pkg/code/code.go
├── test/e2e/, integration/, smoke/
├── scripts/
│   ├── make-rules/
│   │   ├── deploy.mk          # 含 VERSION 跳过、--force-conflicts、--wait
│   │   └── tools.mk           # 含前端工具安装
│   └── test_api.sh
├── build/docker/<n>/
│   ├── Dockerfile             # go mod tidy + GOPROXY=goproxy.cn
│   └── build.sh
├── configs/
│   ├── components.yaml        # image: "" 跳过部署
│   └── project.env
├── docs/swagger.yaml
├── monitoring/
│   ├── prometheus/prometheus.yml, rules/<n>.yml
│   ├── alertmanager/alertmanager.yml
│   └── grafana/dashboards/<n>.json, provisioning/
├── deployments/<n>/           # Helm chart
│   └── values.yaml            # service.port=8080, probe path=/healthz
├── snapshots/README.md
├── .github/workflows/ci.yml
├── go.mod (go 1.25)
└── go.sum

# --with-frontend 追加：
└── frontend/
    ├── src/
    │   ├── App.tsx            /login 和 /* 路由骨架
    │   ├── api/client.ts      Bearer 字符串拼接（无反引号）
    │   ├── contexts/AuthContext.tsx
    │   ├── components/Layout.tsx  nav=[]，业务填充
    │   └── pages/Login.tsx + Home.tsx
    ├── Dockerfile             node:20-alpine + nginx:alpine
    └── nginx.conf             SPA fallback + /api 反代
```

---

## 日志系统（2026-03-23 新增）

```
internal/logger/logger.go   — CLI 特化 slog 初始化
```

| 环境变量 | 默认值 | 说明 |
|---------|-------|------|
| `LOG_LEVEL` | `info` | 设为 `debug` 开启内部运行轨迹 |
| `LOG_FORMAT` | `text` | 设为 `json` 切换 JSON 格式 |

- 始终写 **stderr**，不干扰 stdout 用户输出
- `runInDir` 失败时 `slog.Debug` 输出原始 stderr，方便排查 git/go 问题

```bash
# 排查 git init / go get 失败
LOG_LEVEL=debug dtk init --name demo-svc --module github.com/you/demo-svc
```

---

## 重要设计约束

### deploy.mk
- `KUBE_CONTEXT ?=` 不能写 `?= ""`，否则 `$(if $(strip))` 永远为真
- `image.repository` 用 `$(firstword $(BINS))` 不用 `$(PROJECT_NAME)`，两者可能不同
- `--force-conflicts` 防 SSA field manager 冲突
- `--wait` 确保 pod ready 后再执行 `deploy.run`
- `docker manifest inspect` 检查 VERSION 是否已推，避免重复 build/push

### Dockerfile
- 用 `go mod tidy` 不用 `go mod download`，本地包需要 tidy 才能找到
- 加 `GOPROXY=https://goproxy.cn,direct`，国内网络拉依赖用

### values.yaml
- `service.port: 8080`（不是默认的 80）
- `livenessProbe/readinessProbe path: /healthz`（不是默认的 /）
- `image.repository: qingchun22/<n>-arm64`，`replaceInDir` 会替换 `<n>`

### Chart.yaml
- `fixChartYAMLs` 用正则替换 `name` 和 `appVersion`，防止遗留 `project` / `1.16.0`

### frontend 骨架
- **强制零业务逻辑**，nav=[]，页面仅 Login + Home
- TS 模板字符串全部改为字符串拼接，避免 Go raw string 反引号冲突

### golang.mk
- `ROOT_PACKAGE` 模板里必须写 `github.com/Ixecd/dev-toolkit`，不能写业务项目路径

---

## auto go get 列表

- github.com/stretchr/testify@latest
- github.com/golang-jwt/jwt/v5
- golang.org/x/crypto/bcrypt
- github.com/prometheus/client_golang/prometheus
- go mod tidy（最后执行）

---

## 代码结构

```
dev-toolkit/
├── cmd/dtk/main.go              CLI 入口，runInit/runDeploy
│                                runDeploy 负责过滤空 image，组装 IMAGES 传给 make
├── internal/
│   ├── logger/
│   │   └── logger.go            slog 初始化（CLI 特化）
│   ├── scaffold/
│   │   ├── init.go              InitProject 主流程
│   │   └── frontend.go          writeFrontendSkeleton（--with-frontend）
│   └── ai/
│       └── ai.go                BuildPlan，image 解析去引号
└── scripts/make-rules/
    ├── deploy.mk                完整部署流程
    └── tools.mk                 前端工具安装
```

---

## 历史快照

```
snapshots/
├── SNAPSHOT-dtk-2026-03-18-scaffold-complete.md
├── SNAPSHOT-dtk-2026-03-18.md
├── SNAPSHOT-dtk-2026-03-19-1.md
├── SNAPSHOT-dtk-2026-03-19-A.md
├── SNAPSHOT-dtk-2026-03-19-B.md
├── SNAPSHOT-dtk-2026-03-19.md
├── SNAPSHOT-dtk-2026-03-20-deploy-e2e(!!!).md
├── SNAPSHOT-dtk-2026-03-20-frontend-skeleton-generic.md
├── SNAPSHOT-dtk-2026-03-20-monitoring.md
├── SNAPSHOT-dtk-2026-03-20-with-frontend.md
└── SNAPSHOT-dtk-2026-03-23-slog.md
```
