# kp 整体架构设计

> 版本：v2.0.0

---

## 定位

kp 是一个 **Go 云原生项目脚手架**，解决两个核心问题：

1. **从零搭建**：`kp init` 生成完整可部署的骨架，含多服务 Helm chart、认证、迁移、监控
2. **持续交付**：`kp deploy` 一键 OPA 检查 + 迁移兼容 + CVE 扫描 + AI 规划 + build + push + helm upgrade + 状态追踪 + 自动自愈

目标用户是 **Go 后端开发者**，不要求熟悉 K8s 运维细节。

---

## 整体架构

```
开发者本机                              K8s 集群
──────────────────────────              ──────────────────────────────────
kp CLI
  ├── kp init      → 生成项目骨架（多 chart）
  ├── kp ai-plan   → 扫描仓库 + LLM 规划 → components.yaml
  │
  ├── kp deploy    → OPA 策略检查
  │       ↓         迁移兼容性检查（oasdiff）
  │       ↓         CVE 扫描（trivy）
  │       ↓         AI 规划（可选）
  │       ↓         DAG 拓扑排序 → 逐层并行 build/push/helm upgrade
  │       ↓ 写状态到 etcd ──────────────→ etcd pod
  │       ↓                                   │
  │   状态机（本地/etcd）          controller pod（A2 Reconciliation）
  │                                   ├── Leader Election（etcd 分布式锁）
  │                                   ├── WorkQueue（三集合去重）
  │                                   ├── etcd Watch（指数退避重连）
  │                                   ├── 8s 周期 Reconcile（兜底）
  │                                   ├── Drift Sync Loop（30s 扫描）
  │                                   └── Sandbox GC Loop（5m 扫描）
  │
  ├── kp status    ← 读状态机 + helm release 信息
  ├── kp status --all-envs  ← 跨集群统一视图
  ├── kp diff      ← helm history / drift 检测 / 环境对比
  ├── kp rollback  → helm rollback（整组逆序）
  ├── kp sandbox   → LOCKED→SNAPSHOTTING→SIMULATING→COMMITTING→RUNNING
  ├── kp audit     → 统一审计日志（deploy + secret + drift）
  ├── kp policy    → OPA 策略引擎
  ├── kp chaos     → Chaos Mesh API 混沌注入
  ├── kp doctor    → 环境检查（含 drift 告警）
  └── kp plugin    → 插件市场（~/.kp/plugins/）
```

---

## 核心设计决策

### 1. 状态机是中枢

所有部署操作通过状态机协调，不允许并发部署：

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING

RUNNING → LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
```

**为什么**：helm upgrade 是有状态操作，并发执行会产生 pending-rollback 死锁。状态机强制串行，并提供完整操作历史。

COMMITTING 阶段永远禁止 force-unlock——DB 正在迁移，强制解锁会导致数据不一致。

### 2. state 包零 K8s 依赖

`internal/state` 只负责 FSM 逻辑，不知道如何查询 K8s 资源。K8s 操作全部在 `internal/controller` 和 `cmd/kp` 里通过 kubectl CLI 实现。

**为什么**：零依赖意味着可以独立测试，不需要 K8s 集群。

### 3. kubectl CLI 而不是 client-go

所有 K8s 操作通过 `kubectl` 命令行实现，不引入 `k8s.io/client-go`。

**为什么**：kubectl 行为经过充分验证；client-go 依赖庞大，增加二进制体积和编译时间。

### 4. 每个服务独立 helm release

不再是一个大 chart 管所有资源，每个服务有独立 release：`{project}-{service}`。

**为什么**：独立回滚不影响其他服务，按依赖顺序部署，出问题容易定位。

### 5. 配置驱动

- `configs/components.yaml`：服务列表、类型、依赖关系、HPA 配置，驱动 planner 和 deploy
- `configs/resources.yaml`：controller 监控的资源，含 force-sync/no-sync-fields/on-missing 策略

### 6. 只保护，不越权

kp 只对自己声明所有权的字段（image/env/ports/resources）执行 force-sync，通过 `--force-conflicts` 解决 SSA 冲突，不干预 Istio/HPA/云厂商注入的字段。

### 7. 降级不阻断

可选组件缺失不阻止核心流程：Trivy 未安装跳过扫描，OPA 未安装跳过策略检查，Prometheus 不可达跳过 error rate 监控，CSI 未安装跳过 PVC 快照，Istio/Nginx 未安装时 Preview 降级生成 README。

---

## 包结构与职责

```
internal/
├── ai/         LLM 客户端（Grok/Claude/OpenAI/豆包）+ 仓库扫描 + prompt
├── planner/    AI 规划：读 components.yaml → DAG → 拓扑排序 → Plan
│               Component: Name/Image/Type/Deps/Namespace/HPA 字段
├── scaffold/   项目生成：模板复制 + 动态文件 + 多 chart 骨架
├── state/      状态机：FSM + etcd/本地持久化（零 K8s 依赖）
│               13 个状态（含 v1.8.0 Sandbox 链）
│               validTransitions 转换表严格约束
└── controller/ A2 Reconciliation Controller
    ├── leader.go       etcd 分布式 Leader Election（TTL=15s）
    ├── workqueue.go    三集合 WorkQueue（queue/dirty/processing）
    ├── reconciler.go   Start()：启动所有 Loop
    ├── heal.go         on-missing 全策略 + OOMKilled + CrashLoopBackOff
    ├── drift_sync.go   30s 漂移扫描 + etcd 审计日志
    └── sandbox_gc.go   5m Sandbox Session 超期清理

