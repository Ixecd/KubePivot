# 部署架构设计

本文说明 `dtk deploy` 的内部设计，以及若干关键决策的原因。

---

## 分层设计

```
┌─────────────────────────────────────────┐
│              dtk deploy                 │  Go CLI
│  读配置 → 过滤组件 → 组装 IMAGES → make    │
└─────────────────┬───────────────────────┘
                  │ makeEnv (IMAGES / VERSION / ARCH ...)
┌─────────────────▼───────────────────────┐
│           make deploy.full              │  Makefile
│  build → push → install → run.all       │
└─────────────────────────────────────────┘
```

**为什么分两层？**

- `dtk`（Go）负责读配置、AI 规划、过滤逻辑，这些需要 Go 的结构化处理
- `make`（Makefile）负责 docker / helm / kubectl 操作，这些用 shell 更自然
- 两层通过环境变量通信，职责清晰，也方便用户单独执行 `make deploy.full` 调试

---

## IMAGES 变量的传递

`dtk deploy` 在调用 `make` 之前，从 plan 中过滤出有效组件，构建 `IMAGES` 环境变量：

```go
var imageNames []string
for _, item := range plan {
    if item.Image == "" {
        continue  // CLI 工具、辅助组件跳过
    }
    imageNames = append(imageNames, item.Name)
}
makeEnv = append(makeEnv, "IMAGES="+strings.Join(imageNames, " "))
```

**为什么不让 Makefile 自己过滤？**

Makefile 变量是纯字符串，没有结构化处理能力，很难在 `$(foreach)` 里判断某个组件是否有 image。
交给 Go 处理更可靠，也更容易测试。

**如果不传 IMAGES 会怎样？**

`deploy.mk` 中 `DEPLOYS ?= $(if $(IMAGES),$(IMAGES),$(BINS))`，
`IMAGES` 为空则 fallback 到 `BINS`（扫描 `cmd/` 目录），
可能混入非预期的文件（如 `README.md`），导致构建 `qingchun22/README.md-arm64:v0.1.0`。

---

## VERSION 跳过机制

```makefile
deploy.build:
    if docker manifest inspect $(REGISTRY_PREFIX)/$(img)-$(ARCH):$(VERSION); then
        echo "Image already exists, skipping build"
    else
        docker build ...
    fi
```

`docker manifest inspect` 查询远端 registry，无需拉取镜像，只检查 manifest 是否存在。
VERSION 不变时跳过 build 和 push，避免每次 `dtk deploy` 都重新构建。

---

## Helm SSA 与 --force-conflicts

Helm 3.x 默认使用 Server-Side Apply（SSA）管理资源。
SSA 使用 field manager 追踪每个字段的"所有权"。

**冲突场景：**

```bash
kubectl set image deployment/myapp myapp=qingchun22/myapp:v0.1.1
# 这个命令的 field manager 是 "kubectl-set"
# .spec.template.spec.containers[name="myapp"].image 被 kubectl-set 持有

helm upgrade myapp ...
# Helm 的 field manager 是 "helm"
# Helm 发现 image 字段被别的 manager 持有 → 冲突报错
```

**解决：** `--force-conflicts` 让 Helm 强制接管这些字段，不再报错。

**根本解决：** 不要在 Helm 管理的 deployment 上直接用 `kubectl set image`。
`deploy.run.%` 里的 `kubectl set image` 在 `deploy.install --wait` 之后执行，
此时 Helm 已经完成更新，`kubectl set image` 只是再滚动一次，实际上是冗余的。
未来可以考虑去掉 `deploy.run.all`，完全由 Helm 管理。

---

## --wait 的必要性

```
没有 --wait：
helm upgrade --install → 立即返回（资源已创建但 pod 未 ready）
                       ↓
deploy.run.all → kubectl set image deployment/myapp ...
              → Error: deployments.apps "myapp" not found  ❌

有 --wait：
helm upgrade --install --wait → 等待 deployment ready 再返回
                              ↓
deploy.run.all → kubectl set image → 正常执行  ✅
```

---

## image.repository 用 firstword(BINS) 的原因

```makefile
--set image.repository=$(REGISTRY_PREFIX)/$(firstword $(BINS))-$(ARCH)
```

`$(BINS)` 由 `golang.mk` 扫描 `cmd/` 目录生成，反映实际 binary 名。
`$(PROJECT_NAME)` 是 `project.env` 里的配置，可能与 binary 名不一致。

典型例子：
```
PROJECT_NAME = dev-toolkit   # helm release name
BINS         = dtk           # cmd/dtk/main.go 扫描结果
```

如果用 `$(PROJECT_NAME)`，镜像名是 `qingchun22/dev-toolkit-arm64`，
但实际推上去的是 `qingchun22/dtk-arm64`，pod 拉镜像失败。

---

## components.yaml 手写 YAML 解析

`internal/ai/ai.go` 使用手写的逐行解析，而不是 yaml 库，原因：

1. 格式固定简单，不需要通用 YAML 解析
2. 减少外部依赖
3. 方便控制字段处理逻辑（如 `image: ""` 去引号）

**注意：** `image: ""` 去掉前缀后是 `""`（带引号的字符串），需要显式 `strings.Trim(val, `"`)` 处理，否则 image 不为空，会参与构建。
