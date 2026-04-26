# KubePivot v2.6 流量层设计草案

> 编写日期：2026-04-26
> 状态：📐 设计草案（14 个设计 Q 已拍板，待实施）
> 适用版本：v2.6.0 起
> 工作量预估：2 周（约 1100 行代码 + 800 行文档）

---

## 摘要

v2.6 给 KubePivot 引入"流量层"——把流量入口、路由规则、蓝绿切换
纳入 GitOps 真相。

**核心论断**：v2.6 不是流量本身的实现（那是 Ingress / Service Mesh 的事）。
v2.6 是 **"GitOps 视角下的流量配置 + 自愈协同"**——把流量配置纳入 Git，
把流量切换纳入 Sandbox 状态机。

---

## 一、设计原则（贯穿 v2.6）

```
1. 不重新发明轮子
   不做 Service Mesh / Ingress Controller / APM
   K8s Ingress / Gateway API / Prometheus 已是行业标准
   
2. Provider 抽象（v2.6 关键设计）
   TrafficProvider 接口
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

## 二、TrafficProvider 接口（v2.6 关键设计）

### 2.1 接口定义

```go
// internal/traffic/provider.go
package traffic

import "context"

// TrafficProvider 抽象不同流量层后端的统一接口
//
// 实现：
//   - IngressProvider          标准 K8s Ingress
//   - GatewayAPIProvider       Gateway API (v1)
//   - 未来可加：LinkerdProvider / IstioProvider 等
type TrafficProvider interface {
    // Name 标识 provider
    Name() string

    // Validate 检查 provider 在当前集群可用
    // 例：GatewayAPIProvider 检查 GatewayClass CRD 是否存在
    Validate(ctx context.Context) error

    // GetCurrentRoutes 获取当前路由（用于 drift 检测）
    GetCurrentRoutes(ctx context.Context, ns, name string) ([]Route, error)

    // ApplyRoutes 原子地应用路由规则
    // COMMITTING 阶段切换流量时调用
    // 失败时返回 error，由 Sandbox 状态机处理 RESTORING
    ApplyRoutes(ctx context.Context, ns, name string, routes []Route) error

    // SetWeight 调整 service 的流量权重（蓝绿场景就是 0 或 100）
    SetWeight(ctx context.Context, ns, name, service string, weight int32) error
}

// Route 流量层无关的路由规则抽象
type Route struct {
    Service string  // 目标 service 名（如 wallet-service-blue）
    Weight  int32   // 0-100，蓝绿就是 0 或 100
    Match   *Match  // 匹配条件（可选）
}

type Match struct {
    Path     string
    PathType string // Exact / Prefix
    Headers  map[string]string
}
```

### 2.2 Provider 实现

| Provider | 文件 | 行数 | 后端资源 |
|----------|------|------|---------|
| `IngressProvider` | `internal/traffic/ingress_provider.go` | ~150 | `networking.k8s.io/v1` Ingress |
| `GatewayAPIProvider` | `internal/traffic/gateway_provider.go` | ~200 | `gateway.networking.k8s.io/v1` HTTPRoute |
| 自动检测 | `internal/traffic/auto_detect.go` | ~80 | 探测集群有哪些 CRD |

### 2.3 自动检测逻辑

```go
// auto_detect.go
func AutoDetect(ctx context.Context) (TrafficProvider, error) {
    // 优先 Gateway API（未来标准）
    if hasGatewayAPI(ctx) {
        return NewGatewayAPIProvider(), nil
    }
    
    // fallback 到 Ingress（最普及）
    if hasIngressCRD(ctx) {
        return NewIngressProvider(), nil
    }
    
    return nil, ErrNoTrafficProvider
}

func hasGatewayAPI(ctx context.Context) bool {
    // kubectl get crd gatewayclasses.gateway.networking.k8s.io
    // 返回 true / false
}
```

### 2.4 显式覆盖（resources.yaml 写法）

```yaml
# 默认：自动检测
traffic:
  routes:
    - service: wallet-service-blue
      weight: 100

# 显式：通过 kind 字段
traffic:
  kind: Ingress           # 或 Gateway
  ref:
    name: wallet-ingress
  routes:
    - service: wallet-service-blue
      weight: 100
```

---

## 三、resources.yaml 完整 schema（v2.6 扩展）

```yaml
# v2.6 新增 traffic + deployment 字段
resources:
  - kind: Deployment
    name: wallet-service-blue
    on-missing: auto-heal
  
  - kind: Deployment
    name: wallet-service-green
    on-missing: auto-heal

  # v2.6 新增
  - kind: Service
    name: wallet-service-blue
    on-missing: alert
    
  - kind: Service
    name: wallet-service-green
    on-missing: alert

