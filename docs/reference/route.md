# route

> KubePivot 流量层抽象。统一 Ingress / Gateway API 的 Provider 接口，驱动蓝绿部署的流量切换。
> 最后更新：2026-05-08（v3.2）

---

## 设计哲学

route 包是在 K8s 已有的 Ingress 和 Gateway API 之上做了一层统一的 Provider 抽象。

核心原则（`doc.go`）：

1. **不引入 client-go** — 所有 K8s 操作通过 `kubectl` CLI
2. **Provider 自包含** — 不依赖 controller / state 包，可独立使用
3. **路由抽象与具体后端解耦** — `Route` / `Match` 是中间表示，与 Ingress spec 或 HTTPRoute spec 无关
4. **自动检测 + 显式覆盖** — `AutoDetect` 探测集群支持的 provider，调用方可显式指定

```
┌─────────────────────────────────────────┐
│            调用方                        │
│  (sandbox.go / verified_traffic_writer)  │
├─────────────────────────────────────────┤
│          route.Route (中间表示)          │
├─────────────────────────────────────────┤
│          Provider 接口                   │
├──────────────────┬──────────────────────┤
│  IngressProvider │ GatewayAPIProvider   │   ← 可扩展: Istio / Linkerd
├──────────────────┴──────────────────────┤
│         kubectl (executor)              │
├─────────────────────────────────────────┤
│     K8s Ingress / HTTPRoute 资源        │
└─────────────────────────────────────────┘
```

---

## Provider 接口

```go
type Provider interface {
    Name() string
    Validate(ctx context.Context) error
    GetCurrentRoutes(ctx context.Context, ns, name string) ([]Route, error)
    ApplyRoutes(ctx context.Context, ns, name string, routes []Route) error
    SetWeight(ctx context.Context, ns, name, service string, weight int32) error
}
```

| 方法 | 作用 |
|---|---|
| `Name()` | 返回 `"Ingress"` 或 `"GatewayAPI"` |
| `Validate()` | 检查集群是否支持该 Provider（kubectl 探测 API resources / CRD） |
| `GetCurrentRoutes()` | 从 K8s 读取当前 Ingress 或 HTTPRoute，解析为 `[]Route` |
| `ApplyRoutes()` | 将 `[]Route` 转换为 K8s 资源并 `kubectl apply`（原子） |
| `SetWeight()` | 调整单个 service 的权重（蓝绿切换核心操作） |

### Route 中间表示

```go
type Route struct {
    Service string  // K8s Service 名
    Weight  int32   // 流量权重 [0, 100]
    Match   *Match  // 路径/header 匹配条件（可选）
}

type Match struct {
    Path     string            // 路径（如 "/api"）
    PathType string            // "Exact" 或 "Prefix"
    Headers  map[string]string // header 匹配条件
}
```

**与 resources.yaml 的双层结构**：

```
resources.yaml (用户配置)
  TrafficRoute { Service: "xxx-blue", Weight: 100 }   ← yaml 解析层
         │
         ▼ (sandbox.go runBlueGreenSwitch 转换)
  route.Route  { Service: "xxx-blue", Weight: 100 }   ← 中间表示
         │
         ▼ (Provider.ApplyRoutes)
  K8s Ingress / HTTPRoute                              ← 实际资源
```

---

## 后端对比

| | IngressProvider | GatewayAPIProvider |
|---|---|---|
| K8s 资源 | `networking.k8s.io/v1` Ingress | `gateway.networking.k8s.io/v1` HTTPRoute |
| 验证方式 | `kubectl api-resources --api-group=networking.k8s.io` | `kubectl get crd httproutes.gateway.networking.k8s.io` |
| 原生加权 | ❌ 不支持 | ✅ `backendRefs[].weight` |
| weight=0 处理 | 不出现在 spec 中（省略） | 出现在 backendRefs 中（保留） |
| 蓝绿切换 | 替换唯一 backend service | 交换 weight 值（100↔0） |
| Canary 支持 | ❌ 需 annotation hack | ✅ 原生支持渐进权重 |

### Ingress 蓝绿实现

Ingress 没有原生权重，蓝绿切换 = 替换整个 backend：

```
蓝色阶段: backend.service.name = "myapp-blue"
绿色阶段: backend.service.name = "myapp-green"
```

`ApplyRoutes` 构造完整 Ingress manifest → `kubectl apply -f -`。保留用户自定义字段（annotations / ingressClassName / tls / host），只替换 `spec.rules[].http.paths[].backend`。

### Gateway API 蓝绿实现

HTTPRoute 原生支持多 backendRef 加权：

```yaml
spec:
  rules:
  - backendRefs:
    - name: myapp-blue
      port: 80
      weight: 100       # 切换时改为 0
    - name: myapp-green
      port: 80
      weight: 0         # 切换时改为 100
```

