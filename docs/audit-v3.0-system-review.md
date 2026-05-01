# KubePivot v3.0 全系统审计报告

> 审计日期：2026-05-01
> 审计范围：cmd/kp/ (59 文件) + internal/ (22 包)
> 代码规模：~54,456 行 Go 代码，478 commits，3 个 GA tag (v2.8.0 / v2.9.0 / v3.0.0)
> 审计目标：v3.1 GPU 调度前，对现有代码质量做一次全面体检

---

## 执行摘要

KubePivot v3.0 是一个工程纪律优秀的项目。Master 分支没有已知的运行时崩溃或数据损坏风险。发现的问题集中在三个方面：

1. **RBAC 覆盖不完整**（7 个写操作命令缺少权限检查）
2. **代码重复**（3 处函数逻辑在 cmd 层和内层各有一份实现）
3. **硬编码与配置缺失**（并发限制、默认值、TODO 占位符）

以下是完整发现，按严重度排列。

---

## 严重度定义

| 级别 | 含义 |
|---|---|
| **CRITICAL** | 运行时损坏、数据丢失、或安全漏洞 |
| **HIGH** | 功能缺陷、生产环境可触发 |
| **MED** | 代码异味、技术债务，暂不触发但需修 |
| **LOW** | 风格不一致、文档过时、可推迟 |

---

## CRITICAL (0 项)

经过全面审计，未发现 CRITICAL 级别问题。Master 分支工程纪律保持良好。

---

## HIGH (5 项)

### H1. RBAC 权限检查覆盖不完整

**位置**：cmd/kp/ 多个文件

**现状**：v2.8 引入的 `mustCheck()` 仅在 4 个命令入口接入：

| 命令 | 权限 | 状态 |
|---|---|---|
| `kp deploy` | `PermDeploy` | ✓ |
| `kp resume` | `PermDeploy` | ✓ |
| `kp rollback` | `PermRollback` | ✓ |
| `kp down` | `PermRollback` | ✓ |
| `kp sandbox` | `PermSandbox` | ✓ |
| `kp controller install` | `PermControllerInstall` | ✓ |
| `kp controller uninstall` | `PermControllerUninstall` | ✓ |

**以下写操作命令缺少 mustCheck**：

| 命令 | 风险 | 建议权限 |
|---|---|---|
| `kp migrate run` | 直接执行 DB 迁移 | `PermMigrate` |
| `kp pvc backup/restore` | 操作 PVC 快照 | `PermPVC` |
| `kp secret rotate/seal/sync` | 操作 Secret | `PermSecret` |
| `kp chaos inject/stop` | 注入故障 | `PermChaos` |
| `kp promote` | 切换蓝绿流量 | `PermPromote` |
| `kp supply-chain verify` | 验证签名（CI/CD 阻断） | `PermSupplyChain` |
| `kp sizing recommend` | 读取 Prometheus 数据 | `PermSizing` |

**说明**：读操作命令（`status`, `doctor`, `history`, `diff`, `scan`, `audit`）可以不加 RBAC，符合"读开放、写管控"原则。但以上列出的命令都有副作用，应在 v3.1 之前补上。

**建议**：
- 在 `internal/rbac/types.go` 新增 7 个 `Permission` 常量
- 在各命令入口一行接入 `mustCheck(audit.ResolveActor(), ns, rbac.PermXXX)`
- 工作量：~30 分钟

---

### H2. writeToFile 是 TODO 占位符，SBOM 文件输出失效

**位置**：`cmd/kp/supplychain.go:347`

```go
func writeToFile(path, content string) error {
    return fmt.Errorf("TODO: implement writeToFile with project file utils")
}
```

**影响**：`kp supply-chain sbom --output sbom.json` 会失败。用户无法将 SBOM 输出到文件（stdout 仍正常）。

**建议**：
- 用 `os.WriteFile` 作为最小实现（对齐 sizing.go 的写法）
- 或复用 `internal/scaffold` 的文件写入工具
- 工作量：~5 分钟

---

### H3. parseMemoryResource 重复实现 Quantity 解析器

**位置**：`cmd/kp/sizing.go:146`

KubePivot 在 `internal/metrics/` 已有自实现的 Quantity 解析器（v2.7.0 引入，SI vs 二进制、CPU milli-cores / Memory bytes）。`parseMemoryResource` 在 cmd 层又写了一遍，且仅支持 `Gi/Mi/Ki` 三种后缀，不支援 `G/M/K`、`m`（milli）、浮点数。

