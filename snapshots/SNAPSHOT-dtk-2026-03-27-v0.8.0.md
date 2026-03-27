# SNAPSHOT — dev-toolkit

**里程碑**：v0.8.0 稳到无坑，P0 + P1 全部完成
**日期**：2026-03-27
**版本**：v0.8.0

---

## 本轮完成（v0.7.0 → v0.8.0）

### 1. 挡门功能全部踹开

**`ARCH` 自动检测**：
- scaffold 生成 `project.env` 时自动 `go env GOARCH` 填入
- `dtk deploy` 时 `ARCH` 为空也自动检测，不再依赖用户手动填

**边界 case 加固**：
- `checkPendingRollback` 扩展为 `checkHelmReleaseState`，处理三种异常状态：
  - `pending-rollback`：删除 secret + ForceState RUNNING
  - `pending-install`：删除 release 重新安装
  - `failed`：提供回滚或重新部署选项
- 提取 `confirmYN()` 和 `deletePendingSecret()` 公共函数
- `isImagePullError()` 检测镜像拉取失败，输出可操作的排查提示

**`dtk diff`**：
- 默认对比最新两个 revision 的 helm values
- `--from/--to` 指定任意 revision
- 递归 map diff，`+` 新增 / `~` 变更 / `-` 删除

### 2. 全面单元测试

| 包 | 测试数 | 覆盖率 |
|---|---|---|
| internal/planner | 20 | 100% |
| internal/state | 57 | 35.4% |
| internal/scaffold | 34 | 28.2% |
| internal/controller | 20 | 29.7% |

**发现并修复的真实 bug**：
- `replaceInDir` 不跳过 `.git` 目录，会污染 git 内部文件
- `fixChartYAMLs` 使用 Go 不支持的 lookahead 正则，遇到有 `maintainers:` 的 Chart.yaml 直接 panic
- `heal.go` rollback 成功后 `RUNNING → RUNNING` 非法转换

### 3. controller 接口重构

- 抽出 `Detector` 接口和 `HelmClient` 接口
- `Reconciler` 依赖抽象，测试时注入 mock
- `KubectlDetector` 和 `RealHelmClient` 作为生产实现

### 4. CI 更新

- 核心逻辑包加 `-race` 检测
- 分两步：core logic + 全量

---

## 遗留问题

| # | 问题 | 优先级 |
|---|------|--------|
| 1 | AI 扫描组件接入真实 LLM | P1 |
| 2 | 多服务支持 | P1 |
| 3 | 统一进度输出格式，带时间戳 | P2 |
| 4 | 关键步骤耗时打印 | P2 |
| 5 | web3-blitz deploy.mk 同步 | P1 |

---

## 历史快照

```
snapshots/
├── ...
├── SNAPSHOT-dtk-2026-03-27-v0.5.1.md
├── SNAPSHOT-dtk-2026-03-27-v0.6.0.md
├── SNAPSHOT-dtk-2026-03-27-v0.7.0.md
└── SNAPSHOT-dtk-2026-03-27-v0.8.0.md  ← 本次
```
