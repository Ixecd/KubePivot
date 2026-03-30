# Helm Chart 指南

`kp init` 生成的项目自带**零外部依赖**的 Helm chart，每个服务独立一个 chart 目录，`kp deploy` 按依赖顺序全部拉起。

---

## 目录结构（v1.0.0+）

```
deployments/<n>/
├── <n>-postgres/              # StatefulSet 独立 chart
│   ├── Chart.yaml
│   ├── values.yaml            # storage: 1Gi
│   └── templates/
│       ├── statefulset.yaml
│       └── service.yaml
│
├── <n>-etcd/                  # Deployment 独立 chart
│   ├── Chart.yaml
│   └── templates/
│       ├── deployment.yaml
│       └── service.yaml
│
├── <n>/                       # 业务服务 chart
│   ├── Chart.yaml
│   ├── values.yaml
│   └── templates/
│       ├── deployment.yaml    # 含 initContainers
│       ├── service.yaml
│       ├── serviceaccount.yaml
│       └── NOTES.txt
│
└── <n>-controller/            # controller chart（默认 disabled）
    ├── Chart.yaml
    ├── values.yaml
    └── templates/
        ├── deployment.yaml
        ├── rbac.yaml
        └── configmap.yaml
```

每个服务对应独立的 helm release：`{project}-{service}`。

---

## 启动顺序

`depends_on`（kp 层面）控制**部署顺序**，`initContainers`（K8s 层面）控制**启动顺序**，双重保障：

```
<n>-postgres（先部署）
    ↓ readiness: pg_isready 通过
<n>-etcd（先部署）
    ↓ readiness: GET /health 200
<n>（后部署）
    initContainers:
        wait-postgres: nc -z <n>-postgres 5432
        wait-etcd:     nc -z <n>-etcd 2379
    ↓ 主容器启动
    → 连接 postgres，执行 migrate
    → 连接 etcd
    → 启动 HTTP 服务
```

即使 K8s 因某种原因先调度了业务服务，initContainers 会阻塞到依赖就绪。

---

## 组件说明

### postgres

```yaml
image: postgres:16-alpine
storage: PVC 1Gi（重启数据不丢）
probe: pg_isready -U user -d <n>
service name: <n>-postgres
```

**连接串格式：**

```
postgres://user:pass@<n>-postgres:5432/<n>?sslmode=disable&search_path=public
```

⚠️ **`search_path=public` 不能省略**：pgx v5 驱动连接时会重置 `search_path` 为空，不加这个参数会导致 Go 程序找不到 `public` schema 下的任何表。

### etcd

```yaml
image: quay.io/coreos/etcd:v3.5.14
storage: emptyDir（重启后重新注册，代价可接受）
probe: GET /health
service name: <n>-etcd
```

**单节点必须显式指定 peer 参数：**

```yaml
command:
  - etcd
  - --listen-client-urls=http://0.0.0.0:2379
  - --advertise-client-urls=http://<n>-etcd:2379
  - --listen-peer-urls=http://0.0.0.0:2380
  - --initial-advertise-peer-urls=http://0.0.0.0:2380   # 必须
  - --initial-cluster=default=http://0.0.0.0:2380        # 必须
```

缺少后两行会导致 `initial-cluster` 和 `initial-advertise-peer-urls` 不一致，etcd 无法启动。

### 业务服务

```yaml
initContainers:
  - name: wait-postgres
    image: busybox:1.35
    command: ['sh', '-c', 'until nc -z <n>-postgres 5432; do sleep 2; done']
  - name: wait-etcd
    image: busybox:1.35
    command: ['sh', '-c', 'until nc -z <n>-etcd 2379; do sleep 2; done']
```

---

## values.yaml 配置项

```yaml
replicaCount: 1

image:
  repository: qingchun22/<n>-arm64
  pullPolicy: IfNotPresent
  tag: ""           # kp deploy 会通过 --set image.tag=VERSION 覆盖

service:
  type: ClusterIP
  port: 8080

resources: {}

env:
  - name: DATABASE_URL
    value: "postgres://user:pass@<n>-postgres:5432/<n>?sslmode=disable&search_path=public"
  - name: ETCD_ENDPOINTS
    value: "<n>-etcd:2379"
```

---

## 自定义配置

### 修改数据库密码

修改 `<n>-postgres/templates/statefulset.yaml` 里的 `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB`，同步修改业务服务 chart `values.yaml` 里的 `DATABASE_URL`。

### 使用 K8s Secret 管理敏感配置（推荐生产）

```bash
kubectl create secret generic wallet-secrets -n <n> \
  --from-literal=DATABASE_URL="postgres://user:pass@<n>-postgres:5432/<n>?sslmode=disable&search_path=public" \
  --from-literal=JWT_SECRET="your-jwt-secret"
```

```yaml
# values.yaml
envFrom:
  - secretRef:
      name: wallet-secrets
```

### 增大 postgres 存储

修改 `<n>-postgres/values.yaml`：

```yaml
storage: 10Gi
```

⚠️ PVC 创建后不能直接修改 storage，需要先扩容 PVC 或重建 StatefulSet。

### etcd 持久化（生产环境）

默认 etcd 用 `emptyDir`，重启后数据丢失（业务服务重新注册即可）。如需持久化，把 `<n>-etcd/templates/deployment.yaml` 改为 StatefulSet + PVC。

---

## 不需要 postgres / etcd 的项目

删掉对应的 chart 目录，同时删掉业务服务 chart `deployment.yaml` 里对应的 initContainer，更新 `components.yaml` 移除该组件的 `depends_on`。

---

## 常见问题

**etcd 日志有大量 `unrecognized environment variable: ETCD_SERVICE_PORT_CLIENT`**

K8s 默认把同 namespace 的所有 Service 注入到每个 pod 的环境变量。etcd 把 `ETCD_` 前缀变量都当配置读，`ETCD_SERVICE_PORT_CLIENT` 是 K8s 自动注入的，会触发 warn。这是 warn 不是 error，不影响运行。生产环境可加：

```yaml
spec:
  enableServiceLinks: false
```

**wallet-service 卡在 `Init:0/2`**

initContainers 在等 postgres 和 etcd。注意 service name 是 `<n>-postgres` 和 `<n>-etcd`（带项目前缀），老项目迁移时 service name 可能不同，见 [gotchas.md](gotchas.md) 老项目迁移章节。

```bash
kubectl logs -n <n> <pod-name> -c wait-postgres
kubectl logs -n <n> <pod-name> -c wait-etcd
kubectl get pods -n <n>
```

**postgres pod 起来了但 wallet-service 还是连不上**

确认 `DATABASE_URL` 里的 `search_path=public` 存在，service name 是 `<n>-postgres` 不是 `postgres`。

**migration 报 `duplicate migration file`**

`migrations/` 目录下有两个相同版本号的文件，版本号必须全局唯一：

```bash
ls internal/db/migrations/
```