cmd/kp/          CLI 入口（25 个子命令）
examples/
└── policies/   OPA 策略示例（no-latest-tag / require-resource-limits）
```

**依赖方向**（严格单向）：

```
cmd/kp → internal/{ai,planner,state,controller,scaffold}
internal/controller → internal/state（只读状态，不写）
internal/{scaffold,planner,ai,state}（各自独立）
```

---

## Controller 详细设计

### Leader Election

```
多副本 Controller 通过 etcd 分布式锁竞选 Leader
TTL=15s，心跳每 5s 续约
非 Leader 进入等待，Leader 宕机后其他副本自动接管（最长 15s）
```

### WorkQueue 三集合去重

```
queue     []string              待处理队列
dirty     map[string]struct{}   已知待处理（含正在处理中又来的事件）
processing map[string]struct{}  正在处理中

Add(key)：
  if inDirty → 跳过（已在队列或处理中）
  if inProcessing → 只加 dirty，不入 queue
  else → 同时加 dirty + queue

done(key)：
  delete processing[key]
  if inDirty → 重新入 queue（处理中收到的新事件）
```

### Drift Sync Loop

```
每 30s 扫描所有 force-sync=true 的资源
对比 helm chart 期望值 vs live 集群值
硬冲突 → helm upgrade --force-conflicts 强制对齐
no-sync-fields 豁免字段跳过
审计日志写入 etcd /kubepivot/<project>/<ns>/drift/<ts>
```

### Sandbox GC Loop

```
每 5m 扫描 .kp/sandbox/*.json
超过 TTL（默认 3600s）→ 清理 sandbox-id label 资源 + ForceState → IDLE
```

---

## 数据流

### kp deploy（多服务路径）

```
project.env → deployConfig
components.yaml → planner.BuildLayers → []Layer（拓扑分层）
                                            ↓
OPA 策略检查（有 opa 命令才跑）      isMultiService?
迁移兼容性检查（有 oasdiff 才跑）         ↓ yes
镜像安全扫描（有 trivy 才跑）      deployLayers：
                                     同层 goroutine + semaphore 并行
state.New(etcd/local) → Machine      层间串行
    ↓                                build/push（只做一次）
Transition(INIT→DEPLOY→VALIDATE→RUN) helm upgrade --force-conflicts
                                     kubectl rollout status
                                         ↓ 失败
                                     级联 rollback → 整组 rollback
```

### kp sandbox（原子性迁移）

```
IDLE/RUNNING → LOCKED（阻止其他 kp deploy）
LOCKED → SNAPSHOTTING（kp pvc backup，有 CSI 才执行）
SNAPSHOTTING → SIMULATING（K8s Job 预跑迁移，降级 dry-run）
SIMULATING → COMMITTING（真实迁移 + kp deploy）
COMMITTING → RUNNING（成功）
任意失败 → RESTORING（pvc restore + helm rollback）→ IDLE
```

---

## 测试策略

| 包 | 测试方式 | 测试数 |
|---|---|---|
| internal/planner | 纯逻辑，直接测 | 32 |
| internal/state | 纯逻辑 + localStore（TempDir）含 Sandbox 转换 | 62 |
| internal/scaffold | 纯逻辑 + 文件操作（TempDir） | 34 |
| internal/controller | Detector/HelmClient mock 注入，含 heal/workqueue | 35 |
| **合计** | | **163+** |

etcdStore 和 validator.go 依赖外部（etcd/kubectl），不做单元测试。
所有测试通过 `go test ./... -race`。

---

## 路线图

| 版本 | 主题 | 状态 |
|------|------|------|
| v1.0.0 | 多服务 DAG + A2 Controller + 安全合规基线 | ✅ |
| v1.4.0 | 跨版本迁移（KubePivot 改名） | ✅ |
| v1.5.x | StatefulSet + etcd 健康监控 + 蓝绿 e2e + Secret 轮转 | ✅ |
| v1.6.0 | Controller HA（Leader Election + WorkQueue）+ 可观测性 | ✅ |
| v1.7.0 | 状态漂移治理（终态强权）+ HPA + on-missing 全策略 | ✅ |
| v1.8.0 | Operation Sandbox + Header Preview + Warmup | ✅ |
| v1.9.0 | 多集群联邦 + 企业合规（audit + OPA + Vault）| ✅ |
| v2.0.0 | 插件平台 + self-update + Chaos Mesh | ✅ |
