# dev-toolkit (dtk) 🛠️

> Go 云原生项目脚手架：`dtk init` 生成完整项目骨架，`dtk deploy` 一键 AI 规划 + 多服务拓扑部署 + 状态追踪 + 自动自愈。

---

## 为什么是 dtk？

搭一个新的 Go 服务通常需要：手写 Dockerfile、配 Helm chart、接 postgres/etcd、写迁移脚本、搞 JWT/RBAC、配 Prometheus……这些和业务无关的工作往往要花掉一两天。

dtk 把这些全部模板化。`dtk init` 一条命令，完整可部署的项目骨架生成完毕；`dtk deploy` 一条命令，AI 规划资源 → 拓扑排序 → build → push → 多服务独立 helm release → 状态追踪，全自动。

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

# AI 规划服务配置（可选）
export DTK_LLM_PROVIDER=grok
export DTK_LLM_API_KEY=xai-xxx
dtk ai-plan

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
  --output         输出目录，默认 ./<n>
  --template       模板根目录，或设置 DTK_TEMPLATE_ROOT 环境变量
  --force          强制覆盖已有目录
  --with-frontend  同时生成 React + Vite + Tailwind 前端骨架
```

生成内容：

```
myapp/
├── cmd/myapp/               # 服务入口（含 /healthz 路由）
├── internal/
│   ├── api/                 # HTTP handler + 路由
│   ├── auth/                # JWT + RBAC 中间件
│   ├── db/                  # 数据库连接 + 迁移
│   ├── metrics/             # Prometheus 指标
│   └── pkg/code/            # 业务错误码
├── deployments/myapp/       # 多服务独立 Helm chart（v1.0.0+）
│   ├── myapp-postgres/      # StatefulSet 独立 chart
│   ├── myapp-etcd/          # Deployment 独立 chart
│   ├── myapp/               # 业务服务 chart（含 initContainers）
│   └── myapp-controller/    # A2 Controller chart（默认 disabled）
├── migrations/              # SQL 迁移文件（golang-migrate 格式）
├── configs/
│   ├── project.env          # 部署配置
│   ├── components.yaml      # 服务列表 + 依赖关系（AI 规划输入）
│   └── resources.yaml       # controller 监控的 K8s 资源列表
├── monitoring/              # Prometheus + Alertmanager + Grafana
├── handoff/
│   ├── HANDOFF.md           # 项目上下文，写给下一个 Claude
│   └── AI-CODING-GUIDE.md  # AI 编码约束指南
└── test/                    # e2e / integration / smoke
```

### `dtk ai-plan`

扫描代码仓库，调用 LLM 自动生成 `configs/components.yaml`。

```bash
dtk ai-plan [flags]

Flags:
  --suggest-only  只打印建议，不写入文件
  --desc          补充描述，帮助 LLM 更准确分析

# 配置 LLM provider
export DTK_LLM_PROVIDER=grok      # grok / claude / openai / doubao
export DTK_LLM_API_KEY=xai-xxx
export DTK_LLM_MODEL=grok-3       # 可选，有默认值
export DTK_LLM_ENDPOINT=          # 可选，私有化部署时覆盖
```

支持 Grok（xAI）/ Claude（Anthropic）/ OpenAI / 豆包（字节跳动）四个 provider。

详见 [AI 使用手册](docs/guide/zh-CN/ai.md)。

### `dtk deploy`

按拓扑排序多服务部署：同层并行，层间串行，失败级联 rollback。

```bash
dtk deploy [flags]

Flags:
  --namespace   K8s namespace，默认读 project.env
  --context     kubectl context，默认读 project.env
  --kubeconfig  kubeconfig 文件路径，默认 ~/.kube/config
  --dry-run     只打印规划，不执行
```

部署流程（多服务）：

```
BuildLayers（拓扑排序）→ []Layer

for each 层级（同层并行，层间串行）：
    build → push → helm upgrade --install → rollout status
    ↓ 失败
    级联 rollback（失败服务 + 下游，逆序）
    整组 rollback → dtk down
```

状态机流转：

```
IDLE / RUNNING
      │ dtk deploy
      ▼
INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                   ↓              ↓
             ROLLING_BACK ←───────┘
               CLEANING → IDLE
