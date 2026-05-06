# FORGET.md — 设计文档扫描：未实现 / 未考虑

> 扫描日期：2026-05-05  
> 扫描范围：`docs/design/` 下 34 个设计文档  
> 用途：防止设计已写但实现未跟、边界 case 被遗忘。不要求详尽，但要求具体。

---

## 一、v3.1 当前已设计但未实现（可能）

这些在设计文档中明确标注了方案，但代码还没跟上。

### Scheduler / Rescheduler

1. **`kp sizing recommend` 独立 CLI** — 当前 sizing 只在 `kp deploy` hook 里跑，没有独立命令让用户单独触发推荐。决策栈 §2.6 标注"待完成"。

2. **Confidence-based 自动应用 sizing 建议** — `Confidence < 0.7` 跳过，但高置信度也需用户确认，没做自动应用。决策栈 §2.6 标注"待完成"。
   后续建议：阈值不应硬编码 0.7。Batch 任务启动期 CV 偏高导致置信度偏低是正常现象，
   应根据 Profile 动态调整阈值（如 ProfileGPU 对稀疏数据更宽容，阈值可降到 0.5）。

2a. **VPA 建议仅生成第一个 Pod** — deploy_sizing.go:152 标注"Level6 支持批量"，
    当前只写第一个成功 Pod 到 vpa-suggestion.yaml。GPU 调度场景下批量生成优先级更高。

3. **多项目 sizing 批量报告** — 一次扫全集群所有项目的资源优化建议。决策栈 §2.6 标注。

4. **Layer 3 调度决策可追溯 (`kp explain`)** — 当前 `kp explain` 对 Layer 3 调度决策显示 "not yet available"。决策栈 §3.4。

5. **Rescheduler 自定义指标支持** — 当前只感知 CPU/Memory，不感知 GPU 利用率、网络带宽等。scheduler.md § 行 315。

6. **Jitter 阈值生产校准** — `jitterThreshold=0.95` / `jitterSpikeCount=3` 是经验值，无生产数据反馈校准。scheduler.md § 行 316。

7. **OOM 事件自动接线** — `ReportOOM()` 接口存在，但未从 controller 的 Pod 状态监听器调用。scheduler.md § 行 308。

8. **Pod affinity/anti-affinity / 拓扑约束** — 设计提到应做外层 `ConstraintChecker`，未实现。scheduler.md § 行 95。

9. **GPU/Disk/扩展资源 BinPack** — v3.1 已加 GPU 节点过滤 + dpNode GPU 约束，完整 3D BinPack 待 v3.2。scheduler.md § 行 94。

9a. **GPU 迁移冷启动代价** — GPU Pod 通常伴随数 GB CUDA 镜像拉取。Rescheduler 迁移（含故障迁移）时应评估目标节点是否有镜像缓存，避免驱逐后新 Pod 长时间 ImagePullBackOff。

9b. **显存碎片化（vGPU/共享显存）** — 当前 DP 以整卡为粒度。v3.2 共享场景下离散化步长需从"卡数"细化到"GB"，`compute` 的 GPU 维度量化步长参考 MIG 分区大小。

10. **三维 DP 仅对 training profile 升维** — Web 服务（profile=web/batch/db）无 GPU 需求，继续走 2D DP（cpu+mem）。只有 `profile=training` 且 `resources.yaml` 声明了 `gpu` 字段的任务才激活三维 DP（cpu+mem+gpu）。Solver 入口需按 workload-class 做 dispatch，避免全体状态空间膨胀。

### Controller

11. **OOMKilled / CrashLoopBackOff 自动自愈** — controller 当前只处理资源缺失，不处理 Pod 崩溃场景。controller.md § 行 296，目标 v2.5.0。

12. **Controller Worker Pool Prometheus exporter** — enqueued/done/failed 统计了但只用于 `kp controller status` 输出，未暴露 Prometheus 指标。controller.md § 行 422。

13. **etcd 未配置时 K8s Lease API 选举** — 当前降级为"3 副本单机模式"，计划 v2.4.0 切原生 Lease API。controller.md § 行 430。

### Event Stream / Informer

14. **Bench 3 watch 吞吐测试未做** — 需要 envtest + etcd + kube-apiserver 二进制，是 v2.7 唯一未完成的 bench。eventstream-impl-notes.md § 行 539-553。

