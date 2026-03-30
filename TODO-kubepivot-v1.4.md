# TODO — KubePivot 路线图

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链。
> 乾为天、为尊，枢为核心枢纽。
> 名字是要配得上的。
> 当前：v1.3.1 + v1.4.0 WIP

---

## 🚨 v1.4.0 剩余（跨版本迁移）

### 数据库迁移
- [ ] 迁移失败自动回滚到上一个 DB 版本 + helm rollback 联动

### API 版本协同
- [ ] `components.yaml` 支持 `api_version` 字段，声明服务对外 API 版本
- [ ] 部署时检查依赖服务的 API 版本是否满足当前服务要求，不满足则警告

### Helm Values 跨版本迁移
- [ ] `kp diff --migrate` — 不只对比 values 差异，还生成迁移建议
- [ ] 检测废弃字段（deprecated values），升级前主动提示
- [ ] `kp upgrade` — 专门的版本升级命令，自动处理 values 格式变更

---

## 🚨 v1.5.0 — StatefulSet 状态同步（第二块硬骨头）

> 目标：有状态服务的升级/扩缩容不再让人心跳加速

### StatefulSet 滚动升级策略
- [ ] `kp deploy` 支持 StatefulSet 升级策略配置（RollingUpdate/OnDelete）
- [ ] StatefulSet 升级前自动备份 PVC 快照（接 VolumeSnapshot）
- [ ] 升级失败时 PVC 快照自动还原（不只是 helm rollback，还要数据回滚）
- [ ] `kp status` 展示 StatefulSet 每个 pod 的状态（ordinal/ready/version）

### PVC 数据迁移
- [ ] `kp pvc migrate` — 跨存储类迁移（从 local-path 迁到 ceph/longhorn）
- [ ] PVC 扩容检查：升级前自动检测存储是否充足
- [ ] `kp pvc backup` / `kp pvc restore` — 手动备份和还原

### etcd 集群状态
- [ ] etcd 从单节点 Deployment 升级到 3 节点集群的迁移 SOP
- [ ] etcd 数据健康检查（碎片整理、压缩、告警阈值）
- [ ] `kp doctor` 加 etcd 健康项：
  - `etcdctl endpoint health` 连通性
  - `raftAppliedIndex` vs `raftIndex` 差值监控（差值过大预示脑裂风险）
  - 磁盘使用率告警

---

## 🟡 v1.6.0 — 大规模场景支持

> 目标：从 10 个服务扩展到 100 个服务不卡壳

### 多 namespace 依赖
- [ ] `components.yaml` 支持跨 namespace 依赖声明
- [ ] `kp deploy` 跨 namespace 拓扑排序
- [ ] NetworkPolicy 跨 namespace 访问控制自动生成

### 部署性能
- [ ] build 并行度控制（`--parallelism` flag，防止构建风暴）
- [ ] 增量部署：只重新 build/push 有代码变更的服务（基于 git diff）
- [ ] `kp deploy --changed-only` — 只部署本次 commit 涉及的服务

### 可观测性
- [ ] `kp deploy` 输出结构化部署日志（JSON），支持接入 ELK/Loki
- [ ] 部署耗时分析：每个服务 build/push/helm/rollout 各阶段耗时统计
- [ ] `kp history --export` — 导出部署历史为 CSV/JSON

---

## 🟡 v1.7.0 — 真正的自愈（不只是 helm rollback）

> 目标：controller 从"重启型自愈"升级到"智能型自愈"

### 自愈策略插件化
- [ ] `resources.yaml` 支持自定义自愈策略：`on-missing: recreate | rollback | scale-down | alert | custom`
- [ ] `custom` 策略支持执行用户自定义脚本
- [ ] 自愈动作审计日志：记录每次自愈的触发原因、执行动作、结果

### 异常模式识别
- [ ] OOMKilled 检测：自动调整 memory limits 并重新部署
- [ ] CrashLoopBackOff 分析：区分启动错误 vs 运行时错误，给出不同处理策略
- [ ] ImagePullBackOff 自动重试 + 告警（区分网络问题 vs 镜像不存在）

### 容量自愈
- [ ] HPA 集成：`components.yaml` 声明 `min_replicas` / `max_replicas` / `target_cpu`
- [ ] 自动生成 HorizontalPodAutoscaler 模板
- [ ] `kp status` 展示 HPA 当前副本数 vs 目标副本数

---

## 🟢 v2.0.0 — 企业级扩展插件

