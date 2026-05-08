## v2.8 — Enterprise Governance（实际交付）

### 实际交付内容（2026-04-28 完成）

v2.8 最终交付远超原始计划，以下是各模块的实际实施情况：

#### A. SSO / OAuth 集成（已完成）
- `internal/auth/sso.go` — AuthProvider 接口（Name/Login/VerifyToken）
- `internal/auth/oauth.go` — 完整 OAuth2 + PKCE 流程（混合模式 callback server）
- `internal/auth/dex.go` — Dex/OIDC discovery 自动获取 endpoints
- `internal/auth/google.go` — Google OIDC（endpoints 写死，极稳定）
- `internal/auth/github.go` — GitHub 非 OIDC + /user/emails fallback
- `internal/auth/credentials.go` — ~/.kp/credentials/ 文件管理（0600 权限）
- `cmd/kp/login.go` / `cmd/kp/whoami.go` — CLI 入口
- 0 第三方依赖（标准库 net/http + crypto/sha256 自实现 PKCE）
- 37 个测试用例覆盖 PKCE/state/credentials/callback 完整流程

#### B. RBAC 多团队隔离（已完成）
- `internal/rbac/checker.go` — Checker 接口（Check/Reload）
- `internal/rbac/file_based.go` — FileBasedChecker（原子指针 + 无锁读 + glob 匹配）
- `internal/rbac/types.go` — Team/TeamConfig/Permission 完整定义
- Q-B6=C 跨 team excluded 全局优先（qc 神来之笔，黑名单绝对优先完整实现）
- `cmd/kp/team.go` — kp team 8 个子命令（add/remove/list/show/member/check/validate）
- `cmd/kp/rbac_helper.go` — mustCheck 1 行接入模式（7 个 critical 命令接入）
- `internal/controller/rbac_hook.go` — controller 端 RBAC（fail-open 默认 + ENFORCE 切 fail-close）
- 31 个 RBAC 核心测试 + 22 个 team CLI 测试
- 0 第三方 RBAC 库（不引入 Casbin / OPA）

#### G. 加密 at rest — Sealed Secrets（已完成）
- `internal/sealed/wrapper.go` — kubeseal CLI wrapper（0 Go SDK）
- `cmd/kp/secret_seal.go` — kp secret seal 命令 + --from-literal/--from-file 双模式
- 27 个测试用例（全 mock，CI 不依赖 kubeseal 二进制）
- SOPS + KMS 集成推迟至 v2.8.x

#### H. 镜像签名 + 供应链安全（已完成，4 层级完整交付）
- **H-Level1**: cosign key-based 签名验证 + doctor 检测
- **H-Level2**: syft SBOM 生成（CycloneDX/SPDX 双标准）+ ensureSyftAvailable
- **H-Level3**: 声明式策略 + deploy pre-check hook + `--skip-supply-chain` 逃生阀
- **H-Level4**: keyless OIDC 签名验证 + SBOM OCI 上传（oras wrapper + `--push` flag）
- `internal/supplychain/` — Verifier 接口 + cosign/syft/oras CLI wrappers
- 全部采用 CLI wrapper 模式，0 第三方 Go SDK（与 KubePivot 哲学一致）

#### 其他模块（已完成）
- **Audit 模块**：`internal/audit/` — actor 标准化（SSO email > USER@host > anonymous@host）
  - 7 个 critical 命令接入 audit denied 记录
  - 28 个测试用例覆盖三档降级 + 并发安全
- **Controller 端 RBAC**：`internal/controller/rbac_hook.go` — D-Level1 最小可行交付
  - fail-open 默认 + ENV KUBEPIVOT_RBAC_ENFORCE 切 fail-close

**实际代码量**：远超原始 2200 行预估，SSO/RBAC/Audit/加密/供应链 5 大模块总计约 6000+ 行
**实际交付日期**：2026-04-28

---

## v2.9 — Resource Sizing Engine（实际交付）

### 实际交付内容（2026-04-29 完成）

v2.9 最终交付了从 B-Level1 到 B-Level5 的完整资源优化引擎：

#### B-Level1: 核心 DP 求解器
- `internal/sizing/dp.go` — 2D DP 求解器（CPU 线性 + Mem 线性离散化，80×128=10240 状态空间）
- 得分函数：Profile 驱动权重（web: 0.7/0.3, batch: 0.3/0.7, db: 0.3/0.7, default: 0.5/0.5）
- 历史数据指数衰减：W = 0.5^(days/7)
- `kp sizing recommend` 命令集成

