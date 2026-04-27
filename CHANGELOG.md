# Changelog

KubePivot (kp) 所有显著变更记录。

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.0.0/)。
版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

---

## [v2.7.0] - 2026-04-27

### Added
- **Event Stream Infrastructure**（`internal/eventstream/`）
  - 自研 Informer + Cache，0 client-go 依赖
  - `Informer` 接口：Start / Get / List / Subscribe / Stop / Stats
  - 三层 Cache：Hot / Warm / Cold（v2.7.0 仅实现 Hot 层 lock-free snapshot）
  - `ParseSkeleton` 增量序列化：仅解 metadata + spec.replicas + status.phase
  - `SkeletonCache` lock-free 读 + RWMutex 写（atomic.Value snapshot）
  - HTTP watch loop + 重连退避 + 差异化 resync（按资源类型 5min-30min）
  - v2.5 sharding adapter 集成（`NewShardSetAdapter`）
  - Prometheus metrics collector（9 指标，可注册到 v2.7.x HTTP server）
- **In-cluster auth**（`internal/eventstream/auth.go`）
  - `K8sConfig` 不可变配置 + `resolveK8sConfig` 三路径优先：
    显式 URL > KubeConfig (v2.7.x) > in-cluster ServiceAccount
  - in-cluster：自动读 `/var/run/secrets/.../token` + `/ca.crt`
  - TLS 1.2 minimum，强制 RootCAs 校验，禁止 InsecureSkipVerify
  - `bearerAuthTransport` 通过 `req.Clone()` 防 mutate 原 request
  - `readTokenFile` / `readCAFile` 函数变量注入（测试 mock 模式）
- **Controller 渐进切换到 Informer Cache**（双保险，fail soft）
  - `InformerPool` (`informer_pool.go`)：管理 controller 范围的 informer
  - `InformerDetector` (`informer_detector.go`)：实现 `Detector` 接口
    - cache hit → 信任 → 直接返回 true（避免 kubectl fork ~100ms）
    - cache miss / unsupported kind / informer 未启动 → fallback 到 KubectlDetector
  - `LabelGetter` 接口 + `loadResourceLabels` fast path：
    - 类型断言路径，cache 命中直接读 `Resource.Labels`
    - 既有 `KubectlDetector` / `mockDetector` 不实现 → 走原 kubectl
  - 既有 `KubectlWatcher` / `kubectl get` 路径完整保留
- **MetricsClient 业务指标层**（`internal/metrics/`）
  - `MetricsClient` 接口：GetPodMetrics / GetNodeMetrics / List 系列
  - `KubectlMetricsClient` 实现：调 `kubectl top pod/node -o json`
  - `Quantity` 自实现 K8s 资源量解析（0 client-go）：
    - CPU: milli-cores（"100m" → 100，"1.5" → 1500）
    - Memory: bytes（"1Gi" → 2³⁰，"1G" → 10⁹）
    - 公式：Value = Base × Multiplier，支持小数 + SI/二进制单位
  - `kubectl func` 字段注入式 mock（与 `readTokenFile` 同模式）
  - 为 v2.9 Sizing Engine 二维 DP 铺垫数据基础
- **Bench 3 Watch 稳态吞吐**（`benchmark/eventstream/watch_throughput_test.go`）
  - vs client-go: 1.65-2.97x 时间提升 / **5.4x allocs 降低** ⭐
  - 单事件 ~36µs vs ~66µs；10000 events 382ms vs 632ms
  - 公平赛道（fake watch source，与 Bench 1/2/4/5 同维度）
  - 与 Bench 5 内存放大率数据互证（root cause: 反序列化策略差异）
- **设计文档**（`docs/design/`）
  - `eventstream-draft.md`（设计草案，~1290 行）
  - `eventstream-perf.md`（5 项基准数据，~452 行）
  - `eventstream-impl-notes.md`（实施实录，~620 行）
  - `FUTURE.md`（架构远景探索 / Cell-based Architecture 种子）

### Changed
- `internal/controller/global.go`
  - `StartGlobal` 加 informer pool 启动（在 orphanSweeper 之后）
  - `informerPool` 提前创建（worker pool 闭包引用）
  - `SetShardMgr` 延迟绑定（解决 shardMgr 自引用循环）
  - `handleTask` 加 `informerPool` 参数 + `InformerDetector` 注入
- `internal/controller/heal.go`
  - `loadResourceLabels` 顶部加 `LabelGetter` fast path（6 行）
  - 既有 kubectl 路径保留为 fallback
