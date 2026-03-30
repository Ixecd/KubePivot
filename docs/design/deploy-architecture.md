# 部署架构设计

本文说明 `kp deploy` 的内部设计，以及若干关键决策的原因。

---

## 分层设计

```
┌──────────────────────────────────────────────────────────┐
│                      kp deploy                          │  Go CLI
│  读配置 → 前置检查 → 状态机 → 拓扑排序 → 逐层部署          │
└──────────────────┬───────────────────────────────────────┘
                   │
        ┌──────────┴──────────┐
        │ 单服务               │ 多服务
        ▼                     ▼
  make deploy.full     deployLayers（goroutine 并行）
  build/push/install   每个服务独立 helm release
        │                     │
        └──────────┬──────────┘
                   ▼
           状态机 → RUNNING
```

**单服务 vs 多服务判断**：

```go
isMultiService := len(layers) > 1 || (len(layers) == 1 && len(layers[0]) > 1)
```

单服务走原有 `make deploy.full` 路径，向后兼容；多服务走 `deployLayers`，每个服务独立 release。

---

## 前置检查

`kp deploy` 在执行之前检查：

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
      │ kp deploy
      ▼
INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                   ↓              ↓
             ROLLING_BACK ←───────┘  （失败自动回滚）
                   ↓
               CLEANING → IDLE      （首次部署失败，清理 namespace）
```

状态持久化到 etcd（优先）或 `~/.kp/state/<project>/<ns>.json`（降级）。

---

## 多服务部署流程

```
BuildLayers → []Layer

for each 层级（同层 goroutine 并行，层间串行）：
    deployService：
        ① chart 目录存在检查（不存在快速失败，不重试）
        ② image 为空且无 chart → 跳过（CLI 工具）
        ③ build 镜像（只做一次，失败直接返回）
        ④ push 镜像（只做一次）
        ⑤ helm upgrade --install {project}-{service}（最多重试 3 次）
        ⑥ kubectl rollout status --timeout=120s

    ↓ 任意服务失败
    collectAffected → Downstream（失败服务 + 所有下游，逆拓扑顺序）
    for each affected：
        helmReleaseExists 检查（未安装的跳过）
        helm rollback {release}
    ↓ 级联 rollback 也失败
    整组逆序 rollback（所有已成功部署的 release）
    ↓ 整组也失败
    kp down（清理 namespace）
```

**build/push 只做一次**：失败直接返回，不随 helm 重试而重复执行。

**helmReleaseExists**：用 `helm history --max 1` 检查 release 是否存在，防止 rollback 从未安装的 release 触发误下线。

---

## SSA 冲突自动处理（单服务路径）

deploy 失败时检查是否是 SSA managedFields 冲突：

```go
if isSSAConflict(err.Error()) {
    clearAllManagedFields(cfg)  // 批量清除（幂等）
    retryDeployWithSSAFix(cfg, makeEnv, root)
}
```

**为什么批量清除**：helm 错误信息格式随版本变化，精确解析容易出错；批量清除是幂等操作，多清了没副作用。

---

## 镜像拉取失败检测

deploy 失败时检查是否是镜像问题，输出可操作的排查提示：

```
检测到镜像拉取失败，请检查：
  1. 镜像是否已推送：docker manifest inspect registry/myapp-arm64:v1.0.0
  2. registry 是否需要登录：docker login
  3. ARCH 是否正确：当前 arm64，集群节点架构是否匹配
```

---

## IMAGES 变量的传递（单服务路径）

`kp deploy` 从 plan 中过滤出有 image 的组件，构建 `IMAGES` 环境变量传给 Makefile：

```go
for _, item := range plan {
    if item.Image != "" {
        imageNames = append(imageNames, item.Name)
    }
}
makeEnv = append(makeEnv, "IMAGES="+strings.Join(imageNames, " "))
```

多服务路径每个服务单独传 `IMAGES=<service-name>`，不走 Makefile 的 IMAGES 逻辑。

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

`docker manifest inspect` 查询远端 registry，无需拉取镜像。VERSION 不变时跳过 build 和 push，重跑 deploy 只更新 helm values。

---

## components.yaml 与 AI 规划

`internal/planner` 读取 `configs/components.yaml`，`kp ai-plan` 自动生成：

```yaml
components:
  - name: wallet-service
    type: deployment
    port: 2113
    image: wallet-service
    replicas: 2
    cpu: 200m
    memory: 256Mi
    depends_on:
      - postgres
      - etcd
```

`kp ai-plan` 支持 Grok / Claude / OpenAI / 豆包四个 provider，通过 `DTK_LLM_PROVIDER` 和 `DTK_LLM_API_KEY` 配置。详见 [AI 使用手册](../guide/zh-CN/ai.md)。

---

## 统一进度输出

所有部署步骤通过 `P.Start/Done/Fail/Info` 统一输出，带时间戳和耗时：

```
[15:38:09] 🏗  构建镜像 wallet-service（v0.1.10）
[15:38:23] ✓  构建完成（14.2s）
[15:38:23] 📤 推送镜像 wallet-service（v0.1.10）
[15:38:28] ✓  推送完成（5.5s）
[15:38:28] ⛵ helm upgrade web3-blitz-wallet-service
[15:38:29] ✓  helm upgrade 完成（0.6s）
[15:38:29] 🔍 等待 rollout 就绪
[15:38:29] ✓  服务就绪（0.3s）
[15:38:29] ✅ 部署完成，状态: RUNNING (version=v0.1.10)
```

---

## A2 Reconciliation Controller

`kp deploy` 完成后，controller pod 接管后续自愈：

```
kp deploy（CLI）→ 写状态到 etcd → 返回
                        ↓
controller（K8s pod，常驻）
    ├── etcd Watch（事件驱动，指数退避重连）
    └── 8s 周期 Reconcile（兜底）
            ↓
        资源缺失 → helm rollback → 自动恢复（~10s）
```

详见 [controller 设计文档](controller.md)。
