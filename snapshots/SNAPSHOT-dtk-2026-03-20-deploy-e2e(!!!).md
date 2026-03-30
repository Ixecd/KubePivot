# kubepivot 快照 — dtk deploy 端到端打通

> 归档时间：2026-03-20
> 里程碑：deploy-e2e — 从一堆 bug 到 `1/1 Running`，血泪史完整记录

---

## 最终结果

```
NAME                  READY   STATUS    RESTARTS   AGE
ok-76bd99b5b6-jdkhw   1/1     Running   0          13s

Ready:          True
QoS Class:      Guaranteed
Restart Count:  0
```

`dtk deploy` 一键走完 build → push → helm install → rollout，全绿。

---

## deploy 完整流程（最终版）

```
dtk deploy
  │
  ├── 1. 读 configs/components.yaml       # 解析组件列表
  ├── 2. 读 configs/project.env           # 读取 VERSION / ARCH / REGISTRY_PREFIX 等
  ├── 3. printPlan                        # 打印 AI 规划结果
  ├── 4. 过滤 image="" 的组件             # CLI 工具不部署，只部署有 image 的服务
  ├── 5. 组装 IMAGES 环境变量传给 make    # 关键：dtk 负责过滤，make 只管构建
  │
  └── make deploy.full
        ├── deploy.build                  # docker build，VERSION 不变则跳过
        ├── deploy.push                   # docker push，VERSION 不变则跳过
        ├── deploy.install                # helm upgrade --install --wait
        └── deploy.run.all                # kubectl set image + rollout status
```

---

## 关键配置说明

### `configs/project.env`

```env
PROJECT_NAME=ok
REGISTRY_PREFIX=qingchun22
KUBE_CONTEXT=                  # 留空 = 用当前 kubectl context，不能写 ""
KUBE_NAMESPACE=ok
ARCH=arm64                     # 或 amd64，影响镜像 tag 和 helm set
VERSION=v0.1.0                 # 改这个触发重新 build+push，不改则跳过
```

**VERSION 是触发重新部署的开关：**
- VERSION 不变 → `docker manifest inspect` 发现远端已有该 tag → 跳过 build 和 push → helm 用已有镜像
- VERSION 变了 → 重新 build → push → helm 更新镜像 tag

### `configs/components.yaml`

```yaml
components:
  - name: ok          # 对应 cmd/ok，也是 deployment 名
    port: 8080
    image: ok         # 非空 = 需要部署；留空或 "" = 跳过（CLI 工具用这个）
```

**`image` 字段规则：**
- 非空字符串 → 参与 build/push/deploy
- 空字符串 `""` 或不填 → 跳过，不构建不部署
- `dtk` 这种 CLI 工具设 `image: ""` 避免被套娃部署到 K8s

---

## 今天踩的所有 Bug（按发现顺序）

---

### Bug 1｜Helm SSA field manager 冲突

**报错：**
```
conflict with "kubectl-set" using apps/v1: .spec.template.spec.containers[name="ok"].image
Error: UPGRADE FAILED: conflict occurred
```

**原因：** 之前用 `kubectl set image` 直接改过 deployment，污染了 field manager。
Helm SSA（Server-Side Apply）再来 apply 时发现字段被别的 manager 持有，冲突。

**修法：** `deploy.install` 加 `--force-conflicts`：
```makefile
$(HELM) upgrade --install ... --force-conflicts
```

---

### Bug 2｜`--record` 废弃警告

**报错：**
```
Flag --record has been deprecated, --record will be removed in the future
```

**原因：** `kubectl set image ... --record` 的 `--record` 在新版 kubectl 已废弃。

**修法：** 删掉 `--record`，变更历史由 Helm revision 管理。

---

### Bug 3｜`deploy.install` 不等 pod ready 就返回

**报错：**
```
Error from server (NotFound): deployments.apps "ok" not found
```

**原因：** `helm upgrade --install` 默认异步返回，Helm 刚创建完资源就回来了，
后续 `deploy.run` 里的 `kubectl set image` 立刻去找 deployment，但 pod 还没起来。

