# KubePivot 三层资源决策栈

> 编写日期：2026-04-27
> 状态：部分实现（Layer 1 ✅ / Layer 2 ⏳ / Layer 3 ⏳）
> 关联文档：[ROADMAP.md](../../ROADMAP.md) / [architecture.md](architecture.md)

---

## 摘要

KubePivot 不只是 K8s 部署工具，从 v2.x 开始演化为**资源决策系统**。

核心论断：**用户应该声明业务意图，不应该声明资源配额。**

具体地：
- 用户写："我要部署一个 web 服务，预期峰值 QPS 5000"
- KubePivot 算："这个服务应该是 cpu=200m / mem=256Mi / replicas=3，
  分配到 node-3 / node-7 / node-12 上"

为达成这个目标，KubePivot 设计了**三层决策栈**：

```
Layer 1: AI Cold Start  ✅ v2.x 已实现
Layer 2: DP Sizing      ⏳ v2.9 规划
Layer 3: DP Scheduler   ⏳ v3.0 规划
```

三层各司其职，无冲突无重叠，按时间维度接力。
本文档描述这个决策栈的完整 mental model。

---

## 三层栈概览

```
                     [项目 + 业务描述]
                            │
                            ↓
          ┌─────────────────────────────────────┐
          │   Layer 1: AI Cold Start            │
          │   静态代码 + LLM → 起点配置           │
          │   ✅ kp ai-plan (v2.x)              │
          └─────────────────────────────────────┘
                            │
                    [components.yaml]
                            │
                            ↓
                       [部署运行]
                            │
                  [metrics 数据累积]
                            │
                            ↓
          ┌─────────────────────────────────────┐
          │   Layer 2: DP Sizing                │
          │   历史数据 + 二维 DP → 单 Pod 最优     │
          │   ⏳ kp sizing recommend (v2.9)     │
          └─────────────────────────────────────┘
                            │
                  [resources.yaml 更新]
                            │
                            ↓
          ┌─────────────────────────────────────┐
          │   Layer 3: DP Scheduler             │
          │   sizing + 节点拓扑 → 节点放置         │
          │   ⏳ kp scheduler (v3.0)            │
          └─────────────────────────────────────┘
                            │
                            ↓
                    [Pod → Node 映射]
```

### 三层对比

| 维度 | Layer 1 (AI) | Layer 2 (Sizing) | Layer 3 (Scheduler) |
|------|--------------|------------------|---------------------|
| 输入 | 静态代码 + 用户描述 | 历史 metrics 数据 | Pod sizing + 节点拓扑 |
| 输出 | 起点 components.yaml | 每 Pod 最优 requests/limits | Pod → Node 映射 |
| 时机 | 项目创建 / 业务大变 | 周期性，数据 ≥ 7d | 部署时 + 周期性 5min/15min |
| 数据依赖 | 零（不依赖运行时） | 强（需 metrics-server） | 强（需 sizing 输出 + node 状态） |
| 决策方式 | LLM 推理（"猜"） | 二维 DP + 启发式 | 双 DP 协同 |
| 价值 | "零配置部署"成为可能 | 单 Pod 利用率 70%+ | 节点 CPU 85%+, Mem 70%+ |
| 状态 | ✅ v2.x 已实现 | ⏳ v2.9 规划 | ⏳ v3.0 规划 |

---

## Layer 1: AI Cold Start

### 1.1 设计意图

**问题**：项目刚创建时，没有任何运行数据。
- VPA / KRR / Goldilocks 等基于 metrics 的方案完全失效
- K8s 默认调度器只能基于用户写死的 requests/limits 调度
- 用户被迫在零信息下"瞎蒙" cpu=100m / mem=128Mi 这种典型值

**解法**：用 LLM 从代码语义推断起点配置。

LLM 看到：
- 目录结构（`cmd/api/` / `cmd/worker/` / `cmd/cli/`）
- main.go 前 50 行（导入了哪些库 / 启动了什么 server）
- go.mod（依赖了什么框架）
- README + Dockerfile（业务描述）
- 用户 `--desc` 补充

LLM 输出：
- 每服务的 `cpu / memory / replicas / port / image`
- 一段 reasoning 解释判断依据

这不是"算"出来的最优值，是 LLM 基于语义的"合理猜测"。
**它的价值不在精确，而在让"零配置部署"从理论变成可能。**

### 1.2 代码归档

```
internal/ai/
├── plan.go        AIPlan / AIComponent / BuildPrompt / ParsePlan / RenderComponentsYAML
├── llm.go         LLMClient 接口 + 4 个实现
└── scan_repo.go   RepoContext / ScanRepo / buildDirTree / scanServices
```

CLI 入口：`cmd/kp/ai_plan.go` → `runAIPlan()`

### 1.3 关键接口

