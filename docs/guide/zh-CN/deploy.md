# 部署指南

`dtk deploy` 是 dev-toolkit 的核心命令，封装了从构建镜像到 K8s 滚动更新的完整流程。

---

## 完整流程

```
dtk deploy
  │
  ├── 1. 读取 configs/components.yaml     解析组件列表
  ├── 2. 读取 configs/project.env         读取 VERSION / ARCH / REGISTRY_PREFIX
  ├── 3. AI 规划资源                       replicas / cpu / memory / storage
  ├── 4. 过滤 image="" 的组件             CLI 工具不部署，只部署有 image 的服务
  ├── 5. 组装 IMAGES 环境变量传给 make    dtk 负责过滤，make 只管构建
  │
  └── make deploy.full
        ├── deploy.build                  docker build（VERSION 不变则跳过）
        ├── deploy.push                   docker push（VERSION 不变则跳过）
        ├── deploy.install                helm upgrade --install --wait
        └── deploy.run.all                kubectl set image + rollout status
```

---

## VERSION 机制

**VERSION 是触发重新部署的开关。**

```
VERSION 不变 → docker manifest inspect 发现远端已有该 tag
             → 跳过 build 和 push
             → helm 用已有镜像，不更新

VERSION 变了 → 重新 build → push → helm 更新 image tag → 滚动更新
```

每次发布新版本：

```bash
# 编辑 configs/project.env
VERSION=v0.2.0

# 重新部署
dtk deploy
```

---

## components.yaml 详解

```yaml
components:
  - name: myapp       # 必须与 cmd/ 下 binary 名一致，也是 deployment 名
    port: 8080        # 容器端口
    image: myapp      # 非空 = 参与 build/push/deploy
                      # 留空或 "" = 跳过，不构建不部署
```

**`image` 为空的使用场景：**

CLI 工具（如 `dtk` 本身）不应部署到 K8s。CLI 启动后打印 usage 立刻退出，
K8s 会认为进程崩溃，导致 CrashLoopBackOff 无限重启。

```yaml
components:
  - name: dtk
    port: 0
    image: ""    # CLI 工具，跳过部署
```

---

## project.env 详解

```env
PROJECT_NAME=myapp          # 项目名，helm release name 和 namespace 默认值

REGISTRY_PREFIX=qingchun22  # 镜像前缀，拼出来是 qingchun22/myapp-arm64:v0.1.0
                             # 可以是 Docker Hub username 或 ACR 地址

KUBE_CONTEXT=               # kubectl context 名
                             # 留空 = 使用当前 context
                             # ⚠️ 禁止写 ""，空字符串会导致 --kube-context "" 报错

KUBE_NAMESPACE=myapp        # K8s namespace，不存在时自动创建

ARCH=arm64                  # 镜像架构，影响 tag 后缀（arm64 / amd64）

VERSION=v0.1.0              # 镜像 tag，改这个触发重新 build+push
```

---

## helm upgrade 参数说明

```makefile
helm upgrade --install $(PROJECT_NAME) $(CHART_DIR) \
    --set image.repository=$(REGISTRY_PREFIX)/$(firstword $(BINS))-$(ARCH) \
    --set image.tag=$(VERSION) \
    --force-conflicts \    # 防止 SSA field manager 冲突
    --wait \               # 等 pod ready 再返回，确保 rollout 能找到 deployment
    --timeout 120s
```

**`--force-conflicts`**：如果曾用 `kubectl set image` 直接改过 deployment，
Helm SSA 会遇到 field manager 冲突。`--force-conflicts` 强制接管这些字段。

**`--wait`**：Helm 默认异步返回，不加 `--wait` 的话 deployment 还没 ready，
后续 `kubectl set image` 就会报 `deployments.apps "x" not found`。

**`$(firstword $(BINS))`**：镜像名取自 `cmd/` 目录扫描结果，
不用 `$(PROJECT_NAME)` 是因为两者可能不同（如 `PROJECT_NAME=dev-toolkit`，binary 是 `dtk`）。

---

## 部署到国内 / 生产环境

**镜像仓库换阿里云 ACR：**

```env
REGISTRY_PREFIX=registry.cn-hangzhou.aliyuncs.com/yournamespace
```

国内推拉不需要代理，速度稳定。

**多架构镜像：**

```bash
make image.multiarch PLATFORMS="linux_amd64 linux_arm64"
make push.multiarch
```

---

## 常见问题

**部署卡住不动**

`--wait` 在等 pod ready，另开终端查看：

```bash
kubectl get pods -n myapp
kubectl describe pod -n myapp <pod-name>
kubectl logs -n myapp <pod-name>
```

**SSA field manager 冲突**

```
conflict with "kubectl-set" using apps/v1
```

清除 field manager 记录：

```bash
kubectl patch deployment myapp -n myapp \
  --type=merge \
  -p '{"metadata":{"managedFields":null}}'
```

**pod 一直 0/1 Running，不变 Ready**

检查 readiness probe。生成的 `values.yaml` 中探测路径是 `/healthz`，
确认服务确实在该路径返回 200。

```bash
kubectl exec -n myapp <pod-name> -- wget -qO- http://localhost:8080/healthz
```