**影响**：两个 Quantity 解析器的行为可能不一致。`kp sizing recommend` 输入的 memory 值经过一个解析器，实际 deploy 流程经过另一个。

**建议**：
- 把 `internal/metrics` 的 Quantity 解析器导出为公开 API
- 删除 cmd 层的重复实现
- 工作量：~15 分钟

---

### H4. getWeights 函数在 cmd 层和 internal 层各有一份实现

**位置**：
- `cmd/kp/deploy_sizing.go:520` → `getWeights()`
- `internal/sizing/dp.go` → `weights()` (私有)

注释自己承认了："逻辑必须与 internal/sizing/dp.go 的 weights 函数完全同步。Level5 重构: 统一导出或提取公共包"。

**影响**：两份代码可能 drift。当前值一致（`ProfileWeb=0.7/0.3, ProfileBatch/DB=0.3/0.7, default=0.5/0.5`），但未来修改权重时容易漏改。

**建议**：
- 将 `weights()` 导出为 `WeightsForProfile()` 或在 `internal/sizing/` 建 `config.go` 集中管理
- 删除 cmd 层的 `getWeights`
- 工作量：~10 分钟

---

### H5. 部署 sizing hook 中的硬编码默认值与 flag 默认值不一致

**位置**：`cmd/kp/deploy_sizing.go:69-83` + `cmd/kp/deploy.go:71-73`

```go
// flag 默认值（deploy.go）
flags.DurationVar(&cfg.prometheusWindow, "prometheus-window", 0, ...)
flags.DurationVar(&cfg.prometheusStep, "prometheus-step", 0, ...)
flags.Float64Var(&cfg.sizingThreshold, "sizing-threshold", 0, ...)

// 但 runSizingHook 里的回退值（deploy_sizing.go）
defaultWindow := 7 * 24 * time.Hour
defaultStep := 15 * time.Minute
defaultThreshold := 0.7
```

**问题**：flag 默认值设为 0，真实默认值硬编码在函数体内。`--help` 输出会显示 default=0，而非用户实际得到的 `7d` / `15m` / `0.7`。

**建议**：把默认值写进 flag 定义：
```go
flags.DurationVar(&cfg.prometheusWindow, "prometheus-window", 7*24*time.Hour, ...)
```
工作量：~5 分钟

---

## MED (6 项)

### M1. 并发限流硬编码为 10

**位置**：`cmd/kp/deploy_sizing.go:58`

```go
sem = make(chan struct{}, 10) // 信号量限流
```

**影响**：大规模集群下（P=100），10 并发可能成为瓶颈。但这个值合理——打在 Prometheus 上的 QPS 不会太高。

**建议**：加 `--sizing-concurrency` flag，默认 10。工作量：~10 分钟。

---

### M2. YAML 注释丢失风险

**位置**：`cmd/kp/deploy_sizing.go:378-405` (简单路径) vs `cmd/kp/deploy.go:845-887` (`updateComponentsSizing`)

`updateComponentsSizingBatch` 有两条路径：
- **简单路径**：`yaml.Unmarshal` → 改值 → `yaml.Marshal`，会丢失原始 YAML 注释
- **注释注入路径**：用 `yaml.Node` API 保留注释，但对非标准 YAML 结构返回 error

`updateComponentsSizing`（老函数，仍存在）也只有简单路径，必然丢注释。

**建议**：
- 删除 `updateComponentsSizing`，统一用 `updateComponentsSizingBatch`
- 注释路径兜底：找不到 components sequence 时 fallback 到简单路径 + warn 日志
- 工作量：~30 分钟

---

### M3. osExitFunc 使用不一致

**位置**：`cmd/kp/supplychain.go:17`

```go
var osExitFunc = os.Exit  // 声明在 supplychain.go
```

**实际使用**：
- `deploy_sizing.go` 使用 `osExitFunc(1)` ✓
- `sizing.go` 使用 `osExitFunc(1/2)` ✓
- `supplychain.go` 使用 `osExitFunc(1/2)` ✓
- **其余 56 个文件全部使用 `os.Exit(1)`** ✗

**影响**：`osExitFunc` 的设计意图是测试友好（可 mock），但只在 3 个文件中使用。其余文件无法在测试中拦截退出。

**建议**：
- 要么全量替换为 `osExitFunc`（工作量大，不推荐）
- 要么接受现状，在测试中用 `TestMain` + subprocess 模式测退出码
- 推荐后者。工作量：0（现状可接受）

---

### M4. samplePodMetrics 实现被注释掉

**位置**：`cmd/kp/sizing.go:114-141`