#### B-Level2: Prometheus 生产级集成
- `internal/metrics/prometheus.go` — Prometheus HTTP API wrapper（纯 net/http，0 额外依赖）
- 批量原子写入（临时文件 + os.Rename）
- 全量 Pod 遍历 + 业务语义测试

#### B-Level3: 并发优化 + 置信度灰度 + 动态配置
- 10 并发信号量限流（100 Pods ~1s 完成，保护下游 Prometheus）
- 软/硬失败分级（`isSoftFailure` 关键词匹配 + 降级继续 / 认证错阻断）
- Confidence 灰度拦截（<0.7 跳过自动应用，低置信度主动退回人工审查）
- 动态参数透传（`--prometheus-window/--step` flag + YAML sizing.window/step）

#### B-Level4: 智能推荐 + 可解释性
- `internal/sizing/intelligence.go` — RecommendProfile 函数（纯规则匹配，非 ML）
- CPU/Mem 比值分析 + 内存绝对值 → 自动建议 web/batch/db
- 可解释性：reason 字符串（"compute-intensive pattern (CPU/Mem ratio=2.34 > 2.0)"）
- 保守兜底：置信度 <0.6 → 降级 default，样本不足 → 返回默认 + 低置信度提示
- 渐进启用：auto_profile 默认 false，用户显式开启

#### B-Level5: 生产就绪
- ComputeWithWeights: 支持自定义权重（外部可调）
- 可解释性注释注入（yaml.v3 LineComment API）
- VPA 协调预研（只读模式，生成 VPA YAML 供用户参考）
- 集成测试补全（fallback 链路 + 低样本兜底）

**核心哲学**：算法谦逊——低置信度时主动退回人工审查
**实际代码量**：远超原始 2300 行预估，总计约 3500+ 行（含完整测试）
**实际交付日期**：2026-04-29

---

## v3.0 — 乾枢智能调度系统（实际交付）

### 实际交付内容（2026-04-30 完成）

v3.0 不仅是原计划的“智能调度系统”，更被正式命名为**乾枢 (QianShu)**——寓意“天之枢轴，调度之核心”。最终交付远超原始 6 周计划，在不到一周内完成了从设计到全链路闭环的完整交付：

#### 调度器核心（Level 1-3）
- `internal/scheduler/adapter.go` — 5 个接口解耦（PodLister/NodeLister/MetricsProvider/SizingProvider/PlanWriter）
- `internal/scheduler/bin_pack.go` — FFD + 0-1 背包 DP + 二维 keep 回溯
- `internal/scheduler/coordinator.go` — 双 DP 协同收敛循环（迭代上限 5 次）
- `internal/scheduler/assigner.go` — 贪心 Best Fit 实时分配器（Webhook 热路径）
- `internal/scheduler/plan_writer.go` — GitOps 原子写入 components.yaml
- `internal/scheduler/kubectl_adapter.go` — kubectl 数据源适配

#### Webhook 实时调度（Level 4）
- `internal/scheduler/webhook.go` — HTTPS Admission Webhook 服务器
- `internal/scheduler/cert.go` — 自签 TLS 证书生成（支持 caBundle 注入）
- `internal/scheduler/parser.go` — CPU/Memory 资源解析
- 安全 JSON Patch + 失败放行（Allowed:true）

#### 运行时重调度（Level 5）
- `internal/scheduler/rescheduler.go` — 周期性重调度（5min）+ 不平衡检测 + Pod 迁移
- `internal/scheduler/metrics.go` — 7 个 Prometheus 指标暴露
- 抖动检测 + 多级降级（Level 0-3）+ Pause/Resume 紧急刹车
- PodAssigner 接口解耦

#### 部署集成
- `internal/controller/global.go` — Webhook + Rescheduler 启动 + 分片接管主动同步
- `tools/scheduler_test/main.go` — 真实集群验证工具
- `docs/design/scheduler.md` — 完整调度器设计文档
- `docs/design/rescheduler.md` — 完整重调度器设计文档
- `docs/design/DEPENDENCY_POLICY.md` — 依赖管理政策

