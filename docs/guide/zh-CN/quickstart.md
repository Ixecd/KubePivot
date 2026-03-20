# 快速上手

从零开始，在本地把一个 dtk 生成的项目跑到 K8s 上，预计 **5 分钟**。

---

## 前置条件

确保以下工具已安装并可用：

```bash
go version      # 1.25+
docker version
kubectl version --client
helm version
```

有可用的 K8s 集群（本地 OrbStack / Rancher Desktop，或远程集群），以及 Docker 镜像仓库账号。

---

## 第一步：安装 dtk

```bash
go install github.com/Ixecd/dev-toolkit/cmd/dtk@latest
dtk --help  # 验证安装成功
```

---

## 第二步：生成项目骨架

```bash
dtk init --name myapp --module github.com/me/myapp
cd myapp
```

`dtk init` 自动完成：
- 生成完整项目结构
- 替换所有模板占位符
- `git init` + 初始提交
- `go get` 核心依赖 + `go mod tidy`

---

## 第三步：安装工具链

```bash
make tools
```

安装 golangci-lint、goimports、golines 等所有开发工具。**首次克隆后必须执行一次。**

---

## 第四步：配置部署参数

编辑 `configs/project.env`：

```env
PROJECT_NAME=myapp
REGISTRY_PREFIX=your-dockerhub-username   # 改成你的镜像仓库前缀
KUBE_CONTEXT=                             # 留空=当前 context，禁止写 ""
KUBE_NAMESPACE=myapp
ARCH=arm64                                # 或 amd64
VERSION=v0.1.0
```

编辑 `configs/components.yaml`：

```yaml
components:
  - name: myapp     # 必须与 cmd/ 下的 binary 名一致
    port: 8080
    image: myapp    # 非空=参与部署；留空=跳过（CLI 工具用这个）
```

---

## 第五步：一键部署

```bash
dtk deploy
```

输出示例：

```
AI 规划结果:
- myapp: replicas=1 cpu=100m memory=128Mi storage=1Gi
===========> Building qingchun22/myapp-arm64:v0.1.0
===========> Pushing qingchun22/myapp-arm64:v0.1.0
===========> Installing chart myapp to myapp
NAME: myapp  STATUS: deployed
===========> Deploying myapp v0.1.0 on arm64
deployment "myapp" successfully rolled out
```

验证：

```bash
kubectl get pods -n myapp
# NAME                     READY   STATUS    RESTARTS   AGE
# myapp-xxxxxxxxx-xxxxx    1/1     Running   0          30s
```

---

## 下一步

- 查看 [deploy.md](./deploy.md) 了解完整部署流程和 VERSION 机制
- 查看 [../../../docs/design/scaffold.md](../../design/scaffold.md) 了解脚手架生成逻辑
- 执行 `make help` 查看所有可用命令