# v2.6 新增字段
traffic:
  kind: Ingress           # 或 Gateway，省略则自动检测（C）
  ref:
    name: wallet-ingress
  
  routes:
    - service: wallet-service-blue
      weight: 100
    - service: wallet-service-green
      weight: 0

# v2.6 新增字段
deployment:
  strategy: blue-green      # v2.6.0 只支持 blue-green
  blue: wallet-service-blue
  green: wallet-service-green
  
  validation:
    pod_ready_timeout: 60s   # Q12: pod ready 判定的等待时间
```

---

## 四、蓝绿切换的 Sandbox 状态机协同

### 4.1 状态机变化（不增新状态）

```
原 v2.4.0 状态机：
  LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
                                ↓（任意失败）
                            RESTORING → IDLE

v2.6.0 蓝绿行为（COMMITTING 内部分两步顺序执行，状态机不变）：

LOCKED:
  锁定 namespace，禁止其他 deploy 介入
  
SNAPSHOTTING:
  PVC snapshot（如有），记录蓝色 Service 当前状态
  
SIMULATING:
  helm install Green Deployment + Green Service
  Green Service 创建成功，但 traffic.routes 仍指向 Blue
  Green Pod 启动 + ready
  Green Pod 在 traffic 层 weight=0（不接流量）
  
COMMITTING (v2.6.0 内部分两步):
  Step 1: TrafficProvider.ApplyRoutes(green=100, blue=0)
          原子修改 Ingress / Gateway 路由规则
          K8s API 单次 update 保证原子
  Step 2: 等 PodReadyTimeout 秒（默认 60s）
          检查 Green Pod 全部 ready
          若不 ready → 触发 RESTORING
  
RUNNING:
  Green 接收 100% 流量
  Blue Deployment 保留（用于潜在快速回滚）
  Blue Service 保留
  Drift Sync 持续监控
  
RESTORING (失败时):
  TrafficProvider.ApplyRoutes(green=0, blue=100)
  回滚流量到 Blue
  helm rollback Green Deployment（销毁 Green）
  → IDLE
```

### 4.2 关键不变量

```
✓ COMMITTING 之前：100% Blue 流量（用户无感）
✓ COMMITTING 之后：100% Green 流量（用户无感）
✓ COMMITTING 中间任意失败：流量自动回滚 + Green 销毁
✓ 整个过程用户看到的"切换瞬间"是 K8s API update 的原子时刻
```

### 4.3 代码改动（internal/state/）

```go
// 现有 state.go 的 COMMITTING 处理
func (sm *StateMachine) handleCommitting(ctx context.Context) error {
    // ... 现有 helm upgrade 逻辑 ...
    
    // v2.6 新增：流量切换
    if sm.project.Traffic != nil {
        provider, err := sm.trafficProvider()  // 自动检测或显式
        if err != nil {
            return fmt.Errorf("traffic provider 不可用: %w", err)
        }
        
        if err := provider.ApplyRoutes(ctx, sm.ns, sm.name, sm.project.Traffic.Routes); err != nil {
            return fmt.Errorf("流量切换失败: %w", err)
        }
        
        // Q12 健康度判定
        if err := sm.waitPodReady(ctx, sm.project.Deployment.Validation.PodReadyTimeout); err != nil {
            return fmt.Errorf("Green Pod 未就绪: %w", err)
        }
    }
    
    return sm.transition(StateRunning)
}
```

---

## 五、命名约定与资源所有权

### 5.1 蓝绿 Service 命名（Q9: 后缀法）

```
统一约定：<service-name>-blue / <service-name>-green
例：
  wallet-service-blue   → Deployment wallet-service-blue
  wallet-service-green  → Deployment wallet-service-green
  
外部入口 Service / Ingress 没有后缀：
  wallet-service        → 实际是 routes 决定指向 blue 还是 green
```

理由：与 web3-blitz v2.0 既有约定一致，迁移成本低。

### 5.2 资源所有权标记（Q6: 双标记）

```yaml
# v2.6 KubePivot 修改 Ingress 时加：
metadata:
  labels:
    app.kubernetes.io/managed-by: kp
    kubepivot.io/strategy: blue-green
  annotations:
    kubepivot.io/managed: "true"
    kubepivot.io/managed-fields: "spec.rules"   # 表明 KubePivot 管哪些字段
    kubepivot.io/last-traffic-switch: "2026-04-26T08:00:00Z"
