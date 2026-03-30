# SNAPSHOT — kubepivot

**里程碑**：体验拉满 + 稳定性修复 + 端到端验证通过
**日期**：2026-03-27
**版本**：v0.5.1

---

## 本轮完成

### 1. 新增命令

**`dtk doctor`** — 环境依赖诊断：
- 检查 Go 版本（≥ 1.21）、Docker 运行状态、kubectl/helm 可用性、K8s 集群连通性
- 检查 project.env 存在性、REGISTRY_PREFIX 是否填写
- 区分 error（必须修）和 warn（可能有问题），exit code 1 表示有 error

**`dtk status`** — 三层状态一屏看清：
- 状态机状态（state / 最后更新 / 原因）
- K8s pod 实际状态（kubectl get pods 直接列出，不依赖 resources.yaml）
- Helm release 信息（revision / status / 更新时间，json 解析修复空字段问题）
- `--history` flag 显示最近 10 条状态转换历史（tabwriter 对齐）

### 2. 稳定性修复

| Bug | 根因 | 修复 |
|---|---|---|
| `RUNNING → ROLLING_BACK` 非法转换 | validTransitions 缺失该路径 | 补上，同步单元测试 |
| `helm rollback` 报 `release has no 0 version` | revision 传 0 | 查 history 取 latest-1，revision=1 时友好报错 |
| `--namespace` 在 helmRollback 重复传 | runner.go 拼参数错误 | 去掉重复 |
| rollback 失败转 CLEANING | 错误处理逻辑错误 | 失败时转回 RUNNING |
| `dtk status` Helm Status 为空 | yaml 嵌套层级解析错误 | 改用 json 输出解析 |
| `dtk status` pod 名字前多空格 | tabwriter icon 列宽问题 | icon 和名字拼接，只对齐两列 |

### 3. helm pending-rollback 自动处理

`dtk deploy` 和 `dtk resume` 前置检查，检测到 pending-rollback 时：
- 提示用户说明情况
- 询问是否自动清理（y/N）
- 删除 pending-rollback secret
- `ForceState()` 强制重置状态机为 RUNNING
- 继续部署

新增 `state.Machine.ForceState()` 方法，跳过转换表，专用于异常恢复场景。

### 4. 文档

- `README.md` 重写：加 why-dtk、所有命令说明、A2 controller 架构、values.yaml 开关
- `docs/guide/zh-CN/quickstart.md`：step-by-step 从零到 pod Running，含预期输出和排错
- `docs/guide/zh-CN/gotchas.md`：已知坑（并发、状态机、helm、环境、配置）
- `handoff/AI-CODING-GUIDE.md`：scaffold 生成，AI 编码约束指南

### 5. 端到端验证

完整跑通 9 步（controller 自愈跳过）：

```
dtk init → dtk deploy → dtk status → dtk status --history
→ dtk doctor → dtk deploy(升级) → dtk rollback
→ dtk release --deploy → dtk down
```

详见 `docs/e2e/E2E-TEST-REPORT-v0.5.1.md`

---

## 遗留问题

| # | 问题 | 优先级 |
|---|------|--------|
| 1 | `startEtcdWatcher` 断线后不重连 | P1 |
| 2 | SSA 冲突自动清除 | P1 |
| 3 | controller 自愈流程端到端验证 | P1 |
| 4 | controller 单元测试缺失 | P1 |
| 5 | `dtk init --dry-run` | P2 |

---

## 历史快照

```
snapshots/
├── ...
├── SNAPSHOT-dtk-2026-03-27-controller-stabilization.md
├── SNAPSHOT-dtk-2026-03-27-v0.5.0.md
└── SNAPSHOT-dtk-2026-03-27-v0.5.1.md  ← 本次
```
