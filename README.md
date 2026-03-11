# dev-toolkit (dtk) ⛓️ v1.0

**Go + K8s Helm Scaffold**：1行 init boilerplate，1键 AI-plan + deploy (helm + image + resources + rollout)

## 🚀 Quickstart

```
go install github.com/Ixecd/dev-toolkit/cmd/dtk@latest
dtk init --name myapp --module github.com/me/myapp
cd myapp
go mod tidy
git init
git add --all .
dtk deploy  # 默认即开启认证授权
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
--name <lowercase>  # project (ROOT_PACKAGE)
--module <github.com/me/myapp>  # go mod
--output ~/myapp  # default ./myapp
--template <dir>  # DTK_TEMPLATE_ROOT
--force  # overwrite
```

gen：
```
Makefile (tidy gen lint build image push deploy)
cmd/myapp/main.go (http 8080 /healthz)
configs/components.yaml (AI plan input)
configs/project.env (PROJECT_NAME=myapp KUBE_NAMESPACE=myapp REGISTRY_PREFIX=local)
deployments/myapp/Chart.yaml values.yaml templates/
build/docker/myapp/Dockerfile
```

### dtk deploy [flags]

```
--components configs/components.yaml  # AI input
--namespace myns  # default project.env KUBE_NAMESPACE
--context ctx  # k context
--dry-run  # plan only
```

flow：
1. AI plan resources (replicas cpu mem storage)
2. make deploy.full (helm install + k set image/scale/resources)
3. rollout status --timeout=300s

**git:master*** ignore (legacy arg)。

## 🛠️ Makefile Targets

```
make tidy gen lint cover build  # dev
make image.multiarch push.multiarch  # build/push amd64/arm64
make deploy.full  # helm + run
make release VERSION=v1.0.0  # tag push
make help  # full
```

vars：
```
VERSION=v1.0.0 ARCH=amd64 REGISTRY_PREFIX=local ROOT_DIR=$(pwd)
```

## 🔧 Customization

**components.yaml**：
```
components:
 - name: first
   port: 8080
   image: first
```

AI → replicas=1 cpu=100m memory=128Mi

**project.env**：
```
PROJECT_NAME=first
KUBE_NAMESPACE=first
REGISTRY_PREFIX=local
```

**Chart.yaml/values.yaml**：helm templates deployment svc。

**Dockerfile**：build/docker/first/Dockerfile alpine copy bin。

## 🐛 Troubleshooting

- **HEAD**：git commit/tag v0.1.0 pre-deploy
- **Chart deps**：template clean (dependencies: [])
- **image empty**：VERSION/REGISTRY_PREFIX export
- **override**：Makefile rm release.tag block

## 🎖️ v1.0 Changelog

- template replace demo-svc → name
- project.env gen
- Chart.yaml deps clean
- main.go makeEnv env + vars
- ROOT_DIR pwd
- e2e init→deploy pod up

## 🤝 Contrib

1. fork github.com/Ixecd/dev-toolkit
2. dtk init --name fix --module your/fix
3. code
4. make image push deploy
5. PR

**license**：MIT