- `internal/eventstream/informer_impl.go`
  - `NewInformer` 调 `resolveK8sConfig` 解析连接配置
  - `httpClient` 用 `K8sConfig.HTTPClient()` 替换裸 client（含 Bearer/TLS）

### Tests
- `internal/eventstream/`：~3700 行代码 + ~2200 行测试
  - 230+ test cases / sub-cases，覆盖率 89.0%
  - auth (20) / informer (15) / cache (16) / sharding adapter / metrics (16)
- `internal/controller/`：+~600 行（双保险接入）
  - InformerPool (10) / InformerDetector (9) / 集成测试
  - 既有 75 个测试 0 改动（Detector 接口不变）
- `internal/metrics/`：1119 行（含测试）
  - 14 cases / 60+ sub-cases，覆盖率 81.2%
  - parseCPU 18 sub / parseMemory 17 sub / KubectlMetricsClient 7
- 集成验证：所有包 race detector 全绿，make dev 全绿

### Documentation
- `docs/design/eventstream-draft.md` ~1290 行设计草案（Q1-Q14 拍板记录）
- `docs/design/eventstream-perf.md` 完整 5 项基准 + Bench 3 数据
- `docs/design/eventstream-impl-notes.md` Day 1-5 实施日志（含 bug 修复链）
- `FUTURE.md` v2.8/v3.0 远景探索（Cell-based Architecture）
- `ROADMAP.md` ~2143 行 v2.7→v3.0 路线图（v2.6 release 时已写）

### 设计原则核对
- ✅ 0 client-go（自研 informer + auth + Quantity 解析全部 stdlib）
- ✅ 单二进制（eventstream / metrics 编译进 kp 主二进制）
- ✅ 双保险（Informer 失败 → fallback kubectl，既有 v2.5/v2.6 路径不破）
- ✅ 函数变量注入式 mock（readTokenFile / newInformerFunc / kubectl func）
- ✅ 数据驱动决策（Bench 5 数据驱动自研路径，不藏数据）
- ✅ 设计先行（每个 Step 都 Q&A 拍板再实施）

### 工程教训
- **Informer 接口与 KubePivot executor 的不一致**
  - executor 是具体类型 `*KpExecutor` 单例，无 `Executor` interface
  - 假设标准 Go testing 模式踩坑（unused import / SetExecutor 不存在）
  - 修复：函数变量注入（`kubectl func`）替代全局 mock，与既有模式对齐
- **设计先行 + 全局信息收集是后期必需**
  - "设计的太多，遗忘很正常" — 5300+ 行 controller 不可能凭记忆改造
  - 每个 Step 都先 grep / 看代码 / 对照既有风格再下手
  - 多个"假设的标准模式"被 cross-check 拦下（DryRun anti-pattern / envtest 路径）
- **诚实标注"未实施"项胜过隐藏**
  - kubeconfig 解析占位 error 比"看起来像支持但跑不通"更诚实
  - Day 3 informer impl 的 in-cluster auth 是"诚实债务"，Step 2a-1 补上
  - perf.md 中"Bench 3 envtest 推迟" → Step 4 实际改 fake source 路径，
    诚实记录设计调整理由
- **工程纪律 > 数字仪式感**
  - 早期想 commit 427 落点 v2.7.0 tag 数字仪式感
  - 实际 commit 数顺其自然，4月27日 tag 日期意义保留
  - tag 落点 commit 数字次要，质量优先
- **基准公平性原则**
  - Bench 3 选 fake watch（与 Bench 1/2/4/5 同赛道）而非 envtest
  - 避免 kube-apiserver 性能噪声掩盖核心逻辑差异
  - 数据可信度高于"看起来更严谨的环境"

### Commits
- `8be0d41` Day 2: eventstream 包基础
- `075ecf8` Day 3: watch loop + 单元测试
- `12b0966` Day 4: v2.5 sharding adapter
- `792c84a` Day 5: Prometheus metrics collector
- `d874c0d` Step 2a-1: in-cluster auth + 接入 informer
- `1bd2574` Step 2a-2: informer pool 接入 controller
- `59c72b1` Step 2b-1: InformerDetector with kubectl fallback
- `d04bf72` Step 2b-2: LabelGetter fast path
- `4f6df60` Step 3: MetricsClient 业务指标层
- `49fe8b1` Step 4: Bench 3 数据公开（feature 分支 7599d61）

---

## [v2.6.0] - 2026-04-26

### Added
- **流量层 Provider 抽象**（`internal/route/`）
  - `Provider` 接口统一封装 Ingress 和 Gateway API
  - `IngressProvider` 基于 `networking.k8s.io/v1`，蓝绿场景直接切 backend.service.name
  - `GatewayAPIProvider` 基于 `gateway.networking.k8s.io/v1`，HTTPRoute 原生 weight 支持
  - 自动检测（Gateway API 优先 → Ingress fallback）+ 显式覆盖