15. **atomic.Value lock-free Hot layer 延后** — v2.7.0 用 mutex，计划 v2.7.1+ 切换（如果监控显示锁竞争）。eventstream-impl-notes.md § 行 132-135。

16. **OnShardChanged callback 未订阅** — shard 变更被动靠 30min resync ticker 捕获，goroutine listener 延后 v2.7.1+。eventstream-impl-notes.md § 行 369-372。

17. **Subscriber 级别指标** — v2.7.0 未暴露，担心基数爆炸，v2.7.1+ 预留。eventstream-impl-notes.md § 行 451-458。

18. **Cold layer（mmap 磁盘层）** — 设计 § 描述了 Cold layer 方案但无限期延后。eventstream-draft.md § 行 345-353。

### Traffic / Multi-env

19. **verified-traffic ConfigMap 清理** — 项目清理时不删 verified-traffic ConfigMap，靠 namespace 删除兜底。v2.6.2 候选。traffic-multi-env-impl-draft.md § 行 1178。

20. **verified-traffic 版本签名** — 防 ConfigMap 篡改，v2.8 Enterprise Governance 范围。traffic-multi-env-impl-draft.md § 行 1181。

21. **verified-traffic 新鲜度机制** — `verifiedAt` 太旧应告警，v2.7+ 候选。traffic-multi-env-impl-draft.md § 行 1183。

22. **verifiedTrafficWriter Prometheus 指标** — 写入次数/年龄，需 v2.7 informer 框架。traffic-multi-env-impl-draft.md § 行 1176。

23. **`kp deploy --from-env --sync-to-git`** — 回写 git 给 ArgoCD/Flux 接管。v2.7+ 候选。traffic-multi-env-impl-draft.md § 行 1186。

24. **多环境流量配置传播 (`--from-env`)** — `kp deploy --env prod --from-env staging`，v2.6.1 设计已写但未实现。traffic-layer.md § 行 99。

### Sandbox / Warmup

25. **SIMULATING Job 真实执行验证** — 需要 K8s 集群 + postgres + golang-migrate 镜像。sandbox.md § 行 170-175。

26. **PVC snapshot 集成验证** — 需要 CSI VolumeSnapshot 支持。sandbox.md。

27. **Controller GC 过期清理验证** — 需长期运行集群。sandbox.md。

28. **Istio/NGINX weight patch 验证** — 需真实 Istio/NGINX 集群。preview-warmup.md § 行 187-191。

29. **canary error-rate 采样验证** — 需 Prometheus `http_requests_total` 指标。preview-warmup.md。

### 其他

30. **多语言脚手架（11 语言）** — 已拍板但未实施，当前仅 Go。`--type`/`--no-app` flag、per-language 集成测试。init-multi-lang-draft.md ~7 天 + 21 用例。

31. **`kp doctor --dockerfiles`** — 检查 Docker base image CVE。init-multi-lang-draft.md § 行 1553。

32. **GPU fields in resources.yaml** — 已设计 `gpu` / `gpuCount` / `migProfile` 字段，标注 v3.1 激活。init-multi-lang-draft.md § 行 240-254。

33. **`kp self-update`** — CLI 自更新。upgrade.md § 行 100-101。

---

## 二、v3.2 池化实施依赖（当前仅设计）

34. **PoolInfo + computePoolUtilization** — 数据结构 + 按池聚合计算。

35. **池间不平衡检测 + 池内碎片率** — run() 需要从节点级升级到池级。

36. **Annotation Bundle + 僵尸迁移清理** — 7 个 migration annotation key + Controller 重启扫 annotations 恢复。

37. **MigrationManager 状态机（Stateless 路径）** — Evicting → WaitingForReady → Complete + Rollback。

38. **Fencing 协议（Stateful 路径）** — Sidecar Informer + gRPC 信号注入 + Point of No Return。依赖 etcd Learner sidecar 暴露 gRPC fencing 端点。

39. **Dry-run 模式** — Rescheduler 打 annotation 但不执行 evict，验证碎片算法稳定性。

40. **池定义来源自动聚类** — v3.2 用 Node Label 推导，自动聚类留后续。

---

## 三、已完成但未在真实环境验证

