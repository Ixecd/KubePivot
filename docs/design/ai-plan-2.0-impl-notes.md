# KubePivot ai-plan 2.0 实施日志

> 编写日期：2026-05-06（实时）
> 状态：✅ Phase 1-5 全部完成
> 关联文档：[ai-plan-2.0-draft.md](ai-plan-2.0-draft.md)（设计草案） / [scanner.go](../../internal/ai/scanner.go) / [gpu_detect.go](../../internal/ai/gpu_detect.go) / [prompt.go](../../internal/ai/prompt.go) / [sizing_check.go](../../internal/ai/sizing_check.go) / [diff.go](../../internal/ai/diff.go)
> 作用：记录设计 → 实施过程中的实际交付内容、决策调整、边界取舍

---

## 摘要

ai-plan 2.0 五阶段 Pipeline 全部落地。LLM 从"数值决定者"降级为"框架构建者"——
输出组件拓扑 + profile + 策略，CPU/Mem/GPU 具体数值由 KubePivot sizing 引擎填充。

---

## 实际交付 vs 设计文档

### Phase 1：仓库扫描增强 ✅

| 设计 | 实施 | 偏差 |
|------|------|------|
| 多语言依赖扫描（6 种文件） | `scanLangDeps()` 支持 requirements.txt / pyproject.toml / package.json / pom.xml / Cargo.toml / CMakeLists.txt | 一致 |
| GPU 库检测 | `detectGPULibs()` 跨 deps/Dockerfile/go.mod 扫描 | 一致 |
| 集群已有 Pod 查询 | 未实现（需要 kubectl + Prometheus 上下文） | 留 Phase 3 |
| resources.yaml/components.yaml 对比 | 未实现（v1.0 已有 ExistingPlan 字段，但未做 diff） | 留 Phase 5 |

#### 实施中的设计调整

**truncate 截断风险修复**：设计文档未提及截断策略。v1.0 `ScanRepo` 对 `go.mod` 和依赖文件硬编码 2000 字符前缀截断。如果 GPU 关键库（如 `torch`）出现在文件末尾，`detectGPULibs` 会漏检。

修复：新增 `extractGPULines()` 流式扫描——先遍历文件提取所有 GPU 相关行（跳过 `#`/`//` 注释），拼到截断内容顶部，再截断。

**依赖文件中的宽松匹配**：设计文档的 `gpuImportPatterns` 包含 `torch.cuda`，但 `requirements.txt` 写的是 `torch>=2.0`（不带 `.cuda`）。新增 `gpuBroadPatterns` 列表（`torch`、`tensorflow-gpu` 等），仅在依赖文件扫描时使用，代码扫描仍用精确匹配。

### Phase 4：GPU 智能感知 ✅

| 设计 | 实施 | 偏差 |
|------|------|------|
| 三层检测（代码/环境/镜像） | `DetectGPU()`：deps → Dockerfile → 型号推断 | 一致 |
| 型号推断启发式（11 条规则） | `gpuHeuristics` 表 + `inferGPUModel()` | 一致 |
| GPU 信任分层 | `GPUConfidenceByLib()` / `MaxGPUConfidence()` | 一致 |
| MIG 感知 | `RecommendMIG()` | 一致 |
| Cluster Capability Scan | 未实现（需要 kubectl get nodes） | 留后续 |
| Code-Metric Correlation | 未实现（需要 Prometheus + image tag 对比） | 留 Phase 5 |

#### 实施中的设计调整

**显存单位常量化**：设计文档直接写 `80 * 1024 * 1024 * 1024`。实施时引入 `GB = 1024 * 1024 * 1024` 常量，启发式规则和单元测试统一使用 `80 * GB`、`40 * GB` 等。

### Phase 2：LLM 角色转变 ✅

`BuildPrompt()` 重写：LLM 从"猜 CPU/Mem 数值"变为"输出框架 + profile + 策略"。
- CPU/Memory 默认填 `auto`（由 KubePivot sizing 引擎填充）
- 新增 profile 推断指南（web/batch/db/gpu）
- 检测到 GPU 库时注入 prompt → LLM 可将相关组件设为 `profile: gpu`
- `ParsePlan()` 默认值同步更新：CPU/Mem → auto，Source → llm-estimated
- `AIComponent` 加 `Profile` / `Deps` 字段