> 不内置，做插件，控制核心工具复杂度

- [ ] **Vault 集成**：集中式密钥管理，满足金融/出海合规
- [ ] **审计日志**：操作/部署/权限日志持久化，满足 SOC2 审计要求
- [ ] **OPA 策略引擎**：自定义企业合规规则，部署前策略检查
- [ ] **多集群管理**：统一管理 dev/staging/prod 多套集群的部署状态
- [ ] **RBAC 最小权限自动生成**：基于实际资源用量分析，生成最小权限 Role

---

## 🟢 P2 — 长期愿景

- [ ] 服务级 FSM（v2.0，目前是项目级）
- [ ] `kp ai-plan` 规则专家系统优先于大模型
  - 第一阶段：静态分析规则（扫 Dockerfile/go.mod/main.go，确定性输出）
  - 发现 postgres 依赖 → 自动声明 statefulset；发现 etcd client → 自动添加 etcd 组件
  - 第二阶段：大模型作为"建议层"，在规则结果基础上补充说明，不直接写配置
  - 确定性（Deterministic）永远比概率（Probabilistic）更重要，尤其在基础设施层
- [ ] Web UI：部署状态可视化大盘
- [ ] Terraform provider：用 IaC 管理 KubePivot 项目配置

---

## ✅ 已完成

### v1.4.0 跨版本迁移（主体）
- [x] `kp migrate status` — 自动探测 golang-migrate/Atlas，展示 DB 版本 vs K8s 版本对齐状态
- [x] `kp migrate plan` — 扫描待执行迁移文件，正则检测破坏性变更，风险分级，`--output-json` CI/CD 友好
- [x] 部署前自动迁移兼容性检查（集成到 `deployLayers`，破坏性变更阻断部署）
- [x] `kp compat check` — 基于 oasdiff 检测 API 破坏性变更，`--output-json` 支持
- [x] `kp doctor` 加 oasdiff 版本检查
- [x] 蓝绿发布：`kp deploy --strategy=blue-green` + `kp promote`，etcd 优先状态持久化
- [x] canary hook：`scripts/canary-hook.sh` 用户自定义接入点

### v1.x 前置任务
- [x] SBOM 生成（`kp scan --sbom`，CycloneDX 格式）
- [x] cosign keyless 签名（`kp deploy --sign`，`kp scan --verify`）
- [x] ACR 格式支持（`.aliyuncs.com` 自动跳过 `-arch` 后缀）
- [x] 进度输出带颜色（TTY 自动检测，pipe 降级纯文本）

### v1.3.0 供应链安全
- [x] `kp scan` 独立 CVE 扫描（Trivy），支持 `--severity` 自定义阻断级别
- [x] `kp deploy` 自动集成 Trivy 扫描
- [x] `kp doctor` trivy 版本检查
- [x] `tools.mk` 加 `install.trivy` / `install.cosign`（跨平台官方脚本）
- [x] 实战验证：web3-blitz grpc CVE-2026-33186（CVSS 9.1）被扫出并修复
- [x] 品牌焕新：dev-toolkit → KubePivot，dtk → kp，DTK_* → KP_*

### v1.2.0 安全合规基线
- [x] `kp doctor` 安全检查（明文密码/Pod SecurityContext/RBAC/NetworkPolicy）
- [x] `kp init` 模板天生合规：NetworkPolicy + Pod SecurityContext + 资源 limits
- [x] `kp init` etcd chart StatefulSet + PVC
- [x] `kp init` 自动生成 `scripts/create-secret.sh`
- [x] `kp deploy` 前检查 secret 是否存在
- [x] controller RBAC 最小权限
- [x] `kp resume` bug 修复

### v1.1.0
- [x] controller 自愈 e2e 验证（~13s 恢复）
- [x] controller SSA 冲突处理
- [x] `kp status` 多 release 独立展示
- [x] `kp rollback` 拓扑逆序进度
- [x] `kp init --dry-run`
- [x] 修复 controller 三个 bug

### v1.0.0 封神 🏆
- [x] 多服务独立 helm release（`{project}-{service}`）
- [x] DAG + Kahn 拓扑排序 + 级联 rollback
- [x] A2 Reconciliation Controller
- [x] 143 个单测全绿，CI -race
- [x] 全量文档

---

> 乾枢不是名字，是承诺。
> 配得上这个名字，需要把企业真正卡住的硬骨头一块一块啃掉。