```go
// LLMClient 抽象 LLM provider
type LLMClient interface {
    Complete(ctx context.Context, prompt string) (string, error)
}

// 已实现 4 个 provider：
//   newClaudeClient   → claude-sonnet-4-20250514
//   newOpenAIClient   → gpt-4o
//   newDoubaoClient   → doubao-pro-32k
//   newGrokClient     → grok-3
// 通过环境变量切换：
//   DTK_LLM_PROVIDER = claude / openai / doubao / grok
//   DTK_LLM_API_KEY  = 对应 API key
```

```go
// AIPlan LLM 返回的规划结果
type AIPlan struct {
    Components []AIComponent `json:"components"`
    Reasoning  string        `json:"reasoning"`
}

type AIComponent struct {
    Name     string `json:"name"`
    Port     int    `json:"port"`
    Image    string `json:"image"`
    Replicas int    `json:"replicas"`
    CPU      string `json:"cpu"`     // "200m"
    Memory   string `json:"memory"`  // "256Mi"
    Storage  string `json:"storage"` // "1Gi"
}
```

### 1.4 数据流

```
kp ai-plan
    │
    ├─ ScanRepo(root, userDesc)
    │   ├─ buildDirTree()        目录结构（深度 3）
    │   ├─ 读 go.mod             依赖
    │   ├─ scanServices()        cmd/ 下每个服务的 main.go 前 50 行
    │   ├─ 读 configs/components.yaml  现有配置（如有）
    │   ├─ findDockerfiles()     build/docker/*/Dockerfile
    │   └─ 读 README.md          前 50 行
    │
    ├─ BuildPrompt(repoCtx)      拼接成完整 prompt
    │
    ├─ client.Complete(ctx, prompt)  调 LLM API（60s timeout）
    │
    ├─ ParsePlan(raw)            JSON 反序列化 + 默认值补全
    │
    ├─ 终端打印规划结果 + reasoning
    │
    └─ 用户确认后写入 configs/components.yaml
```

### 1.5 局限

```
1. 是"猜"不是"算"
   LLM 没看过实际负载，无法精确预测
   同一项目两次调用结果可能不一致（LLM 有随机性）
   
2. 依赖外部 LLM API
   需要 API key
   有调用成本
   有网络延迟（~3-10 秒）
   
3. 业务理解有上限
   LLM 看 main.go 前 50 行 + Dockerfile 推断业务
   复杂业务可能误判
   建议用户用 --desc 提供准确描述
   
4. 不会随业务演化
   LLM 输出后即固化，不持续优化
   业务变化后需要 Layer 2 接管，或重新 kp ai-plan

这些局限不是 Layer 1 的"问题"，是 Layer 1 的"边界"。
Layer 1 解决的是"零配置起点"，不是"持续优化"。
持续优化是 Layer 2 / Layer 3 的事。
```

---

## Layer 2: DP Sizing

> 完整设计见 [ROADMAP.md v2.9 章节](../../ROADMAP.md)

### 2.1 设计意图

**问题**：项目运行一段时间后，Layer 1 的"猜"不再是最优。
- 实测数据告诉你：你的 web 服务 P95 cpu=180m，但 Layer 1 猜 cpu=100m → OOM 风险
- 或者反过来：Layer 1 猜 cpu=500m，实测 P95 cpu=80m → 资源浪费 80%

**解法**：基于历史 metrics 数据，用二维 DP 求最优 (CPU, Memory) 配置。

```
状态：dp[c][m]  c = CPU request, m = Memory request
得分：dp[c][m] = w1 * cpu_util(c) + w2 * mem_util(m) - w3 * waste_penalty(c, m)
约束：P95(usage) ≤ requested * (1 - headroom)
        OOMKill 概率 < 0.1%
```

通过 4 个启发式（二分裁剪 / 单调性 / 业务模板 / 时间分桶）
把 80×128=10240 状态空间降到 ~600，单 Pod 求解 < 1s。

### 2.2 与 Layer 1 的衔接

```
数据 < 24h (项目刚部署)：
  → Layer 1 配置生效
  → Layer 2 不动用户配置
  → kp sizing recommend 返回 Confidence=0

数据 24h - 7d (数据少)：
  → Layer 2 跑 DP，Confidence 低
  → 给出建议 + 警告
  → 用户选择是否应用

数据 ≥ 7d (数据足)：
  → Layer 2 接管
  → Confidence ≥ 0.7 时 mode=auto 自动应用
  → Layer 1 退出主流程
```

### 2.3 业务大变时回到 Layer 1