41. **多集群 e2e** — traffic-multi-env 设计声明多集群未验证（仅在单集群多 namespace 测过）。traffic-multi-env-impl-draft.md § 行 894-910。

42. **跨云/跨 region 验证** — 需要真实云 K8s 集群，从未跑过。traffic-multi-env-impl-draft.md。

43. **大规模验证 (>100 ns)** — verifiedTrafficWriter 并发 100+ namespace 未验证，受限 Orbstack 8 GiB。traffic-multi-env-impl-draft.md。

44. **GPU 拓扑正确性** — 单元测试覆盖算法，但 NVLink 拓扑亲和需要至少 2 台 A100 节点真实验证。gpu-scheduling-draft.md § 行 460。

45. **H100/B200/L40S 兼容性** — 理论上 DCGM 标准指标兼容，但从未用这些 GPU 型号测试过。gpu-scheduling-draft.md § 行 464。

46. **24 小时长稳内存泄漏** — 30 分钟没问题，24 小时没测过。performance.md § 行 67。

47. **Event Stream 生产环境验证** — 所有 bench 用 fake data（非真实 K8s API），Linux RSS 用 macOS heap 替代。eventstream-perf.md § 行 185-205 + 300-301。

48. **client-go Informer 对比基准** — 设计多次提到应做，驱动 v2.7 自建 Informer 决策，从未执行。sharding.md § 行 9.2 + performance.md § 行 8。

49. **Performance cube 完整基准** — 只测了 P=50/R=3/N=10 一个点，3D 矩阵（P×R×N）全自动基准未做。sharding.md § 行 9.1。

---

## 四、已设计但未拍板（draft 状态，需 qc 决定做不做）

50. **CBA（Cell-based Architecture）** — 2D Matrix-orchestration / Workload-class 自动判定 / Cell-to-Pod 映射 / 故障隔离策略。整体等 v3.2+ 重新评估。

51. **ai-plan 2.0** — Phase 1-5 ✅ 已全部实施。已知局限：
    - GPU 关键词匹配为粗粒度 `strings.Contains`，文档文件或 `torchvision`（非 CUDA）可能误触发。需增加排除列表或路径过滤。
    - 显存推断静态映射 Llama-70B→80GB，未考虑 4/8-bit 量化。需加量化感知启发式或让用户声明。
    - MIG 推荐仅 1g.10gb 单一规格，缺少 2g.20gb 等多实例推荐逻辑。
    - scanner 仅扫描 `cmd/` 目录，不支持 `services/` 等单目录结构。
    - conf 路径硬编码 `configs/components.yaml`，不支持 HELM 或自定义路径。
    - 2000 字符截断对 `package-lock.json`/`pom.xml` 可能不够。
    - `client.go` `io.ReadAll` 静默忽略错误，网络断开时 `json.Unmarshal` 抛 EOF 而非真实错误。

52. **`kp controller update`** — 三维 sizing 模型（P→S→C）/ 安全截断 / 孤儿 Lease 清理 / 28 个计划测试用例。controller-update-sizing-draft.md。

53. **canary 全功能** — 指标健康检查（5xx rate/p99 latency）/ shadow traffic mirroring。traffic-layer.md。

54. **GPU sharing（MPS/TimeSlicing/MIG Auto-Config）** — v3.2，池化之后。MIG Auto-Config 涉及"写硬件"能力。gpu-scheduling-draft.md / gpu-sharing-carbon-kink.md。

55. **碳感知基础设施** — CarbonIntensityProvider + CarbonSDK 对接 + `kp scheduler status` 展示。v3.1 单卡阶段落地。Waiting Queue 延迟调度留 v3.2。gpu-sharing-carbon-kink.md §3。

56. **KinK 大规模 GPU 模拟工具** — 10000+ 假 GPU 节点 + 6 个测试场景。v3.2，tools/kink/ 独立编译。gpu-sharing-carbon-kink.md §4。

---

## 五、已知设计局限 / 边界 trade-off（不修，但要知道）

57. **FNV hash 分布不均** — 连续短字符串（如 `kp-bench-001..050`）±40% 不均。生产名通常随机所以影响小，但未验证。sharding.md § 行 6.3。

58. **Shard 切换 10s gap** — 切换时 ~10s 无 Pod 在 reconcile 某 shard。mid-deployment 任务会掉，下次 reconcile 捡起。sharding.md § 行 8.2 Q2。

