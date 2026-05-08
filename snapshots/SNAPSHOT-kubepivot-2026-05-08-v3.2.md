# KubePivot v3.2 — 战场清扫：文档补全 + 技术债盘点

> 编写日期：2026-05-08
> 分支：Master
> HEAD: 294ec8f
> Total commits: 537
> Co-Authored-By: DeepSeek

---

## 这份快照跟 05-07 的区别

05-07 的快照记录了 v3.2 的**代码交付**（KVCache 冲刺 / 迁移引擎 / CBA / 池化）。
05-08 的快照记录的是**战场清扫** — 没有新功能，但 v3.2 从"代码写完了"变成了"可以交接了"。

---

## 做了什么

### 文档补全（8 篇）

从 `internal/` 代码反向提取设计决策，让下一个接手的人不需要重读 537 commits：

| 文档 | 覆盖内容 |
|------|---------|
| `docs/cmd/controller/controller.md` | resources.yaml 全字段 + 5 种自愈策略 + supply-chain + force-sync + 异常检测 |
| `docs/cmd/scheduler/scheduler.md` | 乾枢两维 DP + 池化层 + CBA + MigrationManager + Webhook + Rescheduler |
| `docs/cmd/sizing/sizing.md` | 2D DP 算法五步详解 + 5 种 Profile + 智能推荐 + GPU sizing + VPA 协调 |
| `docs/reference/eventstream.md` | Informer 状态机 + SkeletonCache CoW + PodCache delta buffer + Label 压缩 + vs client-go |
| `docs/reference/deployment.md` | kp deploy 管线 + components.yaml 全字段 + 状态机 + 拓扑分层 + 失败恢复链 |
| `docs/reference/route.md` | Provider 接口 + Ingress/Gateway API 双实现 + 蓝绿切换 + 自动检测 |
| `GITOPS-MANIFESTO.md` | v2.5 → v3.2 哲学不变，事实更新（能力地图 / 适用边界 / 对比表） |
| `DEEPSEEK.md` | DeepSeek 工作直觉传递（grep 先于判断 / 说"不做"的价值 / 减法手要狠） |

### 技术债盘点（94 → 19）

`FORGET.md` 全量扫描 34 个设计文档 × 当前代码，94 项 → 筛掉已实现和不紧急的 → 19 项：

```
P0 (7):  shard gap/不均 | Webhook TLS | WorkerPool 背压 | 死循环 | Prometheus | etcd config
P1 (12): OOM接线 | jitter校准 | Fencer OOB | Watch Phase4 | 拓扑约束 | label匹配 |
         跨ns deploy | per-service rollback | chart disabled | etcd PVC | GPU fields | FNV hash
```

剩余 75 项（P2 Ops / P3 Polish / 长期演进）——暂时不提，规模化再扫。

### 状态文件全量刷新

| 文件 | 变更 |
|------|------|
| `TODO.md` | v3.2 已完成补全（7 个新条目）+ v3.3 P0/P1 对齐 FORGET |
| `SNAPSHOT.md` | 537 commits / 294ec8f HEAD / 文档地图更新 |
| `HANDOFF.md` | 文档地图新增 cmd/ + reference/ / 直接依赖列明 |
| `ROADMAP.md` | v3.2 实际交付回顾 + "未按计划交付"修正（ai-plan/碳感知 状态更新） |
| `FORGET.md` | 全量重编号 + 6 项摘帽 + 3 项 v3.3 摘帽 + 归零到 19 |

### 归档

```
archived/todo/TODO-v3.2.md       ← 05-07 版 TODO
archived/snapshot/SNAPSHOT-v3.2.md  ← 05-07 版 SNAPSHOT
archived/handoff/HANDOFF-v3.2.md    ← 05-07 版 HANDOFF
```

---

## 本次窗口的数字

```
新文件:      3 (DEEPSEEK.md + 2 snapshots)
重写:        8 篇文档
更新:        5 个状态文件
归档:        3 个文件到 archived/
扫包:        6 个 internal package (controller/scheduler/sizing/eventstream/route/state)
grep 验证:   FORGET 33 条逐条 cross-check
00:00-now:   一次性收尾，0 返工
```

---

## 项目全貌（v3.2 终态）

```
KubePivot v3.2.0
├── 537 commits, 0 fix commit on Master
├── 20 包 test+race 全绿，959 单测 + 39 benchmark
├── 自研 KVCache: 14.5ns Get / 333ns Put / 1.13x 放大率
├── 乾枢调度器: 两维 DP + 池化 + 迁移引擎 + CBA
├── Controller: Pod/Node Informer 全接线 + OOM/CrashLoop 自愈
├── 部署: components.yaml → Kahn 拓扑 → 状态机驱动 → 级联回滚
├── 流量: Ingress + Gateway API 双 Provider + 蓝绿声明式切换
├── Sizing: 2D DP (80×128) + 5 Profile + 自适应权重
├── Config: system.yaml 6组31字段 + controller update --apply
├── 安全: SSO/RBAC/Sealed/供应链审计 全链路
├── 碳感知: CarbonSDK + kp scheduler status --carbon-region
├── 文档: 8 篇新/重写 + FORGET 94→19 + MANIFESTO v3.2
└── 待办: 19 项 P0+P1 (FORGET.md)
```

---

## 临别

05-07 的快照结尾是性能数字。05-08 的快照结尾没什么数字 —— 只有整洁的文档目录、归零的技术债清单、和对下一个窗口说的一句"你不需要重读 537 commits"。

这就是 v3.2 真正的交付物：不是更多代码，是更少的未知。

---

*关联: [SNAPSHOT-kubepivot-2026-05-07-v3.2.md](SNAPSHOT-kubepivot-2026-05-07-v3.2.md) — 代码交付快照*
