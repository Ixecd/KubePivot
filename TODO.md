# TODO — KubePivot 路线图

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链
> 乾为天、为尊，枢为核心枢纽
> 当前：v2.0.0 ✅

---

## ⚠️ 已知技术债（诚实清单）

| 优先级 | 描述 | 原因 | 计划 |
|--------|------|------|------|
| P1 | SSA `--field-manager=kubepivot`：helm v4 不支持此 flag，当前用 `--force-conflicts` 替代 | helm v4 API 限制 | helm v4 文档稳定后研究 |
| P2 | SIMULATING K8s Job：代码完整，待 K8s + postgres + golang-migrate 环境验证 | 无匹配测试环境 | 有环境时 |
| P2 | PVC 快照联动：待 CSI VolumeSnapshot 环境验证 | 无 CSI 集群 | 有 CSI 时 |
| P2 | patchIstioWeight / patchNginxWeight：待 Istio/Nginx 集群验证 | 无对应集群 | 有环境时 |
| P2 | sampleErrorRate Prometheus：待 http_requests_total 指标验证 | 无 Prometheus | 有环境时 |
| P2 | drift etcd 审计 collectDriftAudit：etcd 路径实现完整，待端到端验证 | 需运行中 etcd | 有 etcd 集群时 |
| P2 | Controller GC 端到端：5m Loop 逻辑正确，待长时间运行验证 | 需长跑环境 | v2.1.0 |
| P2 | 蓝绿 timing 统计为 `-`：deployBlueGreen 未接入 deployTiming 表格 | 路径分支问题 | 顺手修 |
| P3 | `kp upgrade --service` 待 e2e | 缺 e2e 场景 | v2.1.0 |
| P3 | Vault 深度集成：当前 net/http 实现可用，v2.1.0 考虑 Vault Go SDK | 当前实现已够用 | v2.1.0 |

---

## 🟢 v2.1.0 — 稳定性 + 验证

> 代码基本完整，这个版本专注验证和补强

- [ ] 有 CSI 环境时：验证 PVC 快照联动（`kp migrate run` + `kp upgrade`）
- [ ] 有 Istio/Nginx 时：验证 patchTrafficWeight + kp warmup 完整流程
- [ ] 有 Prometheus 时：验证 sampleErrorRate PromQL 查询
- [ ] Controller GC Loop 端到端验证（长时间运行）
- [ ] 蓝绿 timing 统计接入 deployTiming 表格
- [ ] Vault Go SDK 替换 net/http 实现（可选，当前实现已可用）
- [ ] `kp upgrade --service` e2e 验证
- [ ] drift etcd 审计端到端验证

---

## 🟢 v2.2.0 — 社区 + 曝光

> 代码已经够用了，这个版本专注让更多人知道

- [ ] 技术博客：掘金/知乎/GitHub Discussions 发布 GITOPS-MANIFESTO
- [ ] `CONTRIBUTING.md`：贡献指南，降低社区参与门槛
- [ ] `GOVERNANCE.md`：项目治理文档（CNCF Sandbox 要求）
- [ ] `SECURITY.md`：安全披露流程
- [ ] GitHub Issue 模板（bug report / feature request）
- [ ] 第一批真实用户的 `kp init` 使用反馈

---

## 🔵 v3.0.0 — Feelings 基础设施

> KubePivot 作为 Feelings 的底层 CD 平台

- [ ] `kp feelings init`：专为 Feelings 项目定制的脚手架
- [ ] 神经接口服务的部署模板（低延迟、高可用）
- [ ] Feelings Controller：感受数据的实时调谐
- [ ] 多集群跨地域同步（感受数据不能有 SLA 问题）

---

## 🔵 长期愿景

- [ ] Web UI：部署状态可视化 + 实时 Drift 监控
- [ ] Terraform Provider：`kubepivot_project` resource
- [ ] 服务级 FSM（当前是项目级）
- [ ] `kp ai-plan` 规则专家系统（确定性优先于概率）
- [ ] KWOK 万节点 CI 自动化压测流水线
- [ ] CNCF Sandbox 申请（需要社区 + 贡献者多样性）

---

## ✅ 已完成（v1.0.0 → v2.0.0）