#### 脚手架安全基线升级
- `internal/scaffold/helm.go` — 全面重构，生产级安全模板
  - Secret 管理（单一事实来源）
  - RBAC 三元组（SA + Role + RoleBinding）
  - NetworkPolicy 最小化（Postgres/etcd 仅允许业务 Pod 访问）
  - readOnlyRootFilesystem 默认 true（CIS Kubernetes 基准）
  - InitContainer 超时保护

**实际代码量**：远超原始 6150 行预估，调度器核心约 2550 行，完整交付超 4000+ 行（含测试）
**测试覆盖**：18 个调度器测试 + 8 个重调度器测试 + 4 个 Webhook 测试 + 完整前序测试无回归
**实际交付日期**：2026-04-30，总 commit 数 472
---

## v2.8.1 — v2.x 收尾（持续改进，不打 tag）

### 1. 目标

```
v2.x 阶段的"答应了但没做"清单收口。
不打 tag，作为 v2.8.0 → v2.9.0 之间的过渡 commits。

主要任务：
  1. web3-blitz 升级到 v2.6 蓝绿
  2. 数据保护机制（resources.yaml protect: true）
  3. 多环境流量配置传播（kp deploy --from-env）
  4. tools/codegen 多 const block 改造
  5. kp release 自动 push（含 push --tags）
```

### 2. 任务详情

#### 2.1 web3-blitz 升级到 v2.6 蓝绿

```
当前 web3-blitz：
  - 用 v2.0 命令式蓝绿（kp deploy --bluegreen + kp promote）
  - 工作正常但不是 GitOps 友好
  
升级路径：
  1. 写 web3-blitz/configs/resources.yaml 的 traffic 字段
  2. 改 deploy 流程：kp deploy → kp sandbox commit
  3. 测试蓝绿切换 + 失败回滚
  4. 文档化作为"真实生产案例"

价值：
  - 验证 KubePivot v2.6 在真实业务里的可用性
  - example-blue-green 是教学，web3-blitz 是真实
  - 完成后写一篇博客《用 KubePivot v2.6 重做 web3-blitz 蓝绿》
```

#### 2.2 数据保护机制

```
原计划：v2.8.0 主版本含
新计划：放到 v2.8.1（v2.8 主版本聚焦企业治理 4 项）

设计：
  resources.yaml 加 protect: true
  
  示例：
  resources:
    - kind: PersistentVolumeClaim
      name: postgres-data
      protect: true   # ← v2.8.1 新增
  
  reconcile 检测到 protect=true 资源"应该删除/重建"时：
    1. 阻断操作
    2. 返回明确错误
    3. 提示用户：kp confirm <ns>/<resource> 显式确认
  
  kp release 阻断：
    如果即将打 tag 的 commit 包含 protected 资源删除
    → 阻断 release
    → 要求 --force-release-with-data-loss 显式标记
```

#### 2.3 多环境流量配置传播

```
原计划：v2.6.1 候选
新计划：放到 v2.8.1 一并实现

kp deploy --env prod --from-env staging
  从 staging 环境读"已验证的 traffic 配置"
  应用到 prod 环境
  不做"分批切流"，只做"配置传播"

实现：
  - cmd/kp/deploy.go 加 --from-env 参数
  - controller 读 staging configmap 的 resources.yaml
  - 提取 traffic 字段，应用到 prod 部署
  - 工作量：~200 行 + 测试
```

## v3.x+ — 乾枢智能调度演进：GPU → 池化

### 背景与定位

KubePivot v3.0 已具备 CPU/Memory 的部署时调度、运行时重调度及多级降级能力。
v3.1 的核心使命是完成**单维资源维度补全（GPU）+ 二维 DP 升级为三维 DP
（CPU + Memory + GPU）+ 碳排放感知调度**，将乾枢从通用业务调度器升级为
AI 基础设施调度器。

v3.2 在此基础上叠加**池化调度层**（Node Pool / CPU-Memory Pool），
将调度视角从”逐节点贪心”切换到”全局容量治理”，实现碎片率驱动的跨节点迁移。

生产环境对 GPU 调度工具的核心诉求：
- **省钱**：千卡 A100 集群月租金百万级，利用率从 50%→80% 直接省 30% 成本。
- **合规**：2026 年 ESG 强制披露趋势下，需要自动化碳足迹追踪和报告。
- **稳定**：不与现有魔改调度器冲突，不引入不可控风险。

乾枢的策略：先用”省钱”打入技术层，证明稳定高效；再用”合规”上管理层视野，
成为不可替代的”AI 基础设施标准配置”。

