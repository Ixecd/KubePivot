# dev-toolkit (dtk) 🛠️

> Go 云原生项目脚手架：`dtk init` 生成完整项目骨架，`dtk deploy` 一键 AI 规划 + K8s 部署 + 状态追踪 + 自动自愈。

---

## 为什么是 dtk？

搭一个新的 Go 服务通常需要：手写 Dockerfile、配 Helm chart、接 postgres/etcd、写迁移脚本、搞 JWT/RBAC、配 Prometheus……这些和业务无关的工作往往要花掉一两天。

dtk 把这些全部模板化。`dtk init` 一条命令，完整可部署的项目骨架生成完毕；`dtk deploy` 一条命令，AI 规划资源 → build → push → helm upgrade → 状态追踪，全自动。

---

## 快速开始

详细步骤见 [quickstart.md](docs/guide/zh-CN/quickstart.md)，这里是最短路径：

```bash
# 安装
go install github.com/Ixecd/dev-toolkit/cmd/dtk@latest

# 生成项目
dtk init --name myapp --module github.com/me/myapp
cd myapp

# 编辑部署配置
vim configs/project.env   # 填 REGISTRY_PREFIX、KUBE_CONTEXT

# 部署
dtk deploy
```

---

## 命令

### `dtk init`

生成完整 Go 项目骨架。

```bash
dtk init --name myapp --module github.com/me/myapp [flags]

Flags:
  --name           项目名（小写字母、数字、-）必填
  --module         Go module 路径，默认同 --name
  --output         输出目录，默认 ./<name>
  --template       模板根目录，或设置 DTK_TEMPLATE_ROOT 环境变量
  --force          强制覆盖已有目录
  --with-frontend  同时生成 React + Vite + Tailwind 前端骨架
```

生成内容：

```
myapp/
├── cmd/myapp/               # 服务入口
├── internal/
│   ├── api/                 # HTTP handler + 路由
│   ├── auth/                # JWT + RBAC 中间件
│   ├── db/                  # 数据库连接 + 迁移
│   ├── metrics/             # Prometheus 指标
│   └── pkg/code/            # 业务错误码
├── deployments/myapp/       # 自包含 Helm chart
│   └── templates/
│       ├── deployment.yaml          # 业务服务，含 initContainers 启动顺序
│       ├── myapp-postgres-*.yaml    # postgres:16-alpine + PVC
│       ├── myapp-etcd-*.yaml        # coreos/etcd:v3.5.14
│       ├── controller-*.yaml        # A2 Reconciliation Controller 骨架
│       └── NOTES.txt                # 部署后组件状态展示
├── migrations/              # SQL 迁移文件（golang-migrate 格式）
├── configs/
│   ├── project.env          # 部署配置
│   ├── components.yaml      # AI 规划输入
│   └── resources.yaml       # controller 监控的 K8s 资源列表
├── monitoring/              # Prometheus + Alertmanager + Grafana
├── snapshots/               # 里程碑快照归档
├── handoff/
│   ├── HANDOFF.md           # 项目上下文，写给下一个 Claude
│   └── AI-CODING-GUIDE.md  # AI 编码约束，填充业务逻辑的指南
└── test/                    # e2e / integration / smoke
```

### `dtk deploy`

AI 规划资源 → build → push → helm upgrade → 状态追踪。

```bash
dtk deploy [flags]

Flags:
  --namespace   K8s namespace，默认读 project.env
  --context     kubectl context，默认读 project.env
  --kubeconfig  kubeconfig 文件路径，默认 ~/.kube/config
  --dry-run     只打印规划，不执行
```

部署流程：

```
IDLE / RUNNING
      │ dtk deploy
      ▼
INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                   ↓              ↓
             ROLLING_BACK ←───────┘  （失败自动回滚）
                   ↓
               CLEANING → IDLE      （首次部署失败，清理 namespace）
```

### `dtk resume`

检查 K8s 实际状态，从中断点恢复。适用于进程被意外终止的情况。

```bash
dtk resume [--namespace <ns>] [--context <ctx>] [--kubeconfig <path>]
```

### `dtk rollback`

手动触发 helm rollback，回到上一个版本。

```bash
dtk rollback [--namespace <ns>] [--context <ctx>] [--kubeconfig <path>]
```

### `dtk release`

打版本 tag，更新 VERSION，可选触发部署。

```bash
dtk release --version v1.2.0 [--deploy] [--no-push]
```

完整流程：校验 semver 格式 → 检查工作区干净 → 更新 `configs/project.env` → git commit + tag + push → 可选触发 `dtk deploy`。

