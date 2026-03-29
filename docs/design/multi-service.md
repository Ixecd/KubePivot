# 多服务支持设计文档

> 适用：dev-toolkit v1.0.0
> 状态：✅ 已实现，e2e 验证通过

---

## 一、设计动机

v1.0.0 之前 dtk 每个项目只支持一个 helm release，所有组件打包在一个 chart 里：

1. **无法独立回滚单个服务**：wallet-service 出问题，整个 chart 一起回滚，postgres 数据也受影响
2. **无法按依赖顺序部署**：helm 一次性创建所有资源，只能靠 initContainers 控制启动顺序
3. **扩展性差**：新增服务需要修改公共 Chart.yaml 和 templates

**v1.0.0 目标**：每个服务独立一个 helm release，独立 build/push/deploy/rollback。

---

## 二、components.yaml 格式

```yaml
components:
  - name: postgres
    type: statefulset          # deployment（默认）/ statefulset
    port: 5432
    image: ""                  # 空 = 跳过 build/push，使用预置镜像

  - name: etcd
    type: deployment
    port: 2379
    image: ""

  - name: wallet-service
    type: deployment
    port: 2113
    image: wallet-service      # 有 image = 需要 build/push
    depends_on:
      - postgres
      - etcd

  - name: admin-service
    type: deployment
    port: 8080
    image: admin-service
    depends_on:
      - wallet-service
```

### 字段规范

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | ✅ | 服务名，小写字母数字连字符 |
| `type` | string | ❌ | `deployment`（默认）/ `statefulset` |
| `port` | int | ✅ | 服务端口 |
| `image` | string | ❌ | 镜像名，空 = 跳过 build/push |
| `depends_on` | []string | ❌ | 依赖的服务名列表 |
| `replicas` | int | ❌ | 副本数，默认 1 |
| `cpu` | string | ❌ | CPU limit，如 `200m` |
| `memory` | string | ❌ | 内存 limit，如 `256Mi` |
| `storage` | string | ❌ | 存储大小（StatefulSet），如 `1Gi` |

---

## 三、目录结构

`dtk init` 生成多个独立 chart 目录：

```
deployments/{name}/
├── {name}-postgres/           # StatefulSet 独立 chart
│   ├── Chart.yaml
│   ├── values.yaml
│   └── templates/
│       ├── statefulset.yaml
│       └── service.yaml
│
├── {name}-etcd/               # Deployment 独立 chart
│   ├── Chart.yaml
│   └── templates/
│       ├── deployment.yaml
│       └── service.yaml
│
├── {name}/                    # 业务服务 chart
│   ├── Chart.yaml
│   ├── values.yaml
│   └── templates/
│       ├── deployment.yaml
│       ├── service.yaml
│       ├── serviceaccount.yaml
│       └── NOTES.txt
│
└── {name}-controller/         # controller chart（默认 disabled）
    ├── Chart.yaml
    ├── values.yaml
    └── templates/
        ├── deployment.yaml
        ├── rbac.yaml
        └── configmap.yaml
```

### helm release 命名规范

```
{project}-{service}

web3-blitz-postgres
web3-blitz-etcd
web3-blitz-wallet-service
web3-blitz-controller
```

---

## 四、依赖图与拓扑排序

### 依赖图示例

```
postgres ──┐
           ├──→ wallet-service ──→ admin-service
etcd     ──┘
```

### 拓扑排序（Kahn 算法）

```
层级 0（无依赖，并行）：postgres, etcd
层级 1（依赖层级 0）：wallet-service
层级 2（依赖层级 1）：admin-service
```

循环依赖在 `BuildLayers` 时检测，发现立即报错：

```
错误：检测到循环依赖，涉及服务：[service-a service-b]
```

未定义依赖也会报错：

```
错误：服务 wallet-service 依赖 nonexistent，但 nonexistent 未在 components.yaml 中定义
```

---

## 五、部署流程