### 核心挑战 (对比 v2.9 通用业务)

| 维度 | 通用业务 (v2.9) | AI 训练 (v3.x) | 设计影响 |
|------|----------------|---------------|----------|
| 资源粒度 | CPU: millicores, Mem: bytes | GPU: 整数卡 / MIG 0.1 卡 | 离散化策略需支持混合粒度 |
| 状态空间 | `dp[c][m]` 2D | `dp[c][m][g]` 3D | DP 矩阵膨胀，需稀疏优化 + 剪枝 |
| 浪费惩罚 | CPU/Mem 权重均衡 | GPU 权重 >> CPU/Mem | cost 函数需动态权重 (profile=training) |
| 拓扑敏感 | 无 (只看资源够不够) | NVLink/PCIe/RDMA 带宽敏感 | score 函数增加 `topology_bonus` |
| 调度原子性 | 单 Pod 独立调度 | Gang Scheduling (All-or-Nothing) | 引入 PodGroup + 原子决策 |
| 资源曲线 | 流量周期性抖动 | 阶段性强波动 (预处理→计算) | business-template: training + 动态 headroom |
| 失败代价 | 重启秒级恢复 | 训练中断 = 小时级算力浪费 | 显存 OOM 预测 + 提前扩容/迁移 |

## v3.1 — GPU 调度 + 三维 DP + 碳排放感知

### 前提

**KubePivot v3.0 所有命令、所有 flag、所有默认行为都实际验证一遍，暴露任何隐藏的问题。** 这是我们在进入 v3.1 之前，对现有代码质量做的一次全面体检——不是为了推翻重来，而是为了清晰地知道什么是好的、什么需要修、什么可以再等等。

### 目标

让乾枢具备 GPU 资源感知、Sizing 三维升维（CPU + Memory + GPU）、
碳排放感知调度及拓扑亲和调度能力。不打穿 GPU Operator 的职责范围，
专注”在已有节点上决定 GPU 任务的最优放置”。

v3.1 完成后打 tag，然后进入 v3.2 池化实现。

### 前置任务

- **Write-heavy Cache 性能基准**：在 10k 节点模拟环境下，测试 GPU 分配场景的
  写锁竞争延迟。根据 benchmark 结果决定是否引入细粒度锁（RWMutex per Node）
  或 Lock-free 替换 `UsedMemory` 等数值字段。  
  *验收：输出 benchmark 数据，作为后续优化决策的锚点。*

### 核心交付

#### A. GPU 资源感知层

```
- 扩展 NodeInfo 结构体，新增 GPUInfo 字段：
  型号（A100-SXM4-80GB / H100-PCIe-80GB）
  显存总量/已用量
  设备索引 + 健康状态
  拓扑标记（HasNVLink / Clique / Ring）
- 数据来源：DCGM Exporter → Prometheus → MetricsProvider 接口
- 不引入 nvidia-smi 直接调用
```

#### B. Sizing 引擎三维 DP 升级

```
- 状态空间从 dp[cpu][mem] 升级为 dp[cpu][mem][gpu]（三维 DP）
- 稀疏化：只枚举合理组合（如 gpu_count ∈ {0,1,2,4,8}）
- 新增 profile=training：GPU 权重 >> CPU/Mem
- 显存 OOM 预测：基于历史显存曲线 + 训练阶段识别
- 三维 DP 目标：CPU + Memory + GPU 联合求解最优资源规格
```

#### C. 拓扑感知调度

```
- AffinityScore 引入 NVLink 拓扑评分
- 优先将同训练任务的 Pod 调度到有 NVLink 互联的节点组
- 处理”卡上有显存但没卡可用”的碎片场景：调度器识别单卡任务
  分散占用 8 卡节点的”碎片化”状态，优先将大任务（8 卡）放置到
  连续空闲的节点
- 动态阈值：引入基于等待时间的衰减系数 α，当大任务等待超时后逐步
  释放预留资源给小任务，防止饥饿
```

#### D. Gang Scheduling 协作

```
- 与 Volcano / Coscheduling 社区方案协作，不自己实现 All-or-Nothing
- 乾枢专注”选择最优节点组合”，生成 PodGroup 规范交给社区调度器执行
- 在 Cache Map 中预锁定 GPU 资源，避免竞态
```

#### E. 碎片整理

