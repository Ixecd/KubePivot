# KubePivot v2.6 蓝绿部署示例

> 一个最小可运行的蓝绿部署 demo，用 `kp sandbox commit` 在 30 秒内体验
> KubePivot v2.6 的流量切换能力。

---

## 这个 demo 演示什么

**v2.6 蓝绿部署的完整链路**：
- 在 K8s 集群部署一个 Web 服务（whoami），同时存在 blue / green 两个版本
- 通过 Ingress（或 Gateway API）把流量定向到 blue
- 用 `kp sandbox commit` 把流量原子切换到 green
- 失败自动回滚到 blue

**v2.6 的核心价值**：流量切换不需要手动 `kubectl edit ingress`，而是：
1. 在 `resources.yaml` 声明流量配置
2. KubePivot 把它纳入 Sandbox 状态机
3. 切换 + Pod ready 健康判定 + 失败自愈一气呵成

---

## 前置条件

- K8s 集群（任何版本均可，本 demo 在 orbstack / minikube / kind 上验证）
- KubePivot v2.6.0+（`kp version` 检查）
- kubectl 配置正确（`kubectl get nodes` 能看到节点）
- 集群安装了任何 Ingress controller（nginx-ingress / traefik 等）
  或 Gateway API CRD（`kubectl get crd gatewayclasses.gateway.networking.k8s.io`）

---

## 快速开始

```bash
# 1. 进入 demo 目录
cd docs/example-blue-green

# 2. 一键 setup（部署 blue 版本 + 创建 Ingress）
make setup

# 3. 验证当前流量指向 blue
make verify
# 期望输出：Hostname: whoami-blue-xxxxx

# 4. 触发蓝绿切换（部署 green + 流量切到 green）
make switch

# 5. 再次验证流量
make verify
# 期望输出：Hostname: whoami-green-xxxxx

# 6. 清理
make cleanup
```

整个流程在干净集群上 **<2 分钟** 完成。

---

## 文件结构

```
docs/example-blue-green/
├── README.md                  本文件
├── Makefile                   一键命令封装
├── resources.yaml             v2.6 蓝绿配置（KubePivot 读取）
├── chart/                     Helm chart（蓝/绿两个 Deployment + Service + Ingress）
│   ├── Chart.yaml
│   ├── values.yaml
│   └── templates/
│       ├── deployment-blue.yaml
│       ├── deployment-green.yaml
│       ├── service-blue.yaml
│       ├── service-green.yaml
│       └── ingress.yaml
└── scripts/
    ├── setup.sh               初始 blue 部署
    ├── switch.sh              sandbox commit 触发切换
    └── cleanup.sh             清理
```

---

## resources.yaml 关键字段

```yaml
resources:
  - kind: Deployment
    name: whoami-blue
    on-missing: auto-heal

  - kind: Deployment
    name: whoami-green
    on-missing: auto-heal

# v2.6.0 流量层配置
traffic:
  # kind 可选：Ingress / Gateway / 不写=自动检测
  # 自动检测顺序：Gateway API > Ingress
  kind: Ingress

  strategy: blue-green

  refs:
    name: whoami-ingress

  routes:
    - service: whoami-blue
      weight: 100
    - service: whoami-green
      weight: 0

  validation:
    podReadyTimeoutSec: 60
```

**关键概念**：
- `routes` 列表中 `weight=100` 的 service 是当前活跃版本
- 切换时把 weight 在 blue / green 间互换
- `kp sandbox commit` 自动完成 helm upgrade + 流量切换 + Pod ready 验证

---

## 蓝绿切换的内部时序

`make switch` 触发的具体动作：

```
1. 用户更新 resources.yaml（把 weight 100 / 0 互换）
2. kp sandbox start              → LOCKED
3. kp sandbox snapshot            → SNAPSHOTTING（PVC 快照如有）
4. kp sandbox simulate            → SIMULATING（迁移 dry-run）
5. kp sandbox commit:
   a. runSandboxCommit            → 执行 helm upgrade（部署 green）
   b. runBlueGreenSwitch (v2.6):
      - LoadResources resources.yaml
      - HasBlueGreen() 判断启用
      - route.ProviderForKind() 构造 provider（Ingress 或 Gateway）
      - provider.ApplyRoutes()    → 修改 Ingress backend.service.name
      - waitDeploymentReady()     → 等 green Pod 全部 ready
   c. → RUNNING

如任何步骤失败：
   → RESTORING                   → runSandboxRestore（流量回 blue + green 销毁）
   → IDLE
```

---

## 失败回滚验证

```bash
# 模拟切换失败：手动把 green deployment 改坏
kubectl patch deploy whoami-green -p '{"spec":{"template":{"spec":{"containers":[{"name":"whoami","image":"non-existent:tag"}]}}}}'

# 触发切换
make switch

# 期望：
#   - 流量切换到 green
#   - Pod ready 等待超时（image pull failed）
#   - KubePivot 自动 RESTORING
#   - 流量回 blue
#   - green Deployment 销毁

# 验证流量仍在 blue
make verify
# 期望输出：Hostname: whoami-blue-xxxxx
```

---

## 与 web3-blitz 升级的关系

本 demo **不依赖** web3-blitz。web3-blitz 升级到 v2.6 是 v2.8 计划任务。

如果想在真实业务项目里启用蓝绿：
1. 项目已是 KubePivot 管理（kp init + kp deploy 跑过）
2. 在 `configs/resources.yaml` 加 `traffic:` 字段（参考本 demo）
3. 部署时用 `kp sandbox commit` 而不是 `kp deploy`

---

## 进一步阅读

- [流量层设计文档](../design/traffic-layer.md) — 完整的 v2.6 设计
- [Sandbox 状态机](../design/state-machine.md) — COMMITTING 内分两步原理
- [Provider 接口](../../internal/route/provider.go) — Go 代码 + 注释

---

## 故障排查

### "Route provider is not available"

集群既没有 Ingress 资源类型也没有 Gateway API CRD：

```bash
# 检查 Ingress
kubectl api-resources | grep ingresses

# 检查 Gateway API
kubectl get crd | grep gateway.networking.k8s.io
```

至少需要其中一个。安装 nginx-ingress 是最简单的 fallback：
```bash
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/cloud/deploy.yaml
```

### 切换后 Pod ready 超时

`waitDeploymentReady` 用 `kubectl rollout status` 等待，超时默认 60 秒。
原因可能：
- 镜像拉取失败（检查 `kubectl describe pod` 的 events）
- 健康检查失败
- 资源不足（CPU / memory limit）

调高超时：在 resources.yaml 改 `validation.podReadyTimeoutSec`。

### 流量切换看似成功但 curl 仍命中 blue

可能是 Ingress controller 缓存或 DNS：
```bash
# 直接看 ingress 后端
kubectl get ingress whoami-ingress -o jsonpath='{.spec.rules[0].http.paths[0].backend.service.name}'
# 期望：whoami-green
```

如果显示 green 但 curl 仍是 blue，重启 Ingress controller pod。
