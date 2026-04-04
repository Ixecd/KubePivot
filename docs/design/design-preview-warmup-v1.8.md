# 设计文档 — Header-based Preview + Warmup（v1.8.0）

> 作者：qc（Ixecd）
> 日期：2026-04-04
> 版本：v1.8.0

---

## 一、为什么需要 Preview + Warmup

蓝绿发布的核心问题：`kp promote` 是**全量切换**，没有中间状态。

```
kp deploy → inactive slot 就绪
kp promote → 100% 流量切换
```

如果新版本有隐性问题（低频 bug、性能退化），全量切换后才发现，已经影响了所有用户。

**Preview** 解决验证问题：特定 Header 的请求先打到新 slot，开发者可以在不影响线上的情况下验证新版本。

**Warmup** 解决切换风险：按步骤（10% → 50% → 100%）线性增量权重，每步监控 error rate，超阈值自动回滚。

---

## 二、整体设计

### 前置条件

| 能力 | 需要 | 降级方案 |
|------|------|---------|
| Header 路由 | Istio 或 Nginx Ingress | 生成使用指引 README |
| 权重调整 | Istio VirtualService 或 Nginx Ingress | 打印手动操作提示 |
| Error rate 监控 | Prometheus + http_requests_total 指标 | 跳过监控，继续 warmup |

所有能力都是**可选的，降级不阻断**。没有 Istio 照样可以用 port-forward 验证新版本，只是少了 Header 路由的便利性。

---

## 三、kp deploy --preview 流程

```
kp deploy --preview
       │
       ▼ （蓝绿策略）
deployBlueGreen
       │
       ▼
部署到 inactive slot（和普通蓝绿一样）
       │
       ▼
runPreviewGen(cfg, plan, root, projectName, slot)
       │
       ├── detectTrafficLayer
       │     ├── Istio → generateIstioVirtualService
       │     ├── Nginx → generateNginxIngressCanary
       │     └── none  → generatePreviewReadme
       │
       ▼
输出到 deployments/<project>/preview/
⚠️  不自动 apply（只保护，不越权）
```

### Istio VirtualService 模板

```yaml
spec:
  http:
  # x-kp-preview: green → green slot
  - match:
    - headers:
        x-kp-preview:
          exact: "green"
    route:
    - destination:
        host: <project>-<service>-green
  # 默认 → active slot
  - route:
    - destination:
        host: <project>-<service>-blue
        weight: 100
```

### Nginx Ingress 模板

```yaml
annotations:
  nginx.ingress.kubernetes.io/canary: "true"
  nginx.ingress.kubernetes.io/canary-by-header: "x-kp-preview"
  nginx.ingress.kubernetes.io/canary-by-header-value: "green"
```

---

## 四、kp warmup 流程

```
kp warmup --service wallet-service --steps 10,50,100 --interval 2m,5m

for each step:
    patchTrafficWeight(step%)
         │
         ├── Istio → kubectl patch virtualservice weight
         └── Nginx → kubectl patch ingress canary-weight
         │
    sleep(interval)
         │
    sampleErrorRate（Prometheus PromQL）
         │
         ├── rate > threshold → patchTrafficWeight(0%) → exit 1
         └── rate OK → 继续下一步

最后一步完成 → 提示用户运行 kp promote
```

### PromQL 查询

```promql
sum(rate(http_requests_total{service="wallet-service",status=~"5.."}[2m]))
/ sum(rate(http_requests_total{service="wallet-service"}[2m]))
```

需要服务暴露标准 `http_requests_total` 指标，推荐使用 `github.com/prometheus/client_golang`。

### Prometheus 服务发现

kp 尝试两个地方找 Prometheus：
1. `monitoring` namespace，label `app=prometheus`
2. `monitoring` namespace，service name `prometheus-server`（helm chart 默认）

找不到则返回 -1，warmup 跳过 error rate 检查继续执行。

---

## 五、kp init 模板预留

`kp init` 生成的服务 chart 里包含 `virtualservice-preview.yaml`，全部注释掉，作为使用说明：

```yaml
# virtualservice-preview.yaml
# 仅在 strategy: blue-green 时使用
# kp deploy --preview 会自动生成填充版本
# 需要 Istio 已安装
#
# 手动 apply 后，用 x-kp-preview: <slot> header 将流量路由到非活跃 slot
```

---

## 六、完整使用示例

```bash
# 1. 部署新版本到 inactive slot，生成 Preview 路由模板
kp deploy --preview

# 2. 审查并 apply Preview 路由（Istio 场景）
cat deployments/web3-blitz/preview/virtualservice-wallet-service-preview.yaml
kubectl apply -f deployments/web3-blitz/preview/virtualservice-wallet-service-preview.yaml

# 3. 用 Header 验证新版本
curl -H "x-kp-preview: green" https://api.example.com/healthz
curl -H "x-kp-preview: green" https://api.example.com/api/v1/wallet/balance

# 4. 验证通过，开始 warmup（10% → 50% → 100%）
kp warmup --service wallet-service \
  --steps 10,50,100 \
  --interval 2m,5m \
  --err-threshold 0.01

# 5. warmup 完成，全量切换
kp promote --service wallet-service
```

---

## 七、"只保护，不越权" 原则的体现

- **Preview 模板不自动 apply**：生成到 `deployments/<proj>/preview/`，用户审查后手动执行
- **warmup 不自动 promote**：最后一步权重到 100% 后，还需要用户手动 `kp promote`
- **Prometheus 不可达不报错**：只跳过 error rate 检查，不阻断 warmup
- **没有 Istio/Nginx 不报错**：降级生成 README 指引

---

## 八、待验证项（TODO）

| 项 | 验证条件 | 计划 |
|----|---------|------|
| patchIstioWeight | Istio 集群 + VirtualService | v1.9.0 之前 |
| patchNginxWeight | Nginx Ingress Controller | v1.9.0 之前 |
| sampleErrorRate | Prometheus + http_requests_total | v1.9.0 之前 |
