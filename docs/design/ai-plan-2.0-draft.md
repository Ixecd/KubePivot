# KubePivot ai-plan 2.0 — 从"猜"到"证" 设计草案

> 状态：📝 draft — 待 qc 拍板
> 日期：2026-05-02
> 关联：[ai-plan.go](../../cmd/kp/ai_plan.go) / [client.go](../../internal/ai/client.go) / [sizing](../../internal/sizing/) / [gpu-scheduling-draft.md](gpu-scheduling-draft.md)
> 背景：v1.0 ai-plan 仅靠 LLM 静态猜测资源。2.0 把 KubePivot 已有的 sizing、GPU 检测、
>       Prometheus 数据接入 ai-plan 的决策链，LLM 从"唯一决策者"变成"框架构建者"

---

## 一、核心理念

v1.0 的问题：LLM 拥有 100% 资源决定权。它没见过集群、没读过 Prometheus、不知道 GPU 型号——却要决定 CPU/Mem/GPU 的值。结果是保守（偏大）的静态配置。

v2.0 的核心转变：

```
v1.0: LLM 猜 → 写入 components.yaml → 结束

v2.0: LLM 建框架 → Sizing 填充数据 → GPU 检测补维度 → Dry-run 验证 → Deploy
```

LLM 从"顾问"变成"架构师"——它决定有多少个组件、组件之间的关系、用什么 profile。具体的数值由 KubePivot 自己的 sizing 引擎基于真实数据填充。

---

## 二、五阶段 Pipeline

```
$ kp ai-plan

🔍 Phase 1: 仓库扫描（v1.0 保留，增强）
🧠 Phase 2: LLM 分析（v1.0 保留，输出从"完整配置"变为"框架+profile"）
📊 Phase 3: Sizing 验证（新增——动态反馈环）
🔥 Phase 4: GPU 智能感知（新增——多维扫描）
✅ Phase 5: 生成 + 校验（新增——dry-run → deploy 闭环）
```

### Phase 1: 仓库扫描（增强）

v1.0 扫描内容：目录结构、go.mod、cmd/、Dockerfiles、README。

v2.0 追加：

| 新增扫描项 | 用途 |
|---|---|
| `requirements.txt` / `pyproject.toml` / `package.json` / `pom.xml` / `Cargo.toml` / `CMakeLists.txt` | 多语言依赖检测 |
| `torch` / `tensorflow` / `transformers` / `vllm` 等 GPU 库 | Phase 4 GPU 检测 |
| `nvidia/cuda` 基础镜像 | Phase 4 GPU 检测 |
| 集群已有 Pod（`kubectl get pods -l app=<name>`） | Phase 3 Sizing 热更新 |
| `resources.yaml` 和 `components.yaml` 已有值 | 与现有配置对比 |

### Phase 2: LLM 分析（角色转变）

**v1.0 输出**：完整的 components.yaml（CPU/Mem/Replicas 都是 LLM 猜的）

**v2.0 输出**：框架 + profile + 不确定字段留空

```yaml
# ai-plan v2.0 生成的 components.yaml
components:
  - name: llm-inference
    port: 8080
    image: llm-inference
    profile: gpu                    # LLM 推断: GPU 推理任务
    sizing:                         # LLM 留空，等 Phase 3 填充
      mode: auto
    gpu:                            # LLM 留空，等 Phase 4 填充
      detect: true
    deps:
      - redis
      - postgres
```

LLM 现在只决定三件事：
1. 组件拓扑（有几个服务、依赖关系）
2. 每个组件的 profile（web/batch/db/gpu）
3. 哪些字段应该走 sizing 自动填充

### Phase 3: Sizing 验证（动态反馈环）