整个 `samplePodMetrics` 函数被注释掉了，但 `runSizingRecommend` 在第 70 行调用了它。说明这个函数实际上在 `deploy_sizing.go:327` 有另一份可用实现（被 sizing.go import 使用）。

**问题**：
- 注释掉的代码容易让后来者困惑"怎么有两个实现"
- 注释里有 `// 注意: 实际应提取到 internal/sizing/sample.go`

**建议**：提取到 `internal/sizing/sample.go` 作为公开 API，两处 import 共用。删除 cmd 层的两份实现。工作量：~20 分钟。

---

### M5. Hardcoded make targets in deploy.go

**位置**：`cmd/kp/deploy.go:484-534`

```go
runCmd(root, makeEnv, "make", "deploy.build")
runCmd(root, makeEnv, "make", "deploy.push")
runCmd(root, makeEnv, "make", "deploy.install")
runCmd(root, makeEnv, "make", "deploy.run.all")
```

**影响**：单服务 deployment 路径强依赖 Makefile 中有这 4 个 target。多服务路径（`deployLayers`）用 helm 直接操作，不走 make。两种路径的行为模型不同。

**说明**：这是有意设计——单服务走"构建 + 推送 + 部署"全链路，多服务走"helm-only"快速路径。但 make 依赖是隐式的，如果 Makefile 被删除或改名，`kp deploy`（单服务模式）会静默失败。

**建议**：在 `checkDeps` 或 `ensurePrerequisites` 中检测 Makefile 存在性。工作量：~10 分钟。

---

### M6. Hardcoded "default" namespace in multiple commands

**位置**：
- `chaos.go:68,126`
- `deploy.go:68`
- `pvc.go:302`
- `secret.go:85`
- `sizing.go:40-41`

多处硬编码 `"default"` 作为 namespace 默认值。这些应该从 `project.env` 或 `--namespace` flag 解析。

**影响**：用户在非 default namespace 操作时，如果忘记传 `--namespace`，命令会在错误 ns 操作。不是 bug（每个命令都有 `--namespace` flag），但默认值 `"default"` 在显式声明 namespace 的生产环境中是 misleading 的。

**建议**：统一为从 env 解析的 fallback，而非硬编码 "default"。工作量：~15 分钟。

---

## LOW (5 项)

### L1. CLAUDE.md / HANDOFF.md / TODO.md 严重过时

这三个核心文档停留在 v2.7.0 视角：
- CLAUDE.md 提到 "~16000 行代码" → 实际 ~54000 行
- HANDOFF.md 提到 "total commits: 431" → 实际 478
- TODO.md 的 v2.8/v2.9/v3.0 都在 ✅ 已完成列表 → 实际已完成
- 未记录 v2.8 (SSO/RBAC/加密/签名)、v2.9 (Sizing Engine)、v3.0 (Scheduler/乾枢)

**建议**：v3.1 开始前更新，工作量：~1 小时。

---

### L2. supplychain.go 中 `P.Start`/`P.Fail`/`P.Done` 依赖未验证

`runSupplyChainSbom` 的 push 块（第 276-310 行）调用了 `P.Start`, `P.Done`, `P.Fail`, `P.Info`。这些来自 `progress.go` 的进度显示工具。push 功能本身是可选特性（`--push` flag），但如果用户触发且 `P` 的实现有变，会 panic。

**建议**：确认 `P` 在 supplychain 调用链中已初始化。工作量：~5 分钟。

---

### L3. `updateComponentsSizing` 已废弃但仍保留

**位置**：`cmd/kp/deploy.go:845-887`

`updateComponentsSizingBatch` 是升级版（支持原子写入 + 注释注入），但老函数 `updateComponentsSizing` 仍存在。搜索引用确认它已无调用方。

**建议**：删除，或加 `// Deprecated:` 注释。工作量：~5 分钟。

---

### L4. main.go 内嵌的命令无独立文件

以下命令在 `main.go` 中直接定义函数体，无独立 `.go` 文件：

| 命令 | 位置 | 行数估算 |
|---|---|---|
| `kp init` | main.go:130-170 | ~40 行 |
| `kp rollback` | deploy.go:276-385 | ~110 行（已有文件） |
| `kp supply-chain` | supplychain.go | （已有文件） |
| `kp context` | env.go? | 需确认 |
| `kp ai-plan` | ai_plan.go | （已有文件） |

实际检查后，`init`/`rollback` 已经在文件中了。审计脚本的归类有些偏差。唯一真正在 main.go 里且较大的函数是 `runInit`（~40 行），以及 `Root` / `readEnvFile` / `runCmd` 等工具函数。

