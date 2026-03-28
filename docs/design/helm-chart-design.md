# dtk Helm Chart 设计文档

> 适用：所有通过 `dtk init` 生成的项目

---

## 设计原则

### 零外部依赖
`helm install` 一条命令部署所有组件，不依赖任何第三方 chart。

**原因**：
- Bitnami 2025 年对免费用户限制镜像访问，`helm dependency update` 随时可能失败
- 第三方 chart 的 probe、启动脚本与自选镜像不匹配（healthcheck.sh 不存在问题）
- 生产环境不应依赖外部 registry 的可用性

### 配置驱动，按需启用

所有基础设施组件都有 `enabled` 开关：

```yaml
postgres:
  enabled: true

etcd:
  enabled: true

controller:
  enabled: false  # 配置好镜像后再开启
```

### 组件命名加项目名前缀

`{name}-postgres`、`{name}-etcd` 避免同 namespace 下多项目冲突。

---

## 组件清单

| 组件 | 类型 | 镜像 | 持久化 | 端口 |
|------|------|------|--------|------|
| `{n}-postgres` | StatefulSet | postgres:16-alpine | PVC 1Gi | 5432 |
| `{n}-etcd` | Deployment | quay.io/coreos/etcd:v3.5.14 | emptyDir | 2379/2380 |
| `{n}` | Deployment | 用户自定义 | 无 | 8080 |
| `{n}-controller` | Deployment | 用户自定义 | 无 | - |

**etcd 为什么用 emptyDir**：dtk 场景里 etcd 主要用于分布式锁和状态持久化，重启后业务服务会重新注册，代价可接受。生产环境如需持久化改为 PVC 即可。

---

## 启动顺序与依赖

```
{n}-postgres（StatefulSet）
    ↓ readiness: pg_isready 通过
{n}-etcd（Deployment）
    ↓ readiness: GET /health 200
{n}（Deployment）
    initContainers:
        wait-postgres: nc -z {n}-postgres 5432  （仅 postgres.enabled 时）
        wait-etcd:     nc -z {n}-etcd 2379      （仅 etcd.enabled 时）
    ↓ 主容器启动
```

initContainers 带 `{{- if .Values.postgres.enabled }}` guard，关闭组件时自动跳过。两者都关闭时整个 `initContainers:` 块不渲染，避免 K8s 报 empty initContainers 错误。

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

部署成功后 helm 自动打印：

```
✅ myapp 部署成功！

命名空间: myapp
版本:     v0.1.0
时间:     2026-03-27 09:15:26

组件状态:
  业务服务   ✓ running
  postgres  ✓ enabled
  etcd      ✓ enabled
  controller ✗ disabled

快速访问:
  kubectl get pods -n myapp
  kubectl logs -n myapp deployment/myapp
  kubectl port-forward -n myapp deployment/myapp 8080:8080
```

---

## controller 骨架

`controller.enabled: false` 时三个文件全部不渲染：

- `controller-rbac.yaml`：ServiceAccount + ClusterRole（全量权限，上线前收紧）+ ClusterRoleBinding
- `controller-deployment.yaml`：挂载 resources ConfigMap，注入环境变量
- `resources-configmap.yaml`：`configs/resources.yaml` 内容通过 `--set-file` 注入

---

## 环境变量约定

```yaml
env:
  - name: DATABASE_URL
    value: "postgres://user:pass@{n}-postgres:5432/{n}?sslmode=disable&search_path=public"
  - name: ETCD_ENDPOINTS
    value: "{n}-etcd:2379"
```

**search_path=public 说明**：pgx v5 驱动连接时会重置 search_path 为空，必须在 DSN 中显式指定，否则找不到 public schema 下的表。

---

## 目录结构

```
deployments/<n>/
├── Chart.yaml                         # dependencies: []
├── values.yaml                        # 含所有组件 enabled 开关
└── templates/
    ├── _helpers.tpl
    ├── NOTES.txt
    ├── deployment.yaml                # 业务服务，含 initContainers guard
    ├── service.yaml
    ├── serviceaccount.yaml
    ├── hpa.yaml
    ├── ingress.yaml
    ├── {n}-postgres-statefulset.yaml  # postgres，带 enabled guard
    ├── {n}-etcd-deployment.yaml       # etcd，带 enabled guard
    ├── controller-rbac.yaml           # controller，带 enabled guard
    ├── controller-deployment.yaml
    └── resources-configmap.yaml
```

---

## 已知问题与解决方案

| 问题 | 原因 | 解决 |
|------|------|------|
| pgx v5 找不到表 | search_path 被重置 | DSN 加 `&search_path=public` |
| Bitnami 镜像拉不下来 | Docker Hub 限制 | 自写 yaml，用官方镜像 |
| Bitnami healthcheck.sh 不存在 | 镜像与 chart 不匹配 | 官方镜像 + 标准 probe |
| pod 启动顺序无法保证 | K8s 无 depends_on | initContainers + nc 探测 |
| SSA 冲突 | field manager 冲突 | `--force-conflicts` + 自动清除 managedFields |