```
冷启动: 仓库没有运行历史
  └─ 保持 LLM 的静态推荐（标记为 # Estimated by ai-plan）

热更新: 仓库已在集群运行
  └─ 调 kp sizing recommend --pod=<name> --profile=<auto>
  └─ 获取 Prometheus 过去 24h/7d 指标
  └─ LLM 推荐值 vs Prometheus P95/P99 实测值对比
     ├─ 实测值更低 → 用实测值 + 注释 # Refined by kp-sizing (actual usage: 80Mi)
     └─ 实测值更高 → warn 用户 + 保持 LLM 推荐值
  └─ 终端输出: "检测到该应用已在生产环境运行，实测峰值内存为 80Mi，
               已将 LLM 推荐的 256Mi 自动下调，预计可节省 68% 资源成本。"
```

**冲突解决策略**：

| 场景 | LLM 值 | Sizing 值 | 决策 |
|---|---|---|---|
| LLM 偏保守 | 500m CPU | 120m CPU（P95） | 用实测值 |
| LLM 偏激进 | 128Mi Mem | 512Mi Mem（P99） | warn + 用实测值 |
| 全新服务 | 500m CPU | 无历史 | 用 LLM 值 |
| 指标过期 | - | Staleness > 60s | 用 LLM 值 + warn |

#### Code-Metric Correlation（版本陷阱防御）

如果仓库代码刚刚经历了大规模重构（例如从 Python 换成 Go），Prometheus 给出的旧版本历史指标会误导新版本部署。

```
校验逻辑:
  1. 计算本地代码的 Checksum (SHA256 of components.yaml + 关键源码)
  2. 读集群 Pod 的 image tag 或 annotation
  3. 如果 tag 不一致 + 资源差异 > 50%:
     └─ Sizing 权重从 80% 降至 20%
     └─ 终端提示: "代码变动较大（image tag 不一致），历史指标仅供参考"
```

#### OOMKilled 事件感知

Sizing 不能只看 P95/P99——还要看"死亡记录"。

```
校验逻辑:
  1. 查 Pod events: kubectl describe pod | grep OOMKilled
  2. 过去 48h 有 OOMKilled:
     └─ 强制 limits.memory += 30-50%（即使 P95 很低）
     └─ 终端提示: "⚠️ 该应用近期发生过 OOMKilled，已自动提升 memory 安全余量"
```

### Phase 4: GPU 智能感知（多维扫描）

不是简单的 `grep torch`。三层检测：

**代码层**：

```go
var gpuImportPatterns = []string{
    "torch.cuda", "tensorflow", "jax", "vllm",
    "onnxruntime-gpu", "cupy", "numba.cuda",
    "nvidia.nccl", "cudf",
}
```

**环境层**：

```
Python: 扫描 requirements.txt → torch>=2.0, vllm==0.4.2
Node:   扫描 package.json → @tensorflow/tfjs-node-gpu
Go:     扫描 go.mod → github.com/NVIDIA/go-nvml
Rust:   扫描 Cargo.toml → cudarc
C++:    扫描 CMakeLists.txt → find_package(CUDA)
```

**镜像层**：

```dockerfile
# 检测 Dockerfile 中是否使用 GPU 基础镜像
FROM nvidia/cuda:12.4.1-runtime-ubuntu22.04  → GPU detected
FROM python:3.12-slim                          → no GPU
```

**自动填充**：

```yaml
resources:
  - name: llm-inference
    gpu:
      count: 1
      product: auto-detect     # kp deploy 时根据集群可用 GPU 选择
      mig: false
      # 如果 go.mod 里有 go-nvml → 推断需要 NVLink 感知
      # 如果 requirements.txt 有 vllm → 推断显存需求大 → 推荐 A100/H100
```

**GPU 型号推断启发式**：

| 代码特征 | 推断 GPU 需求 | 推荐型号 |
|---|---|---|
| `vllm` + `Llama-70B` | 高显存 (>40GB) | A100-80GB / H100-80GB |
| `vllm` + `Llama-7B` | 中显存 (>16GB) | A10 / L40S |
| `torch.cuda` + CNN | 中显存 (>8GB) | T4 / A10 |
| `transformers` + `bert` | 低显存 (>4GB) | T4 |
| `diffusers` + `stable-diffusion` | 中显存 + 高算力 | A10 / A100 |

#### 集群能力预检（Cluster Capability Scan）

