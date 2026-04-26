# KubePivot v2.6 流量层

> 状态：✅ v2.6.0 已实施
> 实施日期：2026-04-26（commits 786b59d → ea48e5c）
> 适用版本：v2.6.0+

---

## 摘要

v2.6 给 KubePivot 引入**流量层**——把流量入口、路由规则、蓝绿切换
纳入 GitOps 真相。

**核心论断**：v2.6 不是流量本身的实现（那是 Ingress / Service Mesh 的事）。
v2.6 是 **"GitOps 视角下的流量配置 + 自愈协同"**——把流量配置纳入 Git，
把流量切换纳入 Sandbox 状态机。

**与 v2.0 蓝绿的关系**：
- v2.0 提供命令式蓝绿（`kp deploy --bluegreen` + `kp promote`），仍可用
- v2.6 提供声明式蓝绿（`resources.yaml` + `kp sandbox commit`），推荐
- v2.6 是 v2.0 蓝绿的"GitOps 化"重构，不是替代

---

## 一、设计原则

```
1. 不重新发明轮子
   不做 Service Mesh / Ingress Controller / APM
   K8s Ingress / Gateway API / Prometheus 已是行业标准

2. Provider 抽象（v2.6 关键设计）
   route.Provider 接口
   不锁死特定流量后端
   未来加 Linkerd / Istio 都是新增 Provider，不改 reconcile

3. 复用 Sandbox 状态机
   流量切换是 COMMITTING 阶段的子动作
   不引入新状态机

4. 蓝绿先于金丝雀
   v2.6.0 仅做蓝绿（确定性高）
   金丝雀的"健康度判定"是大头，留 v2.6.1 / v2.7

5. 与 v2.5.0 哲学一致
   每 pod 自治 + drop 幂等 + 简单 > 完美
```

---

## 二、Provider 接口（实施完成）

代码位置：`internal/route/provider.go`

```go
type Provider interface {
    Name() string
    Validate(ctx context.Context) error
    GetCurrentRoutes(ctx context.Context, ns, name string) ([]Route, error)
    ApplyRoutes(ctx context.Context, ns, name string, routes []Route) error
    SetWeight(ctx context.Context, ns, name, service string, weight int32) error
}

type Route struct {
    Service string
    Weight  int32
    Match   *Match
}
```

### 2.1 已实现 Provider

| Provider | 文件 | 后端资源 | 状态 |
|----------|------|---------|------|
| `IngressProvider` | `internal/route/ingress_provider.go` | `networking.k8s.io/v1` Ingress | ✅ v2.6.0 |
| `GatewayAPIProvider` | `internal/route/gateway_provider.go` | `gateway.networking.k8s.io/v1` HTTPRoute | ✅ v2.6.0 |

### 2.2 自动检测

代码位置：`internal/route/auto_detect.go`

```go
func AutoDetect(ctx context.Context) (Provider, error) {
    // 优先 Gateway API（K8s 流量层未来标准）
    if gw := NewGatewayAPIProvider(); gw.Validate(ctx) == nil {
        return gw, nil
    }
    // fallback Ingress（最普及）
    if ing := NewIngressProvider(); ing.Validate(ctx) == nil {
        return ing, nil
    }
    return nil, ErrAutoDetectFailed
}

func ProviderForKind(ctx context.Context, kind string) (Provider, error)
// 未指定 kind → AutoDetect
// 指定 kind   → NewProvider(kind) + Validate
```

### 2.3 错误码（internal/code/）

```
ErrRouteProviderNotAvailable  503  Route provider is not available
ErrRouteResourceNotFound      404  Traffic resource not found
ErrRouteInvalid               400  Route rule is invalid
ErrRouteApplyFailed           500  Apply route rules failed
ErrRouteAutoDetectFailed      500  Auto detect route provider failed
```

编号区段 `110000-110099` 留给 `internal/route` 包。

---

## 三、resources.yaml schema（v2.6 扩展）