### v2.0.0 企业级插件平台
- [x] `kp version` + `kp update`（GitHub releases API self-update）
- [x] `kp plugin install/list/remove`（插件市场，go install + 包装脚本）
- [x] 未知命令自动转发插件（execPlugin 兜底）
- [x] `kp release` 自动同步 kpVersion 常量
- [x] `kp chaos inject/list/stop/status`（Chaos Mesh API，4 种混沌类型）
- [x] OPA stdin pipe（bytes.NewReader，input JSON 正确传递）
- [x] drift etcd 审计（clientv3.WithPrefix 实现）
- [x] Vault fetchVaultKV：net/http 替换 curl
- [x] 单元测试从 143 → 236（+65%）
- [x] 全文档统一大版本更新（architecture/state-machine/quickstart/commands）
- [x] GITOPS-MANIFESTO.md（第 329 个 commit，生日数字）
- [x] 目录结构整理（.DS_Store/.gitignore/dev-toolkit 清理）

### v1.9.0 多集群联邦 + 企业合规
- [x] `kp context add/list/remove/show`
- [x] `kp deploy/status/diff --env <n>`
- [x] `kp status --all-envs`（动态列宽，ANSI-safe 对齐）
- [x] `kp diff --from-env / --to-env`
- [x] `kp audit`（jsonl/csv/table，三来源聚合）
- [x] `kp policy add/list/remove/check`（OPA 策略引擎）
- [x] `kp secret sync --from vault`（net/http 实现，幂等 apply）

### v1.8.0 迁移原子性 + Operation Sandbox
- [x] Sandbox 状态机（5 新状态）+ kp sandbox start/status/unlock
- [x] COMMITTING 永远禁止 force-unlock
- [x] LOCKED 阻止其他 kp deploy
- [x] Controller GC Loop（5m 扫描超期 Session）
- [x] `kp deploy --preview`（Istio/Nginx/降级三路）
- [x] `kp warmup`（线性权重 + error-rate 监控）
- [x] DB 迁移 + PVC 快照联动（代码完整，待 CSI 验证）
- [x] `kp migrate fix-dirty`（只保护，不越权）

### v1.7.0 状态漂移治理
- [x] `kp diff --drift`（三级分层 Hard/Managed/Exempted）
- [x] `kp doctor` 集成 drift 告警
- [x] `--force-conflicts` SSA 冲突解决
- [x] force-sync Controller 30s 扫描
- [x] on-missing 全策略（recreate/rollback/scale-down/alert/custom）
- [x] OOMKilled → memory limit +25%
- [x] CrashLoopBackOff → startup/runtime 分类，自动 rollback
- [x] HPA 集成（autoscaling/v2）

### v1.6.0 Controller HA + 可观测性
- [x] Leader Election（etcd 分布式锁，TTL=15s）
- [x] WorkQueue 三集合去重（防事件风暴）
- [x] `kp doctor --perf`（Apiserver P99 延迟）
- [x] `--parallelism`（semaphore 并发控制）
- [x] `--changed-only`（git diff 增量部署）
- [x] 部署耗时统计表格
- [x] 结构化 JSON 日志（LOG_FORMAT=json）
- [x] KWOK 500 节点压测（DAG P99=40ms，Apiserver P99=562ms）

### v1.5.x Secret 轮转 + StatefulSet + 蓝绿 e2e
- [x] `kp secret rotate --strategy graceful`（双密码过渡期）
- [x] `kp secret audit`（TLS 证书过期检测）
- [x] StatefulSet pod 详情展示
- [x] etcd 健康监控
- [x] 蓝绿 e2e 验证 + P1 状态机 bug 修复

### v1.4.0 跨版本迁移（KubePivot 改名）
- [x] `kp migrate status/plan/run`（golang-migrate + Atlas 自动检测）
- [x] `kp compat check`（oasdiff API 兼容性）
- [x] `kp diff --migrate`
- [x] `kp upgrade` 全链路

### v1.0.0 ~ v1.3.0 封神 🏆
- [x] DAG 拓扑排序（Kahn 算法）+ 并行部署
- [x] A2 Reconciliation Controller + etcd 状态机
- [x] 供应链安全（trivy CVE + cosign 签名）
- [x] 安全合规基线（Pod Security / Network Policy / RBAC）
- [x] 143 单测全绿，CI -race

---

> 乾枢不是名字，是承诺。
> 配得上这个名字，需要把企业真正卡住的硬骨头一块一块啃掉。
>
> 从生日的第一行代码，到第 329 个 commit，
> 每一行都在问同一个问题：怎样让下一个开发者不必再踩我踩过的坑。
