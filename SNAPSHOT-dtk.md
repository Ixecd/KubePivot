# dev-toolkit 快照

> 用途：新会话开始时直接把这个文件扔给 Claude，5秒对齐，继续工作。
> 最后更新：2026-03-20

---

## 项目是什么

Go 云原生脚手架工具 `dtk`，1 行命令生成完整项目骨架，支持 AI plan + Helm deploy。

```bash
# 纯后端
dtk init --name demo-svc --module github.com/you/demo-svc

# 后端 + 前端骨架
dtk init --name demo-svc --module github.com/you/demo-svc --with-frontend
```

---

## 命令总览

| 命令 | 说明 |
|------|------|
| `dtk init` | 生成项目骨架 |
| `dtk deploy` | AI plan + Helm 部署 |

### `dtk init` flags

| flag | 说明 |
|------|------|
| `--name` | 项目名（lowercase，必填）|
| `--module` | Go module 路径 |
| `--output` | 输出目录（默认 ./<name>）|
| `--template` | 模板根目录（默认 DTK_TEMPLATE_ROOT 或当前目录）|
| `--force` | 允许覆盖非空目录 |
| `--with-frontend` | 同时生成 `frontend/` 骨架 |

---

## `dtk init` 完整生成结构

```
<name>/
├── cmd/<name>/main.go
├── internal/
│   ├── api/handler.go, server.go
│   ├── auth/auth.go, middleware.go, rbac.go
│   ├── metrics/metrics.go
│   └── pkg/code/code.go
├── test/e2e/, integration/, smoke/
├── scripts/test_api.sh
├── build/docker/<name>/Dockerfile, build.sh
├── configs/components.yaml, project.env
├── docs/swagger.yaml
├── monitoring/
│   ├── prometheus/prometheus.yml, rules/<name>.yml
│   ├── alertmanager/alertmanager.yml
│   └── grafana/dashboards/<name>.json, provisioning/
├── deployments/<name>/          # Helm chart
├── snapshots/README.md
├── .github/workflows/ci.yml
├── go.mod (go 1.25)
└── go.sum

# --with-frontend 追加：
└── frontend/
    ├── package.json             React 18 + Vite + Tailwind
    ├── vite.config.ts           /api 代理到 :8080
    ├── tsconfig.json
    ├── tailwind.config.ts       CSS 变量体系
    ├── Dockerfile               nginx 多阶段构建
    ├── nginx.conf               SPA fallback + /api 反代
    └── src/
        ├── App.tsx              /login 和 /* 路由骨架
        ├── api/client.ts        fetch 封装
        ├── contexts/AuthContext.tsx
        ├── components/Layout.tsx    侧边栏（nav=[]，业务填充）
        └── pages/
            ├── Login.tsx
            └── Home.tsx         纯占位页
```

---

## auto go get 列表

- github.com/stretchr/testify@latest
- github.com/golang-jwt/jwt/v5
- golang.org/x/crypto/bcrypt
- github.com/prometheus/client_golang/prometheus
- go mod tidy（最后执行）

---

## monitoring 骨架（自动生成）

| 文件 | 内容 |
|------|------|
| `prometheus/prometheus.yml` | scrape prometheus + 项目服务 |
| `prometheus/rules/<name>.yml` | 3条告警：ServiceDown / HighErrorRate / HighLatency |
| `alertmanager/alertmanager.yml` | Telegram 模板，bot_token 占位符 |
| `grafana/dashboards/<name>.json` | 4个 Panel：请求速率/错误率/P99/业务错误 |
| `grafana/provisioning/` | datasource + dashboard 自动加载 |

---

## internal 骨架（自动生成）

| 包 | 内容 |
|----|------|
| `internal/api` | handler.go（Healthz/Home）, server.go（NewMux）|
| `internal/auth` | JWT 生成/校验, JWTMiddleware, RBACMiddleware 接口 |
| `internal/metrics` | HTTPRequestTotal / HTTPRequestDuration / BusinessErrorTotal |
| `internal/pkg/code` | 通用错误码（100000-100005）|

---

## 代码结构

```
dev-toolkit/
├── cmd/dtk/main.go              CLI 入口，flag 解析，runInit/runDeploy
├── internal/
│   ├── scaffold/
│   │   ├── init.go              InitProject 主流程，所有 write* 函数
│   │   └── frontend.go          writeFrontendSkeleton（--with-frontend）
│   └── ai/                      BuildPlan，解析 components.yaml
└── configs/                     模板文件
```

---

## 设计约束

- **frontend 骨架零业务逻辑**：无领域字段，无具体页面，nav=[]，业务层自行扩展
- **反引号安全**：前端模板中所有 TS 模板字符串改为字符串拼接，避免 Go raw string 冲突
- **--with-frontend 可选**：不带 flag 行为完全不变
- projectNamePattern：`^[a-z0-9-]+$`
- go 版本固定 1.25

---

## 历史快照

```
snapshots/
├── SNAPSHOT-dtk-2026-03-20-monitoring.md      # metrics + prometheus + alertmanager + grafana
└── SNAPSHOT-dtk-2026-03-20-with-frontend.md   # --with-frontend flag + 通用前端骨架
```
