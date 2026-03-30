# SNAPSHOT — kubepivot

**里程碑**：v1.0.0 封神 🏆（最终版）
**日期**：2026-03-29
**版本**：v1.0.0

---

## 本轮完成（v0.9.0 → v1.0.0）

### 多服务独立 helm release

每个服务独立 chart，独立 release，独立 rollback：

```
deployments/{name}/
├── {name}-postgres/     StatefulSet 独立 chart
├── {name}-etcd/         Deployment 独立 chart
├── {name}/              业务服务 chart
└── {name}-controller/   controller chart（kubepivot-controller）
```

helm release 命名：`{project}-{service}`

### 拓扑排序部署

```
BuildLayers（Kahn 算法）→ []Layer
同层 goroutine 并行，层间串行
失败 → 级联 rollback → 整组 rollback → dtk down
```

关键修复：
- build/push 只做一次，不随 helm 重试重复执行
- `helmReleaseExists`（helm history --max 1）防止 rollback 未安装的 release 误触发 dtk down
- chart 不存在快速失败，不重试
- image 为空且无 chart → 自动跳过（CLI 工具）
- 清理旧 templates/ 里的 controller 文件，修复 helm --wait 超时

### controller 统一命名

所有项目 controller pod 统一命名 `kubepivot-controller`，部署在各自 namespace，共用同一镜像，不需要每个项目单独构建。

### e2e 验证

**e2e 项目（全新 dtk init 生成）**：
```
3层拓扑：
  层级 1（并行）：e2e-e2e-postgres, e2e-e2e-etcd
  层级 2：e2e-e2e
  层级 3：e2e-worker（CLI 工具，跳过）
全部 Running ✅
```

**web3-blitz（老项目完整迁移）**：
```
2层拓扑：
  层级 1（并行）：web3-blitz-web3-blitz-postgres, web3-blitz-web3-blitz-etcd
  层级 2：web3-blitz-wallet-service × 2
全部 Running ✅
```

### 文档全量更新

- `docs/design/` 系列（architecture/controller/deploy/helm/scaffold/state-machine/multi-service）
- `docs/guide/zh-CN/` 系列（commands/gotchas/helm）
- 老项目迁移 SOP 补进 gotchas

---

## 完整路线图

```
v0.3.x  dtk init 基础骨架
v0.4.x  状态机 + A2 Controller 基础
v0.5.x  体验命令（doctor/status/history）
v0.6.x  稳定性（etcd重连/SSA/etcd迁移）
v0.7.x  边界 case（pending处理/diff/ARCH）
v0.8.x  全面单元测试（143个）+ CI
v0.9.0  AI 扫描组件（dtk ai-plan）+ 统一进度输出
v1.0.0  多服务独立 release + 拓扑排序 + 级联 rollback + 文档  🏆
```

---

## 测试覆盖

| 包 | 测试数 |
|---|---|
| internal/planner | 32 |
| internal/state | 57 |
| internal/scaffold | 34 |
| internal/controller | 20 |
| **合计** | **143** |

---

## 遗留（v1.1.0）

| # | 任务 |
|---|------|
| 1 | 构建 kubepivot-controller 镜像，controller 自愈 e2e 验证 |
| 2 | dtk status 展示每个 release 独立状态 |
| 3 | controller SSA 冲突处理 |
| 4 | dtk rollback 打印拓扑逆序进度 |

---

## 快照归档

```
snapshots/
├── SNAPSHOT-dtk-2026-03-27-v0.6.0.md
├── SNAPSHOT-dtk-2026-03-27-v0.7.0.md
├── SNAPSHOT-dtk-2026-03-27-v0.8.0.md
├── SNAPSHOT-dtk-2026-03-28-v0.9.0.md
├── SNAPSHOT-dtk-2026-03-28-v1.0.0.md
└── SNAPSHOT-dtk-2026-03-29-v1.0.0-final.md  ← 本次，真正封神
```