```

`managed-fields` 是漂移治理的关键——KubePivot 只 reconcile 这些字段，
不动 Ingress 上其他字段（如 `spec.tls`、用户手动加的 annotation）。
体现"只保护，不越权"原则。

---

## 六、Drift 治理与流量层

```
v2.6 把流量层资源加入 Drift Sync 范围：

Hard 漂移（自动修正）：
  - traffic.routes 被手动改 → reconcile 修回
  - Ingress/Gateway 被删 → 重建
  - Service 被删 → 重建

Managed 漂移（豁免，透明展示）：
  - 用户在 Ingress 上加自定义 annotation（如 cert-manager.io/issuer）
  - Service 上的 nodePort 被 K8s 自动分配
  
Exempted 漂移（完全忽略）：
  - kube-system 注入的 finalizer
  - cloud provider 自动生成的 LoadBalancer 字段
```

---

## 七、多环境流量传播（v2.6.1）

> v2.6.1 不打 tag，作为 v2.6.0 之后的持续改进。

### 7.1 设计

```bash
# 在 staging 环境验证流量配置
kp deploy --env staging
# resources.yaml 里的 traffic 配置应用到 staging
# 蓝绿切换成功 + 验证通过

# 传播到 prod
kp deploy --env prod --from-env staging
```

### 7.2 实现

```
1. KubePivot 从 staging 环境的 ConfigMap 读取已验证的 traffic 配置
2. 应用到 prod
3. 在 prod 走完整 LOCKED → ... → RUNNING 链路
4. 不做"分批切流"——prod 自己的蓝绿在 prod 完成
```

### 7.3 与 GitOps 一致性

```
配置传播 ≠ 配置同步
传播是"主动拷贝"，发生在 kp deploy 时
拷贝后 staging 和 prod 的 traffic 配置是独立的
未来在 prod 修改 traffic 不会传回 staging
```

---

## 八、健康度判定（Q12: 仅 Pod ready）

### 8.1 v2.6.0 范围

```
COMMITTING Step 2 等待逻辑：

deadline = now + PodReadyTimeout (默认 60s)
loop:
    检查 Green Deployment 的 spec.replicas vs status.readyReplicas
    if readyReplicas == replicas:
        break (健康，进 RUNNING)
    if now > deadline:
        return error (触发 RESTORING)
    sleep 5s
```

### 8.2 v2.6.0 不做的

```
✗ 5xx error rate 监控
✗ p99 latency 监控
✗ 业务 metrics 验证
```

这些是 canary 的核心特征。v2.6 蓝绿只做"切换 + Pod ready 验证"。
metrics 接入留 v2.7+。

---

## 九、失败回滚（Q11: 自动 cleanup Green）

### 9.1 RESTORING 行为

```
触发条件：
  - COMMITTING Step 1 失败（流量切换失败）
  - COMMITTING Step 2 失败（Pod ready 超时）
  - SIMULATING 期间 Green 启动失败
  - Drift 监控发现 RUNNING 后 Green 崩溃（重启 ≥5 次）

行为：
  1. TrafficProvider.ApplyRoutes(blue=100, green=0)
     回滚流量到 Blue
  2. helm uninstall Green
     销毁 Green Deployment + Service
  3. → IDLE
  
不留 Green 资源（Q11 选 A）：
  保持系统干净
  Debug 时用户用 kp deploy --no-cleanup 临时保留
