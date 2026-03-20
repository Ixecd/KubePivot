# 部署参考

---

## dtk deploy 方式（推荐）

从 v1.0 开始，推荐使用 `dtk deploy` 替代手动 kubectl apply 流程。

### 本地 K8s（OrbStack / Rancher Desktop）

```bash
# 确认 context
kubectl config get-contexts
kubectl config use-context orbstack  # 或你的本地 context 名

# 配置
vim configs/project.env
# KUBE_CONTEXT=orbstack
# REGISTRY_PREFIX=your-dockerhub-username
# VERSION=v0.1.0

# 部署
dtk deploy
```

### 云上 K8s（阿里云 / 腾讯云 / 华为云）

```bash
# 1. 从云平台下载 kubeconfig，合并到本地
cp ~/Downloads/kubeconfig ~/.kube/config-prod
export KUBECONFIG=~/.kube/config:~/.kube/config-prod
kubectl config get-contexts  # 确认新 context 出现

# 2. 配置
vim configs/project.env
# KUBE_CONTEXT=your-cloud-context
# REGISTRY_PREFIX=registry.cn-hangzhou.aliyuncs.com/yournamespace  # 推荐用 ACR，国内稳
# VERSION=v0.1.0

# 3. 部署
dtk deploy
```

### 镜像仓库推荐

| 仓库 | 适用场景 | 说明 |
|------|---------|------|
| Docker Hub | 开发测试 | 国内推送需代理 |
| 阿里云 ACR | 生产 / 国内 | 免费额度够用，推拉都快 |
| GitHub Container Registry | CI/CD | 配合 GitHub Actions 方便 |

---

## Makefile 手动部署方式

如果不想用 `dtk deploy`，也可以直接调用 make targets：

```bash
# 构建镜像
make image ARCH=arm64 VERSION=v0.1.0

# 推送镜像
make push REGISTRY_PREFIX=your-prefix VERSION=v0.1.0

# Helm 部署
make deploy

# 或一步到位
make deploy.full
```

---

## 多架构部署

```bash
# 构建 amd64 + arm64
make image.multiarch PLATFORMS="linux_amd64 linux_arm64" VERSION=v0.1.0

# 推送
make push.multiarch PLATFORMS="linux_amd64 linux_arm64" VERSION=v0.1.0
```

---

## 手动 kubectl 方式（历史参考）

> 以下为旧版手动流程，仅供参考，不推荐在使用 dtk 的项目中采用。

1. 创建 namespace：`kubectl create namespace <project>`
2. 创建 docker-registry secret
3. `docker build + docker push`
4. `helm upgrade --install` 或 `kubectl apply -f deployments/`
5. `kubectl rollout status deployment/<n>`

dtk deploy 封装了以上所有步骤，并加入了 VERSION 跳过、SSA 冲突处理、资源规划等增强。

---

## 回滚

```bash
# 查看 helm 历史
helm history <project-name> -n <namespace>

# 回滚到上一版本
helm rollback <project-name> -n <namespace>

# 回滚到指定版本
helm rollback <project-name> <revision> -n <namespace>
```

---

## 删除部署

```bash
# 删除 helm release（保留 namespace）
helm uninstall <project-name> -n <namespace>

# 删除整个 namespace
kubectl delete namespace <namespace>
```
