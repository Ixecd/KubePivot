# SNAPSHOT — dev-toolkit

**里程碑**：v0.9.0 AI + 体验拉满
**日期**：2026-03-28
**版本**：v0.9.0

---

## 本轮完成（v0.8.0 → v0.9.0）

### 1. dtk ai-plan — AI 扫描组件

全新命令，扫描代码仓库调用 LLM 自动生成 `configs/components.yaml`：

**支持四个 LLM provider**：

| Provider | 环境变量 | 默认模型 |
|----------|---------|---------|
| Grok（xAI） | `DTK_LLM_PROVIDER=grok` | `grok-3` |
| Claude | `DTK_LLM_PROVIDER=claude` | `claude-sonnet-4-20250514` |
| OpenAI | `DTK_LLM_PROVIDER=openai` | `gpt-4o` |
| 豆包 | `DTK_LLM_PROVIDER=doubao` | `doubao-pro-32k` |

支持 `DTK_LLM_ENDPOINT` 覆盖 API 地址，私有化部署直接接入。

**扫描内容**：目录结构（depth 3）、go.mod、cmd/ 服务 main.go（前 50 行）、Dockerfile、README、用户补充描述（`--desc`）

**命令**：

```bash
dtk ai-plan                    # 扫描 + 询问 + 写入
dtk ai-plan --suggest-only     # 只看建议，不写入
dtk ai-plan --desc "BTC/ETH 充提币系统，wallet-service 是核心"
```

**实测（web3-blitz + Grok）**：
- 正确识别 `wallet-service` 为核心 HTTP 服务（端口 2113、replicas=2）
- 正确识别 `chain-miner` 为 CLI 工具（image 为空，跳过 build/push）
- 正确排除基础设施组件（bitcoind、geth-rpc）

### 2. 统一进度输出（Progress）

新增 `cmd/dtk/progress.go`，统一所有命令的输出格式：

```
[15:38:09] 🏗  构建镜像 wallet-service（v0.1.10）
[15:38:23] ✓  构建完成（14.2s）
[15:38:23] 📤 推送镜像 wallet-service（v0.1.10）
[15:38:28] ✓  推送完成（5.5s）
[15:38:28] ⛵ helm upgrade web3-blitz
[15:38:29] ✓  helm upgrade 完成（0.6s）
[15:38:29] 🔍 等待 rollout 就绪
[15:38:29] ✓  服务就绪（0.3s）
[15:38:29] ✅ 部署完成，状态: RUNNING (version=v0.1.10)
```

`make deploy.full` 拆成四步独立调用：
- `make deploy.build` → 构建（含耗时）
- `make deploy.push` → 推送（含耗时）
- `make deploy.install` → helm upgrade（含耗时）
- `make deploy.run.all` → rollout 就绪（含耗时）

### 3. 文档完善

- `docs/guide/zh-CN/ai.md`：AI 使用手册（四个 provider 配置、扫描内容、输出格式、实战示例、FAQ）
- `docs/design/multi-service.md`：多服务支持设计文档（独立 helm release、依赖图、拓扑排序、级联 rollback）
- `docs/guide/zh-CN/gotchas.md`：补充多服务场景、热修复整体发布模式
- `docs/design/` 系列全量更新至 v0.8.0

---

## 遗留问题

| # | 问题 | 优先级 |
|---|------|--------|
| 1 | 多服务支持（独立 helm release + 依赖图 + 级联 rollback） | v1.0.0 |
| 2 | web3-blitz NOTES.txt 同步 dtk 最新版本 | P1 |
| 3 | web3-blitz deploy.mk 同步 dtk 最新版本（镜像存在检查） | P1 |
| 4 | `dtk init --dry-run` | P2 |

---

## 历史快照

```
snapshots/
├── ...
├── SNAPSHOT-dtk-2026-03-27-v0.6.0.md
├── SNAPSHOT-dtk-2026-03-27-v0.7.0.md
├── SNAPSHOT-dtk-2026-03-27-v0.8.0.md
└── SNAPSHOT-dtk-2026-03-28-v0.9.0.md  ← 本次
```
