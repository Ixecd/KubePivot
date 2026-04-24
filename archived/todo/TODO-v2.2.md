# TODO — KubePivot 路线图

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链
> 乾为天、为尊，枢为核心枢纽
> 当前：v2.2.0 ✅ 真实集群自愈闭环通过

---

## 🔴 v2.3.0 — 架构级跃迁

> 这一版不是补丁，是架构调整。资源模型从"每项目独立"重构为"集群唯一"。

### P0 — Controller 全局单一 HA（核心）

**现状**：每个项目都部署自己的 controller（namespace-scoped），3 副本。多项目场景严重浪费。

**目标**：集群单一 controller（cluster-scoped），所有项目共享。

- [ ] ClusterRole 升级（cross-namespace list/watch 所有资源）
- [ ] controller 新部署位置：`kubepivot-system` namespace
- [ ] 项目接入方式：通过 annotation 或 label 自声明
  - `kubepivot.io/managed=true`（全局开关）
  - `kubepivot.io/project=<name>`（项目维度）
  - `kubepivot.io/reconcile=enabled`（per-resource 细粒度开关）
- [ ] Label/annotation selector watch（替代现有的 resources.yaml 配置）
- [ ] Leader Election 从 namespace 级升到 cluster 级
- [ ] 状态机 key 从 `kubepivot/<project>/<ns>/state` 改为更通用格式
- [ ] `kp init` 默认不再在项目里部署 controller（除非 `--standalone-controller`）
- [ ] 迁移指南：v2.2.0 → v2.3.0 如何从 per-project 迁到 global

**收益估算**：
```
3 个项目场景：
  v2.2.0：3 × 3 = 9 个 controller pod
  v2.3.0：      3     个 controller pod
  节省：6 个 pod（~2GB 内存）
  
10 个项目场景：
  v2.2.0：30 个 pod（~10GB 内存）
  v2.3.0：3 个 pod（~1GB 内存）
  节省：27 个 pod，~9GB
```

### P0 — Controller 启动状态恢复

**现状**：controller 启动时状态机默认 IDLE，不知道上次 kp deploy 完成到哪个阶段。自愈成功后尝试 IDLE → RUNNING 触发状态机转换错误。

**目标**：controller 启动时从 etcd 读取最后一次状态，恢复内存状态机。

- [ ] `loadStateFromEtcd()` 启动时读 key `kubepivot/<project>/<ns>/state`
- [ ] 如果 etcd 无记录 → 保持 IDLE
- [ ] 如果 etcd 有记录 → `sm.ForceState(lastState, "controller: 从 etcd 恢复")`
- [ ] 恢复 syncStateRunning 的正常转换逻辑（不再需要"尊重契约"的降级策略）

### P0 — helm --history-max 限制

**现状**：每次自愈都执行一次 helm rollback，revision 数量不断累积。`release=feelings-server-feelings-server revision=4` 已经到 4 了，一个周末能到几十上百。helm release secret 会撑爆 namespace。

**目标**：控制 history 数量，自动清理老旧 revision。

- [ ] helm rollback 命令加 `--history-max=10`
- [ ] controller 定期 GC：超过阈值的旧 revision 自动删除
- [ ] 配置项暴露到 values.yaml

### P1 — rbac.yaml embedded template

**现状**：`internal/scaffold/helm.go` 里 rbac 是 Go 源码 fmt.Sprintf 拼接的长字符串。tab/空格缩进被 Go 源码格式污染过一次。

**目标**：改用 `embedded_templates/controller/rbac.yaml.tmpl`，text/template 变量替换。

- [ ] 创建 `internal/scaffold/embedded_templates/controller/rbac.yaml.tmpl`
- [ ] `writeControllerChart` 改为 embedded 读取 + 变量替换
- [ ] 同时做：configmap.yaml / deployment.yaml 一起迁移

### P1 — Dockerfile 多架构容错

**现状**：`docker buildx` 拉基础镜像时 GitHub API 经常限流或 auth.docker.io EOF，需要手动重试 3-5 次。

**目标**：构建脚本自动重试 + 国内镜像源降级。

- [ ] `scripts/build-image.sh` 封装多架构构建
- [ ] 3 次重试逻辑
- [ ] Docker 镜像加速器配置文档（Daocloud / USTC / 阿里云）
- [ ] 国内 Helm 源降级（mirrors.aliyun.com/helm/）

### P2 — 顺手修

- [ ] web3-blitz 蓝绿 release 名解析（-blue/-green 后缀支持）
- [ ] web3-blitz-web3-blitz-controller pending-upgrade secret 清理
- [ ] 蓝绿 timing 统计接入 deployTiming 表格
- [ ] Controller GC Loop 端到端验证
- [ ] drift etcd 审计端到端