```
BuildLayers → []Layer（拓扑分层）
    ↓
isMultiService 判断（多层 或 同层多个服务）
    ↓ true
deployLayers：
    for each 层级（同层 goroutine 并行，层间串行）：
        deployService：
            ① chart 存在检查（不存在快速失败，不重试）
            ② image 为空且无 chart → 跳过（CLI 工具）
            ③ build 镜像（只做一次，不随重试重复）
            ④ push 镜像（只做一次）
            ⑤ helm upgrade --install（最多重试 3 次）
            ⑥ kubectl rollout status --timeout=120s
        ↓ 失败
        触发级联 rollback
    ↓ 所有层级成功
DEPLOYING → VALIDATING → RUNNING
```

**build/push 只做一次**：失败时直接返回，不进入重试循环。helm upgrade + rollout 最多重试 3 次。

---

## 六、失败与回滚策略

### 级联 rollback

```
wallet-service 部署失败（重试 3 次）
    ↓
Downstream(layers, "wallet-service")
→ [admin-service, wallet-service]（逆拓扑顺序）
    ↓
for each affected：
    helmReleaseExists 检查（未安装的跳过，不报错）
    helm rollback {release}
    ↓ 全部成功
状态机 → RUNNING（部分降级），返回错误
    ↓ 有 rollback 失败
整组逆序 rollback（所有已成功部署的 release）
    ↓ 全部成功
状态机 → RUNNING，返回错误
    ↓ 整组也失败
状态机 → CLEANING → dtk down → IDLE
```

**关键设计**：`helmReleaseExists` 用 `helm history --max 1` 检查，防止 rollback 从未安装的 release 导致误触发 dtk down。

### 重试策略

| 参数 | 值 |
|------|-----|
| build/push | 只做一次，失败直接返回 |
| helm upgrade 最大重试 | 3 次 |
| rollout 超时 | 120s |
| 同层失败 | 整层都触发级联 rollback |

### 同层失败策略

同层任意一个服务失败 → 整层触发级联 rollback，不部署后续层级。干净利落，不留脏状态。

---

## 七、状态机

v1.0 保持项目级 FSM，不做服务级：

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
```

reason 字段记录详细信息，服务级状态留 v2.0 实现。

---

## 八、initContainers 双重保障

`depends_on` 控制**部署顺序**（dtk 层面），initContainers 控制**启动顺序**（K8s 层面），两者配合：

```yaml
initContainers:
  - name: wait-postgres
    image: busybox:1.35
    command: ['sh', '-c', 'until nc -z {name}-postgres 5432; do sleep 2; done']
  - name: wait-etcd
    image: busybox:1.35
    command: ['sh', '-c', 'until nc -z {name}-etcd 2379; do sleep 2; done']
```

即使 K8s 因某种原因先调度了业务服务，initContainers 会阻塞到依赖就绪。

---

## 九、rollback 设计说明

**不支持 `dtk rollback --service`**，强制统一版本发布回滚。

原因：
- 各服务版本之间有隐式契约（API 接口、数据库字段），单服务回滚容易造成版本割裂
- 整组回滚保证版本一致性，配合 `dtk release --version` 统一发版
- 简化实现，复用现有拓扑逆序逻辑

热修复场景：修复代码 → `dtk release --version v0.2.1 --deploy`，其他服务镜像 tag 不变跳过 build/push，只有修复的服务重新部署。

---

## 十、已知限制

| # | 限制 | 状态 |
|---|------|------|
| 1 | 状态机仍是项目级 | v2.0 实现服务级 FSM |
| 2 | dtk status 未展示每个 release 状态 | v1.1.0 |
| 3 | 跨 namespace 依赖不支持 | 暂无计划 |
| 4 | 老项目迁移到多 chart 需手动操作 | 见 gotchas.md |

---

## 十一、e2e 验证（web3-blitz）

```
web3-blitz-wallet-service   revision=3   STATUS: deployed
  wallet-service-875d6d7c7-jblpp   1/1   Running  ✅
  wallet-service-875d6d7c7-lj4rg   1/1   Running  ✅

日志验证：
  数据库迁移完成 ✅
  数据库已连接   ✅
  etcd 已连接    ✅
  API 服务已启动 ✅
```
