# dev-toolkit (dtk) ⛓️ v1.0

**Go + K8s Helm Scaffold**：1行 init boilerplate，1键 AI-plan + deploy (helm + image + resources + rollout)

> **目标用户**：有云原生经验的 Go 后端开发者。使用前需要装好 Go 1.25+ / Docker / kubectl / helm，并有可用的 K8s 集群和镜像仓库账号。

## 🚀 Quickstart

```bash
go install github.com/Ixecd/dev-toolkit/cmd/dtk@latest
dtk init --name myapp --module github.com/me/myapp
cd myapp
make tools  # 安装所有工具链（首次必须）
# 编辑 configs/project.env，填写 REGISTRY_PREFIX / KUBE_CONTEXT / VERSION
dtk deploy
```

**e2e 30s**：boilerplate → helm ns deploy → AI replicas=1 cpu100m mem128Mi → rollout。

## ⚙️ Features

- **init**：Makefile + scripts + helm charts + docker + githooks + vscode
- **deploy**：AI yaml plan (configs/components.yaml) → helm upgrade + k set image/resources/scale + rollout status
- **multiarch**：make image.multiarch PLATFORMS=linux/amd64,arm64
- **env**：configs/project.env PROJECT_NAME KUBE_NAMESPACE REGISTRY_PREFIX
- **vars**：VERSION ARCH REGISTRY_PREFIX auto ?= v0.1.0 amd64 local

**no bullshit**：no node_modules, alpine base, go mod tidy ready。

## 📋 Commands

### dtk init [flags]

```
--name <lowercase>              # project name
--module <github.com/me/myapp>  # go mod
--output ~/myapp                # default ./myapp
--template <dir>                # DTK_TEMPLATE_ROOT
--force                         # overwrite
--with-frontend                 # 同时生成 React + Vite + Tailwind 前端骨架
```

gen：

```
Makefile (tidy gen lint build image push deploy)
cmd/myapp/main.go (http 8080 /healthz)
configs/components.yaml (AI plan input)
configs/project.env (PROJECT_NAME KUBE_NAMESPACE REGISTRY_PREFIX)
deployments/myapp/Chart.yaml values.yaml templates/
build/docker/myapp/Dockerfile
monitoring/ (prometheus + alertmanager + grafana)
```

### dtk deploy [flags]

```
--components configs/components.yaml  # AI input
--namespace myns                      # default project.env KUBE_NAMESPACE
--context ctx                         # k context
--dry-run                             # plan only
```

flow：

1. AI plan resources (replicas cpu mem storage)
2. docker build/push（VERSION 未变则自动跳过）
3. helm upgrade --install --wait（等 pod ready 再继续）
4. kubectl set image + rollout status --timeout=300s

## ⚙️ 关键配置

**configs/project.env**：

```env
PROJECT_NAME=myapp
REGISTRY_PREFIX=your-dockerhub-username  # 必填
KUBE_CONTEXT=                            # 留空=当前 context，禁止写 ""
KUBE_NAMESPACE=myapp
ARCH=arm64
VERSION=v0.1.0                           # 改这个触发重新 build+push
```

**configs/components.yaml**：

```yaml
components:
  - name: myapp
    port: 8080
    image: myapp   # 留空或 "" = 跳过部署（CLI 工具用这个，防止 CrashLoopBackOff）
```

## 🛠️ Makefile Targets

```
make tidy gen lint cover build  # dev
make image.multiarch push.multiarch  # build/push amd64/arm64
make deploy.full  # helm + run
make tools  # 安装所有工具链
make help   # full
```

## 🐛 Troubleshooting

- **deploy 卡住**：另开终端 `kubectl get pods -n <ns>` 查看，`kubectl describe pod` 看详情
- **ImagePullBackOff**：检查 REGISTRY_PREFIX 是否正确，该 VERSION 镜像是否已推送成功
- **国内 docker push 超时**：代理开 TUN 模式，或改用阿里云 ACR
- **SSA 冲突**：曾用 kubectl set image 直接改过 deployment，执行 `kubectl patch deployment <n> -n <ns> --type=merge -p '{"metadata":{"managedFields":null}}'` 清除
- **CrashLoopBackOff 套娃**：CLI 工具的 `image` 必须留空，不能部署到 K8s
- **make build 找不到 package**：检查 Makefile 中 ROOT_PACKAGE 是否与 go.mod module 路径一致

## 🎖️ v1.0 Changelog

- `--with-frontend` flag，生成通用 React 前端骨架
- deploy：VERSION 不变自动跳过 build/push
- deploy：helm `--force-conflicts` + `--wait` 防冲突
- template：Dockerfile 改用 go mod tidy + GOPROXY=goproxy.cn
- template：Chart.yaml name / appVersion / service.port / healthz probe 自动修正
- fix：IMAGES 从 plan 构建并传给 make，CLI 工具自动跳过
- e2e init→deploy pod `1/1 Running` 打通

## 🤝 Contrib

1. fork github.com/Ixecd/dev-toolkit
2. dtk init --name fix --module your/fix
3. code
4. make image push deploy
5. PR

**license**：MIT