---

## ⚠️ 已知技术债（诚实清单）

| 优先级 | 描述 | 原因 | 计划 |
|--------|------|------|------|
| P1 | SSA `--field-manager=kubepivot`：helm v4 不支持，当前用 `--force-conflicts` | helm v4 API 限制 | helm v4 文档稳定后 |
| P2 | SIMULATING K8s Job：代码完整，待 K8s + postgres + golang-migrate 环境验证 | 无匹配测试环境 | 有环境时 |
| P2 | PVC 快照联动：待 CSI VolumeSnapshot 环境验证 | 无 CSI 集群 | 有 CSI 时 |
| P2 | patchIstioWeight / patchNginxWeight：待 Istio/Nginx 集群验证 | 无对应集群 | 有环境时 |
| P2 | sampleErrorRate Prometheus：待 http_requests_total 指标验证 | 无 Prometheus | 有环境时 |
| P3 | Vault 深度集成：当前 net/http 实现可用，考虑 Vault Go SDK | 当前实现已够用 | v2.3.0+ |
| P3 | `kp sync` 真实用户验证：v1.x 项目升级到 v2.x 框架 | 待第一批用户 | v2.3.0 |
| P3 | `kp init --type minimal/full` | 需求调研 | v2.3.0+ |

---

## 🟢 v2.4.0 — 社区 + 曝光

> 代码已经够用了，这个版本专注让更多人知道

- [ ] v2.2.0 自愈 demo 录屏（删 Deployment → 10 秒自愈）
- [ ] 技术博客：v2.3.0 架构跃迁（全局单一 controller 的必要性）
- [ ] `CONTRIBUTING.md`：贡献指南
- [ ] `GOVERNANCE.md`：项目治理（CNCF Sandbox 要求）
- [ ] `SECURITY.md`：安全披露流程
- [ ] GitHub Issue 模板（bug report / feature request）
- [ ] 第一批真实用户的 `kp init` 反馈

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

## ✅ 已完成（v1.0.0 → v2.2.0）

### v2.2.0 真实集群自愈闭环 🔱
- [x] `internal/executor/executor.go`：单例 + 信号量限流子进程执行层
- [x] `resolveBin()` 自动识别 scratch 容器 / 本机开发环境路径
- [x] controller 包全量替换 exec.Command（heal / drift_sync / resources / sandbox_gc）
- [x] `cmd/kp/runner.go` runOutput 兼容层走 executor，调用方零改动
- [x] `cmd/kp/deploy.go` ensureSecret CmdKubectl + --context 支持
- [x] etcd key 破坏性升级：`dtk/` → `kubepivot/`
- [x] RBAC 权限补全：secrets 完整 CRUD（helm release state 修复）
- [x] RBAC 补全：deployments/services/configmaps/namespaces/networking 等完整 CRUD
- [x] Dockerfile：Go 1.26 + scratch + helm v3.17.1 硬编码
- [x] 多架构镜像构建（amd64 + arm64 manifest list）
- [x] `syncStateRunning` 尊重状态机契约（不再强行 IDLE → RUNNING）
- [x] `forceUnlockIfSandboxState` 参数瘦身
- [x] 真实集群验证：2026-04-23 <10 秒自愈闭环

### v2.1.0 脚手架适配性 + 扩展性
- [x] `kp sync`：框架文件升级，不动业务代码
- [x] `.githooks/post-receive`：git push → kp deploy（GitOps 愿景落地）
- [x] 错误码体系内嵌 `kp init`（code + response + handler）
- [x] `make gen`：自动生成错误码文档
- [x] `kp deploy` 自动创建 dev Secret
- [x] `COMPONENT_NAMES`：find cmd/ 实现
- [x] `sed -i` 跨平台兼容（macOS/Linux）
- [x] CRD 资源自愈（`isCRDKind` + `healCRDApply`）

### v2.0.0 企业级插件平台
- [x] `kp version` + `kp update`（GitHub releases API self-update）
- [x] `kp plugin install/list/remove`（插件市场）
- [x] `kp chaos`（Chaos Mesh API，4 种混沌类型）
- [x] OPA stdin pipe / drift etcd 审计 / Vault net/http 实现
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
> 2026-04-23 15:43:51，承诺第一次在真实集群里兑现：
> 删除 Deployment，10 秒内无人介入恢复 Running。
>
> 从生日的第一行代码，到 329 commit，到 v2.2.0 自愈闭环，
> 每一行都在问同一个问题：
> 怎样让系统自己解决问题，而不是 AI 告诉你有问题。
