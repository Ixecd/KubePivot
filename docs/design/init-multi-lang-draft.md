# kp init 多语言脚手架 — 设计草案

> 状态：✅ 已拍板，进入实施
> 关联：[init.md](../cmd/init/init.md) / [scaffold.go](../../internal/scaffold/scaffold.go) / [helm.go](../../internal/scaffold/helm.go) / [skeleton.go](../../internal/scaffold/skeleton.go)
> 背景：v3.1 GPU 调度前，脚手架从 Go-only 扩展到 12 语言 + 零侵入模式
> 拍板记录：qc 2026-05-01

---

## 一、核心理念

`kp init` 不生成"脚手架"——它生成**部署契约**。无论选择何种语言、何种 workload 类型，`kp deploy` 的行为完全一致：读 `configs/` 做决策，读 `deployments/` 做渲染，走 `Makefile` 做构建。

```
┌──────────────────────────────────────────────────────────┐
│                    壳 (Shell)                            │
│   Dockerfile + .dockerignore + 业务骨架                   │
│   语言相关：go / python / java / rust / cpp               │
│   可选：--no-app 跳过                                     │
├──────────────────────────────────────────────────────────┤
│                    核 (Core)                              │
│   configs/ + deployments/ + scripts/ + Makefile          │
│   语言无关：kp deploy 只读这四层                            │
│   必须：所有语言生成完全相同的结构                            │
└──────────────────────────────────────────────────────────┘
```

两条铁律：

1. **核层对所有语言完全一致**。Python 项目和 Rust 项目的 `configs/`、`deployments/`、`scripts/` 结构完全相同，`kp deploy` 不感知语言差异。
2. **壳层只影响构建，不影响部署**。不同语言的 Dockerfile 和业务骨架不同，但 `Makefile` 的 `deploy.build/push/install` 接口签名统一。

---

## 二、生成目录结构（完整展开）

```
myapp/                                   # --output 指定，默认 ./<name>
│
├── configs/                             # [核] 乾枢决策核心数据
│   ├── project.env                      #   PROJECT_NAME / VERSION / KUBE_NAMESPACE / REGISTRY_PREFIX / ARCH
│   ├── components.yaml                  #   服务组件拓扑（最小化模板，用户编辑）
│   ├── resources.yaml                   #   资源规格 + 流量声明
│   └── teams.yaml                       #   RBAC 权限模型
│
├── deployments/                         # [核] Helm Chart（统一模板）
│   └── myapp/                           #   项目名 = Chart 名 = Release 名
│       ├── Chart.yaml                   #     apiVersion: v2, name: myapp, version: 0.1.0
│       ├── values.yaml                  #     image.repository / image.tag / service.port / resources
│       └── templates/
│           ├── _helpers.tpl             #     公共模板（标签、全名）
│           ├── deployment.yaml          #     [web/grpc] Deployment
│           ├── service.yaml             #     [web/grpc] Service
│           ├── ingress.yaml             #     [web/grpc] Ingress（可选，默认注释掉）
│           ├── job.yaml                 #     [job] Job 或 CronJob
│           └── NOTES.txt                #     helm install 后的提示信息
│
├── scripts/                             # [核] 自动化辅助
│   └── create-secret.sh                 #   创建/更新 K8s Secret（幂等）
│
├── Makefile                             # [核] 统一操作入口
│                                        #   make deploy.build  → docker build
│                                        #   make deploy.push   → docker push
│                                        #   make deploy.install → helm upgrade
│                                        #   make deploy.run.all → kubectl rollout status
│
│   ═══════════════════════════════════════════════════════════
│   以下仅 --no-app=false 时生成（默认）
│   ═══════════════════════════════════════════════════════════
│
├── Dockerfile                           # [壳] 语言相关 Dockerfile
├── .dockerignore                        # [壳] 构建忽略规则
│
└── src/                                 # [壳] 业务骨架（按语言不同）
    ├── ...                              #   详见第五节各语言详情
```

---

## 三、Flag 设计

### 新增 flag

```
kp init --name <name> [--lang <lang>] [--type <type>] [--no-app] [--module <module>] [其余既有 flag]

--lang    string   default="go"    应用层语言：go | python | java | rust | cpp | cs | zig | kotlin | ts | php | swift | lua
--type    string   default="web"   Workload 类型：web | grpc | job
--no-app  bool     default=false   跳过应用层骨架，只生成核层文件
```

### 保留 flag（行为不变）

```
--name           string   项目名（必填，小写字母+数字+连字符）
--module         string   Go module 路径（仅 --lang=go 时生效）
--output         string   输出目录，默认 ./<name>
--template       string   自定义模板根目录，默认用嵌入式模板
--force          bool     允许非空目录输出
--with-frontend  bool     生成 React+Vite+Tailwind 前端骨架（仅 --lang=go 时生效）
--dry-run        bool     只打印生成内容，不写文件
```

### flag 校验与互斥规则

```
--lang 非法值    → 报错："unsupported language: xxx (supported: go, python, java, rust, cpp, cs, zig, kotlin, ts, php, swift, lua)"
--lang != go  + --module → warn："--module ignored for non-Go projects"（不报错）
--lang != go  + --with-frontend → warn："--with-frontend only supported for Go"（不生效）
--no-app=true + --lang    → warn："--lang ignored when --no-app is set"（不报错）
--no-app=true + --type    → warn："--type ignored when --no-app is set"
--type 非法值   → 报错："unsupported workload type: xxx (supported: web, grpc, job)"
```

### 组合行为矩阵

| --lang | --type | --no-app | 生成内容 |
|---|---|---|---|
| go (默认) | web (默认) | false (默认) | 核 + Go web 业务骨架（与改造前完全一致） |
| python | web | false | 核 + Python FastAPI 骨架 |
| java | web | false | 核 + Java Spring Boot 骨架 |
| kotlin | web | false | 核 + Kotlin Ktor 骨架 |
| rust | web | false | 核 + Rust Axum 骨架 |
| cpp | web | false | 核 + C++ Drogon 骨架 |
| cs | web | false | 核 + C# AOT Minimal API 骨架 |
| zig | web | false | 核 + Zig std.http 骨架 |
| ts | web | false | 核 + TypeScript Express 骨架 |
| php | web | false | 核 + PHP FrankenPHP 骨架 |
| swift | web | false | 核 + Swift Vapor 骨架 |
| lua | web | false | 核 + Lua OpenResty 骨架 |
| 任意 | 任意 | true | **只写核层**（configs/ + deployments/ + scripts/ + Makefile + Dockerfile） |

---

## 四、核层模板详情

核层模板位于 `internal/scaffold/templates/core/`，通过 `embed.FS` 编译进二进制。在生成阶段，所有 `{{.VarName}}` 占位符被替换为实际值。

### 4.1 project.env

