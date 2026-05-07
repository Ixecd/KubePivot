# HANDOFF — KubePivot v3.2

> 编写日期：2026-05-07
> Last release: v3.2.0
> Total commits: 523
> Co-Authored-By: DeepSeek

---

## 一、项目定位

**KubePivot (kp)** 是一个面向 GitOps 场景的 Kubernetes 部署与调度工具。
v3 起从"工具→工程→智能调度系统"演进。

**核心论断**：
- v1-v2.6: 工具 → 工程（部署/蓝绿/状态机/分片/流量层）
- v2.7-v3.2: 工程 → 智能调度系统（Informer KVCache → 池化 → 迁移引擎 → CBA）
- v3.2: KVCache 性能冲刺（Delta buffer / Sharded / merge-on-read / RV CAS）
- v3.3+: 智能调度系统（Watch 接线 → Fencer → GPU 迁移 → Weight-aware HashRing）

**技术选型**：
- **不引入 client-go 运行时依赖** — 自研 Informer + KVCache
- **CAP 选型 AP（最终一致 + 可用 + 分区容错）** — 不强一致
- **CoW + delta buffer** — 写 O(1)，读零分配
- **函数变量注入式 mock** — 不搞 interface mock + DI

---

## 二、现在能做什么

### 用户视角

```bash
kp init / kp deploy / kp status / kp sandbox / kp migrate
kp scheduler status / kp scheduler reschedule  ← v3.2 新增
```

### 开发者视角（v3.2 新增）

**KVCache 性能**：
```
Put:    O(1) delta write, ~120ns      (v3.2 初版 387μs → v3.2.1 1160x)
Get:    lock-free, 0 alloc, ~25ns     (client-go: 39ns/27B/1alloc)
ListAll: delta empty → pre-built 0 alloc / delta non-empty → merge-on-read
Sharded: 16 路 namespace hash，64 并发 160ns
放大率: 1.13x (client-go 4.69x)
```

**迁移引擎**：
```
MigrationManager: Evicting → WaitingForReady → Complete/Failed/Paused
Dual-Path: Stateful 超时 Paused+Retry, Stateless 超时 Failed
Rehydrate: 重启从 Pod annotation 恢复状态
DryRun: clone map 模式，真实状态不触碰
```

**CBA**：
```
HashRing: 40 vnodes/pod 一致性哈希，Pod 增减仅 ~25% Cell 漂移
FencingConfig: FallbackChain (AnnotionWatch→Webhook→SIGTERM) + HardTimeout
MigrationTargetHint: Rescheduler evict 前写 hint → webhook 读 → 防回弹
```

---

## 三、开发约束

### 3.1 不引入 client-go

KubePivot `go.mod` 零 K8s API import。所有操作通过 `kubectl` CLI + HTTP/JSON webhook。
benchmark 分支 `feature/v3.2-client-go-ab` 临时引入 client-go v0.34 做 A/B 对比，不合并 Master。

### 3.2 commits/ 目录

每个 commit 前写草稿到 `commits/`，`git commit -F commits/v3.2-xxx.txt`。

### 3.3 设计先行

新功能：`docs/design/<feature>-draft.md` → 拍板 → 实施 → 去 -draft → -impl-notes.md。

### 3.4 函数变量注入式 mock

```go
var newInformerFunc = eventstream.NewInformer  // 测试替换
var readTokenFile = os.ReadFile                // 测试替换
var kubectl = func(...) ([]byte, error)        // 测试替换
```

### 3.5 SNAPSHOT 归档

每次 minor release 归档到 `archived/` + `snapshots/`。

---

## 四、当前工作流

```bash
make dev     → go build ./... + go test ./... -race + go install
make bench   → go test -bench=. ./internal/eventstream/  # KP KVCache
```

性能基准：
- `internal/eventstream/kv_cache_bench_test.go` — 7 项 benchmark
- `feature/v3.2-client-go-ab` — client-go A/B 基准
- `benchmark/README.md` — Real YAML 工具链 + Prune Gate

---

## 五、当前可工作状态

```
HEAD:            c4f176c
last commit:     perf(eventstream): KVCache 内存放大率 1.13x + 并发压测 + Labels 黑洞分析
total commits:   523
go module:       github.com/Ixecd/kubepivot

internal/ 包:
  eventstream / scheduler / controller / metrics / route / sharding
  executor / state / planner / scaffold / ai / logger / ...
```

---

## 六、文档地图

```
根目录:
  HANDOFF.md / TODO.md / SNAPSHOT.md / FORGET.md
  CLAUDE.md / CHANGELOG.md / ROADMAP.md

docs/design/ (v3.2 核心):
  informer-kv-cache-impl-notes.md  实施日志 v4 (十二节)
  kvcache-perf-analysis.md         性能分析 (八节，含局限)
  bench-kp-vs-client-go.md         A/B 基准报告
  bench-client-go-ab.md            benchmark 设计
  pooling-migration.md             池化+迁移设计

snapshots/  /  archived/  /  commits/  /  benchmark/
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
2026-05-07  v3.2 刷新
            归档 v3.2 版到 archived/handoff/HANDOFF-v3.2.md
            全量重写：523 commits, v3.2 KVCache 性能冲刺 + 迁移引擎 + CBA
```
