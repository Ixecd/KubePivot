# TODO — KubePivot 路线图

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链
> 乾为天、为尊，枢为核心枢纽
> 当前：v2.1.0 ✅

---

## 必须做（简单）
1. 全局清理剩余 exec.Command("kubectl"/"helm")
2. 统一替换为 executor.GetExecutor()
3. 确认所有文件已导入 executor 包

## 优化项（先生说下午改）
4. 重构 kubectlBaseArgs() 移除重复 --kubeconfig
5. 确保所有命令只注入一次 kubeconfig
6. 清理无用注释、格式化代码

## 验证（最后一步）
7. 本地构建 bin/kp 测试
8. 检查日志无 kubectl not found
9. 资源对账 & 自愈功能正常

## 小细节
10. 去掉 Sh() 相关风险调用（scratch 无 sh）
11. Generic 执行器保持白名单安全机制

## ⚠️ 已知技术债（诚实清单）

| 优先级 | 描述 | 原因 | 计划 |
|--------|------|------|------|
| P1 | SSA `--field-manager=kubepivot`：helm v4 不支持此 flag，当前用 `--force-conflicts` 替代 | helm v4 API 限制 | helm v4 文档稳定后研究 |
| P2 | SIMULATING K8s Job：代码完整，待 K8s + postgres + golang-migrate 环境验证 | 无匹配测试环境 | 有环境时 |
| P2 | PVC 快照联动：待 CSI VolumeSnapshot 环境验证 | 无 CSI 集群 | 有 CSI 时 |
| P2 | patchIstioWeight / patchNginxWeight：待 Istio/Nginx 集群验证 | 无对应集群 | 有环境时 |
| P2 | sampleErrorRate Prometheus：待 http_requests_total 指标验证 | 无 Prometheus | 有环境时 |
| P2 | drift etcd 审计 collectDriftAudit：etcd 路径实现完整，待端到端验证 | 需运行中 etcd | 有 etcd 集群时 |
| P2 | Controller GC 端到端：5m Loop 逻辑正确，待长时间运行验证 | 需长跑环境 | v2.2.0 |
| P2 | 蓝绿 timing 统计为 `-`：deployBlueGreen 未接入 deployTiming 表格 | 路径分支问题 | 顺手修 |
| P3 | `kp upgrade --service` 待 e2e | 缺 e2e 场景 | v2.2.0 |
| P3 | Vault 深度集成：当前 net/http 实现可用，考虑 Vault Go SDK | 当前实现已够用 | v2.2.0 |
| P3 | `kp sync` 真实用户验证：v1.x 项目升级到 v2.x 框架 | 待第一批用户 | v2.2.0 |
| P3 | `kp init --type minimal/full` | 需求调研 | v2.2.0 |

---

## 🟢 v2.2.0 — 社区 + 曝光

> 代码已经够用了，这个版本专注让更多人知道

- [ ] 技术博客：掘金/知乎/GitHub Discussions 发布 GITOPS-MANIFESTO
- [ ] `CONTRIBUTING.md`：贡献指南，降低社区参与门槛
- [ ] `GOVERNANCE.md`：项目治理文档（CNCF Sandbox 要求）
- [ ] `SECURITY.md`：安全披露流程
- [ ] GitHub Issue 模板（bug report / feature request）
- [ ] 第一批真实用户的 `kp init` 使用反馈
- [ ] `kp sync` 真实用户验证（v1.x 项目升级到 v2.x 框架）
- [ ] 有 CSI 环境时：验证 PVC 快照联动
- [ ] 有 Istio/Nginx 时：验证 patchTrafficWeight + kp warmup
- [ ] 有 Prometheus 时：验证 sampleErrorRate PromQL
- [ ] Controller GC Loop 端到端验证
- [ ] 蓝绿 timing 统计接入 deployTiming 表格

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
- [ ] Git 原生内嵌完整实现（post-receive + Controller watch Git）

---

## ✅ 已完成（v1.0.0 → v2.1.0）

### v2.1.0 脚手架适配性 + 扩展性
- [x] `kp sync`：框架文件升级，不动业务代码
  - 强制覆盖：`Makefile` / `scripts/make-rules/*.mk` / `.githooks/`
  - 合并更新：`configs/project.env`（新增字段，不覆盖已有值）
  - 永远不动：`cmd/` / `internal/` / `migrations/` / `go.mod`
  - 提示用户：`deployments/`（chart 结构变化，人工决定）
- [x] `kp sync --dry-run`：预览变更不执行
- [x] `kp sync --only scripts/makefile/hooks/env`：精细控制
- [x] `.githooks/post-receive`：git push → kp deploy（GitOps 愿景落地）
- [x] `.githooks/pre-push`：push 前自动跑测试
- [x] 错误码体系内嵌 `kp init`
  - `internal/pkg/code/`：ErrorCode + Error 类型（iota + codegen 注释格式）
  - `internal/pkg/response/`：OK() / Fail() 统一 HTTP 响应
  - `internal/api/handler.go`：直接使用 response.OK
- [x] `make gen`：自动生成错误码文档（`docs/guide/zh-CN/api/error_code_generated.md`）
- [x] `kp deploy` 自动创建 dev Secret（不存在则幂等创建，不阻断部署）
- [x] `create-secret.sh` 去除 web3-blitz 硬编码，改为通用模板
- [x] `kp init` 零配置（embed 模板，`go install` 后直接可用）
- [x] `COMPONENT_NAMES`：改为 `find cmd/` 实现，只编译有 cmd/ 的服务
- [x] `sed -i` 跨平台兼容（macOS/Linux）
- [x] CRD 资源自愈支持（`isCRDKind` + `healCRDApply`）
- [x] 单元测试从 236 → 239

### v2.0.0 企业级插件平台
- [x] `kp version` + `kp update`（GitHub releases API self-update）
- [x] `kp plugin install/list/remove`（插件市场，execPlugin 兜底）
- [x] `kp release` 自动同步 kpVersion 常量
- [x] `kp chaos inject/list/stop/status`（Chaos Mesh API，4 种混沌类型）
- [x] OPA stdin pipe / drift etcd 审计 / Vault net/http 实现
- [x] 单元测试从 143 → 236（+65%）
- [x] GITOPS-MANIFESTO.md（第 329 个 commit，生日数字）

### v1.9.0 ~ v1.0.0
- [x] 多集群联邦 + 企业合规（audit/OPA/Vault/context）
- [x] Operation Sandbox + Header Preview + Warmup
- [x] 状态漂移治理（Hard/Managed/Exempted）
- [x] Controller HA（Leader Election + WorkQueue）
- [x] Secret 轮转 / StatefulSet / 蓝绿 e2e
- [x] DAG 拓扑排序 + A2 Controller + 安全合规基线

---

> 乾枢不是名字，是承诺。
> 配得上这个名字，需要把企业真正卡住的硬骨头一块一块啃掉。
>
> 从生日的第一行代码，到第 329 个 commit，
> 每一行都在问同一个问题：怎样让下一个开发者不必再踩我踩过的坑。
