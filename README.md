# dev-toolkit (dtk) 🛠️

> Go 云原生项目脚手架：`dtk init` 生成完整项目骨架，`dtk deploy` 一键 AI 规划 + K8s 部署 + 状态追踪。

## 🚀 Quickstart

```bash
go install github.com/Ixecd/dev-toolkit/cmd/dtk@latest

dtk init --name myapp --module github.com/me/myapp
cd myapp
make tools                    # 安装工具链（首次必须）

# 编辑 configs/project.env，填写 REGISTRY_PREFIX / KUBE_CONTEXT / VERSION
dtk deploy
```

## ✨ 功能

- **`dtk init`**：生成完整 Go 项目骨架，含自包含 Helm chart、golang-migrate、JWT/RBAC、监控、测试、前端（可选）
- **`dtk deploy`**：读取 `components.yaml`，AI 规划资源，自动 build → push → helm upgrade → rollout → validate
- **`dtk resume`**：检查 K8s 实际状态，从中断点恢复部署
- **`dtk rollback`**：手动触发 helm rollback，一键回到上一版本
- **状态机**：部署状态持久化到 etcd / 本地文件，自动回滚，完整历史记录
- **自包含 Helm chart**：postgres + etcd + 业务服务全在 `templates/`，零外部 chart 依赖
- **多集群支持**：`--kubeconfig` + `--context` 指定任意集群

## 📋 命令

### dtk init

```bash
dtk init --name myapp --module github.com/me/myapp [flags]

Flags:
  --name           项目名（小写字母、数字、-）
  --module         Go module 路径
  --output         输出目录，默认 ./<n>
  --template       模板根目录，或设置 DTK_TEMPLATE_ROOT
  --force          强制覆盖已有目录
  --with-frontend  同时生成 React + Vite + Tailwind 前端骨架
```

### dtk deploy

```bash
dtk deploy [flags]

Flags:
  --components  components.yaml 路径，默认 configs/components.yaml
  --namespace   K8s namespace，默认读 project.env
  --context     kubectl context，默认读 project.env
  --kubeconfig  kubeconfig 文件路径，默认 ~/.kube/config
  --dry-run     只打印规划，不执行
```

部署流程（带状态机）：

```
IDLE/RUNNING
    │ dtk deploy
    ▼
INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                   ↓              ↓
             ROLLING_BACK ←───────┘（失败自动回滚）
```

### dtk resume

```bash
dtk resume [--namespace <ns>] [--context <ctx>] [--kubeconfig <path>]
```

检查 K8s 实际状态，从中断点恢复。适用于进程被意外终止的情况。

### dtk rollback

```bash
dtk rollback [--namespace <ns>] [--context <ctx>] [--kubeconfig <path>]
```

手动触发 `helm rollback`，回到上一个版本。

## 🏗️ 生成的项目结构

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
│       ├── postgres-statefulset.yaml  # postgres:16-alpine + PVC
│       ├── etcd-deployment.yaml       # coreos/etcd:v3.5.14
│       └── deployment.yaml            # 含 initContainers 启动顺序
├── build/docker/myapp/Dockerfile
├── configs/
│   ├── project.env                 # 部署配置
│   └── components.yaml             # AI 规划输入
├── monitoring/                     # Prometheus + Alertmanager + Grafana
└── test/                           # e2e / integration / smoke
```

## ⚙️ 关键配置

**`configs/project.env`**：

```ini
PROJECT_NAME=myapp
REGISTRY_PREFIX=your-dockerhub-username   # 必填
KUBE_CONTEXT=                             # 留空=当前 context，禁止写 ""
KUBE_CONFIG=                              # 留空=~/.kube/config
KUBE_NAMESPACE=myapp
ARCH=arm64
VERSION=v0.1.0                            # 改这个触发重新 build+push
ETCD_ENDPOINTS=                           # 留空=本地文件存储状态
```

**`configs/components.yaml`**：

```yaml
components:
  - name: myapp
    port: 8080
    image: myapp     # 留空 = 跳过部署（CLI 工具防 CrashLoopBackOff）
```

## 🔄 状态机

部署状态持久化到 `~/.dtk/state/<project>/<ns>.json`（有 etcd 时用 etcd）：

```json
{
  "state": "RUNNING",
  "version": "v0.1.5",
  "reason": "部署成功",
  "history": [...]
}
```

详见 [状态机设计文档](docs/design/state-machine.md)。

## 📚 文档

- [部署指南](docs/guide/zh-CN/deploy.md)
- [Helm chart 指南](docs/guide/zh-CN/helm.md)
- [多集群部署](docs/guide/zh-CN/kubeconfig.md)
- [状态机设计](docs/design/state-machine.md)
- [Helm chart 设计](docs/design/helm-chart-design.md)

## 🐛 常见问题

**部署卡在 VALIDATING**

业务服务必须实现 `/healthz` 路由返回 200：

```go
mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
})
```

**当前状态为 DEPLOYING，不能发起新部署**

上次部署被中断，运行 `dtk resume` 恢复。

**etcd 日志有大量 `unrecognized environment variable`**

K8s Service 环境变量注入问题，warn 非 error，不影响运行。详见 [Helm chart 指南](docs/guide/zh-CN/helm.md)。

## 📄 License

MIT