### Phase 3：Sizing 反馈环 ✅

`sizing_check.go` 骨架落地：
- `RunSizingCheck()`：冷启动（无历史 → 保持 LLM 值）/ 热更新（已有配置 → 标注 Prometheus 未接入）
- 冲突解决表接口预留（v3.2 接 Prometheus 数据源后激活）
- 每个组件返回 `SizingCheck` 含 ColdStart / LLMValue / Decision / Source

### Phase 5：Diff 展示 ✅

`diff.go` 落地：
- `DiffComponents()`：对比新旧 Plan，检测新增/删除/变更（cpu/mem/replicas/profile/storage）
- `FormatDiff()`：人类可读的 `+`/`-`/`~` 格式输出 + Source 标记
- `PopulateSources()`：按组件标记来源（sizing-refined / gpu-detected），仅对 profile=gpu 的组件生效
- `RenderComponentsYAML()`：渲染时输出 Source 注释

---

## 工程改进

### HTTP Client 连接池

`client.go` 的 `httpClient()` 原每次调用创建新 `http.Client`。改为包级 `sharedHTTPClient` 单例，避免短生命周期 CLI 工具的 TIME_WAIT 堆积。对常驻服务场景尤其重要。

### 显存魔法数字

`gpuHeuristics` 规则表中的 `80 * 1024 * 1024 * 1024` 全部替换为 `80 * GB`，`GB = 1024 * 1024 * 1024`。可读性提升显著。

---

## 边界取舍

**不做的事**：
- **集群能力预检**：需要 `kubectl get nodes`，ai-plan 是纯本地工具，不适合引入 kubectl 依赖
- **Code-Metric Correlation**：需要 Prometheus + 运行中 Pod 的 image tag 对比，是 deploy 阶段的事
- **Phase 2 LLM prompt 改造**：扰动太大，且依赖多 LLM provider 的 prompt 一致性验证

**做了但等后续接线的**：
- `AIComponent.Source` 字段：已定义 `llm-estimated` / `sizing-refined` / `oom-protected`，Phase 3/5 接入时填充
- `GPUDetection.Confidence`：已计算，Phase 5 diff 展示时用

---

## 实施数据

```
文件变更:
  internal/ai/scanner.go         +50 行 (RepoContext 扩展 + deps + GPU 检测 + truncate 修复)
  internal/ai/gpu_detect.go      ~170 行 (NEW: 三层检测 + 型号推断 + 信任分层 + MIG)
  internal/ai/prompt.go          重写 (BuildPrompt + ParsePlan + RenderComponentsYAML)
  internal/ai/sizing_check.go    ~70 行 (NEW: RunSizingCheck 冷/热分支)
  internal/ai/diff.go             ~130 行 (NEW: DiffComponents + FormatDiff + PopulateSources)
  internal/ai/client.go          +2 行 (sharedHTTPClient 单例)

测试:
  internal/ai/scanner_test.go     ~60 行 (3 用例)
  internal/ai/gpu_detect_test.go  ~135 行 (12 用例)
  internal/ai/sizing_check_test.go ~50 行 (3 用例)
  internal/ai/diff_test.go        ~75 行 (5 用例)

make dev: 全绿
```

---

## 编辑记录

```
2026-05-06  创建 v1
            - Phase 1: 多语言依赖扫描 + GPU 库检测 + extractGPULines 流式扫描
            - Phase 4: GPU 三层检测 + 11 条型号推断规则 + 信任分层 + MIG
            - Phase 2/3/5 未实施

2026-05-06  v2 全部交付
            - Phase 2: BuildPrompt 重写 (LLM → 框架构建者) + ParsePlan 默认 auto
            - Phase 3: sizing_check.go RunSizingCheck 冷/热分支骨架
            - Phase 5: diff.go DiffComponents + FormatDiff + PopulateSources
            - AIComponent 加 Profile/Deps 字段, Source 标记来源
            - Bug: PopulateSources 误标记所有组件 → 加 c.Profile=="gpu" 条件
            - Bug: Diff 漏 Storage 字段 → 补齐
            - 工程改进: sharedHTTPClient 单例 + GB 常量
```
