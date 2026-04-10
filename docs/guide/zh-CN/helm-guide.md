# Helm 配置指南

> 适用：KubePivot v2.1.0+
> `kp init` 生成的项目包含 4 个独立 Helm chart，本文说明如何配置和扩展它们。

---

## 一、目录结构

```
deployments/<project>/
├── <project>/              # 业务服务 chart（你主要改这里）
│   ├── Chart.yaml
│   ├── values.yaml         # 配置入口
│   └── templates/
│       ├── deployment.yaml
│       ├── service.yaml
│       ├── serviceaccount.yaml
│       ├── networkpolicy.yaml
│       └── virtualservice-preview.yaml
├── <project>-postgres/     # PostgreSQL chart
│   ├── values.yaml
│   └── templates/
│       ├── statefulset.yaml
│       └── service.yaml
├── <project>-etcd/         # etcd chart
│   ├── values.yaml
│   └── templates/
│       ├── statefulset.yaml
│       └── service.yaml
└── <project>-controller/   # A2 Reconciliation Controller chart
    ├── values.yaml
    └── templates/
        ├── deployment.yaml
        ├── configmap.yaml
        └── rbac.yaml
```

**原则：业务逻辑改 `<project>/values.yaml`，基础设施改对应 chart 的 `values.yaml`，不要直接改 `templates/`。**

---

## 二、业务服务 chart（最常用）

### values.yaml 完整字段说明

```yaml
# 副本数
replicaCount: 1

# 镜像配置（由 kp deploy 自动更新，通常不需要手动改）
image:
  repository: your-registry/myapp-arm64   # 镜像仓库，由 REGISTRY_PREFIX + 服务名 + ARCH 拼接
  pullPolicy: IfNotPresent
  tag: ""                                  # 留空则使用 Chart.AppVersion（即 VERSION 变量）

# Service 配置
service:
  type: ClusterIP      # ClusterIP | NodePort | LoadBalancer
  port: 8080           # 和 cmd/myapp/main.go 里监听的端口一致

# 资源限制（生产环境必须设置）
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi

# 安全上下文
securityContext:
  readOnlyRootFilesystem: false   # 建议 true，但服务若需写临时文件则保持 false

# 环境变量（敏感信息放 Secret，非敏感信息放这里）
env:
  - name: DATABASE_URL
    value: "postgres://user:pass@myapp-postgres:5432/myapp?sslmode=disable"
  - name: ETCD_ENDPOINTS
    value: "myapp-etcd:2379"
  - name: LOG_LEVEL
    value: "info"
```

### 从 Secret 注入环境变量

敏感信息不要写在 `values.yaml` 里，通过 Secret 注入：

```yaml
env:
  # 非敏感：直接写值
  - name: LOG_LEVEL
    value: "info"
  - name: APP_ENV
    value: "production"

  # 敏感：从 Secret 读取
  - name: DATABASE_URL
    valueFrom:
      secretKeyRef:
        name: myapp-secret       # ./scripts/create-secret.sh 创建的 Secret 名
        key: DATABASE_URL
  - name: JWT_SECRET
    valueFrom:
      secretKeyRef:
        name: myapp-secret
        key: JWT_SECRET
```

### 新增端口

服务需要暴露多个端口（如 HTTP + gRPC）时：

```yaml
# values.yaml
service:
  type: ClusterIP
  port: 8080
  grpcPort: 9090    # 新增字段
```

同时修改 `templates/service.yaml`：

```yaml
spec:
  ports:
    - name: http
      port: {{ .Values.service.port }}
      targetPort: http
    - name: grpc
      port: {{ .Values.service.grpcPort }}
      targetPort: grpc
```

以及 `templates/deployment.yaml` 的 containers.ports：

```yaml
ports:
  - name: http
    containerPort: {{ .Values.service.port }}
  - name: grpc
    containerPort: {{ .Values.service.grpcPort }}
```

### 调整健康检查

默认使用 `/healthz` HTTP 探针，如果需要调整：

```yaml
# 调整超时（服务启动慢时）
livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 30    # 默认 10，启动慢的服务调大
  periodSeconds: 10
  timeoutSeconds: 5
  failureThreshold: 3

readinessProbe:
  httpGet:
    path: /ready               # 可以和 liveness 用不同路径
    port: 8080
  initialDelaySeconds: 10
  periodSeconds: 5
```

### 挂载 ConfigMap

```yaml
# values.yaml 新增
configMap:
  enabled: true
  data:
    app.yaml: |
      server:
        timeout: 30s
      feature_flags:
        new_ui: false
```

在 `templates/` 新建 `configmap.yaml`：

```yaml
{{- if .Values.configMap.enabled }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-config
  namespace: {{ .Release.Namespace }}
data:
  {{- toYaml .Values.configMap.data | nindent 2 }}
{{- end }}
```

