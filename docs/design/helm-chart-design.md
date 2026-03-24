# dtk Helm Chart 设计文档

> 版本：2026-03-24
> 适用：所有通过 `dtk init` 生成的项目

---

## 设计原则

### 零外部依赖
`helm install` 一条命令部署所有组件，不依赖任何第三方 chart。

**原因：**
- Bitnami 2025年8月后对免费用户限制镜像访问，`helm dependency update` 随时可能失败
- 第三方 chart 的 probe、启动脚本与自选镜像不匹配（healthcheck.sh 问题）
- 生产环境不应依赖外部 registry 的可用性

### 基础设施即代码
所有组件（postgres、etcd、业务服务）均以 yaml 文件存放在 `deployments/<name>/templates/` 下，版本化管理，可追溯。

### 组件职责分离

```
deployments/<name>/templates/
├── postgres-statefulset.yaml   # 数据库，StatefulSet + PVC
├── etcd-deployment.yaml        # 分布式协调，Deployment + emptyDir
├── <name>-deployment.yaml      # 业务服务，依赖上面两个
└── service.yaml                # 业务服务对外暴露
```

---

## 组件清单

| 组件 | 类型 | 镜像 | 持久化 | 端口 |
|------|------|------|--------|------|
| postgres | StatefulSet | postgres:16-alpine | PVC 1Gi | 5432 |
| etcd | Deployment | quay.io/coreos/etcd:v3.5.14 | emptyDir | 2379/2380 |
| `<name>` | Deployment | 用户自定义 | 无 | 8080 |

**etcd 为什么用 emptyDir：**
etcd 在 dtk 场景里主要用于分布式锁和服务注册，不存储业务数据。重启后业务服务会重新注册，代价可接受。生产环境如需持久化可改为 PVC。

---

## 启动顺序与依赖

```
postgres (StatefulSet)
    ↓ readiness: pg_isready 通过
etcd (Deployment)
    ↓ readiness: GET /health 200
<name> (Deployment)
    initContainers:
        wait-postgres: nc -z postgres 5432
        wait-etcd:     nc -z etcd 2379
    ↓ 主容器启动
    → 连接 postgres，执行 migrate
    → 连接 etcd，注册服务
    → 启动 HTTP 服务
```

### initContainers 实现

```yaml
initContainers:
  - name: wait-postgres
    image: busybox:1.35
    command: ['sh', '-c', 'until nc -z postgres 5432; do echo waiting for postgres; sleep 2; done']
  - name: wait-etcd
    image: busybox:1.35
    command: ['sh', '-c', 'until nc -z etcd 2379; do echo waiting for etcd; sleep 2; done']
```

**为什么用 initContainers 而不是 depends_on：**
K8s 没有原生的服务依赖机制，`depends_on` 是 docker-compose 概念。initContainers 是 K8s 原生方案，pod 内串行执行，主容器保证在所有 initContainers 成功后才启动。

---

## Probe 设计

### postgres
```yaml
readinessProbe:
  exec:
    command: ["pg_isready", "-U", "<user>", "-d", "<db>"]
  initialDelaySeconds: 5
  periodSeconds: 5

livenessProbe:
  exec:
    command: ["pg_isready", "-U", "<user>", "-d", "<db>"]
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

livenessProbe:
  httpGet:
    path: /health
    port: 2379
  initialDelaySeconds: 10
  periodSeconds: 10
```

### 业务服务
```yaml
readinessProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 5

livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 10
  periodSeconds: 10
```

---

## 环境变量约定

业务服务通过环境变量连接基础设施，service name 即 DNS 名：

```yaml
env:
  - name: DATABASE_URL
    value: "postgres://<user>:<pass>@postgres:5432/<db>?sslmode=disable&search_path=public"
  - name: ETCD_ENDPOINTS
    value: "etcd:2379"
```

**search_path=public 说明：**
pgx v5 驱动连接时会重置 search_path 为空，必须在 DSN 中显式指定，否则找不到 public schema 下的表。

---

## Chart.yaml 规范

```yaml
apiVersion: v2
name: <name>
description: A Helm chart for <name>
type: application
version: 0.1.0
appVersion: "0.1.0"
dependencies: []   # 永远为空，所有组件自己维护
```

---

## values.yaml 规范

```yaml
# 业务服务
replicaCount: 1
image:
  repository: <registry>/<name>-<arch>
  pullPolicy: IfNotPresent
  tag: ""

service:
  type: ClusterIP
  port: 8080

# 环境变量（通过 deployment template 注入）
env:
  - name: DATABASE_URL
    value: "postgres://user:pass@postgres:5432/<name>?sslmode=disable&search_path=public"
  - name: ETCD_ENDPOINTS
    value: "etcd:2379"

# Probe
livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 10
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 5
```

---

## 目录结构（dtk init 生成）

```
deployments/<name>/
├── Chart.yaml                        # dependencies: []
├── values.yaml                       # 业务服务配置
└── templates/
    ├── _helpers.tpl
    ├── postgres-statefulset.yaml     # postgres StatefulSet + Service
    ├── etcd-deployment.yaml          # etcd Deployment + Service
    ├── deployment.yaml               # 业务服务，含 initContainers
    ├── service.yaml
    ├── serviceaccount.yaml
    └── NOTES.txt
```

---

## 扩展场景

### 不需要 postgres
如果项目不用数据库，删掉 `postgres-statefulset.yaml`，`deployment.yaml` 里去掉 wait-postgres initContainer 和 DATABASE_URL env。

### 不需要 etcd
同上，删掉 `etcd-deployment.yaml`，去掉 wait-etcd initContainer 和 ETCD_ENDPOINTS env。

### 生产环境 etcd 持久化
把 `etcd-deployment.yaml` 里的 `emptyDir` 改为 PVC，或者换成 StatefulSet。

---

## 已知问题与解决方案

| 问题 | 原因 | 解决 |
|------|------|------|
| pgx v5 找不到表 | search_path 被重置 | DSN 加 `&search_path=public` |
| Bitnami 镜像拉不下来 | Docker Hub 限制 | 自写 yaml，用官方镜像 |
| Bitnami healthcheck.sh 不存在 | 镜像与 chart 不匹配 | 用官方镜像 + 标准 probe |
| etcd readiness 60s | Bitnami 默认值 | 自写 yaml，initialDelaySeconds: 5 |
| pod 启动顺序无法保证 | K8s 无 depends_on | initContainers + nc 探测 |