```yaml
resources:
  - kind: Deployment
    name: wallet-service-blue
    on-missing: auto-heal
  - kind: Deployment
    name: wallet-service-green
    on-missing: auto-heal
  - kind: Service
    name: wallet-service-blue
    on-missing: alert
  - kind: Service
    name: wallet-service-green
    on-missing: alert
  - kind: Ingress
    name: wallet-ingress
    on-missing: auto-heal

# v2.6.0 流量层配置
traffic:
  kind: Ingress              # 可选：Ingress / Gateway / 不写=自动检测
  strategy: blue-green       # v2.6.0 仅 blue-green
  refs:
    name: wallet-ingress     # Ingress 或 HTTPRoute 资源名
  routes:
    - service: wallet-service-blue
      weight: 100
    - service: wallet-service-green
      weight: 0
  validation:
    podReadyTimeoutSec: 60
```

代码位置：`internal/controller/resources.go` 的 `Traffic` / `TrafficRefs` /
`TrafficRoute` / `TrafficValidation` 4 个 struct。

---

## 四、与 Sandbox 状态机的协同

### 4.1 状态机不变

```
v2.4.0 状态机原状（保留）：
  LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
                                ↓（任意失败）
                            RESTORING → IDLE
```

v2.6 不引入新状态——**COMMITTING 内部分两步顺序执行**。

### 4.2 COMMITTING 内分两步（实施在 cmd/kp/sandbox.go）

```
COMMITTING:
  Step 1: runSandboxCommit (v1.8.0 既有)
          - 真实迁移（kp migrate run）
          - helm upgrade（fork kp deploy 子进程）
          失败 → RESTORING

  Step 2: runBlueGreenSwitch (v2.6 新增)
          - 从 configs/resources.yaml 读 Traffic 字段
          - 不启用蓝绿则静默跳过
          - route.ProviderForKind() 构造 provider
          - provider.ApplyRoutes() 切换流量（K8s API 单次 update 原子）
          - waitDeploymentReady() 等 Pod ready
          失败 → RESTORING
```

### 4.3 关键不变量

```
✓ COMMITTING 之前：100% Blue 流量（用户无感）
✓ COMMITTING 之后：100% Green 流量（用户无感）
✓ COMMITTING 中间任意失败：流量自动回滚 + Green 销毁
✓ 整个过程"切换瞬间"是 K8s API update 的原子时刻
```

---

## 五、命名约定与资源所有权

### 5.1 蓝绿 Service 命名（后缀法）

```
统一约定：<service-name>-blue / <service-name>-green
例：
  wallet-service-blue   → Deployment wallet-service-blue
  wallet-service-green  → Deployment wallet-service-green
```

理由：与 web3-blitz v2.0 既有约定一致，迁移成本低。

### 5.2 资源所有权标记（双标记）

KubePivot 修改 Ingress 时加：

```yaml
metadata:
  labels:
    app.kubernetes.io/managed-by: kp
    kubepivot.io/strategy: blue-green
  annotations:
    kubepivot.io/managed: "true"
    kubepivot.io/managed-fields: "spec.rules"
```

`managed-fields` 是漂移治理的关键——KubePivot 只 reconcile 这些字段，
不动 Ingress 上其他字段（如 `spec.tls`、用户手动加的 annotation）。
体现"只保护，不越权"原则。

代码位置：`internal/route/ingress_provider.go` 的 `buildIngressFromRoutes()` 方法。

---

## 六、健康度判定

### 6.1 v2.6.0 范围（Pod ready）

```
COMMITTING Step 2 等待逻辑（cmd/kp/sandbox.go waitDeploymentReady）：

deadline = now + PodReadyTimeoutSec (默认 60s)
loop:
    检查 Green Deployment 的 spec.replicas vs status.readyReplicas
    if readyReplicas == replicas:
        break (健康，进 RUNNING)
    if now > deadline:
        return error (触发 RESTORING)
```

实现：`kubectl rollout status deployment/X -n NS --timeout=Ts`。

### 6.2 v2.6.0 不做的

```
✗ 5xx error rate 监控
✗ p99 latency 监控
✗ 业务 metrics 验证
```

这些是 canary 的核心特征。v2.6 蓝绿只做"切换 + Pod ready 验证"。
metrics 接入留 v2.7+。

---

## 七、失败回滚

### 7.1 RESTORING 行为