代码写了 `import torch`，推荐了 A100，但集群里可能只有 T4——甚至没装 NVIDIA Operator。

```
校验逻辑:
  1. kubectl get nodes -o json | jq '.items[].status.allocatable."nvidia.com/gpu"'
  2. kubectl get pods -n gpu-operator | grep nvidia-device-plugin
  3. 结果:
     ├─ 集群无 GPU → warn: "检测到代码需要 GPU，但当前集群未发现可用 GPU 节点"
     ├─ 集群有 GPU 但型号不匹配 → 自动降级推荐到集群实际型号
     └─ 集群支持 MIG (A100/H100) → 小模型优先推荐 mig-1g.10gb 而非整卡
```

#### MIG 感知

```
校验逻辑:
  1. 检测 cluster 是否支持 MIG (nvidia.com/mig-* 资源存在)
  2. 如果 GPU 需求 ≤ 10GB 显存 + MIG 可用:
     └─ 优先推荐 mig-1g.10gb (更省钱)
     └─ 整卡推荐降级为"备选"
```

#### GPU 信任分层

不同于 Sizing（可基于实测数据调整），GPU 检测是**确定性决策**：

| 检测方式 | 信任度 | 行为 |
|---|---|---|
| 发现 `vllm` import | 100% | 直接锁定高显存需求 |
| 发现 `torch.cuda` | 60% | 建议 GPU，但允许用户降级 CPU |
| 实验性库（如 `triton`） | 30% | 仅提示，不写入配置 |

---

### Phase 4.5: 决策信任分 (Confidence Score)

每个决策因子附带信任分，终端展示时用户一眼能判断"AI 是在猜还是在说事实"：

| 决策因子 | 信任分来源 | 行为 |
|---|---|---|
| **LLM** | 提示词匹配度 | 代码结构极简 → 高；逻辑复杂 → 低 |
| **Sizing** | 采样样本量 | Prometheus 5 分钟数据 → 低（< 0.5）；7 天数据 → 极高（> 0.9） |
| **GPU** | 依赖库确定性 | 发现 `vllm` → 直接锁定；发现 `torch` → 建议 |
| **OOM** | 事件确定性 | 48h 内有 OOMKilled → 直接锁定 memory+30% |

**终端展示**：

```
  llm-inference:
    cpu: 200m     (confidence: 0.92 — 7d Prometheus data)
    mem: 16Gi     (confidence: 0.95 — 7d data + OOMKilled in last 48h ⚠️)
    gpu: 1×A100   (confidence: 1.00 — vllm detected in code)
    replicas: 2   (confidence: 0.60 — LLM estimate, no history)
```

---

### Phase 5: 生成 + 校验 + 部署闭环

```
1. 生成 components.yaml → 终端展示 diff（与已有对比）
2. 资源缺口预警 → 检查 namespace Quota vs 新计划资源需求
3. kp deploy --dry-run → 校验 K8s API 可达性、namespace 存在
4. 用户确认 → kp deploy
```

**资源缺口预警**：

在 dry-run 之前，检查目标 namespace 的 ResourceQuota：

```
⚠️  Quota Warning (namespace: prod):
   - CPU:    请求 1200m, 剩余 800m  (缺口 400m)
   - Memory: 请求 4Gi,   剩余 6Gi   (OK)
   - GPU:    请求 1,     剩余 1     (OK)
   - Recommendation: 扩容集群 CPU 配额，或调整其他 Pod 资源

💡 如果忽略此警告，部署后 Pod 可能处于 Pending 状态。
```

这比部署后看 `Pending` 再排查强一个数量级——ai-plan 在代码还没推到 K8s 之前就把问题暴露出来。

**不再生成即结束**。ai-plan 的输出是部署流水线的起点，不是终点。

---

## 三、决策层级模型