59. **启动时 Lease 竞争** — 3 副本同时启动可能短暂多拿 shard，自愈但产生重复 reconcile。sharding.md § 行 8.2 Q4。

60. **Quota 分配不均** — `ceil(N/replicas)` 下 3 副本 10 shard → 4:4:2 而非 4:3:3，靠 CPU limits 容忍。sharding.md § 行 8.2 Q3。

61. **跨 namespace 依赖不支持** — `kp deploy` 不能处理跨 namespace 的资源依赖。multi-service.md § 行 263。

62. **不支持 per-service rollback** — `kp rollback` 是项目级，不能单 service 回滚。multi-service.md § 行 246-253。

63. **Controller chart 默认 disabled** — 需用户手动 build 镜像 + 填 image 字段 + enable。helm-chart.md § 行 62-68 + 169-179。

64. **Etcd 用 emptyDir 非 PVC** — 生产持久化需手动改 PVC。helm-chart.md § 行 82-83。

65. **IAM groups 未填充** — `mustCheck` 构建 `UserContext` 时 Groups 为空，`group:<name>` 语法未端到端验证。iam-draft.md § 行 455-456。

66. **Audit 日志无轮转** — `~/.kp/audit/rbac.jsonl` 无限增长。低频写入可接受但未解决。iam-draft.md § 行 459-460。

67. **Teams.yaml 注释在 kp team add/remove 时丢失** — `yaml.Marshal` 去注释，gopkg.in/yaml.v3 已知限制。iam-draft.md § 行 461-462。

68. **Audit 与 Auth 隐式耦合** — `audit.ResolveActor()` 直接读 YAML 不 import auth，auth schema 变时静默降级无编译报错。iam-draft.md § 行 463-464。

69. **Controller 内部权限不暴露给 teams.yaml** — `PermDriftSync`/`PermHeal`/`PermSandboxGC`/`PermSweeperLease` 无法配置。iam-draft.md § 行 457-458。

70. **Etcd IAM auth 未激活** — etcd learner bootstrap 文档设计了 username/password/key prefix 隔离，但当前代码未实现，所有操作走单 etcd 连接无 scope 隔离。etcd-learner-bootstrap-draft.md § 行 539。

71. **PV backend NFS 风险** — NFS/网络存储延迟可能触发 Leader Election 超时，设计建议 hostPath/local-storage 但无代码强制。etcd-learner-bootstrap-draft.md § 行 329。

72. **"假 Pod-0" trap** — 分支顺序靠代码正确性保证，无专项测试。etcd-learner-bootstrap-draft.md § 行 90 + 716。

73. **跨 Chart 命名一致性** — Helm `{{ .Release.Name }}` 跨 Chart 不稳定，多 Release 架构需外部注入固定值。scheduler commit c766b37 打脸 11 次后修复。未有自动化检测。

74. **Rollout 状态检查历史 bug** — 曾用 `plan.Name` 而非 release 名称导致 kubectl rollout status 找不到 Deployment。已修复但无回归测试覆盖此 case。

---

## 六、长期演进（v3.x+ / v4.0，纯粹备忘）

75. **多租户决策栈** — org 级 sizing + 资源配额。决策栈 "v3.0 后的展望"。

76. **用户偏好参数** — "cost first" vs "SLA 99.99%" 模式切换。决策栈。

77. **决策反馈闭环** — 用户 override 学习，v4.x。

78. **AI 层演进** — fine-tuned model 或 RAG。决策栈 / ai-plan-2.0。

79. **Karpenter / Cluster Autoscaler 协同** — 未评估。决策栈。

80. **Multi-cluster 支持正式化** — traffic-multi-env 单集群验证过，多集群未。架构设计有预留（KPEnv）。

81. **LinkerdProvider / IstioProvider** — TrafficProvider 接口设计留了坑，当前只有 Ingress + Gateway API。architecture.md § 行 170。

---

## 编辑记录

```
2026-05-05  创建
            扫描 docs/design/ 下 34 个设计文档
            提取未实现功能 + 未验证边界 case + 已知局限
            分 6 类：v3.1 未实现 / v3.2 池化 / 未验证 / 未拍板 / 已知局限 / 长期演进
```
