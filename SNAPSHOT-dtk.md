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
| `--output` | 输出目录（默认 ./<n>）|
| `--template` | 模板根目录（默认 DTK_TEMPLATE_ROOT 或当前目录）|
| `--force` | 允许覆盖非空目录 |
| `--with-frontend` | 同时生成通用 `frontend/` 骨架 |

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
├── scripts/test_api.sh
├── build/docker/<n>/Dockerfile, build.sh
├── configs/components.yaml, project.env
├── docs/swagger.yaml
├── monitoring/
│   ├── prometheus/prometheus.yml, rules/<n>.yml
│   ├── alertmanager/alertmanager.yml
│   └── grafana/dashboards/<n>.json, provisioning/
├── deployments/<n>/          # Helm chart
├── snapshots/README.md
├── .github/workflows/ci.yml
├── go.mod (go 1.25)
└── go.sum

# --with-frontend 追加（internal/scaffold/frontend.go 生成）：
└── frontend/
    ├── package.json             React 18 + Vite + Tailwind（最小依赖集）
    ├── vite.config.ts           /api 代理到 :8080，@ 别名
    ├── tsconfig.json            strict 模式
    ├── tailwind.config.ts       CSS 变量体系（light/dark）
    ├── Dockerfile               node:20-alpine + nginx:alpine
    ├── nginx.conf               SPA fallback + /api 反代
    ├── .gitignore
    └── src/
        ├── App.tsx              /login 和 /* 两条路由，注释留扩展位
        ├── api/client.ts        fetch 封装，Bearer 字符串拼接（无反引号）
        ├── contexts/AuthContext.tsx  token/username/userID，login/logout
        ├── components/Layout.tsx     侧边栏骨架，nav=[]，业务自行填充
        └── pages/
            ├── Login.tsx        通用登录表单
            └── Home.tsx         纯占位页，无任何业务逻辑
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
| `prometheus/rules/<n>.yml` | 3条告警：ServiceDown / HighErrorRate / HighLatency |
| `alertmanager/alertmanager.yml` | Telegram 模板，bot_token 占位符 |
| `grafana/dashboards/<n>.json` | 4个 Panel：请求速率/错误率/P99/业务错误 |
| `grafana/provisioning/` | datasource + dashboard 自动加载 |

---

## internal 骨架（自动生成）

| 包 | 内容 |
|----|------|
| `internal/api` | handler.go（Healthz/Home）, server.go（NewMux）|
| `internal/auth` | JWT 生成/校验，JWTMiddleware，RBACMiddleware 接口 |
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

- **frontend 骨架强制零业务逻辑**：不得出现金额、交易、链、权限点等任何领域字段
- **nav 为空数组**：`Layout.tsx` 中 `nav = []`，路由和导航完全由业务层添加
- **页面仅两个**：`Login.tsx` + `Home.tsx`，其余由业务层自建
- **反引号安全**：前端模板中 TS 模板字符串全部改为字符串拼接，避免 Go raw string 冲突
- **--with-frontend 完全可选**：不带 flag 行为不变
- projectNamePattern：`^[a-z0-9-]+$`
- go 版本固定 1.25

---

## 历史快照

```
snapshots/
├── SNAPSHOT-dtk-2026-03-20-monitoring.md                  # metrics + prometheus + alertmanager + grafana
├── SNAPSHOT-dtk-2026-03-20-with-frontend.md               # --with-frontend flag 实现
└── SNAPSHOT-dtk-2026-03-20-frontend-skeleton-generic.md   # 骨架精简，强制零业务逻辑
```
