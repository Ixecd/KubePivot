# 多服务支持设计文档

> 版本：draft
> 适用：dev-toolkit v1.0.0
> 状态：设计中，未实现

---

## 一、设计动机

现在 dtk 每个项目只支持一个 helm release，所有组件（postgres、etcd、业务服务）都打包在一个 chart 里。这带来几个问题：

1. **无法独立回滚单个服务**：wallet-service 出问题，整个 chart 一起回滚，postgres 数据也受影响
2. **无法按依赖顺序部署**：helm 一次性创建所有资源，只能靠 initContainers 控制启动顺序，无法控制部署顺序
3. **扩展性差**：新增一个服务需要修改公共的 Chart.yaml 和 templates，容易互相干扰

**目标**：每个服务独立一个 helm release，独立 build/push/deploy/rollback，彼此互不影响。

---

## 二、components.yaml 新格式

### 2.1 字段说明

```yaml
components:
  - name: postgres
    type: statefulset          # deployment（默认）/ statefulset
    port: 5432
    image: ""                  # 空 = 使用官方镜像，跳过 build/push

  - name: etcd
    type: deployment
    port: 2379
    image: ""

  - name: wallet-service
    type: deployment
    port: 2113
    image: wallet-service      # 有 image = 需要 build/push
    depends_on:                # 依赖的服务名，必须先部署完成
      - postgres
      - etcd

  - name: admin-service
    type: deployment
    port: 8080
    image: admin-service
    depends_on:
      - wallet-service         # 依赖 wallet-service
```

### 2.2 字段规范

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | ✅ | 服务名，小写字母数字连字符 |
| `type` | string | ❌ | `deployment`（默认）/ `statefulset` |
| `port` | int | ✅ | 服务端口 |
| `image` | string | ❌ | 镜像名，空 = 跳过 build/push，使用预置镜像 |
| `depends_on` | []string | ❌ | 依赖的服务名列表，按顺序部署 |
| `replicas` | int | ❌ | 副本数，默认由 AI 规划 |
| `cpu` | string | ❌ | CPU 限制，如 `200m` |
| `memory` | string | ❌ | 内存限制，如 `256Mi` |
| `storage` | string | ❌ | 存储大小（StatefulSet），如 `1Gi` |

---

## 三、目录结构变化

### 3.1 之前（单 release）

```
deployments/web3-blitz/
├── Chart.yaml
├── values.yaml
└── templates/
    ├── deployment.yaml            # wallet-service
    ├── web3-blitz-postgres-*.yaml # postgres
    ├── web3-blitz-etcd-*.yaml     # etcd
    └── controller-*.yaml
```

### 3.2 之后（多 release）

```
deployments/web3-blitz/
├── postgres/                      # 独立 chart
│   ├── Chart.yaml
│   └── templates/
│       ├── statefulset.yaml
│       ├── service.yaml
│       └── pvc.yaml
│
├── etcd/                          # 独立 chart
│   ├── Chart.yaml
│   └── templates/
│       ├── deployment.yaml
│       └── service.yaml
│
├── wallet-service/                # 独立 chart
│   ├── Chart.yaml
│   ├── values.yaml
│   └── templates/
│       ├── deployment.yaml
│       ├── service.yaml
│       └── serviceaccount.yaml
│
└── admin-service/                 # 独立 chart
    ├── Chart.yaml
    ├── values.yaml
    └── templates/
        ├── deployment.yaml
        ├── service.yaml
        └── serviceaccount.yaml
```

### 3.3 helm release 命名规范

```
{project}-{service}

web3-blitz-postgres
web3-blitz-etcd
web3-blitz-wallet-service
web3-blitz-admin-service
```

---

## 四、依赖图与拓扑排序

### 4.1 构建依赖图

```
postgres ──┐
           ├──→ wallet-service ──→ admin-service
etcd     ──┘
```

### 4.2 拓扑排序结果（分层）

```
层级 0（无依赖，并行部署）：postgres, etcd
层级 1（依赖层级 0）：wallet-service
层级 2（依赖层级 1）：admin-service
```

### 4.3 循环依赖检测

构建 DAG 时检测循环依赖，发现循环直接报错退出：

```
错误：检测到循环依赖：service-a → service-b → service-a
```

---

## 五、部署流程

### 5.1 完整流程

```
解析 components.yaml
    ↓
构建依赖图，拓扑排序，得到分层列表
    ↓
IDLE → INITIALIZING
    ↓
for each 层级（同层并行，层间串行）：
    for each 服务：
        if image != "":
            build 镜像
            push 镜像
        helm upgrade --install {project}-{service} ./deployments/{project}/{service}/
    ↓
INITIALIZING → DEPLOYING
    ↓
for each 层级（按部署顺序）：
    for each 服务：
        kubectl rollout status（最多重试 3 次）
        ↓ 失败
        触发级联 rollback（见第六节）
    ↓ 当前层级全部成功
继续下一层级
    ↓ 所有层级成功
DEPLOYING → VALIDATING → RUNNING
```

### 5.2 同层并行部署

同一层级的服务没有依赖关系，可以并行 build/push/deploy：