```
触发场景：
- 业务转型（web → 秒杀 / batch → streaming）
- 流量数量级变化（10x 增长）
- 历史数据不再代表未来

操作：
$ kp ai-plan --desc "我们要做秒杀，预期峰值 QPS 50000"
  → AI 读现有 components.yaml + 新业务描述
  → 重新规划基线
  → DP 历史数据清零或降权（time decay 加速）
  → 7 天后 Layer 2 重新接管

→ Layer 1 是"业务大变时的重置按钮"
→ Layer 2 是"日常优化的引擎"
→ 两者真正互补，不是替代
```

### 2.4 与 K8s VPA 的差异

```
K8s VPA：
  - 集群级别 controller，持续运行
  - 应用建议需 Pod 重启（破坏性）
  - 算法保守（业务敏感）
  - 不写入 GitOps 真相

KubePivot Layer 2：
  - 部署时（kp deploy）一次性算
  - 写入 configs/resources.yaml（GitOps 真相）
  - 不需要 Pod 重启
  - 算法可激进（DP + 启发式）

共存：
  resources.yaml:
    sizing:
      mode: auto    # KubePivot Layer 2 接管
      # mode: manual → 用户写死，不算
      # mode: vpa    → 配置 VPA 接管，KubePivot 不动
```

### 2.5 局限

```
1. cold start 不行
   < 7 天数据时 Confidence 低
   依赖 Layer 1 兜底

2. 不管节点拓扑
   Layer 2 算出"理想 sizing"
   但节点可能装不下（v2.9 范围之外）
   交给 Layer 3 解决

3. 算法本身复杂度
   DP 求解可能算错离谱配置
   需要 Confidence 字段 + fallback 机制
   完整测试覆盖（>200 case）
```

---

## Layer 3: DP Scheduler

> 完整设计见 [ROADMAP.md v3.0 章节](../../ROADMAP.md)

### 3.1 设计意图

**问题**：Layer 2 算出每 Pod 最优 sizing 后，还要决定每 Pod 放哪个节点。
- K8s 默认调度器：first-fit-decreasing 变种，节点利用率 ~50%
- 仅靠 affinity / anti-affinity 优化有限
- 业界水平：Karpenter 70-85%，阿里 / 字节内部方案 85-95%

**解法**：把 bin packing 问题用 DP 求解。

```
维度 A (拓扑)：节点 × Pod × 资源类型
维度 B (资源)：Pod × CPU × Memory   ← 继承 Layer 2

双 DP 协同：
  Outer: 维度 A bin packing
  Inner: 维度 B sizing（可能受 cap 约束）
  收敛：迭代上限 5 次

目标：
  节点 CPU 利用率 85%+
  节点 Memory 利用率 70%+
  
  务实声明：不冲 98%（行业内部方案的极端数据）
  85% / 70% 已是行业领先（vs K8s 默认 ~50%）
```

### 3.2 与 K8s 默认调度器的关系

```
KubePivot v3.0 选择 模式 1+3 混合：

模式 1 (Mutating Webhook)：
  Pod 创建时拦截，修改 spec.nodeSelector
  实现"运行时调度优化"
  
模式 3 (GitOps 间接)：
  resources.yaml 写死 nodeSelector
  K8s 默认调度器执行
  实现"部署时 GitOps 真相"

不替换 K8s 调度器（模式 2 工程量太大）。
```

### 3.3 局限

```
1. 实施复杂度
   ~3800 行代码 + 双 DP 协同 + webhook
   v3.0 是大版本，可能需要多次迭代

2. 抖动风险
   高利用率本质上离 100% 近
   实施"多级降级"机制（Level 0-3）

3. 与 StatefulSet / 蓝绿 Pod 的边界
   StatefulSet 数据敏感不主动迁移
   蓝绿 Pod 由 v2.6 traffic 控制不参与
```

---

## 三层衔接逻辑

### 4.1 时间维度的接力

```
项目生命周期视角：

t=0     项目创建（kp init）
        ↓
        🟢 Layer 1: kp ai-plan
           扫描代码 + LLM 推理
           输出 components.yaml
           
t=部署  kp deploy
        ↓
        🟢 Layer 3 部分介入：
           部署时 bin packing（如 v3.0 已实现）
           或回退到 K8s 默认调度（v2.x）
        
t=1d    项目运行 1 天
        ↓
        🟡 数据不足
           Layer 1 配置仍生效
           Layer 2 跑 DP 但 Confidence < 0.5，不应用
           Layer 3 周期性重调度（如 v3.0 已实现）

t=7d    项目运行 7 天
        ↓
        🟢 Layer 2 接管
           kp sizing recommend → Confidence > 0.7
           写入 resources.yaml
           Layer 3 消费新 sizing 重新 bin packing
        
t=持续  长期运行
        ↓
        🟢 Layer 2 + Layer 3 持续优化
           Layer 1 退出主流程
        
t=业务大变  
        ↓
        🟢 用户主动 kp ai-plan --desc "新业务"
           Layer 1 重置基线
           Layer 2 历史数据清零或降权
           回到 t=0 节奏
```