```

---

## 十、v2.6.0 工作量分解

### 10.1 代码改动（~1100 行）

| 模块 | 行数 | 描述 |
|------|------|------|
| `internal/traffic/provider.go` | 80 | TrafficProvider 接口 |
| `internal/traffic/ingress_provider.go` | 150 | Ingress 实现 |
| `internal/traffic/gateway_provider.go` | 200 | Gateway API 实现 |
| `internal/traffic/auto_detect.go` | 80 | 自动检测 |
| `internal/traffic/*_test.go` | 200 | 单测 |
| `internal/state/state.go` | +50 | COMMITTING 内分两步 |
| `internal/state/state_test.go` | +80 | 测试覆盖 |
| `internal/controller/global_state.go` | +80 | traffic reconcile |
| `cmd/kp/deploy.go` | +60 | --traffic blue-green 选项 |
| `internal/controller_installer/templates/rbac.yaml` | +20 | Ingress/Gateway 权限 |
| Schema 解析（traffic 字段） | +50 | resources.yaml |
| 配置 ConfigMap 扩展 | +30 | KUBEPIVOT_TRAFFIC_PROVIDER |
| **合计** | **~1100** | |

### 10.2 文档（~800 行）

| 文档 | 行数 | 描述 |
|------|------|------|
| `docs/design/traffic-layer.md` | ~600 | 完整设计文档（替换本草案） |
| `docs/design/state-machine.md` | +100 | 蓝绿协同章节 |
| `docs/design/architecture.md` | +50 | 流量层接入图 |
| `README.md` | +50 | v2.6 蓝绿示例 |

### 10.3 验收 demo（Q14: 独立 demo）

```
docs/example-blue-green/
├── Makefile
├── resources.yaml         蓝绿配置
├── chart/                 Helm chart（蓝/绿两个 deployment）
├── README.md              一键 demo 流程
└── scripts/
    ├── setup.sh
    └── demo-switch.sh    演示蓝绿切换
```

不依赖 web3-blitz。等 web3-blitz 升级后可以**额外**验证（不阻塞 v2.6 release）。

---

## 十一、实施分解（v2.6 三个 Step）

```
Step 1：TrafficProvider 接口 + Ingress 实现（~3 天）
  - internal/traffic/ 包基础
  - IngressProvider 完整实现
  - 单测覆盖
  - 不接 controller，仅作为独立子包

Step 2：业务路径接入（~5 天）
  - state.go COMMITTING 内分两步
  - global_state.go traffic 字段 reconcile
  - cmd/kp deploy --traffic blue-green
  - resources.yaml schema 扩展
  - 集成测试（mock 项目蓝绿切换）

Step 3：GatewayAPIProvider + demo（~2 天）
  - GatewayAPIProvider 实现
  - auto_detect 逻辑
  - docs/example-blue-green/ demo 工程
  - 文档完成
```

合计 **~10 天**（含测试 + 文档），保守估 2 周。

---

## 十二、v2.6.0 vs v2.6.1 vs v2.7 边界

```
v2.6.0 (真 tag)：
  ✓ TrafficProvider 接口
  ✓ Ingress + Gateway API 双 Provider
  ✓ 蓝绿切换 + Sandbox 协同
  ✓ Pod ready 健康判定
  ✓ Drift 治理覆盖流量层
  ✓ 独立 demo 工程

v2.6.1 (内部，不打 tag)：
  - 多环境流量配置传播（kp deploy --from-env）
  - canary 切换设计草案（不实现）
  
v2.7.0+ (远期)：
  - canary 完整实现（含 metrics 健康度判定）
  - shadow 流量镜像
  - 自研 informer（解决 watcher 框架开销）
```

---

## 十三、与既有原则的一致性

```
✓ 不引入 client-go
   IngressProvider / GatewayAPIProvider 都用 kubectl exec
   
✓ 单二进制
   internal/traffic/ 编译进 kp 主二进制
   
✓ 只保护，不越权
   managed-fields annotation 标记 KubePivot 管哪些字段
   不干预用户手动加的 cert-manager / nodePort 等
   
✓ 自愈不是越权
   RESTORING 自动回滚流量 + 销毁 Green
   不留 Green 资源减少污染
   
✓ 简单 > 完美
   仅 Pod ready 判定（不引入 metrics 监控）
   COMMITTING 内分两步（不增新状态）
```

---

## 十四、未做事项 / 待研究（v2.6 之后）

```
[ ] 多 controller 副本下流量切换的协同
   v2.5.0 分片机制下，traffic ApplyRoutes 由哪个 pod 执行？
   猜测：由持有该 ns 所属 shard 的 pod 执行
   验证：v2.6.0 实施时确认
   
[ ] Gateway API 的 BackendRefs 加权路由细节
   gateway.networking.k8s.io/v1 HTTPRoute 的精确 schema
   v2.6.0 实施时调研
   
[ ] 流量切换的事件审计
   K8s Event 记录"谁在何时切了什么流量"
   v2.6.1 加
```

---

## 相关文档

- [架构总览](architecture.md)
- [Controller 设计](controller.md)
- [分片机制](sharding.md)
- [状态机](state-machine.md) — 待 v2.6 实施时扩展蓝绿协同章节
- [GitOps 宣言](../../GITOPS-MANIFESTO.md) — "只保护，不越权"原则

---

## 编辑记录

```
2026-04-26  设计草案创建（qc + Claude）
            14 个设计 Q 全部拍板
            待 v2.6.0 实施时演化为 docs/design/traffic-layer.md
```
