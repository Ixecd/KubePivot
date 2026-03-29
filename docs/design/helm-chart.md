# dtk Helm Chart 设计文档

> 适用：所有通过 `dtk init` 生成的项目（v1.0.0+）

---

## 设计原则

### 零外部依赖

所有组件 yaml 自己维护，不依赖任何第三方 chart。

**原因**：Bitnami 2025 年对免费用户限制镜像访问，`helm dependency update` 随时可能失败；第三方 chart 的 probe、启动脚本与自选镜像不匹配。

### 每个服务独立 helm release

v1.0.0 起从单 chart 改为多 chart，每个服务有独立 release：

```
{project}-postgres
{project}-etcd
{project}-wallet-service
{project}-controller
```

**为什么**：独立回滚不影响其他服务，按依赖顺序部署，出问题容易定位。

### 组件命名加项目名前缀

`{name}-postgres`、`{name}-etcd` 避免同 namespace 下多项目冲突。

---

## 目录结构

`dtk init` 生成四个独立 chart 目录：

```
deployments/{name}/
├── {name}-postgres/           # StatefulSet 独立 chart
│   ├── Chart.yaml
│   ├── values.yaml            # storage: 1Gi
│   └── templates/
│       ├── statefulset.yaml
│       └── service.yaml
│
├── {name}-etcd/               # Deployment 独立 chart
│   ├── Chart.yaml
│   └── templates/
│       ├── deployment.yaml
│       └── service.yaml
│
├── {name}/                    # 业务服务 chart
│   ├── Chart.yaml
│   ├── values.yaml
│   └── templates/
│       ├── deployment.yaml    # 含 initContainers
│       ├── service.yaml
│       ├── serviceaccount.yaml
│       └── NOTES.txt
│
└── {name}-controller/         # controller chart（默认 disabled）
    ├── Chart.yaml
    ├── values.yaml            # enabled: false
    └── templates/
        ├── deployment.yaml
        ├── rbac.yaml
        └── configmap.yaml
```

---

## 组件清单

| 组件 | release 名 | 类型 | 镜像 | 持久化 | 端口 |
|------|-----------|------|------|--------|------|
| `{n}-postgres` | `{project}-postgres` | StatefulSet | postgres:16-alpine | PVC 1Gi | 5432 |
| `{n}-etcd` | `{project}-etcd` | Deployment | quay.io/coreos/etcd:v3.5.14 | emptyDir | 2379/2380 |
| `{n}` | `{project}` | Deployment | 用户自定义 | 无 | 8080 |
| `{n}-controller` | `{project}-controller` | Deployment | 用户自定义 | 无 | - |

**etcd 为什么用 emptyDir**：dtk 场景里 etcd 主要用于分布式锁和状态持久化，重启后业务服务会重新注册，代价可接受。生产环境如需持久化改为 PVC 即可。

---

## 启动顺序与依赖

`depends_on`（dtk 层面）控制**部署顺序**，initContainers（K8s 层面）控制**启动顺序**，双重保障：

```
{n}-postgres（先部署）
    ↓ readiness: pg_isready 通过
{n}-etcd（先部署）
    ↓ readiness: GET /health 200
{n}（后部署，initContainers 等待上游）
    initContainers:
        wait-postgres: nc -z {n}-postgres 5432
        wait-etcd:     nc -z {n}-etcd 2379
    ↓ 主容器启动
```

即使 K8s 因某种原因先调度了业务服务，initContainers 会阻塞到依赖就绪。

---

## Probe 设计

### postgres

```yaml
readinessProbe:
  exec:
    command: ["pg_isready", "-U", "user", "-d", "<n>"]
  initialDelaySeconds: 5
  periodSeconds: 5
livenessProbe:
  exec:
    command: ["pg_isready", "-U", "user", "-d", "<n>"]
  initialDelaySeconds: 15
  periodSeconds: 10
```

### etcd

```yaml
readinessProbe:
  httpGet:
    path: /health
    port: 2379
  initialDelaySeconds: 5
  periodSeconds: 5
```

### 业务服务

```yaml
readinessProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 5
```

**业务服务必须实现 `/healthz` 路由**，dtk VALIDATING 阶段依赖此接口，缺少会触发自动回滚。

---

## NOTES.txt

每个业务服务 chart 部署成功后打印：

```
✅ wallet-service 部署成功！

命名空间: web3-blitz
版本:     v0.1.10

快速访问:
  kubectl get pods -n web3-blitz -l app=wallet-service
  kubectl logs -n web3-blitz deployment/wallet-service
  kubectl port-forward -n web3-blitz deployment/wallet-service 2113:2113
```

---

## controller chart

`controller.enabled: false` 时（默认），三个 templates 文件全部不渲染：

- `rbac.yaml`：ServiceAccount + ClusterRole（全量权限，上线前收紧）+ ClusterRoleBinding
- `deployment.yaml`：挂载 resources ConfigMap，注入环境变量
- `configmap.yaml`：`configs/resources.yaml` 内容通过 `--set-file` 注入

启用步骤：
1. 构建 controller 镜像（含 dtk + kubectl + helm）
2. 填写 `deployments/{n}/{n}-controller/values.yaml` 里的 image 信息
3. `enabled: true`，`dtk deploy`

---

## 环境变量约定

业务服务 chart 默认注入：

```yaml
env:
  - name: DATABASE_URL
    value: "postgres://user:pass@{n}-postgres:5432/{n}?sslmode=disable&search_path=public"
  - name: ETCD_ENDPOINTS
    value: "{n}-etcd:2379"
```

**search_path=public 说明**：pgx v5 驱动连接时会重置 search_path 为空，必须在 DSN 中显式指定，否则找不到 public schema 下的表。

**注意**：如果 postgres 用户名/密码/数据库名不是默认值，需要修改 values.yaml 里的 `env`。

---

## 已知问题与解决方案

| 问题 | 原因 | 解决 |
|------|------|------|
| pgx v5 找不到表 | search_path 被重置 | DSN 加 `&search_path=public` |
| Bitnami 镜像拉不下来 | Docker Hub 限制 | 自写 yaml，用官方镜像 |
| pod 启动顺序无法保证 | K8s 无 depends_on | initContainers + nc 探测 |
| SSA 冲突 | field manager 冲突 | `--force-conflicts` + 自动清除 managedFields |
| 老项目迁移 helm ownership 冲突 | 原 release 已管理该资源 | 见 gotchas.md 老项目迁移章节 |