在 `deployment.yaml` 挂载：

```yaml
volumes:
  - name: config
    configMap:
      name: {{ .Release.Name }}-config

containers:
  - name: myapp
    volumeMounts:
      - name: config
        mountPath: /app/configs/app.yaml
        subPath: app.yaml
```

---

## 三、PostgreSQL chart

### values.yaml 常用配置

```yaml
image:
  repository: postgres
  tag: "15-alpine"

# 数据库初始化
postgres:
  db: myapp
  user: myapp
  password: ""           # 留空，从 Secret 读取（推荐）

# 存储配置
persistence:
  size: 10Gi
  storageClass: ""       # 留空使用默认 StorageClass

# 资源（生产环境按实际调整）
resources:
  requests:
    cpu: 250m
    memory: 256Mi
  limits:
    cpu: 1000m
    memory: 1Gi
```

### 从 Secret 读取数据库密码

在 `templates/statefulset.yaml` 的 env 里：

```yaml
env:
  - name: POSTGRES_DB
    value: {{ .Values.postgres.db }}
  - name: POSTGRES_USER
    value: {{ .Values.postgres.user }}
  - name: POSTGRES_PASSWORD
    valueFrom:
      secretKeyRef:
        name: {{ .Release.Name | replace "-postgres" "" }}-secret
        key: POSTGRES_PASSWORD
```

---

## 四、etcd chart

### values.yaml 常用配置

```yaml
image:
  repository: quay.io/coreos/etcd
  tag: "v3.5.0"

# 单节点（开发/测试）
replicaCount: 1

# 三节点高可用（生产）
# replicaCount: 3

persistence:
  size: 5Gi

resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi
```

---

## 五、Controller chart

A2 Reconciliation Controller 的配置，通常不需要改，除非：

### 调整监控资源列表

Controller 通过 `configs/resources.yaml` 知道要监控哪些资源：

```yaml
# configs/resources.yaml
resources:
  - kind: Deployment
    name: myapp
    namespace: myapp
    on-missing: recreate     # 资源缺失时自动重建
    force-sync: true         # 30s 强制对齐
    no-sync-fields:
      - replicas             # HPA 管理，不强制同步

  - kind: StatefulSet
    name: myapp-postgres
    on-missing: alert        # 数据库缺失只告警，不自动处理

  - kind: StatefulSet
    name: myapp-etcd
    on-missing: recreate
```

`on-missing` 策略说明：

| 策略 | 说明 | 适用场景 |
|------|------|---------|
| `recreate` | 自动重建（helm rollback）| 无状态服务 |
| `rollback` | 回滚到上一版本 | 更新失败的服务 |
| `scale-down` | 缩容到 0 | 降级保护 |
| `alert` | 只告警，不自动处理 | 数据库等有状态服务 |
| `custom` | 执行自定义命令 | 特殊处理逻辑 |

---

## 六、多环境配置

不同环境（dev/staging/prod）的差异通过 `kp context` + 各自的 `values.yaml` 管理：

```bash
# 添加 staging 环境
kp context add --name staging \
  --context my-staging-cluster \
  --namespace myapp-staging

# staging 和 prod 用不同的 values
# deployments/myapp/myapp/values-staging.yaml
# deployments/myapp/myapp/values-prod.yaml
```

在 `deploy.mk` 里按环境加载不同 values：

```makefile
HELM_FLAGS ?=
ifeq ($(KUBE_NAMESPACE),myapp-staging)
  HELM_FLAGS += -f deployments/$(PROJECT_NAME)/$(PROJECT_NAME)/values-staging.yaml
endif
ifeq ($(KUBE_NAMESPACE),myapp-production)
  HELM_FLAGS += -f deployments/$(PROJECT_NAME)/$(PROJECT_NAME)/values-prod.yaml
endif
```

---

## 七、常见问题

**服务一直 Init:0/2（initContainer 卡住）**

postgres 或 etcd 没有启动。确认：
```bash
kubectl get pods -n myapp
kubectl logs -n myapp <postgres-pod>
```

**helm upgrade 报 pending-rollback**

```bash
kubectl delete secret -n myapp \
  $(kubectl get secret -n myapp -l owner=helm \
    -o jsonpath='{.items[?(@.metadata.labels.status=="pending-rollback")].metadata.name}')
kp deploy
```

**修改了 values.yaml 但没有生效**

`kp deploy` 会自动 `helm upgrade --reuse-values`，但如果你改了 values.yaml，需要：
```bash
kp deploy   # 重新部署即可，kp 会读取最新的 values.yaml
```

**想完全自定义 deployment.yaml**

直接修改 `templates/deployment.yaml`，kp 不会覆盖 `templates/` 下的文件（`kp sync` 的 `syncNotify` 策略只提示，不强制覆盖）。