```
┌─────────────────────────────────────────┐
│         LLM (权重: 框架 70%)             │
│  组件拓扑 / profile / deps / 策略        │
├─────────────────────────────────────────┤
│         Sizing (权重: 数值 80%)          │
│  CPU / Memory / 副本数 / Confidence      │
├─────────────────────────────────────────┤
│         GPU Detection (权重: 100%)       │
│  GPU count / product / MIG / NVLink      │
├─────────────────────────────────────────┤
│         Human (权重: 最终裁决)            │
│  Dry-run 确认 / 手动 override / Deploy   │
└─────────────────────────────────────────┘
```

每个决策者管自己最擅长的事。LLM 懂代码但不认识集群，sizing 认识集群但不懂代码。人不参与微调——只做最终确认。

---

## 四、交互设计

```
$ kp ai-plan ./my-llm-app

🔍 Phase 1: Scanning repository... (Python + GPU project detected)
   Found: main.py, requirements.txt, Dockerfile
   GPU libs: torch>=2.0, vllm, transformers

🧠 Phase 2: LLM analysis... (Web service with Transformer inference)
   Components: llm-inference, redis, postgres
   Profile: gpu

📊 Phase 3: Sizing Check...
   - llm-inference: running in 'prod'. P95 CPU=120m, P95 Mem=8.2Gi.
     LLM guessed 500m/4Gi → Updated to 200m/16Gi (实测 + 50% headroom).
   - redis: no history. Keeping LLM estimate (200m/256Mi).
   - postgres: no history. Keeping LLM estimate (500m/1Gi).

🔥 Phase 4: GPU Detection...
   - vllm detected → high VRAM requirement.
   - requirements.txt: torch>=2.0, vllm==0.4.2.
   - Recommending 1 × A100-80GB (based on vllm + model size).

✅ Phase 5: components.yaml generated!

  Changes:
  + components/llm-inference: gpu.count=1, gpu.product=A100-80GB
  ~ components/llm-inference: cpu 500m→200m (refined by sizing)
  + components/redis: new
  + components/postgres: new

  Next:
    kp deploy --dry-run   # Preview
    kp deploy             # Deploy now
```

---

## 五、实施路线

| Step | 内容 | 估计 |
|---|---|---|
| Step 1 | Phase 1 增强：`ScanRepo` 加 GPU 库检测 + 多语言依赖扫描 | ~1 天 |
| Step 2 | Phase 2 改造：LLM 输出从"完整 YAML"变为"框架+profile" | ~0.5 天 |
| Step 3 | Phase 3 Sizing 反馈环：`runSizingHook` 接入 ai-plan | ~1 天 |
| Step 4 | Phase 4 GPU 感知：代码/环境/镜像三层检测 + 型号推断 | ~1 天 |
| Step 5 | Phase 5 闭环：dry-run → 交互确认 → deploy | ~0.5 天 |
| Step 6 | 终端输出美化 + 颜色 + diff 展示 | ~0.5 天 |

---

## 六、编辑记录

```
2026-05-02  qc + DeepSeek 起草
    - 五阶段 Pipeline: 扫描 → LLM → Sizing → GPU → Deploy
    - LLM 角色转变: 框架构建者 (70%) + 数据填充交给 KubePivot
    - Sizing 反馈环: 冷启动 vs 热更新 + 冲突解决策略
    - GPU 三层检测: 代码 import × 环境依赖 × Dockerfile 镜像
    - GPU 型号推断: vllm→A100, torch→T4, diffusers→A10
    - 决策层级: LLM(框架) × Sizing(数值) × GPU(100%) × Human(最终)

2026-05-02 v1.1  鲁棒性补强 (qc)
    - Code-Metric Correlation: 代码 Cheksum vs Pod image tag 版本校验
    - OOMKilled 事件感知: 48h 内 OOM → memory+30-50%
    - Cluster Capability Scan: GPU 型号不匹配自动降级 + NVIDIA Operator 检测
    - MIG 感知: 小模型优先推荐 mig-1g.10gb
    - Confidence Score: 每个决策因子附带信任分 + 终端展示
    - GPU 信任分层: vllm=100% / torch=60% / triton=30%
    - Resource Quota Warning: dry-run 前检查 namespace Quota 缺口
```