```
- 周期性检测 GPU 碎片：识别”低显存占用但阻塞大任务调度”的单卡任务
- 场景：某任务申领 80GB 显存实际只用 20GB，节点上有 8 卡大任务排队。
  乾枢发出 Evict 信号，配合 Sizing 引擎给出新规格，重新调度
- 约束：仅处理可 Checkpoint 恢复的训练任务（框架支持），
  不迁移不可恢复的推理服务
```

#### F. 碳排放感知调度

```
- MetricsProvider 接口扩展 CarbonIntensityProvider（注入，非侵入）
  - 碳强度数据来源：云厂商碳足迹 API / Electricity Maps
  - 预留碳数据源优先级字段（未来可能多源共存）
- CostFactor 公式扩展：
  CostFactor = (GPUPrice × Time) + (CarbonIntensity × Power × CarbonPrice)
- 碳成本进入调度决策的 CostFactor（默认权重 0，用户显式启用）
- kp scheduler status 输出当前碳成本估算
```

#### G. VPA 共存模式

```
- resources.yaml sizing.mode 支持 vpa 选项
- mode=vpa 时乾枢不干预，VPA 负责运行时调整
- mode=auto 时乾枢部署时优化 + 周期性重调度
- 两者通过 annotation 识别对方，避免无限循环
```

#### H. 可观测性增强

```
- kubepivot_gpu_scheduling_decisions_total（GPU 调度决策计数）
- kubepivot_gpu_fragmentation_ratio（GPU 碎片率）
- kubepivot_gpu_migration_total（GPU 任务迁移计数）
- Grafana dashboard：GPU 利用率热力图 + 碎片率趋势 + 碳排放趋势
- 告警规则：GPU 碎片率 > 40% → warning，单节点显存闲置 > 50% → warning
```

### 务实声明

- **不替换 GPU Operator**：依赖 K8s 设备插件暴露 GPU 资源，乾枢只做调度决策
- **不实现 Gang Scheduling 全栈**：依赖社区成熟方案，乾枢专注”选哪个节点”
- **碎片整理仅处理可恢复任务**：不可 Checkpoint 的推理服务不主动迁移
- **碳感知默认关闭**：不给用户增加无法理解的调度行为，用户显式启用后生效
- **不感知 IB 网络拓扑**：v3.1 NVLink 亲和优先，IB 拓扑感知留后续迭代

### 验收

```
✓ kp sizing recommend --pod=gpu-training-pod 输出 GPU 推荐值
✓ 三维 DP 求解器通过 GPU 维度扩展测试
✓ 拓扑感知调度：8 卡训练任务的所有 Pod 被调度到同一 NVLink 组
✓ 碎片检测：识别”有显存但无整卡”的场景并发出警告
✓ DCGM 数据流：Prometheus → MetricsProvider → Sizing 引擎链路畅通
✓ kp scheduler status 展示碳排放估算
✓ Write-heavy Cache 基准数据输出
✓ make dev 全绿
```

---

## v3.2 — ai-plan 2.0 + KV Cache + GPU 共享 + 池化 + CBA

> 设计文档：
>   [docs/design/informer-kv-cache.md](docs/design/informer-kv-cache.md) /
>   [docs/design/pooling-migration.md](docs/design/pooling-migration.md) /
>   [docs/design/gpu-sharing-carbon-kink.md](docs/design/gpu-sharing-carbon-kink.md) /
>   [docs/design/cell-based-architecture.md](docs/design/cell-based-architecture.md) /
>   [docs/design/ai-plan-2.0-draft.md](docs/design/ai-plan-2.0-draft.md)

### 目标

v3.1 做完 GPU 整卡 + 三维 DP + 碳感知基础设施后，v3.2 推进五件事——
按依赖顺序：

1. **ai-plan 2.0** — 多语言依赖扫描 + GPU 库检测 + Sizing 反馈循环
2. **Informer KV Cache** — 调度器数据通路从 kubectl 切换到内存 O(1) 读
3. **GPU 共享** — MPS/TimeSlicing/MIG Auto-Config，利用率 40%→85%
4. **池化调度层** — 全局容量治理，碎片率驱动的跨节点迁移
5. **CBA** — Cell-based Architecture，有状态故障隔离

顺序逻辑：
- ai-plan 2.0 独立于 KV Cache 和池化，可以先做
- KV Cache 是池化的数据通路前提（池化需频繁扫全集群）
- GPU 共享依赖池化提供全局碎片视图（哪张卡碎片严重）
- CBA 依赖 MigrationManager（池化 C）的 Fencing 协议