### `dtk down`

彻底下线服务，删除所有集群资源和本地状态文件。

```bash
dtk down [--namespace <ns>] [--context <ctx>] [--kubeconfig <path>]
```

执行内容（二次确认后）：删除 ClusterRole/ClusterRoleBinding → 删除 namespace → 删除本地状态文件。

### `dtk controller start`

在 controller pod 内部运行，不需要手动调用。

---

## 关键配置

### `configs/project.env`

```ini
PROJECT_NAME=myapp
REGISTRY_PREFIX=your-dockerhub-username   # 必填，Docker Hub 用户名或镜像仓库前缀
KUBE_CONTEXT=                             # 留空=当前 context
KUBE_CONFIG=                             # 留空=~/.kube/config
KUBE_NAMESPACE=myapp
ARCH=arm64                               # 本机架构，arm64 或 amd64
VERSION=v0.1.0                           # 改这个触发重新 build + push
ETCD_ENDPOINTS=                          # 留空=本地文件存储状态
```

### `configs/components.yaml`

```yaml
components:
  - name: myapp
    port: 8080
    image: myapp     # 留空则跳过 build/push（适合纯 CLI 工具）
```

### `deployments/myapp/values.yaml`

基础设施组件开关：

```yaml
postgres:
  enabled: true   # 改为 false 则不部署 postgres，initContainers 自动跳过

etcd:
  enabled: true

controller:
  enabled: false  # 配置好镜像后改为 true，再 dtk deploy
  image:
    repository: your-registry/myapp-controller
    tag: latest
```

---

## A2 Reconciliation Controller

Controller 作为独立 Deployment 运行在 K8s 里，不依赖 CLI 进程：

```
dtk deploy（CLI）→ 写状态到 etcd
                        ↓
myapp-controller（K8s pod）
    ├── etcd Watch（事件驱动，实时响应）
    └── 8s 周期 Reconcile（兜底）
            ↓
        检测资源缺失 → helm rollback → 自动恢复
```

监控的资源由 `configs/resources.yaml` 配置，新增资源只改配置，不改代码：

```yaml
resources:
  - kind: Deployment
    name: myapp
    on-missing: auto-heal   # 缺失时自动 helm rollback 恢复
    max-retry: 3
    fallback: rollback

  # - kind: StatefulSet
  #   name: myapp-postgres
  #   on-missing: auto-heal
```

启用 controller：
1. 构建包含 `dtk` 二进制 + kubectl + helm 的镜像（参考 `build/docker/controller/Dockerfile`）
2. 填写 `values.yaml` 里的 `controller.image.repository`
3. 将 `controller.enabled` 改为 `true`
4. `dtk deploy`

---

## 状态机

部署状态持久化到 etcd（优先）或 `~/.dtk/state/<project>/<ns>.json`（降级）：

```json
{
  "state": "RUNNING",
  "version": "v0.1.5",
  "reason": "部署成功",
  "updated_at": "2026-03-27T07:52:21Z",
  "history": [...]
}
```

详见 [状态机设计文档](docs/design/state-machine.md)。

---

## 常见问题

**部署卡在 VALIDATING**

业务服务必须实现 `/healthz` 路由返回 200：

```go
mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
})
```

**当前状态为 DEPLOYING，不能发起新部署**

上次部署被中断，运行 `dtk resume` 恢复。

**helm upgrade 卡住或报 pending-rollback**

```bash
# 停掉 controller，清理 pending secret，重置状态
kubectl scale deployment/myapp-controller -n myapp --replicas=0
kubectl delete secret -n myapp \
  $(kubectl get secret -n myapp -l owner=helm,name=myapp \
    -o jsonpath='{.items[?(@.metadata.labels.status=="pending-rollback")].metadata.name}')
# 重新 deploy
dtk deploy
```

**etcd 日志有大量 `unrecognized environment variable`**

K8s Service 环境变量注入产生的 warn，非 error，不影响运行。

---

## 文档

- [Quickstart](docs/guide/zh-CN/quickstart.md)
- [部署指南](docs/guide/zh-CN/deploy.md)
- [Helm chart 指南](docs/guide/zh-CN/helm.md)
- [多集群部署](docs/guide/zh-CN/kubeconfig.md)
- [状态机设计](docs/design/state-machine.md)
- [整体架构设计](docs/design/architecture.md)
- [A2 Controller 设计](docs/design/controller.md)
- [命令参考手册](docs/guide/zh-CN/commands.md)

---

## License

MIT