```
PROJECT_NAME={{.Name}}
KUBE_NAMESPACE={{.Name}}
KUBE_CONTEXT=
KUBE_CONFIG=
MODULE_PATH={{.Module}}
REGISTRY_PREFIX=qingchun22
ARCH=arm64
VERSION=v0.1.0
```

变量：`Name`（项目名）、`Module`（module 路径，非 Go 语言时为空）

### 4.2 components.yaml（最小化模板）

```yaml
# ============================================================
# KubePivot 组件配置 — kp init 默认最小模板
# 请按需修改：添加 DB/Cache/GPU/依赖项
# ============================================================
# 编辑提示：
#   - type: Deployment | StatefulSet | Job | CronJob
#   - strategy: rolling(默认) | blue-green | canary
#   - sizing: 空表示由 kp deploy --sizing-mode=auto 自动计算
# ============================================================

components:
  # 示例 1: 无状态 Web 服务
  # - name: myapp
  #   port: 8080
  #   image: myapp
  #   strategy: rolling

  # 示例 2: 带 PostgreSQL 的服务
  # - name: myapp-postgres
  #   type: StatefulSet
  #   deps: []

  # - name: myapp
  #   port: 8080
  #   image: myapp
  #   deps:
  #     - myapp-postgres
  #   strategy: blue-green

  # 示例 3: GPU 训练 Job
  # - name: train
  #   type: Job
  #   image: train
  #   gpu: 1  # 需要 v3.1 GPU 调度支持
```

**设计要点**：不硬塞任何依赖。用户的项目需要什么依赖只有用户知道。注释里的示例覆盖了 web / 有状态 / GPU 三种场景。

### 4.3 resources.yaml（按语言智能预设）

```yaml
# KubePivot 资源配置
# 基础预设值由 kp init --lang 自动生成，kp deploy --sizing-mode=auto 后续优化

resources:
{{range .Components}}
  - name: {{.Name}}
    kind: {{.Kind}}
    onMissing: create
    labels:
      app.kubernetes.io/name: {{.Name}}
      kubepivot.io/managed: "true"
{{end}}

traffic:
  strategy: rolling
```

`resources.yaml` 的生成参数由语言推导：

| 语言 | requests.cpu | requests.memory | limits.cpu | limits.memory |
|---|---|---|---|---|
| Go | 100m | 128Mi | 500m | 256Mi |
| Rust | 100m | 128Mi | 500m | 256Mi |
| C++ | 100m | 128Mi | 500m | 256Mi |
| Zig | 100m | 128Mi | 500m | 256Mi |
| C# (AOT) | 100m | 128Mi | 500m | 256Mi |
| Swift | 100m | 128Mi | 500m | 256Mi |
| Python | 200m | 256Mi | 1000m | 512Mi |
| TS/JS | 200m | 256Mi | 1000m | 512Mi |
| PHP | 200m | 256Mi | 1000m | 512Mi |
| Lua (OpenResty) | 200m | 256Mi | 1000m | 512Mi |
| Java | 500m | 512Mi | 2000m | 1024Mi |
| Kotlin | 500m | 512Mi | 2000m | 1024Mi |

这些值来自各语言运行时的典型基线：

- **Go / Rust / C++ / Zig / C# AOT / Swift**：静态编译或原生二进制，128Mi 足够
- **Python / TS/JS / PHP / Lua**：解释型或 JIT，256Mi 起跑 Web server
- **Java / Kotlin**：JVM ~200Mi 堆起步，512Mi 是 Spring Boot / Ktor 最小存活值

**注意**：这是初始预设，不追求精度。`kp deploy --sizing-mode=auto` 会基于 Prometheus 历史数据给出更精确的建议。

#### GPU 训练任务预留字段（v3.1 启用）

```yaml
# GPU Job 示例（v3.1 GPU 调度支持后启用）
# - name: train
#   kind: Job
#   onMissing: create
#   gpu:
#     count: 1
#     product: NVIDIA-A100-SXM4-40GB
#   nodeSelector:
#     nvidia.com/gpu.product: A100-SXM4-40GB
#   labels:
#     app.kubernetes.io/name: train
```

#### v3.1 GPU Dockerfile 伏笔

Python 和 C++ 是 AI/计算任务最常见的语言。在它们的 Dockerfile 模板中预埋 GPU 基础镜像的注释提示，用户在 `kp init --type=job --lang python` 时可以看到：

**Python GPU 提示**（Dockerfile 顶部注释）：

```dockerfile
# 注意：默认使用 CPU 镜像。如果你的任务是 GPU 训练/推理，请：
# 1. 替换基础镜像为：
#    FROM nvidia/cuda:12.4.1-runtime-ubuntu22.04
# 2. 安装对应 CUDA 版本的 PyTorch/TensorFlow：
#    RUN pip install torch --index-url https://download.pytorch.org/whl/cu124
```

**C++ GPU 提示**（Dockerfile 顶部注释）：

```dockerfile
# 注意：默认使用 CPU 镜像。如果你的任务是 GPU 推理，请：
# 1. 替换基础镜像为：
#    FROM nvidia/cuda:12.4.1-devel-ubuntu22.04
# 2. 链接 CUDA 库：
#    RUN apt-get install -y cuda-libraries-dev-12-4
# 3. 在 CMakeLists.txt 中加 find_package(CUDA REQUIRED)
```

这些提示不改变任何行为，纯粹是注释。用户不启用 GPU 时不受影响。`--no-app` 模式不写 Dockerfile，也无影响。

### 4.4 teams.yaml

```yaml
# KubePivot RBAC 权限配置 — kp init 默认模板
# 编辑此文件后运行 kp team validate 检查语法
# 更多: kp team --help

teams:
  # 示例：给 backend 团队 deploy + sandbox 权限（q2 拍板）
  # - name: backend
  #   members:
  #     - alice@example.com
  #     - bob@example.com
  #   namespaces:
  #     - "{{.Name}}-*"
  #   permissions:
  #     - deploy
  #     - sandbox
```

### 4.5 Helm Chart 模板

#### Chart.yaml

```yaml
apiVersion: v2
name: {{.Name}}
description: Generated by KubePivot
type: application
version: 0.1.0
appVersion: "0.1.0"
```

#### values.yaml（按 --type 不同）

**web / grpc 类型**：

```yaml
replicaCount: 1

image:
  repository: {{.Name}}
  tag: "v0.1.0"
  pullPolicy: IfNotPresent

service:
  type: ClusterIP
  port: {{.Port}}

{{if eq .Type "grpc"}}grpc:
  port: 50051{{end}}

resources:
  requests:
    cpu: {{.RequestCPU}}
    memory: {{.RequestMem}}
  limits:
    cpu: {{.LimitCPU}}
    memory: {{.LimitMem}}

ingress:
  enabled: false
```

**job 类型**：