**修法：** 加 `--wait`，Helm 等 deployment ready 后再返回：
```makefile
$(HELM) upgrade --install ... --wait --timeout 120s
```

---

### Bug 4｜镜像名拼成 `nginx:v0.1.0`

**报错：**
```
Failed to pull image "nginx:v0.1.0": manifest unknown
```

**原因：** `values.yaml` 默认 `image.repository: nginx`，`deploy.install` 只 `--set image.tag`，
没有覆盖 repository，拼出来就是 `nginx:v0.1.0`。

**修法：** `deploy.install` 同时 set repository：
```makefile
--set image.repository=$(REGISTRY_PREFIX)/$(firstword $(BINS))-$(ARCH) \
--set image.tag=$(VERSION)
```

**注意：** 用 `$(firstword $(BINS))` 而不是 `$(PROJECT_NAME)`，
因为 `PROJECT_NAME=kubepivot` 但实际 binary 是 `dtk`，两者可能不同。

---

### Bug 5｜`dtk` CLI 工具被部署到 K8s，无限套娃

**现象：**
```
kubectl logs -n kubepivot kubepivot-xxx
dtk - kubepivot 脚手架

用法:
  dtk init ...
```

**原因：** `dtk` 是 CLI 工具，启动后打印 usage 立刻退出，exit code 1，
K8s 以为进程崩溃，CrashLoopBackOff 无限重启。

**修法：** `configs/components.yaml` 中 `image` 留空，dtk 过滤掉不部署：
```yaml
components:
  - name: dtk
    port: 0
    image: ""    # CLI 工具，不部署到 K8s
```

---

### Bug 6｜`IMAGES` 变量没传给 make，`README.md` 混入构建

**报错：**
```
invalid tag "qingchun22/README.md-arm64:v1.8.0": repository name must be lowercase
```

**原因：** `runDeploy` 解析完 plan 后，从未把 `IMAGES` 写入 `makeEnv`，
`deploy.mk` 里 `$(IMAGES)` 为空，fallback 到 `$(BINS)`，
而 `BINS` 由 `$(wildcard ${ROOT_DIR}/cmd/*)` 扫描，`cmd/` 下不知为何混入了 `README.md`。

**修法一：** `runDeploy` 中从 plan 构建 `IMAGES`，只包含 image 非空的组件：
```go
var imageNames []string
for _, item := range plan {
    if item.Image == "" {
        continue
    }
    imageNames = append(imageNames, item.Name)
}
if len(imageNames) > 0 {
    makeEnv = append(makeEnv, "IMAGES="+strings.Join(imageNames, " "))
} else {
    fmt.Println("没有需要部署的服务（所有组件 image 均为空）")
    return
}
```

**修法二：** `internal/ai/ai.go` `LoadComponents` 解析 `image` 时去掉引号：
```go
val = strings.Trim(val, `"`)  // image: "" → 空字符串，不是 ""
```

---

### Bug 7｜`image: ""` 被解析成字面量 `""`

**原因：** 手写 YAML parser，`image: ""` 去掉前缀 `image:` 后剩 `""`（带引号），
`TrimSpace` 不去引号，导致 image 不为空，被当作有效镜像名参与构建。

**修法：** 见 Bug 6 修法二。

---

### Bug 8｜`KUBE_CONTEXT ?= ""` 空字符串坑

**原因：** `KUBE_CONTEXT ?= ""` 赋值的是字符串 `""`（两个引号字符），
`$(if $(strip $(CONTEXT)),--kube-context $(CONTEXT))` 判断时非空永远为真，
`--kube-context ""` 被传给 helm/kubectl，报找不到名为 `""` 的 context。

**修法：**
```makefile
KUBE_CONTEXT ?=    # 不能写 ""，真正的空值
```

---

### Bug 9｜`Chart.yaml` name 字段没被替换，pod 名含 `project`

**现象：**
```
NAME                                  READY
kubepivot-project-7f4d9b7dd-57cp2   0/1
```

**原因：** Helm deployment 名由 `{{ .Release.Name }}-{{ .Chart.Name }}` 拼成，
`Chart.yaml` 里 `name: project` 没有被 `replaceInDir` 替换掉，
`fixChartYAMLs` 只修了 `dependencies` 字段，漏了 `name`。

**修法：** `fixChartYAMLs` 中用正则显式替换 `name` 和 `appVersion`：
```go
updated = regexp.MustCompile(`(?m)^name:.*$`).
    ReplaceAllString(updated, "name: "+name)
