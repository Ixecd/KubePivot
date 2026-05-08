# HANDOFF — KubePivot v3.2

> 编写日期：2026-05-08
> Last release: v3.2.0
> Total commits: 537
> Co-Authored-By: DeepSeek

---

## 一、项目定位

**KubePivot (kp)** 是一个面向 GitOps 场景的 Kubernetes 部署与调度工具。
v3 起从"工具→工程→智能调度系统"演进。

**核心论断**：
- v1-v2.6: 工具 → 工程（部署/蓝绿/状态机/分片/流量层）
- v2.7-v3.2: 工程 → 智能调度系统（Informer KVCache → 池化 → 迁移引擎 → CBA）
- v3.2: Controller 全接线 + Config 系统化 + 文档补全
- v3.3+: Watch 接线 → Fencer → GPU 3D DP → 背压机制

**技术选型**：
- **不引入 client-go 运行时依赖** — 自研 Informer + KVCache
- **CAP 选型 AP（最终一致 + 可用 + 分区容错）** — 不强一致
- **CoW + delta buffer** — 写 O(1)，读零分配
- **函数变量注入式 mock** — 不搞 interface mock + DI
- **0 外部配置框架** — 不引入 viper/cobra，YAML 用 gopkg.in/yaml.v3

---

## 二、现在能做什么

### 用户视角

```bash
kp init / kp deploy / kp status / kp sandbox
kp scheduler status / kp scheduler reschedule
kp sizing recommend --pod=xxx
kp bench full / kp bench scale
kp controller install / kp controller status
```

### 开发者视角

**KVCache 性能**：
```
Put:    O(1) delta write, ~120ns
Get:    lock-free, 0 alloc, ~25ns     (client-go: 39ns/27B/1alloc)
ListAll: delta empty → pre-built 0 alloc / delta non-empty → merge-on-read
Sharded: 16 路 namespace hash，64 并发 160ns
放大率: 1.13x (client-go 4.69x)
```

**调度器**：
```
维度 A: FFD + 0-1 背包 DP 逐节点装箱
维度 B: 2D DP (80×128) 资源推荐 + sizing.Compute
双 DP 协同: 装箱不收敛时降配重试（最多 5 次）
Rescheduler: 周期不平衡检测 + 4 级抖动降级 + Pod 迁移
```

**Controller**：
```
Pod Informer → PodCacheBridge → PodCache
Node Informer → NodeCacheBridge → NodeCache
InformerDetector: KVCache → kubectl 双路径
OOMKilled: bumpMemory 25% + patch Deployment
CrashLoop >=5: 自动 rollback
```

**迁移引擎**：
```
MigrationManager: Evicting → WaitingForReady → Complete/Failed/Paused
Dual-Path: Stateful 超时 Paused+Retry, Stateless 超时 Failed
Rehydrate: 重启从 Pod annotation 恢复状态
```

---

## 三、开发约束

### 3.1 不引入 client-go

KubePivot `go.mod` 零 K8s API import。所有操作通过 `kubectl` CLI + HTTP/JSON webhook。
benchmark 分支 `feature/v3.2-client-go-ab` 临时引入 client-go v0.34 做 A/B 对比，不合并 Master。

### 3.2 commits/ 目录

每个 commit 前写草稿到 `commits/`，`git commit -F commits/v3.2-xxx.txt`。
commit subject 严格 ASCII（commit hook 检查）。

### 3.3 设计先行

新功能：`docs/design/<feature>-draft.md` → 拍板 → 实施 → 去 -draft → -impl-notes.md。

### 3.4 函数变量注入式 mock

```go
var newInformerFunc = eventstream.NewInformer  // 测试替换
var readTokenFile = os.ReadFile                // 测试替换
```

### 3.5 文档维护

- `docs/cmd/` — CLI 命令文档（每个 kp 子命令一个）
- `docs/reference/` — 内部包参考文档（eventstream/deployment/route）
- 每次 minor release 归档到 `archived/` + `snapshots/`

---

## 四、当前工作流

```bash
make dev     → go build ./... + go test ./... -race + go install
make bench   → go test -bench=. ./internal/eventstream/  # KP KVCache
make push.multiarch IMAGES=controller → buildx 多架构推送
```

性能基准：
- `internal/eventstream/kv_cache_bench_test.go` — 7 项 benchmark
- `feature/v3.2-client-go-ab` — client-go A/B 基准
- `kp bench full` — 6 组 30+ 项 CLI benchmark

---

## 五、当前可工作状态

```
HEAD:            294ec8f
last commit:     fix(controller-build): Node Informer 接线 + buildx 构建重构
total commits:   537
go module:       github.com/Ixecd/kubepivot

直接依赖:
  prometheus/client_golang v1.20.5  (metrics)
  go.etcd.io/etcd/client/v3 v3.6.9 (state store)
  github.com/lib/pq v1.12.1         (migration check)
  gopkg.in/yaml.v3 v3.0.1           (config)

internal/ 包:
  eventstream / scheduler / sizing / controller / route
  metrics / executor / state / planner / scaffold / ai / ...
```

---

## 六、文档地图

```
根目录:
  HANDOFF.md / TODO.md / SNAPSHOT.md / FORGET.md
  CLAUDE.md / ROADMAP.md / CHANGELOG.md / DEPENDENCY_POLICY.md

docs/cmd/ (命令文档):
  controller / scheduler / sizing + 48 个子命令

docs/reference/ (参考文档):
  deployment.md    — 部署引擎 + components.yaml + 状态机
  eventstream.md   — Informer + KVCache 架构
  route.md         — 流量层 Provider 抽象
  cloud.md / golang.md

docs/design/ (设计文档):
  informer-kv-cache-impl-notes.md  实施日志 v4 (十二节)
  kvcache-perf-analysis.md         性能分析 (八节，含局限)
  bench-kp-vs-client-go.md         A/B 基准报告
  pooling-migration.md             池化+迁移设计
  gpu-sharing-carbon-kink.md       GPU 共享 + 碳感知
  cell-based-architecture.md       CBA 设计

archived/  /  snapshots/  /  commits/
```

---

## 七、联系

```
作者:    qc
GitHub:  https://github.com/Ixecd/KubePivot
许可:    MIT
```

---

## 编辑记录

```
2026-05-08  v3.2 收尾
            归档 v3.2 版到 archived/handoff/HANDOFF-v3.2.md
            537 commits, controller 全接线 + config 系统 + 文档补全
            文档地图新增 docs/cmd/ + docs/reference/ 路径
2026-05-07  v3.2 刷新 — 523 commits, KVCache 性能冲刺 + 迁移引擎 + CBA
```