weight=0 的 service 保留在 backendRefs 中，切回时无需重建。

---

## 自动检测

`AutoDetect(ctx)` 探测集群可用的 Provider：

```
1. 尝试 GatewayAPIProvider.Validate()
   → kubectl get crd httproutes.gateway.networking.k8s.io
   → 可用 → 返回 GatewayAPIProvider

2. 回退 IngressProvider.Validate()
   → kubectl api-resources --api-group=networking.k8s.io
   → 可用 → 返回 IngressProvider

3. 都不可用 → ErrRouteAutoDetectFailed (110004)
```

显式指定：

```go
// 不验证，直接构造
p, _ := route.NewProvider("Ingress")
p, _ := route.NewProvider("Gateway")

// 构造 + 验证集群可用性
p, _ := route.ProviderForKind(ctx, "Ingress")
p, _ := route.ProviderForKind(ctx, "")  // 等价 AutoDetect
```

注意：kind 区分大小写 — `"ingress"` 会报错，必须 `"Ingress"`。

---

## 原子性保证

`ApplyRoutes` 通过 `kubectl apply -f -`（stdin pipe）提交完整 manifest，由 K8s API Server 单次 update 保证原子性。

多步 ApplyRoutes 之间的原子性由 Sandbox 状态机保证：
- 切换失败 → state machine → RESTORING → 回滚到原有路由
- 详见 `kp sandbox` 的状态机设计

---

## 受管资源标识

Provider 在写入 Ingress / HTTPRoute 时自动注入以下标识：

| Key | Type | Value |
|---|---|---|
| `app.kubernetes.io/managed-by` | Label | `kp` |
| `kubepivot.io/strategy` | Label | `blue-green` |
| `kubepivot.io/managed` | Annotation | `true` |
| `kubepivot.io/managed-fields` | Annotation | `spec.rules` (Ingress) / `spec.rules[*].backendRefs` (HTTPRoute) |

`managed-fields` 标注声明 KubePivot 只管理路由层，不管理 TLS / host / ingressClassName 等用户字段。ApplyRoutes 保留所有用户的 annotations（除 `kubepivot.io/*` 前缀外）和 spec 字段。

---

## 集成点

### Sandbox 蓝绿切换（`cmd/kp/sandbox.go`）

```
runBlueGreenSwitch()
  │
  ├─ route.ProviderForKind(ctx, traffic.Kind)
  │
  ├─ TrafficRoute → route.Route 转换
  │   蓝绿交换: blue=100→0, green=0→100
  │
  └─ provider.ApplyRoutes(ctx, ns, name, routes)
```

### VerifiedTrafficWriter（Controller）

Controller 中 1 分钟周期扫描 RUNNING + 5min 稳态的项目 namespace：

```
processNamespaceForVerifiedTraffic()
  │
  ├─ route.ProviderForKind(ctx, traffic.Kind)
  ├─ provider.GetCurrentRoutes(ctx, ns, name)
  └─ 写入 kubepivot-verified-traffic ConfigMap
```

用于多环境流量传播链（staging → prod 蓝绿状态传播）。

### resources.yaml Traffic 配置

```yaml
traffic:
  kind: Ingress           # Ingress / Gateway / 空=AutoDetect
  strategy: blue-green    # v2.6 仅 blue-green
  refs:
    name: myapp-ingress
  routes:
    - service: myapp-blue
      weight: 100
    - service: myapp-green
      weight: 0
  validation:
    podReadyTimeoutSec: 60
```

---

## 错误码

区段 110000-110099，定义在 `internal/code/error.go`：

| 错误码 | 常量 | 说明 |
|---|---|---|
| 110000 | `ErrRouteProviderNotAvailable` | Provider 在集群中不可用 |
| 110001 | `ErrRouteResourceNotFound` | Ingress / HTTPRoute 资源不存在 |
| 110002 | `ErrRouteInvalid` | Route 校验失败（weight 超限等） |
| 110003 | `ErrRouteApplyFailed` | kubectl apply 失败 |
| 110004 | `ErrRouteAutoDetectFailed` | 集群不支持任何 Provider |

---

## 可扩展性

未来可新增 Provider 实现，无需改调用方代码：

- **Istio** — VirtualService → route.Route 转换
- **Linkerd** — 通过 ServiceProfile 或 TrafficSplit
- **自定义** — 实现 Provider 接口即可接入

只需在 `auto_detect.go` 的 `AutoDetect` 和 `NewProvider` 中注册即可。

---

## 相关文档

- `docs/cmd/controller/controller.md` — resources.yaml traffic 配置段
- `docs/reference/deployment.md` — components.yaml 蓝绿 strategy
- `docs/example-bluegreen/` — 蓝绿部署完整示例
- `docs/cmd/sandbox/` — 操作沙盒的流量切换
