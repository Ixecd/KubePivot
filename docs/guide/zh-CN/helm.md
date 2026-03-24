# Helm Chart 指南

`dtk init` 生成的项目自带一套**零外部依赖**的 Helm chart，
包含 postgres、etcd、业务服务三个组件，`dtk deploy` 一条命令全部拉起。

---

## 目录结构

```
deployments/<n>/
├── Chart.yaml                        # dependencies: []，不依赖任何第三方 chart
├── values.yaml                       # 所有可配置项
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

## 启动顺序

K8s 没有原生的服务依赖机制，业务服务通过 `initContainers` 等待基础设施就绪：

```
postgres (StatefulSet)
    ↓ readiness: pg_isready 通过后
etcd (Deployment)
    ↓ readiness: GET /health 200 后
<n> (Deployment)
    initContainers:
        wait-postgres → nc -z postgres 5432 通过
        wait-etcd     → nc -z etcd 2379 通过
    ↓ 主容器启动
    → 连接 postgres，执行 migrate
    → 连接 etcd
    → 启动 HTTP 服务
```

**为什么用 `initContainers`：**

`depends_on` 是 docker-compose 概念，K8s 没有。`initContainers` 在 pod 内串行执行，
主容器保证在所有 initContainers 成功退出后才启动，是 K8s 原生的依赖等待方案。

---

## 组件说明

### postgres

```yaml
# postgres-statefulset.yaml
image: postgres:16-alpine
storage: PVC 1Gi（重启数据不丢）
probe: pg_isready -U user -d <n>
service name: postgres
```

业务服务通过 `postgres:5432` 访问，在同 namespace 内直接用 service name 解析。

**连接串格式：**

```
postgres://user:pass@postgres:5432/<n>?sslmode=disable&search_path=public
```

⚠️ **`search_path=public` 不能省略**：pgx v5 驱动连接时会重置 `search_path` 为空，
不加这个参数会导致 Go 程序找不到 `public` schema 下的任何表，
即使 `docker exec psql` 能正常查到。

### etcd

```yaml
# etcd-deployment.yaml
image: quay.io/coreos/etcd:v3.5.14
storage: emptyDir（重启后重新注册，代价可接受）
probe: GET /health
service name: etcd
```

业务服务通过 `etcd:2379` 访问。

**单节点必须显式指定 peer 参数：**

```yaml
command:
  - etcd
  - --listen-client-urls=http://0.0.0.0:2379
  - --advertise-client-urls=http://etcd:2379
  - --listen-peer-urls=http://0.0.0.0:2380
  - --initial-advertise-peer-urls=http://0.0.0.0:2380   # 必须
  - --initial-cluster=default=http://0.0.0.0:2380        # 必须
```

缺少后两行会导致 `initial-cluster` 和 `initial-advertise-peer-urls` 不一致，etcd 无法启动。

### 业务服务

```yaml
# deployment.yaml
initContainers:
  - name: wait-postgres
    image: busybox:1.35
    command: ['sh', '-c', 'until nc -z postgres 5432; do sleep 2; done']
  - name: wait-etcd
    image: busybox:1.35
    command: ['sh', '-c', 'until nc -z etcd 2379; do sleep 2; done']
```

---

## values.yaml 配置项

```yaml
# 镜像配置
image:
  repository: qingchun22/<n>-arm64   # 对应 REGISTRY_PREFIX/<n>-ARCH
  pullPolicy: IfNotPresent
  tag: ""                             # 留空用 Chart.AppVersion，dtk deploy 会覆盖

# 服务端口
service:
  type: ClusterIP
  port: 8080

# 环境变量注入
env:
  - name: DATABASE_URL
    value: "postgres://user:pass@postgres:5432/<n>?sslmode=disable&search_path=public"
  - name: ETCD_ENDPOINTS
    value: "etcd:2379"

# 健康检查
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

## 自定义配置

### 修改数据库密码

```yaml
# values.yaml — 业务服务连接串
env:
  - name: DATABASE_URL
    value: "postgres://myuser:mypass@postgres:5432/mydb?sslmode=disable&search_path=public"
```

同步修改 `postgres-statefulset.yaml` 里的 `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB`。

### 使用 K8s Secret 管理敏感配置（推荐生产）

```bash
kubectl create secret generic wallet-secrets -n <n> \
  --from-literal=DATABASE_URL="postgres://user:pass@postgres:5432/<n>?sslmode=disable&search_path=public" \
  --from-literal=JWT_SECRET="your-jwt-secret"
```

```yaml
# values.yaml
envFrom:
  - secretRef:
      name: wallet-secrets
```

### 增大 postgres 存储

```yaml
# postgres-statefulset.yaml
volumeClaimTemplates:
  - metadata:
      name: postgres-data
    spec:
      resources:
        requests:
          storage: 10Gi   # 按需调整
```

⚠️ PVC 创建后不能直接修改 storage，需要先扩容 PVC 或重建 StatefulSet。

### etcd 持久化（生产环境）

默认 etcd 用 `emptyDir`，重启后数据丢失（业务服务重新注册即可）。
如需持久化，把 `etcd-deployment.yaml` 改为 StatefulSet + PVC：

```yaml
volumeClaimTemplates:
  - metadata:
      name: etcd-data
    spec:
      accessModes: ["ReadWriteOnce"]
      resources:
        requests:
          storage: 1Gi
```

---

## 不需要 postgres / etcd 的项目

删掉对应的 yaml 文件，同时删掉 `deployment.yaml` 里对应的 initContainer：

```bash
# 不需要 postgres
rm deployments/<n>/templates/postgres-statefulset.yaml
# 删掉 deployment.yaml 里的 wait-postgres initContainer
# 删掉 values.yaml 里的 DATABASE_URL env
```

---

## 常见问题

**etcd 日志里有大量 `unrecognized environment variable: ETCD_SERVICE_PORT_CLIENT`**

K8s 默认把同 namespace 的所有 Service 以 `<SERVICE>_PORT`、`<SERVICE>_SERVICE_HOST`
等形式注入到每个 pod 的环境变量里。etcd 把 `ETCD_` 前缀的变量都当配置读，
所以 K8s 注入的 `ETCD_SERVICE_PORT_CLIENT` 会触发 warn。

这是 warn 不是 error，不影响运行。生产环境可在 deployment 里加：

```yaml
spec:
  enableServiceLinks: false
```

**wallet-service 卡在 `Init:0/2`**

initContainers 在等 postgres 和 etcd 就绪。查看等待状态：

```bash
kubectl logs -n <n> <pod-name> -c wait-postgres
kubectl logs -n <n> <pod-name> -c wait-etcd
```

如果一直在等，检查对应 pod 是否 Running 且 Ready：

```bash
kubectl get pods -n <n>
```

**postgres pod 起来了但 wallet-service 还是连不上**

确认 `DATABASE_URL` 里的 `search_path=public` 存在。
pgx v5 连接时会重置 search_path，不加这个参数 Go 程序找不到任何表。

**migration 报 `duplicate migration file`**

`migrations/` 目录下有两个相同版本号的文件：

```bash
ls internal/db/migrations/
```

删掉重复的旧版本，版本号必须全局唯一。
