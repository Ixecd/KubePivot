# SNAPSHOT — kubepivot

**里程碑**：scaffold 完整化 + controller 稳定化 + dtk down + 状态机测试全覆盖
**日期**：2026-03-27
**版本**：v0.5.0

---

## 本轮完成

### 1. controller 包稳定化

state 包彻底解耦，K8s 检测逻辑统一归入 controller 包：

- `DetectResourceExists` / `DetectActualState` 从 state 包移入 `controller/resources.go`
- `LoadResources` 签名修复：接收 `path string`，空字符串降级到环境变量再降级到默认路径
- `getLatestRevision` 修复：去掉 `--max 1`，取 `history[len-1]`，rollback 目标改为 `revision-1`
- `sm.Transition` 返回值不再丢弃，失败时 `slog.Error` 记录
- `OnMissing` case 统一为 `"auto-heal"`，alert case 补全 namespace 字段
- `resourceExists` 死代码删除
- graceful shutdown：`time.Sleep(2s)` → `sync.WaitGroup`，`startEtcdWatcher` 也纳入管控
- 环境变量统一：`KUBECONFIG` → `KUBE_CONFIG`
- `runResume` 改用 `controller.DetectActualState`，补全错误处理

### 2. scaffold 完整化（internal/scaffold/helm.go 独立文件）

**helm chart 生成全面重构**：

- 抽出独立 `internal/scaffold/helm.go`，`writeHelmTemplateSkeleton` 从 scaffold.go 移出
- `NOTES.txt` 移入 `templates/` 目录，部署后正确渲染组件状态和快速访问命令
- 基础设施组件统一加项目名前缀（`{name}-postgres`、`{name}-etcd`），避免 namespace 冲突
- postgres / etcd 加 `enabled` 开关，initContainers 用 `{{- if or }}` guard，避免空 key
- controller yaml 骨架（rbac / deployment / configmap）全部带 `{{- if .Values.controller.enabled }}`
- `controller.enabled: false` 默认关闭，首次 `dtk deploy` 不会因镜像不存在卡住
- `httpRoute.enabled` 补入 values.yaml，修复 helm render nil pointer
- values.yaml 改用 `strings.Builder` 生成，彻底修复 `fmt.Sprintf` raw string 嵌套 bug
- deploy.mk 加 `--set-file controller.resourcesConfig=configs/resources.yaml`
- post-init 输出提示：上线前收紧 RBAC、替换 controller 镜像

**新增生成文件**：

- `configs/resources.yaml`：支持的 kind 列表 + on-missing 策略说明 + 注释示例
- `handoff/HANDOFF.md`：写给下一个 Claude，自动填充日期/仓库/命令速查/目录结构

### 3. dtk down 命令

```bash
dtk down [--namespace] [--context] [--kubeconfig]
```

完整下线流程（二次确认后执行）：
1. 删除 `ClusterRole {name}-controller`
2. 删除 `ClusterRoleBinding {name}-controller`
3. 删除 `namespace {ns}`（含所有 namespaced 资源和 PVC）
4. 删除本地状态文件 `~/.dtk/state/{project}/{ns}.json`

部分失败继续执行，最后汇报所有错误。

### 4. 状态机测试全覆盖

从 15 个扩充到 **47 个**测试，全部通过：

| 分类 | 数量 | 覆盖点 |
|---|---|---|
| 合法转换逐条验证 | 16 | 转换表每条路径单独测试 |
| 非法转换系统性验证 | 8 | 从每个状态出发穷举所有非法目标 |
| 副作用验证 | 1 | 非法转换不污染 reason/history/updatedAt |
| ResumeFromValidating | 2 | 成功路径 + 6种非法调用状态 |
| 字段正确性 | 3 | UpdatedAt/history完整性/version覆盖 |
| EtcdKey | 3 | 格式/唯一性/包含必要部分 |
| 场景测试 | 6 | 首次部署/回滚/验证失败/多轮/优雅下线 |

---

## 已知遗留

| # | 问题 | 优先级 |
|---|------|--------|
| 1 | `startEtcdWatcher` 断线后不重连，只依赖定时对账 | P1 |
| 2 | helm pending-rollback 死锁仍需手动清理 | P1 |
| 3 | `resources.yaml` 为空时 `DetectActualState` 直接报错 | P1 |
| 4 | SSA 冲突处理未实现 | P1 |
| 5 | controller 单元测试缺失 | P1 |

---

## 历史快照

```
snapshots/
├── SNAPSHOT-dtk-2026-03-23-slog.md
├── SNAPSHOT-dtk-2026-03-23-error-handling.md
├── SNAPSHOT-dtk-2026-03-23-migrate.md
├── SNAPSHOT-dtk-2026-03-24-helm-self-contained.md
├── SNAPSHOT-dtk-2026-03-24-kubeconfig.md
├── SNAPSHOT-dtk-2026-03-24-state-machine.md
├── SNAPSHOT-dtk-2026-03-24-state-machine-e2e.md
├── SNAPSHOT-dtk-2026-03-24-release.md
├── SNAPSHOT-dtk-2026-03-24-full.md
├── SNAPSHOT-dtk-2026-03-25-reconciliation-controller.md
├── SNAPSHOT-dtk-2026-03-25-a2-refactor.md
├── SNAPSHOT-dtk-2026-03-25-full.md
├── SNAPSHOT-dtk-2026-03-27-controller-stabilization.md
└── SNAPSHOT-dtk-2026-03-27-v0.5.0.md  ← 本次
```
