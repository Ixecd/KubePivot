# dev-toolkit (dtk) 🛠️

> Go 云原生项目脚手架：`dtk init` 生成完整项目骨架，`dtk deploy` 一键 AI 规划 + K8s 部署。

## 🚀 Quickstart

```bash
go install github.com/Ixecd/dev-toolkit/cmd/dtk@latest

dtk init --name myapp --module github.com/me/myapp
cd myapp
make tools                    # 安装工具链（首次必须）

# 编辑 configs/project.env，填写 REGISTRY_PREFIX / KUBE_CONTEXT / VERSION
dtk deploy
```

30 秒完成：boilerplate → AI 规划资源 → 构建镜像 → helm 部署 → rollout。

## ✨ 功能

- **`dtk init`**：生成完整 Go 项目骨架，含 Helm chart、Dockerfile、Makefile、监控、测试、前端（可选）
- **`dtk deploy`**：读取 `components.yaml`，AI 规划 replicas/cpu/memory，自动 build → push → helm upgrade → rollout
- **自包含 Helm chart**：postgres + etcd + 业务服务全在 `templates/`，零外部 chart 依赖，一键拉起
- **golang-migrate 骨架**：生成项目自带迁移文件目录和 `connect.go`，启动自动执行迁移
- **initContainers 启动顺序**：postgres 就绪 → etcd 就绪 → 业务服务启动，K8s 原生依赖等待
- **VERSION 跳过机制**：镜像 tag 未变则自动跳过 build/push，加速重复部署
- **前置检查**：deploy 前验证 docker/kubectl/helm 是否可用，不可用时给出安装链接

## 📋 命令

### dtk init

```bash
dtk init --name myapp --module github.com/me/myapp [flags]

Flags:
  --name           项目名（小写字母、数字、-）
  --module         Go module 路径
  --output         输出目录，默认 ./<name>
  --template       模板根目录，或设置 DTK_TEMPLATE_ROOT
  --force          强制覆盖已有目录
  --with-frontend  同时生成 React + Vite + Tailwind 前端骨架
```

生成内容：

```
myapp/
├── cmd/myapp/main.go               # HTTP 服务入口，/healthz
├── internal/
│   ├── api/                        # handler + mux
│   ├── auth/                       # JWT + RBAC 中间件
│   ├── db/migrations/              # golang-migrate 骨架
│   ├── metrics/                    # Prometheus metrics
│   └── pkg/code/                   # 错误码
├── deployments/myapp/              # 自包含 Helm chart
│   └── templates/
│       ├── postgres-statefulset.yaml
│       ├── etcd-deployment.yaml
│       └── deployment.yaml         # 含 initContainers
├── build/docker/myapp/Dockerfile
├── configs/
│   ├── project.env                 # 部署配置
│   └── components.yaml             # AI 规划输入
├── monitoring/                     # Prometheus + Alertmanager + Grafana
├── test/                           # e2e / integration / smoke
├── snapshots/                      # 里程碑快照目录
└── docs/swagger.yaml
```

### dtk deploy

```bash
dtk deploy [flags]

Flags:
  --components  components.yaml 路径，默认 configs/components.yaml
  --namespace   K8s namespace，默认读 project.env
  --context     kubectl context，默认读 project.env
  --dry-run     只打印规划，不执行
```

部署流程：

```
1. 读取 configs/components.yaml + project.env
2. AI 规划资源（replicas / cpu / memory / storage）
3. docker build（VERSION 未变则跳过）
4. docker push（VERSION 未变则跳过）
5. helm upgrade --install --wait --timeout 120s
6. kubectl set image + rollout status --timeout=300s
```

## ⚙️ 关键配置

**`configs/project.env`**：

```ini
PROJECT_NAME=myapp
REGISTRY_PREFIX=your-dockerhub-username   # 必填
KUBE_CONTEXT=                             # 留空=当前 context，禁止写 ""
KUBE_NAMESPACE=myapp
ARCH=arm64
VERSION=v0.1.0                            # 改这个触发重新 build+push
```

**`configs/components.yaml`**：

```yaml
components:
  - name: myapp
    port: 8080
    image: myapp     # 留空或 "" = 跳过部署（CLI 工具防 CrashLoopBackOff）
```

## 🏗️ 生成的 Helm chart 架构

```
启动顺序：
postgres (pg_isready 就绪)
    ↓
etcd (GET /health 就绪)
    ↓
myapp (initContainers 等待后启动)
    → 连接 postgres，执行 migrate
    → 连接 etcd
    → 启动 HTTP 服务
```

业务服务默认注入环境变量：

```yaml
env:
  - name: DATABASE_URL
    value: "postgres://user:pass@postgres:5432/myapp?sslmode=disable&search_path=public"
  - name: ETCD_ENDPOINTS
    value: "etcd:2379"
```

> ⚠️ `search_path=public` 不能省略：pgx v5 驱动连接时会重置 search_path，不加会导致找不到表。

## 🗂️ 项目结构

```
dev-toolkit/
├── cmd/dtk/                    # CLI 入口
│   ├── main.go                 # runInit / runDeploy
│   └── preflight.go            # deploy 前置检查
├── internal/
│   ├── planner/                # AI 规划（LoadComponents + BuildPlan）
│   ├── scaffold/               # 项目生成逻辑
│   │   ├── scaffold.go         # InitProject 主流程 + 工具函数
│   │   ├── skeleton.go         # 各种 write*Skeleton 小函数
│   │   ├── helm.go             # writeHelmTemplateSkeleton
│   │   ├── migration.go        # writeMigrationSkeleton
│   │   ├── frontend.go         # writeFrontendSkeleton
│   │   ├── monitoring.go       # writeMonitoringSkeleton
│   │   └── scaffold_test.go
│   └── logger/                 # slog 初始化
├── scripts/make-rules/         # Makefile 规则
│   └── deploy.mk               # 完整部署流程
├── deployments/project/        # Helm chart 模板（dtk init 复制用）
└── docs/
    ├── guide/zh-CN/            # 使用指南
    └── design/                 # 设计文档
```

## 🛠️ Makefile 常用目标

```bash
make build          # 编译
make test           # 单元测试
make lint           # golangci-lint
make tools          # 安装所有工具链
make image          # 构建镜像
make deploy.full    # helm + rollout
make help           # 完整列表
```

## 🐛 常见问题

**deploy 卡住不动**

`--wait` 在等 pod ready，另开终端查看：

```bash
kubectl get pods -n <ns>
kubectl logs -n <ns> <pod> -c wait-postgres   # 看 initContainer
kubectl describe pod -n <ns> <pod>
```

**etcd 日志有大量 `unrecognized environment variable: ETCD_SERVICE_PORT_CLIENT`**

K8s 默认注入同 namespace 的 Service 环境变量，`ETCD_` 前缀的变量会被 etcd 误读为配置，是 warn 非 error，不影响运行。生产环境可加 `enableServiceLinks: false`。

**migration 报 `duplicate migration file`**

`migrations/` 目录下有重复版本号，删掉旧的即可：

```bash
ls internal/db/migrations/
```

**SSA field manager 冲突**

```bash
kubectl patch deployment <name> -n <ns> \
  --type=merge -p '{"metadata":{"managedFields":null}}'
```

## 📚 文档

- [部署指南](docs/guide/zh-CN/deploy.md)
- [Helm chart 指南](docs/guide/zh-CN/helm.md)
- [Helm chart 设计](docs/design/helm-chart-design.md)
- [TODO](TODO.md)

## 📄 License

MIT