```yaml
image:
  repository: {{.Name}}
  tag: "v0.1.0"
  pullPolicy: IfNotPresent

job:
  schedule: "0 2 * * *"         # CronJob 定时，K8s cron 语法
  restartPolicy: Never
  backoffLimit: 3
  ttlSecondsAfterFinished: 3600

resources:
  requests:
    cpu: 100m
    memory: 128Mi
```

#### deployment.yaml（web / grpc 共用）

使用 `_helpers.tpl` 中的模板函数生成标签和选择器。核心差异：

- **web**：`containerPort: {{.Port}}`，readinessProbe 用 `httpGet /healthz`
- **grpc**：`containerPort: 50051`，readinessProbe 用 `exec: ["grpc_health_probe", "-addr=:50051"]`

#### job.yaml（仅 job 类型）

```yaml
{{if eq .Type "job"}}
apiVersion: batch/v1
kind: CronJob
metadata:
  name: {{ include "mychart.fullname" . }}
  labels:
    {{ include "mychart.labels" . | nindent 4 }}
spec:
  schedule: {{ .Values.job.schedule }}
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: {{ .Values.job.restartPolicy }}
          containers:
          - name: {{ .Chart.Name }}
            image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
            resources:
              {{- toYaml .Values.resources | nindent 14 }}
{{end}}
```

### 4.6 Makefile（统一模板）

所有语言共用同一个 Makefile 模板。`deploy.build` 走 `docker build`（多阶段构建，用户不需要本地语言工具链）。

```makefile
# KubePivot 自动化 Makefile — kp init 生成
# deploy.build  → 构建 Docker 镜像
# deploy.push   → 推送镜像到 Registry
# deploy.install → Helm 安装/升级
# deploy.run.all → 等待 rollout 完成

NAME        ?= {{.Name}}
VERSION     ?= $(shell [ -f configs/project.env ] && awk -F= '/^VERSION=/{print $$2}' configs/project.env || echo v0.1.0)
REGISTRY    ?= qingchun22
IMAGE        = $(REGISTRY)/$(NAME):$(VERSION)

# 预飞行检查：确保 Docker 正在运行
.PHONY: _check_docker
_check_docker:
	@docker info >/dev/null 2>&1 || (echo "❌ Error: Docker is not running or not installed"; exit 1)

.PHONY: deploy.build
deploy.build: _check_docker
	@if [ ! -f Dockerfile ]; then \
		echo "ERROR: Dockerfile not found. Run 'kp init' or create one."; \
		exit 1; \
	fi
	docker build -t $(IMAGE) .

.PHONY: deploy.push
deploy.push: deploy.build
	docker push $(IMAGE)

.PHONY: deploy.install
deploy.install:
	@if command -v helm >/dev/null 2>&1; then \
		helm upgrade --install $(NAME) ./deployments/$(NAME) \
			--namespace $$(awk -F= '/^KUBE_NAMESPACE=/{print $$2}' configs/project.env || echo $(NAME)) \
			--create-namespace \
			--set image.tag=$(VERSION); \
	else \
		echo "ERROR: helm not found"; \
		exit 1; \
	fi

.PHONY: deploy.run.all
deploy.run.all:
	kubectl rollout status deployment/$(NAME) \
		--namespace $$(awk -F= '/^KUBE_NAMESPACE=/{print $$2}' configs/project.env || echo $(NAME)) \
		--timeout=120s

.PHONY: deploy
deploy: deploy.push deploy.install deploy.run.all
	@echo "✅ Deploy complete: $(IMAGE)"
```

### 4.7 create-secret.sh

