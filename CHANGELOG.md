# Changelog

KubePivot (kp) 所有显著变更记录。

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.0.0/)。
版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

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
