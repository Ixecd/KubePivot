# SNAPSHOT — dev-toolkit

**里程碑**：端到端全流程验证通过 + bug 修复 + dtk doctor/status
**日期**：2026-03-27
**版本**：v0.5.1

---

## 本轮完成

### 1. 新增命令

**`dtk doctor`**：检查环境依赖，区分 error（必须修）和 warn（可能有问题）：
- Go 版本 ≥ 1.21
- Docker 运行状态
- kubectl / helm 可用性
- K8s 集群连通性
- project.env 存在性
- REGISTRY_PREFIX 是否填写

**`dtk status`**：三层信息一屏看清：
- 状态机状态（state / 最后更新 / 原因）
- K8s pod 实际状态（kubectl get pods 直接列出）
- Helm release 信息（revision / status / 更新时间）
- `--history` flag 显示最近 10 条状态转换历史

### 2. Bug 修复（e2e 测试发现）

| Bug | 根因 | 修复 |
|---|---|---|
| `RUNNING → ROLLING_BACK` 非法转换 | validTransitions 缺失该路径 | 补上，同步更新单元测试 |
| `helm rollback` 报 `release has no 0 version` | revision 传 0，helm 不接受 | 查 history 取 latest-1，revision=1 时报错提示 |
| `--namespace` 在 helmRollback 里重复传 | runner.go 拼参数时重复 | 去掉重复 |
| rollback 失败转 CLEANING | runRollback 错误处理逻辑错误 | 失败时转回 RUNNING，不转 CLEANING |

### 3. 文档

- `README.md` 重写：加 why-dtk、dtk down/release/controller 章节、A2 controller 说明
- `docs/guide/zh-CN/quickstart.md` 新增：step-by-step 从零到 pod Running，含预期输出和排错
- `handoff/AI-CODING-GUIDE.md` scaffold 生成：AI 编码约束，填充业务逻辑指南

### 4. 端到端验证

完整跑通 9 个步骤（controller 自愈跳过）：

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
| 1 | `dtk status` Helm Status 字段为空（yaml 嵌套解析） | P1 |
| 2 | `dtk status` pod 名字前多一个空格（tabwriter 对齐） | P2 |
| 3 | controller 自愈流程未验证 | P1 |
| 4 | `startEtcdWatcher` 断线后不重连 | P1 |
| 5 | helm pending-rollback 死锁仍需手动清理 | P1 |
| 6 | SSA 冲突处理未实现 | P1 |

---

## 历史快照

```
snapshots/
├── ...
├── SNAPSHOT-dtk-2026-03-27-controller-stabilization.md
├── SNAPSHOT-dtk-2026-03-27-v0.5.0.md
└── SNAPSHOT-dtk-2026-03-27-v0.5.1.md  ← 本次
```
