# 蓝绿发布设计

## 概述

KubePivot 内置蓝绿发布策略，无需 Istio / Service Mesh，基于 K8s 原生 Service selector 实现流量切换。金丝雀发布作为用户扩展点，通过 `scripts/canary-hook.sh` 接入。

## 核心思路
```
blue slot（当前活跃，100% 流量）
green slot（新版本，0% 流量，部署验证中）
         ↓ kp promote
green slot（新活跃，100% 流量）
blue slot（保留，用于快速回滚）
```

切换机制：`kubectl patch service` 修改 selector 中的 `bluegreen-slot` label，瞬间完成，零停机。

## 使用方式
```yaml
# configs/components.yaml
components:
  - name: wallet-service
    strategy: blue-green
    image: wallet-service
    port: 2113
```
```bash
# 1. 部署新版本到非活跃 slot（不影响线上流量）
kp deploy

# 2. 验证新版本（curl、日志、监控）
kubectl port-forward -n web3-blitz deployment/wallet-service-green 2113:2113

# 3. 切换流量
kp promote

# 4. 回滚（如有问题）
kp rollback
```

## K8s 资源命名

| 资源 | 命名规则 | 说明 |
|------|---------|------|
| Deployment（blue）| `{service}-blue` | 当前活跃版本 |
| Deployment（green）| `{service}-green` | 新版本待切换 |
| Service | `{service}` | selector 指向活跃 slot |

## 状态持久化

蓝绿状态优先存储在 etcd，降级到本地文件：
```
etcd key：kp/{project}/{namespace}/bluegreen/{service}
本地文件：~/.kp/bluegreen/{project}/{namespace}/{service}-bluegreen.json
```

状态结构：
```json
{
  "active": "blue",
  "blue_tag": "v1.0.0",
  "green_tag": "v1.1.0",
  "updated_at": "2026-03-31T06:00:00Z"
}
```

## 金丝雀扩展点

KubePivot 不内置金丝雀（需要 Nginx Ingress / Istio 等额外组件），但提供标准接入点：
```yaml
# configs/components.yaml
components:
  - name: wallet-service
    strategy: canary
```

`kp deploy` 检测到 `strategy: canary` 时：
1. 打印提示，说明需要自定义实现
2. 调用 `scripts/canary-hook.sh`（如果存在）
3. 降级走 rolling 部署

用户可在 `scripts/canary-hook.sh` 里实现任意金丝雀逻辑：
```bash
#!/bin/bash
# scripts/canary-hook.sh
# 示例：使用 Nginx Ingress 实现 10% 流量切换
kubectl annotate ingress wallet-service \
  nginx.ingress.kubernetes.io/canary="true" \
  nginx.ingress.kubernetes.io/canary-weight="10"
```

## 与 kp rollback 的关系

| 场景 | 行为 |
|------|------|
| promote 后发现问题 | `kp rollback` 切回 blue selector |
| promote 前发现问题 | 直接删除 green Deployment，blue 不受影响 |
| blue/green 都异常 | `kp down` 彻底下线 |
