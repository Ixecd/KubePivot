# SNAPSHOT — kubepivot

**里程碑**：v1.0.0 封神 🏆
**日期**：2026-03-28
**版本**：v1.0.0

---

## 本轮完成（v0.9.0 → v1.0.0）

### 多服务支持：独立 helm release + 拓扑排序部署

**这是 dtk 和其他脚手架真正拉开差距的地方。**

#### planner 重构

- `Component`/`Plan` 新增 `Type`（deployment/statefulset）和 `DependsOn` 字段
- `BuildLayers`：Kahn 算法拓扑排序，返回 `[]Layer`，同层可并行部署
- `BuildPlan`：保持向后兼容，扁平化 layers
- `Downstream`：BFS 找出所有下游服务，逆拓扑顺序，用于级联 rollback
- 循环依赖检测、未定义依赖检测
- 32 个单元测试（20 原有 + 12 新增，100% 覆盖）

#### scaffold 重构

从单一大 chart 拆成四个独立 chart：

```
deployments/{name}/
├── {name}-postgres/     # StatefulSet 独立 chart
├── {name}-etcd/         # Deployment 独立 chart
├── {name}/              # 业务服务 chart（含 initContainers）
└── {name}-controller/   # controller chart（默认 disabled）
```

每个服务独立 helm release：`{project}-{service}`

#### deploy 重构

```
isMultiService → deployLayers
    ↓
for each 层级（同层 goroutine 并行，层间串行）：
    build/push（只做一次）
    helm upgrade --install（最多重试 3 次）
    kubectl rollout status
    ↓ 失败
    helmReleaseExists 检查（防止 rollback 未安装的 release）
    级联 rollback（失败服务 + 下游，逆序）
    整组 rollback（级联失败时）
    dtk down（整组也失败时）
```

单服务走原有 make 路径，向后兼容，goto 留着缅怀历史 😄

#### e2e 验证（web3-blitz）

```
web3-blitz-wallet-service   revision=3   deployed   2/2 Running  ✅
```

全链路验证：
- 多服务模式正确触发（`len(layers[0]) > 1`）
- `chain-miner`（CLI 工具，无 chart）正确跳过
- `wallet-service` 独立 release 成功安装
- initContainers 等待 postgres/etcd 通过
- DB 连接、etcd 连接、migrate、API 启动全部正常
- cascade rollback 正确跳过未安装的 release

---

## 完整路线图回顾

```
v0.3.x  dtk init 基础骨架
v0.4.x  状态机 + A2 Controller 基础
v0.5.x  体验命令（doctor/status/history）
v0.6.x  稳定性（etcd重连/SSA/etcd迁移）
v0.7.x  边界 case（pending处理/diff/ARCH）
v0.8.x  全面单元测试（131个）+ CI
v0.9.0  AI 扫描组件（dtk ai-plan，四个 LLM provider）+ 统一进度输出
v1.0.0  多服务支持：独立 helm release + 拓扑排序 + 级联 rollback  🏆
```

---

## 测试覆盖

| 包 | 测试数 |
|---|---|
| internal/state | 57 |
| internal/scaffold | 34 |
| internal/controller | 20 |
| internal/planner | 32 |
| **合计** | **143** |

---

## 遗留问题（v1.1.0+）

| # | 问题 | 说明 |
|---|------|------|
| 1 | dtk status 多 release 展示 | 每个 release 独立状态 |
| 2 | 服务级 FSM | 目前是项目级 FSM |
| 3 | 灰度发布 | 豆包的建议，后续可做 |
| 4 | web3-blitz 完整迁移到多 chart | 目前 postgres/etcd 还在老 chart |
| 5 | controller SSA 冲突处理 | CLI 层已处理，controller 层待补 |

---

## 历史快照

```
snapshots/
├── SNAPSHOT-dtk-2026-03-27-v0.6.0.md
├── SNAPSHOT-dtk-2026-03-27-v0.7.0.md
├── SNAPSHOT-dtk-2026-03-27-v0.8.0.md
├── SNAPSHOT-dtk-2026-03-28-v0.9.0.md
└── SNAPSHOT-dtk-2026-03-28-v1.0.0.md  ← 本次，封神
```
