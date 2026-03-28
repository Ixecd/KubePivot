# 部署架构设计

本文说明 `dtk deploy` 的内部设计，以及若干关键决策的原因。

---

## 分层设计

```
┌─────────────────────────────────────────────────────┐
│                    dtk deploy                       │  Go CLI
│    读配置 → 前置检查 → 状态机 → AI 规划 → make          │
└─────────────────┬───────────────────────────────────┘
                  │ makeEnv (IMAGES / VERSION / ARCH ...)
┌─────────────────▼───────────────────────────────────┐
│               make deploy.full                      │  Makefile
│    build → push → install → run.all                 │
└─────────────────────────────────────────────────────┘
```

**为什么分两层？**

- `dtk`（Go）负责读配置、前置检查、状态机、AI 规划，这些需要结构化处理
- `make`（Makefile）负责 docker / helm / kubectl 操作，shell 更自然
- 两层通过环境变量通信，职责清晰，用户也可单独执行 `make deploy.full` 调试

---

## 前置检查

`dtk deploy` 在执行之前检查：

```
1. 依赖检查（docker / kubectl / helm 是否可用）
2. helm release 状态检查（checkHelmReleaseState）
   ├── pending-rollback → 删除 secret + ForceState RUNNING（询问用户确认）
   ├── pending-install  → 删除 release 重新安装（询问用户确认）
   └── failed           → 提供回滚或重新部署选项
3. 状态机检查（非 IDLE/RUNNING/TERMINATED 拒绝新部署）
```

---

## 状态机流转

```
IDLE / RUNNING / TERMINATED
      │ dtk deploy
      ▼
INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                   ↓              ↓
             ROLLING_BACK ←───────┘  （失败自动回滚）
                   ↓
               CLEANING → IDLE      （首次部署失败，清理 namespace）
```

状态持久化到 etcd（优先）或 `~/.dtk/state/<project>/<ns>.json`（降级）。

---

## SSA 冲突自动处理

deploy 失败时检查是否是 SSA managedFields 冲突：

```go
if isSSAConflict(err.Error()) {
    // 清除 namespace 下所有资源的 managedFields（幂等操作）
    clearAllManagedFields(cfg)
    // 重试一次
    retryDeployWithSSAFix(cfg, makeEnv, root)
}
```

**为什么批量清除而不是精确定位**：helm 错误信息格式随版本变化，精确解析容易出错；批量清除是幂等操作，多清了没副作用。

---

## 镜像拉取失败检测

deploy 失败时检查是否是镜像问题：

```go
if isImagePullError(err.Error()) {
    // 输出可操作的排查提示：
    // 1. docker manifest inspect 检查镜像是否存在
    // 2. docker login 检查登录状态
    // 3. ARCH 是否和集群节点匹配
}
```

---

## IMAGES 变量的传递

`dtk deploy` 从 plan 中过滤出有 image 的组件，构建 `IMAGES` 环境变量：

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

**为什么不让 Makefile 自己过滤**：Makefile 变量是纯字符串，`$(foreach)` 里无法判断组件是否有 image，交给 Go 处理更可靠。

**如果不传 IMAGES 会怎样**：`deploy.mk` 中 `DEPLOYS ?= $(if $(IMAGES),$(IMAGES),$(BINS))`，`IMAGES` 为空则 fallback 到扫描 `cmd/` 目录，可能混入非预期的文件。

---

## ARCH 自动检测

`ARCH` 优先级：`project.env` > `go env GOARCH` > `amd64`

```go
arch := envOrDefault(env, "ARCH", "")
if arch == "" {
    if out, err := exec.Command("go", "env", "GOARCH").Output(); err == nil {
        arch = strings.TrimSpace(string(out))
    }
}
if arch == "" {
    arch = "amd64"
}
```

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

`docker manifest inspect` 查询远端 registry，无需拉取镜像。VERSION 不变时跳过 build 和 push。

---

## Helm SSA 与 --force-conflicts

**冲突场景**：`kubectl set image` 的 field manager 是 `kubectl-set`，helm 的 field manager 是 `helm`，两者对同一字段的所有权冲突。

**解决**：`--force-conflicts` 让 Helm 强制接管这些字段。

**根本解决**：不要在 Helm 管理的资源上直接用 `kubectl set image`。`deploy.run.all` 里的 `kubectl set image` 是冗余的，未来可考虑去掉。

---

## --wait 的必要性

```
没有 --wait：
helm upgrade → 立即返回（pod 未 ready）
            ↓
deploy.run.all → kubectl set image → Error: deployment not found  ❌

有 --wait：
helm upgrade --wait → 等待 ready 再返回
                    ↓
deploy.run.all → kubectl set image → 正常执行  ✅
```

---

## components.yaml 与 AI 规划

`internal/planner` 读取 `configs/components.yaml`，目前按规则估算资源：

```yaml
components:
  - name: myapp
    port: 8080
    image: myapp
```

> 🚧 **待实现**：接入真实 LLM，扫描代码仓库自动生成/更新 components.yaml，AI 给出资源建议（replicas / cpu / memory）。详见 TODO。

---

## A2 Reconciliation Controller

`dtk deploy` 完成后，controller pod 接管后续自愈：

```
dtk deploy（CLI）→ 写状态到 etcd → 返回
                        ↓
controller（K8s pod，常驻）
    ├── etcd Watch（事件驱动，指数退避重连）
    └── 8s 周期 Reconcile（兜底）
            ↓
        资源缺失 → helm rollback → 自动恢复（~10s）
```

详见 [controller 设计文档](controller.md)。
