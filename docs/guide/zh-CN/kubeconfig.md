# 多集群部署指南

`kp deploy` 支持通过 `--kubeconfig` 和 `--context` 指定目标集群，满足本地开发、staging、生产等多环境部署需求。

---

## kubeconfig 和 context 的关系

**kubeconfig** 是文件，一个文件里可以包含多个集群的连接信息：

```yaml
# ~/.kube/config 示例
clusters:
  - name: prod-cluster
  - name: staging-cluster

contexts:
  - name: prod        # context = 集群 + 用户 + namespace 的组合
    cluster: prod-cluster
    user: admin
  - name: staging
    cluster: staging-cluster
    user: dev

current-context: staging
```

**context** 是在某个 kubeconfig 文件里选哪个集群/用户。

两者是不同层级：kubeconfig 决定"用哪个文件"，context 决定"用文件里的哪个集群"。

---

## 使用方式

### 命令行参数

```bash
# 用默认 kubeconfig（~/.kube/config），切换到 prod context
kp deploy --context prod

# 用另一个 kubeconfig 文件，使用它的默认 context
kp deploy --kubeconfig ~/.kube/prod.yaml

# 用另一个 kubeconfig 文件，并指定其中某个 context
kp deploy --kubeconfig ~/.kube/multi.yaml --context staging
```

### 写入 project.env（推荐，不用每次带参数）

```ini
# configs/project.env
PROJECT_NAME=myapp
KUBE_NAMESPACE=myapp
KUBE_CONTEXT=prod               # 留空=当前 context
KUBE_CONFIG=~/.kube/prod.yaml   # 留空=默认 ~/.kube/config
```

配置好之后直接 `kp deploy` 即可，不需要额外参数。

### 优先级

```
命令行 --kubeconfig  >  project.env KUBE_CONFIG  >  默认 ~/.kube/config
命令行 --context     >  project.env KUBE_CONTEXT >  kubeconfig current-context
```

---

## 常见场景

### 场景一：只有一个 kubeconfig，多个集群

```bash
# 查看有哪些 context
kubectl config get-contexts

# 部署到 prod
kp deploy --context prod

# 部署到 staging
kp deploy --context staging
```

或者写到 `project.env`，不同环境维护不同的 `project.env`：

```
configs/
├── project.env          # 当前活跃环境
├── project.staging.env  # staging 配置
└── project.prod.env     # prod 配置
```

### 场景二：不同集群放在不同 kubeconfig 文件

```bash
# staging
kp deploy --kubeconfig ~/.kube/staging.yaml

# prod
kp deploy --kubeconfig ~/.kube/prod.yaml
```

### 场景三：CI/CD 环境

CI 环境通常把 kubeconfig 注入到特定路径或环境变量，在 `project.env` 或 CI 变量里指定：

```ini
# GitLab CI / GitHub Actions 中
KUBE_CONFIG=/tmp/kubeconfig
KUBE_CONTEXT=ci-cluster
```

---

## 注意事项

**`KUBE_CONTEXT` 不能写 `""`（带引号的空字符串）**

```ini
# ✅ 正确：留空
KUBE_CONTEXT=

# ❌ 错误：会把 "" 当成 context 名传给 kubectl
KUBE_CONTEXT=""
```

原因：`deploy.mk` 用 `$(if $(strip $(CONTEXT)),...)` 判断是否为空，带引号会导致判断失败，kubectl 收到 `--context ""` 报错。

**`KUBE_CONFIG` 支持 `~` 展开**

```ini
KUBE_CONFIG=~/.kube/prod.yaml  # ✅ kp 会自动展开为绝对路径
```

**切换集群后记得同步 `KUBE_NAMESPACE`**

不同集群的 namespace 可能不同，切换集群时顺手检查一下 namespace 配置是否正确。