```
触发条件：
  - COMMITTING Step 2.a 失败（流量切换失败）
  - COMMITTING Step 2.b 失败（Pod ready 超时）
  - SIMULATING 期间 Green 启动失败
  - Drift 监控发现 RUNNING 后 Green 崩溃（v2.6.1 候选）

行为（cmd/kp/sandbox.go runSandboxRestore，v1.8.0 既有）：
  1. helm rollback（销毁 Green）
  2. PVC snapshot restore（如有）
  3. → IDLE

v2.6 复用 v1.8.0 既有的 RESTORING 逻辑，不引入新机制。
```

---

## 八、测试覆盖

### 8.1 单测（27 个 sub-cases）

代码位置：`internal/route/*_test.go`

```
Route.Validate            8 cases（边界/越界/PathType）
ValidateRoutes            6 cases（蓝绿/canary/超总和）
updateWeight              3 cases
Error 链路                2 cases（含 errors.Is/Unwrap）
parseIngressRoutes        3 cases（含去重）
buildIngressFromRoutes_*  2 cases（含 PreserveUserFields）
parseHTTPRouteRoutes      4 cases（含 weight nil 默认 100）
buildHTTPRouteFromRoutes_* 2 cases（含 PreserveUserFields）
NewProvider               6 cases（含大小写敏感、Foo 错误码）

合计 27 个全 PASS，~530 行测试代码
```

### 8.2 集成测试（demo 工程）

代码位置：`docs/example-blue-green/`

```
make setup     部署初始 blue
make verify    检查 Ingress backend 指向
make switch    切换到 green
make cleanup   清理

不依赖 web3-blitz（其升级是 v2.8 计划任务）
任何用户可以在干净 K8s 集群 < 2 分钟完整 demo
```

---

## 九、v2.6 路线图

### 9.1 v2.6.0（已 release）

```
✅ Provider 接口 + Ingress + Gateway API 双实现
✅ Sandbox COMMITTING 内分两步执行
✅ Pod ready 健康判定
✅ 自动失败 RESTORING
✅ 资源所有权标记（双标记）
✅ Drift 治理覆盖流量层（managed-fields annotation）
✅ 完整 demo 工程（example-blue-green）
```

### 9.2 v2.6.1（内部，不打 tag）

```
[ ] 多环境流量配置传播
    kp deploy --env prod --from-env staging
    从 staging 读"已验证的 traffic 配置"应用到 prod
    
[ ] tools/codegen 支持多 const block
    实测发现：v2.6.0 添加 ErrRoute* 错误码时
    codegen 工具不识别"分两个 const block 的同类型常量"
    手工补的 case_generated 是 ad-hoc 方案
    修法：codegen.go genDecl 函数 ~30 行改造
    
[ ] flaky test 调研（如有）
```

### 9.3 v2.7+（远期）

```
[ ] canary 完整实现
    含 metrics 健康度判定（5xx rate / p99 latency）
    
[ ] shadow 流量镜像
    流量复制 + 不影响主线
    
[ ] 自研 informer
    解决 watcher 框架开销
```

---

## 十、与既有原则的一致性核对

```
✓ 不引入 client-go
   IngressProvider / GatewayAPIProvider 都用 executor.GetExecutor() → kubectl
   
✓ 单二进制
   internal/route/ 编译进 kp 主二进制
   
✓ 只保护，不越权
   managed-fields annotation 标记 KubePivot 管哪些字段
   不干预用户手动加的 cert-manager / nodePort / ingressClassName 等
   
✓ 自愈不是越权
   RESTORING 自动回滚流量 + 销毁 Green
   不留 debug 资源减少污染
   
✓ 简单 > 完美
   仅 Pod ready 判定（不引入 metrics 监控）
   COMMITTING 内分两步（不增新状态）
   后缀法命名（不造新约定）
```

---

## 相关文档

- [架构总览](architecture.md) — 第 9 条核心设计决策"流量层抽象"
- [状态机](state-machine.md) — "v2.6 流量层与状态机协同" 章节
- [示例工程](../example-blue-green/README.md) — 完整 demo 教程
- [GitOps 宣言](../../GITOPS-MANIFESTO.md) — "只保护，不越权"原则

---

## 编辑记录

```
2026-04-26  设计草案创建（traffic-layer-draft.md）
            14 个设计 Q 全部拍板
            
2026-04-26  v2.6.0 实施完成（commits 786b59d → ea48e5c）
            草案 → 正式文档（去 -draft 后缀）
            内容大改：从"待实施"到"已实施"
```
