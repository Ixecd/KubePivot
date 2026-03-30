# kp 整体架构设计

---

## 定位

kp 是一个 **Go 云原生项目脚手架**，解决两个核心问题：

1. **从零搭建**：`kp init` 生成完整可部署的骨架，含多服务 Helm chart、认证、迁移、监控
2. **持续交付**：`kp deploy` 一键 AI 规划 + build + push + helm upgrade + 状态追踪 + 自动自愈

目标用户是 **Go 后端开发者**，不要求熟悉 K8s 运维细节。

---

## 整体架构

```
开发者本机                              K8s 集群
──────────────────────────              ──────────────────────────────────
kp CLI
  ├── kp init      → 生成项目骨架（多 chart）
  │
  ├── kp ai-plan   → 扫描仓库 + LLM 规划 → components.yaml
  │
  ├── kp deploy    → 拓扑排序 → 逐层 build/push/helm upgrade
  │       ↓ 每个服务独立 helm release
  │       ↓ 写状态到 etcd ──────────────→ etcd pod
  │       ↓                                   │
  │   状态机（本地/etcd）             controller pod
  │                                       ├── etcd Watch（指数退避重连）
  │                                       └── 8s 周期 Reconcile
  │                                               ↓
  ├── kp status    ←── 读状态机              自动自愈（~10s）
  ├── kp history   ←── 读 history
  ├── kp diff      ←── helm history
  ├── kp rollback  → helm rollback（整组逆序）
  ├── kp doctor    → 环境检查
  └── kp down      → 清理所有资源
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

`internal/state` 只负责 FSM 逻辑，不知道如何查询 K8s 资源。K8s 操作全部在 `internal/controller` 和 `cmd/kp` 里通过 kubectl CLI 实现。

**为什么**：零依赖意味着可以独立测试，不需要 K8s 集群。引入 client-go 会让状态机测试变得复杂。

### 3. kubectl CLI 而不是 client-go

所有 K8s 操作通过 `kubectl` 命令行实现，不引入 `k8s.io/client-go`。

**为什么**：kubectl 行为经过充分验证，是 K8s 的事实标准；client-go 依赖庞大，增加二进制体积和编译时间。

### 4. 每个服务独立 helm release

不再是一个大 chart 管所有资源，每个服务有独立 release：`{project}-{service}`。

**为什么**：独立回滚不影响其他服务，按依赖顺序部署，出问题容易定位。

### 5. 配置驱动

- `configs/components.yaml`：描述服务列表、类型、依赖关系，驱动 planner 和 deploy
- `configs/resources.yaml`：描述 controller 监控的资源，新增资源只改配置不改代码

### 6. 自包含 Helm chart

不依赖任何第三方 chart（Bitnami 等），所有组件 yaml 自己维护。

**为什么**：第三方 chart 的可用性不可控，probe、镜像版本、启动脚本都可能与用户需求不匹配。

### 7. AI 辅助规划

`kp ai-plan` 扫描代码仓库，调用 LLM（Grok/Claude/OpenAI/豆包）自动生成 `components.yaml`，省去手动配置。

---

## 包结构与职责

```
internal/
├── ai/       LLM 客户端（Grok/Claude/OpenAI/豆包）+ 仓库扫描 + prompt
├── planner/  AI 规划：读 components.yaml → DAG → 拓扑排序 → Plan
├── scaffold/ 项目生成：模板复制 + 动态文件 + 多 chart 骨架
├── state/    状态机：FSM + etcd/本地持久化（零 K8s 依赖）
└── controller/ 自愈控制器：资源检测 + helm rollback（依赖 kubectl/helm CLI）

cmd/kp/      CLI 入口：各命令的参数解析和流程编排
```

**依赖方向**（严格单向）：

```
cmd/kp → internal/ai
cmd/kp → internal/planner
cmd/kp → internal/state
cmd/kp → internal/controller
internal/controller → internal/state（只读状态，不写）
internal/scaffold（独立）
internal/planner（独立）
internal/ai（独立）
internal/state（独立，零外部依赖）
```

---

## 数据流

### kp deploy（多服务路径）

```
project.env ──→ deployConfig
components.yaml → planner.BuildLayers → []Layer（拓扑分层）
                                            ↓
state.New(etcd/local) → Machine     isMultiService?
                            ↓               ↓ yes
                     Transition     deployLayers：
                     (INIT→DEPLOY       同层 goroutine 并行
                      →VALIDATE         层间串行
                      →RUNNING)         build/push（只做一次）
                                         helm upgrade --install
                                         kubectl rollout status
                                         ↓ 失败
                                     级联 rollback → 整组 rollback → kp down
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
| internal/planner | 纯逻辑，直接测 | 32（100% 覆盖） |
| internal/state | 纯逻辑 + localStore（TempDir） | 57 |
| internal/scaffold | 纯逻辑 + 文件操作（TempDir） | 34 |
| internal/controller | Detector/HelmClient mock 注入 | 20 |
| **合计** | | **143** |

etcdStore 和 validator.go 依赖外部（etcd / kubectl），不做单元测试。

---

## 路线图

| 版本 | 主题 | 状态 |
|------|------|------|
| v0.4.x | 状态机 + A2 Controller 基础 | ✅ |
| v0.5.x | 体验命令（doctor/status/history） | ✅ |
| v0.6.x | 稳定性（etcd重连/SSA/etcd迁移） | ✅ |
| v0.7.x | 边界 case（pending处理/diff/ARCH） | ✅ |
| v0.8.x | 全面单元测试（143个）+ CI | ✅ |
| v0.9.0 | AI 扫描组件（kp ai-plan，四个 LLM provider）+ 统一进度输出 | ✅ |
| v1.0.0 | 多服务独立 release + 拓扑排序 + 级联 rollback + 文档完善 | ✅ |
| v1.1.0 | kp status 多 release 展示 / 灰度发布 | 🚧 |