updated = regexp.MustCompile(`(?m)^appVersion:.*$`).
    ReplaceAllString(updated, `appVersion: "1.0.0"`)
```

---

### Bug 10｜`service.port: 80`，服务跑在 8080，readiness probe 一直失败

**现象：** pod `0/1 Running`，`kubectl exec` 进去 wget `/healthz` 返回 `ok`，
但 pod 始终 not ready。

**原因：** `values.yaml` 默认 `service.port: 80`（helm create 模板默认值），
liveness/readiness probe 用的是 `http` named port，实际映射到 80，
但容器内服务监听 8080，probe 连 80 当然失败。

**修法：** `deployments/project/values.yaml` 改：
```yaml
service:
  type: ClusterIP
  port: 8080

livenessProbe:
  httpGet:
    path: /healthz    # 对应服务实际健康检查路径
    port: http
readinessProbe:
  httpGet:
    path: /healthz
    port: http
```

---

### Bug 11｜`Dockerfile` 用 `go mod download`，本地包找不到

**报错：**
```
no required module provides package github.com/Ixecd/ok/internal/api
```

**原因：** `go mod download` 只下载外部依赖，本地 internal 包不在 go.sum 里，
build 时找不到。

**修法：** `writeDockerfile` 改用 `go mod tidy`：
```dockerfile
RUN go env -w GOPROXY=https://goproxy.cn,direct
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -o ok ./cmd/ok
```

`go mod tidy` 同时处理外部依赖和本地包，更稳。

---

### Bug 12｜`ROOT_PACKAGE` 模板里写的是 `web3-blitz`

**报错：**
```
no required module provides package github.com/Ixecd/web3-blitz/cmd/ok
```

**原因：** 模板根 `Makefile` 里 `ROOT_PACKAGE := github.com/Ixecd/web3-blitz`，
`replaceInDir` 只有 `"github.com/Ixecd/kubepivot": module` 这条，
`web3-blitz` 没被替换。

**修法：** 模板 `Makefile` 改成：
```makefile
ROOT_PACKAGE := github.com/Ixecd/kubepivot
```
`replaceInDir` 就能正确替换了，不需要加额外规则。

---

## deploy.mk 最终版核心设计

```makefile
# KUBE_CONTEXT 必须用真正的空值，不能写 ""
KUBE_CONTEXT ?=

# 公共 flag 抽成变量，避免三处重复
KUBECTL_FLAGS := $(if $(strip $(CONTEXT)),--context $(CONTEXT)) --namespace $(NAMESPACE)
HELM_FLAGS    := $(if $(strip $(CONTEXT)),--kube-context $(CONTEXT))

# image.repository 用 firstword(BINS)，不用 PROJECT_NAME
# 因为 PROJECT_NAME=kubepivot 但 binary 是 dtk，两者可能不同
deploy.install:
    $(HELM) upgrade --install $(PROJECT_NAME) $(CHART_DIR) \
        --set image.repository=$(REGISTRY_PREFIX)/$(firstword $(BINS))-$(ARCH) \
        --set image.tag=$(VERSION) \
        --force-conflicts \   # 防 SSA 冲突
        --wait \              # 等 pod ready 再返回
        --timeout 120s

# VERSION 不变跳过 build/push
deploy.build:
    if docker manifest inspect $(REGISTRY_PREFIX)/$(img)-$(ARCH):$(VERSION); then
        echo "skipping build"   # 已存在，跳过
    else
        docker build ...        # 重新构建
    fi
```

---

## 历史快照

```
snapshots/
├── SNAPSHOT-dtk-2026-03-20-monitoring.md
├── SNAPSHOT-dtk-2026-03-20-with-frontend.md
├── SNAPSHOT-dtk-2026-03-20-frontend-skeleton-generic.md
└── SNAPSHOT-dtk-2026-03-20-deploy-e2e.md              ← 本文件
```