### 核心交付

#### A. ai-plan 2.0（先导，可独立推进）

```
- Phase 1 增强：多语言依赖扫描（requirements.txt/pyproject.toml/package.json 等）
- GPU 库检测：torch/tensorflow/vllm 等推断 GPU 需求
- Sizing 反馈循环：冷启动 vs 热更新 / code-metric 关联 / OOM 感知
- GPU 智能检测三层：代码 imports → 环境依赖 → Dockerfile base image
- Confidence Score：每决策因子可信度展示
- 最终输出：framework + profile + GPU 字段留白（sizing + GPU detection 填充）
```

#### B. Informer KV Cache

```
- PodCache + NodeCache: map[string]*PodInfo + byNode 二级索引
- 双缓冲 PutBulk: 持锁 341ms → 纳秒级指针交换
- MODIFIED Fast Pre-check: 调度字段无变化跳写锁 + 通知
- atomicUpdate() 封装: pods + byNode 联合原子更新
- 410 Gone 重建: 先建完整新快照，再原子替换
- Go map 内存压缩: fragmentation_ratio 指标 + PutBulk 强制压缩
- InformerAdapter: 实现 PodLister/NodeLister，影子降级到 kubectlAdapter
- Subscribe 回调 + 30s debounce + BulkResync jitter
- 7 个监控指标（含 Watch Heartbeat stale 检测）
```

#### C. GPU 共享

```
- MPS: Multi-Process Service，多推理 Pod 共享单卡（48 client 上限）
- TimeSlicing: 时间片轮转，允许 3x 超卖
- MIG Auto-Config: nvidia-smi mig wrapper 动态重组硬件分区
- RecommendedGPU int → float64（0.1 粒度）
- resources.yaml sharingMode: mps | timeslicing | mig | exclusive
```

#### D. 池化分层拓扑

```
- Node Pool（物理边界）：从 Node Label 推导，第一道过滤
- CPU/Memory Pool（逻辑聚合）：Node Pool 自动投影，池级容量 + 碎片率
- PoolInfo 数据结构 + computePoolUtilization 按池聚合计算
- PoolScore 动态健康度字段
```

#### E. MigrationManager 迁移执行引擎

```
- Rescheduler（决策）+ MigrationManager（执行）职责分离
- 异步迁移状态机：Evicting → WaitingForReady → Verifying → Complete
- Pod Annotation Bundle 持久化迁移状态（7 个 key）
- 僵尸迁移检测 + Annotation 收尾清理
- 并发迁移数按池健康度动态调整
```

#### F. Fencing 协议（Stateful Cell）

```
- Sidecar Informer Watch 自身 Pod Annotation 感知 Fencing 阶段
- Sidecar → 业务容器 gRPC 信号注入（ACK + 重试）
- Point of No Return：进入 Fencing 后不允许自动回滚
- fencing-ready: true 标记确认后推进 Complete
```

#### G. 池级碎片整理

```
- 碎片率传感器：周期性检测池内”碎片率 > 阈值”
- 池内迁移：小 Pod 挪走腾空间给大 Pod
- 池间迁移：高负载池 → 低负载池均衡
- Dry-run 模式：只记录不执行，验证碎片算法稳定性
```

#### H. CBA（Cell-based Architecture）

```
- 2D Matrix-orchestration: Shard × Workload-class
- Workload-class 自动判定（Stateless vs Stateful）
- Cell-to-Pod 映射管理
- 相对健康排名 + 故障可视化
- 前置条件：集群 ≥ 100 ns + 有状态迁移路径就绪
```

### 务实声明

- **ai-plan 2.0 独立推进**：不依赖 KV Cache 或池化，先交付再集成
- **GPU 共享依赖池化**：碎片视图对共享决策至关重要，共享在池化之后
- **CBA 依赖 MigrationManager**：Fencing 协议是 CBA Stateful Cell 的基础
- **KinK 贯穿始终**：tools/kink/ 独立编译，不增 kp 二进制体积

### 验收

```
✓ ai-plan 2.0: 多语言 dp detect 输出 GPU 推荐（不含真实 GPU 验证）
✓ KV Cache: InformerAdapter 替换 kubectlAdapter，ListAllPods < 1ms
✓ GPU 共享: MPS 48 client 上限 + TimeSlicing 3x 超卖 + MIG 分区
✓ PoolInfo 聚合计算（碎片率 / 有效容量 / PoolScore）
✓ 池间不平衡检测 + 池内碎片率阈值触发
✓ MigrationManager 状态机完整路径（含 Fencing）
✓ Dry-run 模式输出迁移建议不执行实际 evict
✓ make dev 全绿
```