**建议**：`runInit` 可以考虑抽到 `init.go`，但非紧急。工作量：0（可推迟）。

---

### L5. copyright 年份不一致

`team.go:1` 和 `login.go:1` 写 `Copyright 2025`，其余文件写 `Copyright 2026`。

**建议**：统一。工作量：~2 分钟。

---

## GPU 调度 (v3.1) 前置评估

当前 KubePivot v3.0 的所有 sizing 和 scheduling 代码都是 CPU/Memory 维度的：

### 已有基础（可复用）

| 组件 | 当前能力 | GPU 扩展性 |
|---|---|---|
| `internal/scheduler/bin_pack.go` | 维度 A (CPU/Mem bin packing) | 需加 GPU 维度到 DP 矩阵 |
| `internal/sizing/dp.go` | 维度 B (Pod CPU/Mem 优化) | 需加 GPU 显存/利用率维度 |
| `internal/metrics/` | KubectlMetricsClient + PrometheusClient | GPU 指标需新 metric 定义 |
| `internal/eventstream/` | Deployment informer | 需加 GPU node/Pod informer |
| `cmd/kp/deploy_sizing.go` | `runSizingHook` 并发优化管道 | 可用于 GPU sizing |

### 需要新建的能力

1. **GPU 拓扑感知**：NVLink / PCIe 拓扑、MIG 分区、GPU 亲和性
2. **GPU 指标采集**：DCGM exporter / NVML、显存使用率、GPU 利用率、张量核心利用率
3. **GPU 调度约束**：`nvidia.com/gpu` resource、node selector、tolerations
4. **GPU 共享 / 时间切片**：MIG 配置、MPS、TimeSlicing
5. **GPU 成本模型**：按 GPU 型号 / 云厂商的定价模型

### eventstream 对 GPU 场景的意义

在前面的讨论中已确认：eventstream 的性能优势（46x List / 5.4x Allocs）体现在**调度器查集群状态的效率**上，不直接作用于 GPU 本身。对于 GPU 场景：
- 高 churn 的 GPU 训练 Job 会使 Watch 事件频率远高于 Web 服务
- 5.4x allocs 降低在 GPU 集群中更有价值（controller pod GC 压力不抢占 GPU workload 的 CPU）
- 内存放大率 1.65x vs 4.69x 对 GPU 节点的 controller sidecar 更重要（GPU 节点内存昂贵）

---

## 数据总览

| 维度 | 数值 |
|---|---|
| 总代码行数 | ~54,456 |
| cmd/kp 源文件 | 59 (非测试) + 13 (测试) |
| internal 包数 | 22 |
| internal 测试文件 | 55 |
| 顶级命令数 | 32 |
| 子命令数 | 50+ |
| 总 RBAC 检查点 | 7 (仅 4 个命令) |
| missing RBAC 的写命令 | 7 |
| 代码重复处 | 3 |
| TODO/FIXME/Broken 占位 | 1 |

---

## 建议修复优先级（v3.1 之前）

### 必须修（阻塞 v3.1）

1. **H1**: 补 7 个写命令的 RBAC 检查 → ~30 分钟
2. **H2**: 修 writeToFile → ~5 分钟

### 应该修（v3.1 开发中顺手修）

3. **H3**: 统一 Quantity 解析器 → ~15 分钟
4. **H4**: 统一 getWeights/weights → ~10 分钟
5. **H5**: 修正 flag 默认值 → ~5 分钟

### 可以修（v3.1 发布前清理）

6. **M1-M6**: 硬编码、注释丢失、示例代码清理 → ~90 分钟
7. **L1-L5**: 文档更新、copyright 统一 → ~75 分钟

**总预计工作量**：~3.5 小时（必须 + 应该）+ ~2.75 小时（可以）= ~6.25 小时

---

## 结论

KubePivot v3.0 代码质量良好。Master 分支保持 0 fix commit 的工程纪律（所有 bug 本地修复后 squash/rebase 进 Master）。主要技术债务集中在 v2.8 RBAC 推进不彻底（7 个写命令漏接权限检查）和 v2.9/v3.0 快速迭代中产生的代码重复（3 处）。

**v3.1 GPU 调度的代码基础是扎实的**——scheduler 框架（bin_pack + coordinator + rescheduler）、sizing engine（2D DP）、和 eventstream + metrics 数据层都已就位。GPU 扩展主要是"加维度"而非"改架构"。

---

审计工具：`tools/audit/audit.py` (Python) + 人工审查（qc + DeepSeek）
报告位置：`docs/audit-v3.0-system-review.md`
