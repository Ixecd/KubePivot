# dtk 整体架构设计

---

## 定位

dtk 是一个 **Go 云原生项目脚手架**，解决两个核心问题：

1. **从零搭建**：`dtk init` 生成完整可部署的骨架，含 Helm chart、认证、迁移、监控
2. **持续交付**：`dtk deploy` 一键 AI 规划 + build + push + helm upgrade + 状态追踪 + 自动自愈

目标用户是 **Go 后端开发者**，不要求熟悉 K8s 运维细节。

---

## 整体架构

```
开发者本机                              K8s 集群
──────────────────────────              ──────────────────────────────────
dtk CLI
  ├── dtk init      → 生成项目骨架
  │
  ├── dtk deploy    → build/push
  │       ↓ helm upgrade
  │       ↓ 写状态到 etcd ──────────────→ etcd pod
  │       ↓                                   │
  │   状态机（本地/etcd）             controller pod
  │                                       ├── etcd Watch（指数退避重连）
  │                                       └── 8s 周期 Reconcile
  │                                               ↓
  ├── dtk status    ←── 读状态机              自动自愈（~10s）
  ├── dtk history   ←── 读 history
  ├── dtk diff      ←── helm history
  ├── dtk rollback  → helm rollback
  ├── dtk doctor    → 环境检查
  └── dtk down      → 清理所有资源
```

---

## 核心设计决策

### 1. 状态机是中枢

所有部署操作通过状态机协调，不允许并发部署：

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
```

**为什么**：helm upgrade 是有状态操作，并发执行会产生 pending-rollback 死锁。状态机强制串行，并提供完整操作历史。

### 2. state 包零 K8s 依赖

`internal/state` 只负责 FSM 逻辑，不知道如何查询 K8s 资源。K8s 操作全部在 `internal/controller` 和 `cmd/dtk` 里通过 kubectl CLI 实现。

**为什么**：零依赖意味着可以独立测试，不需要 K8s 集群。引入 client-go 会让状态机测试变得复杂。

### 3. kubectl CLI 而不是 client-go

所有 K8s 操作通过 `kubectl` 命令行实现，不引入 `k8s.io/client-go`。

**为什么**：
- kubectl 行为经过充分验证，是 K8s 的事实标准
- client-go 依赖庞大，增加二进制体积和编译时间
- kubectl 输出格式稳定，易于解析

### 4. Helm 管理所有资源

不用裸 kubectl apply，所有资源管理通过 Helm。

**为什么**：Helm 提供版本历史，rollback 有记录可查。`dtk rollback` 本质是 `helm rollback`，利用 Helm 的原子性保证。

### 5. 配置驱动

controller 监控的资源由 `configs/resources.yaml` 决定，新增监控资源只改配置，不改代码。

**为什么**：不同项目需要监控不同资源，硬编码会让 controller 变成特定项目专属，失去通用性。

### 6. 自包含 Helm chart

不依赖任何第三方 chart（Bitnami 等），所有组件 yaml 自己维护。

**为什么**：第三方 chart 的可用性不可控，probe、镜像版本、启动脚本都可能与用户需求不匹配。

---

## 包结构与职责

```
internal/
├── planner/      AI 规划：读 components.yaml → 估算资源 → 生成 Plan
├── scaffold/     项目生成：模板复制 + 动态文件 + helm chart 骨架
├── state/        状态机：FSM + etcd/本地持久化（零 K8s 依赖）
└── controller/   自愈控制器：资源检测 + helm rollback（依赖 kubectl/helm CLI）

cmd/dtk/          CLI 入口：各命令的参数解析和流程编排
```

**依赖方向**（严格单向）：

```
cmd/dtk → internal/planner
cmd/dtk → internal/state
cmd/dtk → internal/controller
internal/controller → internal/state（只读状态，不写）
internal/scaffold（独立）
internal/planner（独立）
internal/state（独立，零外部依赖）
```

---

## 数据流

### dtk deploy

```
project.env ──→ deployConfig
components.yaml → planner.BuildPlan → []Plan
                                          ↓
state.New(etcd/local) → Machine       buildMakeEnv
                            ↓               ↓
                     Transition        make deploy.full
                     (INIT→DEPLOY           ↓
                      →VALIDATE       helm upgrade → K8s
                      →RUNNING)             ↓
                                      etcd.Put(state)
```

### controller Reconcile

```
resources.yaml → []Resource
                      ↓
              for each resource:
                  Detector.ResourceExists(kubectl get)
                      ↓ missing + on-missing: auto-heal
                  HelmClient.History → latest revision
                      ↓
                  HelmClient.Rollback(latest-1)
                      ↓
                  sm.Transition(StateRunning)
```

---

## 测试策略

| 包 | 测试方式 | 测试数 |
|---|---|---|
| internal/planner | 纯逻辑，直接测 | 20（100% 覆盖） |
| internal/state | 纯逻辑 + localStore（TempDir） | 57 |
| internal/scaffold | 纯逻辑 + 文件操作（TempDir） | 34 |
| internal/controller | Detector/HelmClient mock 注入 | 20 |
| cmd/dtk | 集成测试 + e2e 验证 | 部分 |

etcdStore 和 validator.go 依赖外部（etcd / kubectl），不做单元测试。

---

## 路线图

| 版本 | 主题 | 状态 |
|------|------|------|
| v0.4.x | 状态机 + A2 Controller 基础 | ✅ |
| v0.5.x | 体验命令（doctor/status/history） | ✅ |
| v0.6.x | 稳定性（etcd重连/SSA/etcd迁移） | ✅ |
| v0.7.x | 边界 case（pending处理/diff/ARCH） | ✅ |
| v0.8.x | 全面单元测试 + CI | ✅ |
| v0.9.x | AI 扫描组件接入真实 LLM | 🚧 |
| v1.0.0 | 多服务支持 + 文档完善 | 🚧 |