- **声明式蓝绿部署**（`resources.yaml` 新增 `traffic:` 字段）
  - Strategy: `blue-green`
  - Routes 列表声明流量权重
  - 与 v2.0 命令式蓝绿（`kp promote`）并存
- **Sandbox COMMITTING 阶段内分两步执行**（`cmd/kp/sandbox.go`）
  - Step 1: helm upgrade + migrate（v1.8.0 既有）
  - Step 2: 流量切换 + Pod ready 健康判定（v2.6 新增）
  - 任意失败自动 RESTORING
- **错误码扩展**（`internal/code/`）
  - 编号区段 110000-110099 留给 `internal/route` 包
  - `ErrRouteProviderNotAvailable` / `ErrRouteResourceNotFound` /
    `ErrRouteInvalid` / `ErrRouteApplyFailed` / `ErrRouteAutoDetectFailed`
- **完整 demo 工程**（`docs/example-blue-green/`）
  - 13 个文件覆盖：Makefile / resources.yaml / Helm chart / 3 个 shell 脚本
  - 不依赖 web3-blitz，任何 K8s 集群 < 2 分钟跑通
- **设计文档**（`docs/design/traffic-layer.md`）

### Changed
- `internal/controller/resources.go` `ResourcesConfig` 新增 `Traffic *Traffic` 字段
  - 向后兼容：未声明则 v2.5.0 行为不变
- `cmd/kp/sandbox.go` 引入 `runBlueGreenSwitch` + `waitDeploymentReady` 辅助函数

### Tests
- `internal/route/` 27 个 sub-cases 全 PASS
- `internal/controller/` 新增 `TestResourcesConfig_HasBlueGreen` 5 cases
- 集成验证：demo 工程在 orbstack 环境跑通

### Documentation
- `docs/design/traffic-layer.md`（v2.6 完整设计 + 实施记录）
- `docs/design/state-machine.md` 新增"v2.6 流量层与状态机协同"章节
- `docs/design/architecture.md` 新增第 9 条核心设计决策"流量层抽象"
- `README.md` "### 蓝绿发布" 章节扩展为 v2.0 命令式 + v2.6 声明式双模式

### 设计原则核对
- ✅ 不引入 client-go（Provider 通过 `executor.GetExecutor()` 调 kubectl）
- ✅ 单二进制（`internal/route/` 编译进 kp 主二进制）
- ✅ 只保护，不越权（`managed-fields` annotation + 完整保留用户字段）
- ✅ 简单 > 完美（不引入 metrics 监控，仅 Pod ready 判定）

### 工程教训
- `tools/codegen` 不支持多 const block 的同类型常量（实测发现）
  - 临时手工补 `code_generated.go` 的 case 分支
  - codegen 改造留 v2.6.1 候选小任务
- HANDOFF.md 3.7 节扩展"中文标点紧贴变量名"陷阱
  - 一天内两次踩同样的 bash 解析坑（hot-reload.sh + cleanup.sh）
  - 实践规范：default 都用 `${var}` 风格

### Commits
- `786b59d` Step 1: 流量层抽象 + Ingress + Gateway API 双 Provider
- `f0244cc` Step 2: Sandbox 蓝绿流量切换接入
- `ea48e5c` Step 3: 蓝绿部署 demo 工程

---

## [v2.5.1] - 2026-04-25

### Changed
- 性能 P2 验证完成：`hot-reload.sh` 100 次幂等 apply 验证通过
- HANDOFF.md 3.7 节"Bash 严格模式陷阱"扩展（中文标点紧贴变量名陷阱）
- `benchmark/scripts/cleanup.sh` 大规模友好改造（≥30 项目自动停 controller）

### Documentation
- `docs/design/sharding-tuning.md` 雏形（环境约束 + 性能立方体待补）
- 累计性能数据：P=10 / P=50 数据点已采集
  （P=99 因 macOS 内存压力数据废弃，需更大测试环境）

---

## [v2.5.0] - 2026-04-25

### Added
- **分片机制**（`internal/sharding/`）
  - 多副本 controller，每 pod 持有部分 namespace
  - HA 边界：故障 pod 的 shard ~10 秒被其他 pod 抢占
- **孤儿清理**（Step 3 of v2.5.0）
  - controller 周期性扫描孤儿 namespace 并 cleanup

---

## 更早版本

更早版本的变更记录见 git log 和各 commit 的详细 message。