```go
var wg sync.WaitGroup
for _, svc := range layer {
    wg.Add(1)
    go func(svc Component) {
        defer wg.Done()
        deployService(svc)
    }(svc)
}
wg.Wait()
```

---

## 六、失败与回滚策略

### 6.1 单服务失败处理

```
wallet-service rollout 失败
    ↓
重试（最多 3 次，间隔 10s）
    ↓ 3 次全部失败
找出 wallet-service 的所有下游（含自身）：
    [wallet-service, admin-service]
    ↓
逆序 rollback：
    helm rollback web3-blitz-admin-service
    helm rollback web3-blitz-wallet-service
    ↓ rollback 成功
打印错误，状态机 → RUNNING（部分降级）
    ↓ rollback 失败
整组逆序 rollback（所有 release）
    ↓ 还失败
打印错误，状态机 → CLEANING → dtk down
```

### 6.2 下游级联 rollback

依赖图的逆向 BFS，找出所有受影响的下游：

```
wallet-service 失败
    ↓ 逆向查找
直接下游：admin-service（depends_on 包含 wallet-service）
admin-service 的下游：无
    ↓
受影响集合：[admin-service, wallet-service]
rollback 顺序（逆向拓扑）：admin-service → wallet-service
```

### 6.3 重试策略

| 参数 | 值 |
|------|-----|
| 最大重试次数 | 3 |
| 重试间隔 | 10s |
| rollout 超时 | 120s |
| rollback 超时 | 60s |

### 6.4 首次部署失败

首次部署（namespace 不存在）失败时，直接删除整个 namespace，回到 IDLE：

```
DEPLOYING → CLEANING → IDLE
```

---

## 七、状态机设计

### 7.1 保持项目级 FSM

v1.0 保持现有项目级状态机，不做服务级 FSM：

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
```

**reason 字段**记录详细信息：

```json
{
  "state": "RUNNING",
  "reason": "部署成功：postgres etcd wallet-service，跳过 admin-service（级联回滚）",
  "version": "v0.2.0"
}
```

### 7.2 v2.0 规划（服务级 FSM，暂不实现）

```
project.state = RUNNING
  ├── service[postgres].state = RUNNING
  ├── service[etcd].state = RUNNING
  ├── service[wallet-service].state = RUNNING
  └── service[admin-service].state = ROLLING_BACK
```

---

## 八、scaffold 生成变化

### 8.1 dtk init 生成多 chart

`dtk init` 不再生成单一 chart，改为按 components.yaml 生成多个独立 chart：

```
内置基础设施（postgres/etcd）→ 生成预置 chart（固定模板）
业务服务（有 image）       → 生成业务 chart（含 values.yaml）
```

### 8.2 每个业务服务 chart 包含

```
deployments/{project}/{service}/
├── Chart.yaml
├── values.yaml          # image.repository / image.tag / replicaCount / resources
└── templates/
    ├── deployment.yaml  # 或 statefulset.yaml（根据 type）
    ├── service.yaml
    ├── serviceaccount.yaml
    └── hpa.yaml
```

### 8.3 initContainers 保留

业务服务 chart 里保留 initContainers 控制启动顺序，作为运行时保障：

```yaml
initContainers:
  - name: wait-postgres
    image: busybox:1.35
    command: ['sh', '-c', 'until nc -z {project}-postgres 5432; do sleep 2; done']
  - name: wait-etcd
    image: busybox:1.35
    command: ['sh', '-c', 'until nc -z {project}-etcd 2379; do sleep 2; done']
```

`depends_on` 控制**部署顺序**，initContainers 控制**启动顺序**，两者配合，双重保障。

---

## 九、命令变化

### 9.1 dtk deploy（无变化）

用户使用方式不变，内部按拓扑排序多次调用 helm。

### 9.3 dtk status（增强）

```bash
dtk status
```

输出新增每个服务的独立状态：

```
项目: web3-blitz  版本: v0.2.0  状态: ✅ RUNNING

服务状态:
  ✓ web3-blitz-postgres       revision=3  deployed
  ✓ web3-blitz-etcd           revision=3  deployed
  ✓ web3-blitz-wallet-service revision=5  deployed
  ✗ web3-blitz-admin-service  revision=2  failed（已回滚）
```

---

## 十、已知限制

| # | 限制 | 说明 |
|---|------|------|
| 1 | 状态机仍是项目级 | 服务级 FSM 留 v2.0 |
| 2 | 同层并行部署的失败处理 | 一个失败时，同层其他服务如何处理需细化 |
| 3 | 跨 namespace 依赖 | 暂不支持，所有服务必须在同一 namespace |
| 4 | 版本对齐 | 各服务 image tag 需要手动保持一致，暂无自动对齐 |

---

## 十一、实现顺序

按以下顺序实现，每步可独立验证：

```
1. planner：解析 type/depends_on，构建依赖图，拓扑排序
2. scaffold：生成多 chart 目录结构
3. deploy：按拓扑顺序多 helm release 部署
4. rollback：级联 rollback 逻辑
5. status：展示每个 release 状态
6. e2e 验证：web3-blitz 双服务场景
```