> **安全警告**：此脚本使用 `kubectl create secret --from-literal`，敏感值可能出现在 CI/CD 日志中。生产环境建议配合 [ExternalSecrets](https://external-secrets.io) 或 `kp secret seal`（SealedSecrets）使用。

```bash
#!/bin/bash
# KubePivot Secret 管理 — kp init 生成
# 幂等：已存在则更新，不存在则创建
# 用法：
#   ./scripts/create-secret.sh          # 执行
#   DRY_RUN=1 ./scripts/create-secret.sh # 预览 YAML，不 apply
set -o pipefail

NAMESPACE=${KUBE_NAMESPACE:-{{.Name}}}
SECRET_NAME="{{.Name}}-secret"

kubectl create secret generic "$SECRET_NAME" \
  -n "$NAMESPACE" \
  --from-literal=DATABASE_URL="${DATABASE_URL:-postgres://{{.Name}}:{{.Name}}@{{.Name}}-postgres:5432/{{.Name}}?sslmode=disable}" \
  --from-literal=JWT_SECRET="${JWT_SECRET:-$(openssl rand -hex 32)}" \
  --from-literal=APP_SECRET="${APP_SECRET:-$(openssl rand -hex 16)}" \
  --save-config \
  --dry-run=client -o yaml | ${DRY_RUN:+cat} ${DRY_RUN:-kubectl apply -f -}

echo "✅ $SECRET_NAME ${DRY_RUN:+YAML preview}${DRY_RUN:-created/updated} (ns: $NAMESPACE)"
```

---

## 五、壳层模板详情（按语言）

壳层模板位于 `internal/scaffold/templates/app/{lang}/`。每种语言产出一个 Dockerfile + `.dockerignore` + 最小业务骨架。

### 5.1 Go（`--lang go`，默认）

**Dockerfile**：multi-stage build，builder 用 `golang:1.25-alpine`，runtime 用 `scratch`。

```dockerfile
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG TARGETOS TARGETARCH
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags '-s -w -extldflags "-static"' \
    -o app ./cmd/{{.Name}}

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/app /app
USER 65532:65532
EXPOSE {{.Port}}
ENTRYPOINT ["/app"]
```

**.dockerignore**：

```
*.md
.git/
.idea/
*.test
.DS_Store
build/
```

**业务骨架**（与当前 `skeleton.go` 生成的内容一致，只是从硬编码 string literal 迁移到 `embed.FS`）：

```
src/
├── cmd/{{.Name}}/main.go              # main: 启动 HTTP server
├── internal/
│   ├── api/
│   │   ├── handler.go                  # Handler{}.Healthz() + Home()
│   │   └── server.go                   # NewMux(h *Handler) *http.ServeMux
│   ├── auth/
│   │   ├── auth.go                     # JWT + bcrypt
│   │   ├── middleware.go               # Bearer token middleware
│   │   └── rbac.go                     # PermissionChecker 接口 + RBAC middleware
│   ├── metrics/
│   │   └── metrics.go                  # Prometheus metrics 注册
│   └── pkg/
│       ├── code/code.go                # 错误码定义
│       └── response/response.go        # 统一 HTTP 响应格式
├── docs/
│   └── swagger.yaml                    # API spec
├── snapshots/
│   └── README.md
└── test/
    ├── e2e/e2e_test.go
    ├── integration/api_test.go
    └── smoke/smoke_test.go
```

**向后兼容**：生成的 `cmd/{{.Name}}/main.go`、`internal/api/*.go`、`internal/auth/*.go` 内容与当前改造前完全相同。现有用户不受影响。

#### `--type=grpc` 差异

- `internal/api/` 替换为 `internal/grpc/`，含 protobuf 定义 + gRPC server
- Dockerfile 加 `RUN go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest`

### 5.2 Python（`--lang python`）

**Dockerfile**：

```dockerfile
FROM python:3.12-slim AS builder
WORKDIR /app
COPY pyproject.toml .
RUN pip install --no-cache-dir --prefix=/install .

FROM python:3.12-slim
COPY --from=builder /install /usr/local
WORKDIR /app
COPY app/ ./app/
EXPOSE {{.Port}}
# 开发环境：单进程 uvicorn
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "{{.Port}}"]
# 生产环境：多 worker gunicorn（取消注释）
# CMD ["gunicorn", "app.main:app", "-w", "4", "-k", "uvicorn.workers.UvicornWorker", "--bind", "0.0.0.0:{{.Port}}"]
```

**.dockerignore**：

```
__pycache__/
*.pyc
.venv/
.git/
*.md
```

**业务骨架**：

```
src/
├── app/
│   ├── __init__.py
│   └── main.py                         # FastAPI app: GET /healthz + GET /
├── pyproject.toml                      # [project] name={{.Name}}, dependencies=["fastapi","uvicorn"]
└── README.md
```

**`app/main.py`**：

```python
from fastapi import FastAPI

app = FastAPI(title="{{.Name}}", version="0.1.0")

@app.get("/healthz")
def healthz():
    return {"status": "ok"}

@app.get("/")
def home():
    return {"service": "{{.Name}}", "status": "ok"}
```

**`pyproject.toml`**：

```toml
[project]
name = "{{.Name}}"
version = "0.1.0"
requires-python = ">=3.12"
dependencies = [
    "fastapi>=0.115.0",
    "uvicorn[standard]>=0.30.0",
]
```

### 5.3 Java（`--lang java`）

**Dockerfile**：

```dockerfile
FROM eclipse-temurin:21-jdk-alpine AS builder
WORKDIR /app
COPY pom.xml .
RUN mvn dependency:go-offline -B
COPY src/ ./src/
RUN mvn package -DskipTests -B

FROM eclipse-temurin:21-jre-alpine
WORKDIR /app
COPY --from=builder /app/target/*.jar app.jar
EXPOSE {{.Port}}
CMD ["java", "-Xms256m", "-Xmx512m", "-jar", "app.jar"]
```

> JVM_OPTS 预设：`-Xms256m -Xmx512m`。与 resources.yaml 的 Java 预设（requests 512Mi / limits 1024Mi）对齐。用户按需调整。

**.dockerignore**：

```
target/
*.class
.git/
*.md
```

**业务骨架**：

```
src/
├── pom.xml                                  # Spring Boot 3.x, java 21
├── src/main/java/com/example/
│   └── Application.java                     # @SpringBootApplication + health endpoint
├── src/main/resources/
│   └── application.properties               # server.port={{.Port}}; server.shutdown=graceful
└── README.md
```

**`Application.java`**：

```java
package com.example;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@SpringBootApplication
@RestController
public class Application {
    public static void main(String[] args) {
        SpringApplication.run(Application.class, args);
    }

    @GetMapping("/healthz")
    public String healthz() { return "ok"; }

    @GetMapping("/")
    public java.util.Map<String, String> home() {
        return java.util.Map.of("service", "{{.Name}}", "status", "ok");
    }
}
```

**`application.properties`**：

```properties
server.port={{.Port}}
server.shutdown=graceful
spring.lifecycle.timeout-per-shutdown-phase=30s
```

> 优雅停机：收到 SIGTERM → 拒绝新请求 → 处理完现有请求 → 退出。`server.shutdown=graceful` + `spring.lifecycle.timeout-per-shutdown-phase=30s` 与 K8s 的 `terminationGracePeriodSeconds`（默认 30s）对齐。

**`pom.xml` 关键节选**：

```xml
<parent>
    <groupId>org.springframework.boot</groupId>
    <artifactId>spring-boot-starter-parent</artifactId>
    <version>3.4.0</version>
</parent>
<dependencies>
    <dependency>
        <groupId>org.springframework.boot</groupId>
        <artifactId>spring-boot-starter-web</artifactId>
    </dependency>
</dependencies>
```

### 5.4 Rust（`--lang rust`）

**Dockerfile**：

```dockerfile
FROM rust:1.85-alpine AS builder
RUN apk add --no-cache musl-dev
WORKDIR /app
COPY Cargo.toml Cargo.lock ./
RUN mkdir src && echo 'fn main() {}' > src/main.rs
RUN cargo build --release
RUN rm -rf src
COPY src/ ./src/
RUN cargo build --release

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/target/release/{{.Name}} /app
USER 65532:65532
EXPOSE {{.Port}}
ENTRYPOINT ["/app"]
```

**.dockerignore**：

```
target/
.git/
*.md
```

**业务骨架**：

```
src/
├── Cargo.toml                    # [package] name="{{.Name}}", deps=["axum","tokio","serde"]
├── src/
│   └── main.rs                   # Axum: GET /healthz + GET /
└── README.md
```

**`Cargo.toml`**：

```toml
[package]
name = "{{.Name}}"
version = "0.1.0"
edition = "2024"

[dependencies]
axum = "0.8"
tokio = { version = "1", features = ["full"] }
serde = { version = "1", features = ["derive"] }
serde_json = "1"
```

**`src/main.rs`**：

```rust
use axum::{Router, routing::get, Json, response::IntoResponse};
use serde::Serialize;

#[derive(Serialize)]
struct Status { status: String }

#[derive(Serialize)]
struct Home { service: String, status: String }

async fn healthz() -> impl IntoResponse { Json(Status { status: "ok".into() }) }
async fn home() -> impl IntoResponse { Json(Home { service: "{{.Name}}".into(), status: "ok".into() }) }

#[tokio::main]
async fn main() {
    let app = Router::new()
        .route("/healthz", get(healthz))
        .route("/", get(home));
    let listener = tokio::net::TcpListener::bind("0.0.0.0:{{.Port}}").await.unwrap();
    axum::serve(listener, app).await.unwrap();
}
```

### 5.5 C++（`--lang cpp`）

**Dockerfile**（含 ccache 构建缓存）：

```dockerfile
FROM ubuntu:24.04 AS builder
RUN apt-get update && apt-get install -y \
    build-essential cmake ccache libdrogon-dev libjsoncpp-dev \
    && rm -rf /var/lib/apt/lists/*
ENV CCACHE_DIR=/ccache
WORKDIR /app
COPY CMakeLists.txt .
COPY src/ ./src/
RUN --mount=type=cache,target=/ccache \
    mkdir build && cd build \
    && cmake .. -DCMAKE_CXX_COMPILER_LAUNCHER=ccache \
    && make -j$(nproc)

FROM ubuntu:24.04
RUN apt-get update && apt-get install -y \
    libdrogon-dev libjsoncpp-dev \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/build/{{.Name}} /app
EXPOSE {{.Port}}
ENTRYPOINT ["/app"]
```

**.dockerignore**：

```
build/
.git/
*.md
```

**业务骨架**：

```
src/
├── CMakeLists.txt               # cmake_minimum_required(3.20), project({{.Name}}), find_package(Drogon REQUIRED)
├── src/
│   └── main.cpp                 # Drogon: GET /healthz + GET /
└── README.md
```

**`CMakeLists.txt`**：

```cmake
cmake_minimum_required(VERSION 3.20)
project({{.Name}} VERSION 0.1.0 LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
find_package(Drogon REQUIRED)
add_executable({{.Name}} src/main.cpp)
target_link_libraries({{.Name}} Drogon::Drogon)
```

**`src/main.cpp`**：

```cpp
#include <drogon/drogon.h>
using namespace drogon;

int main() {
    app().registerHandler("/healthz", [](const HttpRequestPtr&, 
        std::function<void(const HttpResponsePtr&)>&& callback) {
        Json::Value ret;
        ret["status"] = "ok";
        callback(HttpResponse::newHttpJsonResponse(ret));
    });
    app().registerHandler("/", [](const HttpRequestPtr&,
        std::function<void(const HttpResponsePtr&)>&& callback) {
        Json::Value ret;
        ret["service"] = "{{.Name}}";
        ret["status"] = "ok";
        callback(HttpResponse::newHttpJsonResponse(ret));
    });
    app().addListener("0.0.0.0", {{.Port}}).run();
    return 0;
}
```

### 5.6 C#（`--lang cs`）

**Dockerfile**（.NET AOT 原生编译 → scratch，与 Go/Rust 同级）：

```dockerfile
FROM mcr.microsoft.com/dotnet/sdk:9.0 AS builder
WORKDIR /app
COPY *.csproj .
RUN dotnet restore
COPY . .
RUN dotnet publish -c Release -p:PublishAot=true -o /out

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /out/{{.Name}} /app
USER 65532:65532
EXPOSE {{.Port}}
ENTRYPOINT ["/app"]
```

**.dockerignore**：

```
bin/
obj/
.git/
*.md
```

**业务骨架**：

```
src/
├── {{.Name}}.csproj                      # <Project Sdk="Microsoft.NET.Sdk.Web">, PublishAot=true
├── Program.cs                            # Minimal API: MapGet /healthz, MapGet /
└── README.md
```

**`Program.cs`**：

```csharp
var builder = WebApplication.CreateSlimBuilder(args);
var app = builder.Build();
app.MapGet("/healthz", () => Results.Ok(new { status = "ok" }));
app.MapGet("/", () => Results.Ok(new { service = "{{.Name}}", status = "ok" }));
app.Run("http://0.0.0.0:{{.Port}}");
```

**`.csproj` 关键节选**：

```xml
<Project Sdk="Microsoft.NET.Sdk.Web">
  <PropertyGroup>
    <TargetFramework>net9.0</TargetFramework>
    <PublishAot>true</PublishAot>
    <InvariantGlobalization>true</InvariantGlobalization>
  </PropertyGroup>
</Project>
```

### 5.7 Zig（`--lang zig`）

**Dockerfile**（zig build-exe → scratch）：

```dockerfile
FROM alpine:3.21 AS builder
RUN apk add --no-cache zig
WORKDIR /app
COPY build.zig .
COPY src/ ./src/
RUN zig build-exe src/main.zig -O ReleaseSafe -target x86_64-linux-musl --static

FROM scratch
COPY --from=builder /app/main /app
USER 65532:65532
EXPOSE {{.Port}}
ENTRYPOINT ["/app"]
```

**.dockerignore**：

```
zig-out/
zig-cache/
.git/
*.md
```

**业务骨架**：

```
src/
├── build.zig                          # StandardTargetOptimizeOptions, addExecutable, linkSystemLibrary("c")
├── src/
│   └── main.zig                       # std.http.Server: GET /healthz + GET /
└── README.md
```

**`src/main.zig`**（Zig 0.14+ 标准库 HTTP server）：

```zig
const std = @import("std");

pub fn main() !void {
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    const addr = try std.net.Address.parseIp4("0.0.0.0", {{.Port}});
    var server = try addr.listen(.{ .reuse_address = true });
    defer server.deinit();

    while (true) {
        const conn = try server.accept();
        defer conn.stream.close();
        var buf: [4096]u8 = undefined;
        const n = try conn.stream.read(&buf);
        const req = buf[0..n];

        if (std.mem.indexOf(u8, req, "GET /healthz") != null) {
            _ = try conn.stream.write("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{\"status\":\"ok\"}");
        } else {
            _ = try conn.stream.write("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{\"service\":\"{{.Name}}\",\"status\":\"ok\"}");
        }
    }
}
```

**`build.zig`**：

```zig
const std = @import("std");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});
    const exe = b.addExecutable(.{
        .name = "{{.Name}}",
        .root_source_file = b.path("src/main.zig"),
        .target = target,
        .optimize = optimize,
    });
    exe.linkLibC();
    b.installArtifact(exe);
}
```

### 5.8 Kotlin（`--lang kotlin`）

与 Java 共享 JVM 生态，用 Ktor 替代 Spring Boot（更轻量，与 KubePivot 极简哲学一致）。

**Dockerfile**（复用 Java 的 JRE alpine 镜像）：

```dockerfile
FROM eclipse-temurin:21-jdk-alpine AS builder
WORKDIR /app
COPY build.gradle.kts gradlew ./
COPY gradle/ ./gradle/
RUN chmod +x gradlew && ./gradlew dependencies --no-daemon
COPY src/ ./src/
RUN ./gradlew buildFatJar --no-daemon

FROM eclipse-temurin:21-jre-alpine
WORKDIR /app
COPY --from=builder /app/build/libs/*.jar app.jar
EXPOSE {{.Port}}
CMD ["java", "-Xms256m", "-Xmx512m", "-jar", "app.jar"]
```

**业务骨架**：

```
src/
├── build.gradle.kts                       # kotlin("jvm") + ktor + kotlinx-serialization
├── settings.gradle.kts
├── src/main/kotlin/com/example/
│   └── Application.kt                     # fun main(): embeddedServer(Netty, port={{.Port}}) { ... }
└── README.md
```

**`Application.kt`**：

```kotlin
package com.example

import io.ktor.server.application.*
import io.ktor.server.engine.*
import io.ktor.server.netty.*
import io.ktor.server.response.*
import io.ktor.server.routing.*

fun main() {
    embeddedServer(Netty, port = {{.Port}}) {
        routing {
            get("/healthz") { call.respond(mapOf("status" to "ok")) }
            get("/") { call.respond(mapOf("service" to "{{.Name}}", "status" to "ok")) }
        }
    }.start(wait = true)
}
```

### 5.9 TypeScript / JavaScript（`--lang ts`）

Node.js 22 + Express。也可选 Fastify（更快）。

**Dockerfile**：

```dockerfile
FROM node:22-slim AS builder
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci --omit=dev
COPY . .

FROM node:22-slim
WORKDIR /app
COPY --from=builder /app /app
USER node
EXPOSE {{.Port}}
CMD ["node", "src/index.js"]
```

**.dockerignore**：

```
node_modules/
.git/
*.md
```

**业务骨架**：

```
src/
├── package.json                         # dependencies: express
├── src/
│   └── index.js                         # Express: GET /healthz + GET /
└── README.md
```

**`package.json`**：

```json
{
  "name": "{{.Name}}",
  "version": "0.1.0",
  "private": true,
  "dependencies": { "express": "^5.0.0" },
  "scripts": { "start": "node src/index.js" }
}
```

**`src/index.js`**：

```javascript
const express = require("express");
const app = express();
app.get("/healthz", (req, res) => res.json({ status: "ok" }));
app.get("/", (req, res) => res.json({ service: "{{.Name}}", status: "ok" }));
app.listen({{.Port}}, "0.0.0.0", () => console.log("listening on :{{.Port}}"));
```

### 5.10 PHP（`--lang php`）

FrankenPHP：Caddy 内嵌 PHP，单二进制，最接近 KubePivot "零依赖"哲学。不需要 nginx + PHP-FPM sidecar。

**Dockerfile**：

```dockerfile
FROM dunglas/frankenphp:1.4-php8.4 AS builder
WORKDIR /app
COPY composer.json .
RUN composer install --no-dev --no-interaction
COPY . .

FROM dunglas/frankenphp:1.4-php8.4
WORKDIR /app
COPY --from=builder /app /app
EXPOSE {{.Port}}
CMD ["frankenphp", "run", "--config", "Caddyfile"]
```

**业务骨架**：

```
src/
├── composer.json                        # php >=8.2
├── Caddyfile                            # :8080 { php_server }
├── public/
│   └── index.php                        # json response
└── README.md
```

**`Caddyfile`**：

```
:{{.Port}} {
    root * public/
    php_server
}
```

**`public/index.php`**：

```php
<?php
$path = parse_url($_SERVER['REQUEST_URI'], PHP_URL_PATH);
header('Content-Type: application/json');
if ($path === '/healthz') {
    echo json_encode(['status' => 'ok']);
} else {
    echo json_encode(['service' => '{{.Name}}', 'status' => 'ok']);
}
```

### 5.11 Swift（`--lang swift`）

Server-side Swift + Vapor 4，静态链接后可走 scratch。

**Dockerfile**：

```dockerfile
FROM swift:6.0-noble AS builder
WORKDIR /app
COPY Package.swift Package.resolved* ./
RUN swift package resolve
COPY . .
RUN swift build -c release --static-swift-stdlib

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/.build/release/{{.Name}} /app
USER 65532:65532
EXPOSE {{.Port}}
ENTRYPOINT ["/app"]
```

**业务骨架**：

```
src/
├── Package.swift                        # name: "{{.Name}}", dependencies: ["vapor"]
├── Sources/App/
│   └── main.swift                       # Vapor app: GET /healthz + GET /
└── README.md
```

**`Package.swift`**：

```swift
// swift-tools-version: 6.0
import PackageDescription
let package = Package(
    name: "{{.Name}}",
    dependencies: [.package(url: "https://github.com/vapor/vapor.git", from: "4.106.0")],
    targets: [.executableTarget(name: "{{.Name}}", dependencies: [.product(name: "Vapor", package: "vapor")])]
)
```

**`Sources/App/main.swift`**：

```swift
import Vapor

let app = try Application(.detect())
defer { app.shutdown() }

app.get("healthz") { _ in ["status": "ok"] }
app.get { _ in ["service": "{{.Name}}", "status": "ok"] }

try app.run()
```

### 5.12 Lua（`--lang lua`）

OpenResty（nginx + LuaJIT），轻量到极致——整个 runtime < 5MB。

**Dockerfile**：

```dockerfile
FROM openresty/openresty:1.25-alpine
WORKDIR /app
COPY public/ /usr/local/openresty/nginx/html/
COPY nginx.conf /usr/local/openresty/nginx/conf/nginx.conf
EXPOSE {{.Port}}
CMD ["openresty", "-g", "daemon off;"]
```

**业务骨架**：

```
src/
├── nginx.conf                           # content_by_lua_block: 路由 /healthz + /
├── public/
│   └── index.html                       # 静态首页
└── README.md
```

**`nginx.conf`**：

```nginx
events { worker_connections 1024; }
http {
    server {
        listen {{.Port}};
        location /healthz {
            default_type application/json;
            content_by_lua_block { ngx.say('{"status":"ok"}') }
        }
        location / {
            default_type application/json;
            content_by_lua_block { ngx.say('{"service":"{{.Name}}","status":"ok"}') }
        }
    }
}
```

---

## 六、模板变量体系

### 6.1 变量替换策略：`strings.ReplaceAll` vs `text/template`

KubePivot 坚持"工具链最小化"，不引入标准库 `text/template` 之外的依赖。但对于 Helm chart 模板中需要的条件渲染（如 `{{if eq .Type "job"}}`），简单的 `strings.ReplaceAll` 无法表达。

**决策**：物理文件拆分，不引入条件模板引擎。

- `templates/core/deployments/{type}/` 下按 `--type` 值分三个子目录：`web/`、`grpc/`、`job/`
- `emitCore()` 根据 `opts.Type` 选择对应的子目录渲染
- 共享文件（`_helpers.tpl`、`NOTES.txt`、`Chart.yaml`）放在 `templates/core/deployments/_shared/`
- 静态配置文件（`project.env`、`Makefile`、`Dockerfile`、业务骨架）一律用 `strings.ReplaceAll`

这避免了引入模板引擎的复杂性，同时 Helm chart 的条件渲染由"选择不同的源文件"自然解决。

### 6.2 变量列表

所有模板文件中的 `{{.VarName}}` 在 `scaffold.go` 的 `emitFile()` 阶段被替换。变量来自 `InitOptions` + 语言相关的推导。

```go
type templateVars struct {
    // 来自 InitOptions
    Name       string  // --name，项目名
    Module     string  // --module
    Lang       string  // --lang
    Type       string  // --type (web/grpc/job)
    Port       int     // 默认 8080 (web/grpc) / 0 (job)
    NoApp      bool    // --no-app

    // 语言相关的资源预设
    RequestCPU    string  // "100m" / "200m" / "500m"
    RequestMem    string  // "128Mi" / "256Mi" / "512Mi"
    LimitCPU      string  // "500m" / "1000m" / "2000m"
    LimitMem      string  // "256Mi" / "512Mi" / "1024Mi"
}

func deriveResourcePresets(lang string) (cpuReq, memReq, cpuLimit, memLimit string) {
    switch lang {
    case "java", "kotlin":
        return "500m", "512Mi", "2000m", "1024Mi"
    case "python", "ts", "php", "lua":
        return "200m", "256Mi", "1000m", "512Mi"
    default: // go, rust, cpp, cs, zig, swift
        return "100m", "128Mi", "500m", "256Mi"
    }
}
```

---

## 七、代码改造清单

### 7.1 文件变更总览

| 文件 | 改动 | 说明 |
|---|---|---|
| `internal/scaffold/scaffold.go` | 重写 | 两阶段：`emitCore()` + `emitApp(lang)` |
| `internal/scaffold/skeleton.go` | **删除** | 690 行硬编码 → embed.FS 模板 |
| `internal/scaffold/helm.go` | 重写 | 硬编码 → embed.FS 模板 + 变量替换 |
| `internal/scaffold/embed.go` | 新建 | `embed.FS` 声明，嵌入 templates/ |
| `internal/scaffold/frontend.go` | 保留 | Go-only `--with-frontend`，迁移到模板目录 |
| `internal/scaffold/monitoring.go` | 迁移 | → `templates/core/` |
| `internal/scaffold/migration.go` | 迁移 | → `templates/core/` |
| `internal/scaffold/handoff.go` | 保留 | 生成 HANDOFF.md / CLAUDE.md，逻辑不变 |
| `internal/scaffold/ai_coding_guide.go` | 保留 | 不变 |
| `internal/scaffold/templates/` | 新建 | `core/` + `app/{go,python,java,rust,cpp}/` |
| `internal/scaffold/scaffold_test.go` | 扩展 | +8 个测试用例 |
| `cmd/kp/main.go` | +3 flag | `--lang` / `--type` / `--no-app` |
| `cmd/kp/init.go` 或 `main.go:runInit()` | 改 | 传新字段到 `scaffold.InitOptions` |

### 7.2 scaffold.go 重构后的核心逻辑

```go
package scaffold

import "embed"

//go:embed templates/core/* templates/app/*
var templateFS embed.FS

type InitOptions struct {
    Name         string
    Module       string
    OutputDir    string
    TemplateRoot string
    Force        bool
    WithFrontend bool
    DryRun       bool
    Stdout       io.Writer

    // v3.1 新增
    Lang   string // go | python | java | rust | cpp，默认 go
    Type   string // web | grpc | job，默认 web
    NoApp  bool   // 跳过应用层，默认 false
}

func InitProject(opts InitOptions) error {
    // 0. 参数校验 + 默认值
    if opts.Lang == "" { opts.Lang = "go" }
    if opts.Type == "" { opts.Type = "web" }
    if !isValidLang(opts.Lang) { return fmt.Errorf("unsupported language: %s", opts.Lang) }
    if !isValidType(opts.Type) { return fmt.Errorf("unsupported type: %s", opts.Type) }

    // 1. [核] 强制：所有语言相同的部署契约
    if err := emitCore(opts); err != nil { return err }

    // 2. [核] 强制：Dockerfile（--no-app 也写，它是构建契约）
    if err := emitDockerfile(opts); err != nil { return err }

    // 3. [壳] 可选：业务骨架
    if !opts.NoApp {
        if err := emitApp(opts); err != nil { return err }
    }

    return nil
}

func emitCore(opts InitOptions) error {
    // templates/core/ 下的所有文件：
    //   configs/project.env        → {{.Name}}
    //   configs/components.yaml    → （最小化模板，直接输出）
    //   configs/resources.yaml     → （按 lang 预设资源）
    //   configs/teams.yaml         → {{.Name}}
    //   deployments/               → 按 --type 选 web/grpc/job 子目录
    //   scripts/create-secret.sh   → {{.Name}}
    //   Makefile                   → {{.Name}}
    return walkAndEmit("templates/core", opts)
}

func emitApp(opts InitOptions) error {
    // templates/app/{lang}/ 下的所有文件
    return walkAndEmit("templates/app/"+opts.Lang, opts)
}

func walkAndEmit(templateDir string, opts InitOptions) error {
    entries, _ := templateFS.ReadDir(templateDir)
    for _, e := range entries {
        if e.IsDir() { continue }
        content, _ := templateFS.ReadFile(templateDir + "/" + e.Name())
        rendered := renderTemplate(content, buildVars(opts))
        // 写入到 opts.OutputDir 对应路径
        targetPath := filepath.Join(opts.OutputDir, ...)
        if !opts.DryRun { os.WriteFile(targetPath, rendered, perm) }
    }
    return nil
}
```

### 7.3 变量替换实现

KubePivot 不引入 `text/template`（哲学：工具链最小化）。用 `strings.ReplaceAll` 做简单占位符替换：

```go
var replacers = map[string]func(vars templateVars) (string, string){
    "{{.Name}}":        func(v templateVars) (string, string) { return "{{.Name}}", v.Name },
    "{{.Module}}":      func(v templateVars) (string, string) { return "{{.Module}}", v.Module },
    "{{.Port}}":        func(v templateVars) (string, string) { return "{{.Port}}", fmt.Sprintf("%d", v.Port) },
    "{{.RequestCPU}}":  func(v templateVars) (string, string) { return "{{.RequestCPU}}", v.RequestCPU },
    "{{.RequestMem}}":  func(v templateVars) (string, string) { return "{{.RequestMem}}", v.RequestMem },
    "{{.LimitCPU}}":    func(v templateVars) (string, string) { return "{{.LimitCPU}}", v.LimitCPU },
    "{{.LimitMem}}":    func(v templateVars) (string, string) { return "{{.LimitMem}}", v.LimitMem },
}

func renderTemplate(content []byte, vars templateVars) []byte {
    s := string(content)
    for placeholder, fn := range replacers {
        from, to := fn(vars)
        s = strings.ReplaceAll(s, from, to)
    }
    return []byte(s)
}
```

---

## 八、测试计划

### 8.1 单元测试（`scaffold_test.go`）

| 测试函数 | 调用 | 验证 |
|---|---|---|
| `TestInit_GoDefault` | `kp init --name test` | 生成结构与改造前完全一致（文件列表 + 关键文件内容 hash） |
| `TestInit_GoNoApp` | `kp init --name test --no-app` | 只有核层文件，无 `src/`，有 Dockerfile |
| `TestInit_Python` | `kp init --lang python --name test` | 核层 + Python 壳，验证 `app/main.py` 存在 |
| `TestInit_Java` | `kp init --lang java --name test` | 核层 + Java 壳，验证 `pom.xml` + `Application.java` 存在 |
| `TestInit_Rust` | `kp init --lang rust --name test` | 核层 + Rust 壳，验证 `Cargo.toml` + `main.rs` 存在 |
| `TestInit_Cpp` | `kp init --lang cpp --name test` | 核层 + C++ 壳，验证 `CMakeLists.txt` + `main.cpp` 存在 |
| `TestInit_CSharp` | `kp init --lang cs --name test` | 核层 + C# 壳，验证 `.csproj` + `Program.cs` 存在 |
| `TestInit_Zig` | `kp init --lang zig --name test` | 核层 + Zig 壳，验证 `build.zig` + `main.zig` 存在 |
| `TestInit_Kotlin` | `kp init --lang kotlin --name test` | 核层 + Kotlin 壳，验证 `build.gradle.kts` + `Application.kt` 存在 |
| `TestInit_TypeScript` | `kp init --lang ts --name test` | 核层 + TS 壳，验证 `package.json` + `index.js` 存在 |
| `TestInit_PHP` | `kp init --lang php --name test` | 核层 + PHP 壳，验证 `composer.json` + `Caddyfile` 存在 |
| `TestInit_Swift` | `kp init --lang swift --name test` | 核层 + Swift 壳，验证 `Package.swift` + `main.swift` 存在 |
| `TestInit_Lua` | `kp init --lang lua --name test` | 核层 + Lua 壳，验证 `nginx.conf` + `index.html` 存在 |
| `TestInit_InvalidLang` | `kp init --lang cobol --name test` | 报错 `unsupported language`，exit code != 0 |
| `TestInit_InvalidType` | `kp init --type daemonset --name test` | 报错 `unsupported workload type` |
| `TestInit_TypeGrpc` | `kp init --name test --type grpc` | Helm template 含 gRPC port 定义 |
| `TestInit_TypeJob` | `kp init --name test --type job` | Helm template 含 CronJob，无 Service |
| `TestInit_FrontendPython` | `kp init --lang python --with-frontend` | warn 不报错，无前端文件生成 |
| `TestInit_NoAppWithLang` | `kp init --lang java --no-app --name test` | warn 不报错，只写核层 |
| `TestInit_Idempotent_Deny` | 同目录运行两次 `kp init --name x`（不带 --force） | 第二次报错退出 |
| `TestInit_Idempotent_Force` | 同目录运行两次 `kp init --name x --force` | 第二次成功，核层文件不变 |

### 8.2 向后兼容验证

改造后的 `kp init --name demo --module github.com/test/demo` 产生的目录结构、文件内容应与改造前 `skeleton.go` 硬编码版本逐文件一致。用 golden file 测试：

```go
func TestInit_BackwardCompat(t *testing.T) {
    // 改造前：kp init --name demo --module github.com/test/demo
    // 改造后：kp init --name demo --module github.com/test/demo
    // 两个输出目录 diff -r 应无差异
}
```

### 8.3 集成测试

```bash
# 每种语言验证 build 能跑（不推镜像）
kp init --name demo-go   --lang go     && cd demo-go   && make deploy.build
kp init --name demo-py   --lang python && cd demo-py   && make deploy.build
kp init --name demo-java --lang java   && cd demo-java && make deploy.build
kp init --name demo-rs   --lang rust   && cd demo-rs   && make deploy.build
kp init --name demo-cpp  --lang cpp    && cd demo-cpp  && make deploy.build

# --no-app 模式
kp init --name demo-naked --no-app --force && cd demo-naked && make deploy.build
```

---

## 九、实施顺序

| Step | 内容 | 估计 |
|---|---|---|
| Step 1 | 模板系统重构：`embed.FS` + `templates/core/` + `templates/app/go/`，删 `skeleton.go` 硬编码 | ~2 天 |
| Step 2 | Go 向后兼容验证：golden file 测试确保改造前后一致 | ~0.5 天 |
| Step 3 | `--lang` flag + Python 模板 | ~0.5 天 |
| Step 4 | Java + Rust + C++ 模板 | ~1 天 |
| Step 5 | C# + Zig + Kotlin + TS/JS + PHP + Swift + Lua 模板 | ~1.5 天 |
| Step 6 | `--no-app` flag + `--type` flag | ~0.5 天 |
| Step 7 | 全量测试（12 种语言）+ 文档更新 | ~1 天 |

---

## 十、风险与约束

1. **embed.FS 增大二进制体积**：5 种语言 × ~10 个模板文件 × ~500 字节平均 = ~25KB，对 ~30MB 的 kp 二进制可忽略不计。

2. **Dockerfile 安全维护**：镜像 tag 用可变量。未来可引入 `kp doctor --dockerfiles` 检查基础镜像的 CVE 状态。

3. **非 Go 语言的业务骨架不是 KubePivot 核心竞争力**：骨架追求"最小可运行"而非"生产就绪"。用户的项目架构由用户自己决定。KubePivot 的价值在 `kp deploy` 的统一体验。

4. **Makefile 的 `deploy.build` 统一走 docker build**：用户首次构建需要下载基础镜像（~100MB-500MB），网络条件差时可能慢。这是"零工具链要求"的代价，可以接受。

---

## 十一、编辑记录

```
2026-05-01 v1.0  qc + DeepSeek
    - 壳 + 核 两层架构
    - 10 个设计 Q 全部 A/B/C 拍板
    - 5 种语言完整 Dockerfile + 业务骨架 + 构建文件
    - 3 种 workload 类型（web/grpc/job）
    - templates/ 目录结构 + embed.FS 声明
    - scaffold.go 重构后的伪代码
    - 变量替换体系 + 资源预设表
    - 12+2 个测试用例 + 向后兼容 golden file 测试 + 幂等性测试
    - GPU 预留字段 + NUMA 亲和性 annotations 钩子 + GPU Dockerfile 伏笔
    
2026-05-01 v1.1  qc + DeepSeek 补充拍板
    - Makefile _check_docker 预飞行检查
    - create-secret.sh 安全提示 + DRY_RUN 预览模式
    - Java application.properties 优雅停机
    - C++ Dockerfile ccache 构建缓存 + --mount 挂载
    - Python Dockerfile gunicorn 生产模式注释
    - 模板变量策略最终决策：物理文件拆分（不用 text/template）
    - GPU Dockerfile 提示（python/cpp 基础镜像替换指引）
    - 幂等性测试：同目录重复 init 行为验证 + --force 保护

2026-05-01 v1.2  qc + DeepSeek 语言矩阵扩展
    - 新增 7 种语言：C#(AOT) / Zig / Kotlin(Ktor) / TS/JS(Express) / PHP(FrankenPHP) / Swift(Vapor) / Lua(OpenResty)
    - 12 种语言完整 Dockerfile + 业务骨架 + 构建文件
    - 资源预设从 3 档扩展为覆盖 12 语言的 3 档（原生/解释型/JVM）
    - 测试用例从 14 个扩展为 21 个
    - 实施 Step 5 新增（7 语言模板 ~1.5 天）
    - 编辑记录作者修正：qc + DeepSeek 😈
```