```

### `dtk resume`

检查 K8s 实际状态，从中断点恢复。

### `dtk rollback`

手动触发整组 helm rollback，按拓扑逆序回滚所有 release。

### `dtk release`

打版本 tag，更新 VERSION，可选触发部署。

```bash
dtk release --version v1.2.0 [--deploy] [--push=false]
```

### `dtk down`

彻底下线服务，删除所有集群资源和本地状态文件。

### `dtk status` / `dtk history` / `dtk diff` / `dtk doctor`

```bash
dtk status    [--history]       # 查看部署状态
dtk history   [-n 20]           # 查看状态转换历史
dtk diff      [--from N --to M] # 对比两个版本配置差异
dtk doctor                      # 检查环境依赖
```

---

## 关键配置

### `configs/project.env`

```ini
PROJECT_NAME=myapp
REGISTRY_PREFIX=your-dockerhub-username   # 必填
KUBE_CONTEXT=                             # 留空=当前 context
KUBE_NAMESPACE=myapp
ARCH=arm64                               # arm64 或 amd64，dtk init 自动检测
VERSION=v0.1.0                           # 改这个触发重新 build + push
ETCD_ENDPOINTS=                          # 留空=本地文件存储状态
```

### `configs/components.yaml`

```yaml
components:
  - name: postgres
    type: statefulset          # deployment（默认）/ statefulset
    port: 5432
    image: ""                  # 空 = 跳过 build/push，使用预置镜像

  - name: etcd
    type: deployment
    port: 2379
    image: ""

  - name: myapp
    type: deployment
    port: 8080
    image: myapp
    depends_on:                # 依赖关系，dtk 按拓扑顺序部署
      - postgres
      - etcd
```

`dtk ai-plan` 可以自动生成这个文件。

---

## A2 Reconciliation Controller

Controller 作为独立 Deployment 运行在 K8s 里，统一命名为 `dev-toolkit-controller`，部署在各项目自己的 namespace，所有项目共用同一镜像：

```
dtk deploy（CLI）→ 写状态到 etcd → 退出
                        ↓
dev-toolkit-controller（K8s pod，常驻）
    ├── etcd Watch（事件驱动，指数退避重连）
    └── 8s 周期 Reconcile（兜底）
            ↓
        检测资源缺失 → helm rollback → 自动恢复（~10s）
```

监控的资源由 `configs/resources.yaml` 配置：

```yaml
resources:
  - kind: Deployment
    name: myapp
    on-missing: auto-heal
    max-retry: 3
    fallback: rollback
```

启用 controller：
1. 构建 `dev-toolkit-controller` 镜像（包含 dtk + kubectl + helm），全局只需构建一次
2. 编辑 `deployments/<n>/<n>-controller/values.yaml`：`enabled: true`，填写 image
3. `dtk deploy`

---

## 状态机

部署状态持久化到 etcd（优先）或 `~/.dtk/state/<project>/<ns>.json`（降级）。详见 [状态机设计文档](docs/design/state-machine.md)。

---

## 常见问题

**部署卡在 VALIDATING**

业务服务必须实现 `/healthz` 路由返回 200。

**当前状态为 DEPLOYING，不能发起新部署**

上次部署被中断，运行 `dtk resume` 恢复。

**多服务 helm upgrade 失败：ownership 冲突**

老项目迁移到多 chart 结构时会遇到，见 [gotchas.md](docs/guide/zh-CN/gotchas.md) 老项目迁移章节。

**helm upgrade 报 pending-rollback**

```bash
kubectl scale deployment/dev-toolkit-controller -n myapp --replicas=0
kubectl delete secret -n myapp \
  $(kubectl get secret -n myapp -l owner=helm,name=myapp \
    -o jsonpath='{.items[?(@.metadata.labels.status=="pending-rollback")].metadata.name}')
dtk deploy
```

---

## 文档

- [Quickstart](docs/guide/zh-CN/quickstart.md)
- [部署指南](docs/guide/zh-CN/deploy.md)
- [AI 使用手册](docs/guide/zh-CN/ai.md)
- [命令参考手册](docs/guide/zh-CN/commands.md)
- [Helm chart 指南](docs/guide/zh-CN/helm.md)
- [多集群部署](docs/guide/zh-CN/kubeconfig.md)
- [整体架构设计](docs/design/architecture.md)
- [多服务支持设计](docs/design/multi-service.md)
- [状态机设计](docs/design/state-machine.md)
- [A2 Controller 设计](docs/design/controller.md)
- [部署架构设计](docs/design/deploy-architecture.md)

---

## License

MIT