## v4.0 — 平台化探索（所有商业化形式均基于 MIT 开源内核）

### 触发条件

```
以下任一条件发生 → 评估是否启动 v4.0：

  1. KubePivot 在 GitHub 收获 1000+ stars
  2. 有真实多租户需求的客户出现
  3. 商业方向明确（KubePivot Cloud / 商业版 / 咨询）
  4. qc 决定将 KubePivot 作为主业

任一条件不满足 → v4.0 不启动，v3.x 进入维护期
```

### 探索方向（备忘）

```
多团队管控（已完成，v2.8）:
  - teams.yaml + RBAC + namespace 隔离
  - 跨 team excluded 全局优先

Org 级多租户（v4.0 候选）:
  - organization 数据模型（etcd 新 namespace）
  - 跨 org 隔离（namespace 前缀 + RBAC）
  - 资源配额（CPU / Memory / GPU / 项目数）

商业化探索:
  - 开源 + 企业版双轨（GitLab / Sentry 模式）
  - SaaS 化（KubePivot Cloud）
  - 咨询 + 定制
```


## v3.2 — 实际交付回顾

v3.2 的实际交付与原始计划有显著偏差。原始计划包括 ai-plan 2.0、GPU 共享、MIG Auto-Config 等，
实际交付聚焦于 KVCache 性能冲刺 + Controller 全接线 + Config 系统化 + 文档补全。

### 实际交付内容（2026-05-08）

- **KVCache 性能冲刺**：Delta buffer / ShardedPodCache 16 路分片 / merge-on-read / RV CAS / 内存放大率 1.13x
- **迁移引擎 + CBA**：MigrationManager 五阶段状态机 / HashRing / FencingConfig / MigrationTargetHint
- **池化调度**：PoolUtilTracker O(1) 原子计数器 / PoolUtilCache / PoolImbalancePair
- **Controller 接线**：Pod Informer + Node Informer → Bridge → KVCache / OOM 自动调优 / CrashLoop 自动 rollback
- **Config 系统**：system.yaml 6组31字段 / kp controller update --apply / kp sync
- **构建系统**：buildx --push 一条命令 / Makefile push.multiarch / Dockerfile controller + kp 双镜像
- **文档**：docs/cmd/ 48 个子命令补齐 / docs/reference/ 三篇参考文档 / FORGET.md 更新

### 未按原计划交付

- **ai-plan 2.0**：Phase 1-5 已实施（`internal/ai/` 10 文件），已知局限为 GPU 关键词粗粒度 / 显存推断静态
- **GPU 共享（MPS/TimeSlicing）**：MIG 分区量化已实施（`gpu_policy.go`），MPS/TimeSlicing 未做
- **GPU 3D DP**：保守模式占位，等 DCGM 数据流
- **碳感知基础设施**：✅ 已交付。`metrics/carbon.go` CarbonIntensityProvider + CarbonSDKClient（15min TTL）+ `kp scheduler status --carbon-region`

---

## 时间线与依赖（更新）

```
v2.6.0 → v2.7.0 → v2.8 → v2.9 → v3.0（全部已完成，2026-04-26 ~ 04-30）
  ↓
v3.1 部分交付（GPU 整卡基础 + sizing GPU profile + 碳感知 flag 预留）
  ↓
v3.2 交付（KVCache 性能冲刺 + 迁移引擎 + Controller 接线 + Config 系统，2026-05-08）
  ↓
v3.3 Watch 接线 + Fencer + GPU 3D DP + 背压机制（当前）
  ↓
v4.0 平台化探索（触发条件满足后启动，预计 2027+）
```

**关键依赖**：
- v3.3 Watch 接线依赖 Informer Resource → PodEntry 转换链路（已部分完成）
- Fencer 依赖 etcd Learner sidecar gRPC 接口
- GPU 3D DP 依赖 DCGM 数据流就绪

**务实声明**：
- v3.1 / v3.2 的实际交付均偏离原始 ROADMAP 计划，反映了”根据真实需求动态调整优先级”的工程哲学
- v4.0 的启动时机不是时间驱动的，是条件驱动的