### 4.2 数据流（单向）

```
AI / Sizing → Scheduler  (单向)

Scheduler 不反向影响 AI / Sizing
Scheduler 失败时降级到 K8s 默认调度，但不修改 sizing
sizing 失败时降级到 Layer 1，但不影响 scheduler
```

### 4.3 决策可解释性

```
任意时刻用户可以问：
  
  $ kp explain --pod my-app-abc123
  
  Pod my-app-abc123:
    sizing 来源：Layer 2 (DP), Confidence=0.85
    sizing 输入：最近 7 天 metrics（10080 数据点）
    sizing 决策：cpu=220m, mem=200Mi
    
    调度来源：Layer 3 (双 DP), 第 2 次迭代收敛
    调度输入：12 节点 × 78 Pod
    调度决策：node-3
    调度理由：node-3 剩余 CPU 280m / Mem 350Mi 最适配
    
    上次决策：5min 前
    下次评估：~10min 后

→ 决策可见 → 用户信任 → 系统可调试
```

---

## 与历史 v2.x 决策能力的关系

### 5.1 v2.0 命令式蓝绿（kp deploy --bluegreen + kp promote）

```
仍可用，与三层栈正交。

v2.0 命令式蓝绿是"用户主导决策"：
  - 用户决定何时部署新版本
  - 用户决定何时切流量
  - 不需要 AI / DP 介入

三层栈是"用户声明意图，系统决策"：
  - 用户写业务意图
  - 系统决定资源配置 + 节点放置

两者面向不同场景：
  - 用户精确控制 → v2.0 命令式
  - 用户希望系统兜底 → 三层栈

v3.0 起，KubePivot 同时提供两种模式。
```

### 5.2 v2.6 声明式蓝绿（resources.yaml + sandbox commit）

```
流量切换是"决策结果的应用"，不是决策本身。

v2.6 蓝绿决定的是：
  哪个 service 接收流量？（blue 还是 green）

三层栈决定的是：
  每个 Pod 应该多少资源？应该在哪个节点？

两者正交，可以叠加：
  - Layer 2 算出 Pod sizing
  - Layer 3 决定 Pod 节点放置
  - v2.6 traffic 决定 service 流量切换
  - 三件事互不干扰
```

### 5.3 v2.7 Event Stream Infrastructure

```
v2.7 是三层栈的"基础设施层"：
  - 自研 Informer + Cache（v2.5 sharding 集成）
  - metrics-server / Prometheus 接入

数据流：
  v2.7 → Layer 2 (DP Sizing 输入)
  v2.7 → Layer 3 (Scheduler 输入)
  
  v2.7 不直接产生决策
  但没有 v2.7，Layer 2/3 都跑不起来

类比：
  v2.7 是"血液"
  Layer 2/3 是"器官"
  Layer 1 是"备用方案"（无 v2.7 时仍可工作）
```

---

## v3.0 后的展望

```
本节简短记录 v3.0 释放后的潜在演进方向，不展开。

1. 多租户场景下的决策栈
   organization 级别的 sizing 策略
   per-org 资源配额
   依赖 v3.x+ 多租户能力

2. 用户偏好参数
   "我希望优先成本而非性能" / "我要 SLA 99.99%"
   决策栈消费这些偏好做权衡
   依赖 v3.0 决策可解释性的成熟

3. 决策反馈闭环
   用户 override 决策时，系统学习
   "用户多次手动调高 cpu → 调整业务模板"
   这是机器学习方向，是否需要待评估

4. AI 层的演化
   v2.x AI 层用通用 LLM
   v4.x 可能 fine-tune 专用模型
   或引入 RAG（让 LLM 看历史 sizing 决策）

5. 与外部系统的协同
   Karpenter / Cluster Autoscaler 的协同
   "决策栈算 Pod 配置 + Karpenter 算节点"
   是否替代 vs 协同待评估
```

---

## 编辑记录

```
2026-04-27  v0.1 创建（v2.6.0 release 后第二天清晨）
            
            起源：
            qc 4-26 晚上回顾 KubePivot 时
            发现 v2.x 已实现的 kp ai-plan 与 v2.9/v3.0 规划的 DP 调度
            完美互补，不冲突
            
            决定单独成文（不并入 ROADMAP）
            理由：
            - ROADMAP 已 ~2143 行，再加补充会显得"打补丁"
            - 三层决策栈是 KubePivot 的核心 mental model
            - v3.0 release 时这是核心叙事文档
            - 未来其他文档都可以引用此文档

            写作约定：
            - Layer 1 详细（已实现，含代码归档）
            - Layer 2/3 引用 ROADMAP，不展开（避免重复）
            - 衔接逻辑章节是本文档的"心脏"
            - 含每层局限（工程诚实）
            - 不含商业化探讨
```
