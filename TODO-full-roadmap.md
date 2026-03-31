# TODO — KubePivot 路线图

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链。
> 乾为天、为尊，枢为核心枢纽。
> 名字是要配得上的。
> 当前：v1.5.1

---

## 🚨 v1.5.2 — Secret 轮转（先遣战）

> 目标：一键完成密钥泄露后的全链路响应，不等 v2.0.0 Vault

- [ ] `kp secret rotate --secret <name>` — 更新 K8s Secret 内容
- [ ] Secret 引用追踪器：扫描 `deployments/` 下所有 `secretKeyRef`，找出引用该 Secret 的服务
- [ ] 对引用服务执行 `kubectl rollout restart`，最小化抖动
- [ ] 验证新连接成功（/healthz 健康检查）
- [ ] `kp doctor` 加 Secret 过期时间检查（针对 TLS 证书类 Secret）

---

## 🚨 v1.6.0 — Controller 高可用 + 大规模场景

> KWOK 万节点压测验证

### Controller 高可用（P0）
- [ ] Leader Election：基于 `client-go/leaderelection` + K8s `Leases` 资源
- [ ] Controller 支持多副本（`replicas: 3`），任意节点故障不影响自愈
- [ ] `kp doctor` 深度检查 Controller 存活状态 + RBAC 权限（含 `leases` 读写权）
- [ ] `kp doctor` 检查 `helm-diff` 插件是否安装
- [ ] Controller RBAC 自动补全 `leases` 权限（`kp init` 模板更新）

### 大规模部署性能
- [ ] `--parallelism` flag 并行度控制，防止构建风暴
- [ ] `--changed-only` 增量部署（基于 git diff，只 build/push 有变更的服务）
- [ ] 部署耗时统计（build/push/helm/rollout 各阶段，输出表格）
- [ ] KWOK 万节点压测：DAG 排序性能 + 并行部署稳定性验证

### 多 namespace 依赖
- [ ] `components.yaml` 支持跨 namespace 依赖声明
- [ ] `kp deploy` 跨 namespace 拓扑排序
- [ ] NetworkPolicy 跨 namespace 访问规则自动生成

### 可观测性
- [ ] 结构化部署日志（JSON），支持接入 ELK/Loki
- [ ] `kp history --export` 导出部署历史为 CSV/JSON

---

## 🚨 v1.7.0 — 状态漂移治理（Drift）

> 目标：乾枢作为枢纽，具备"终态强权"，任何非 kp 发起的变更在 13s 内被抹除

### Drift 检测
- [ ] `kp diff --drift` — 用 `helm diff` 对比 K8s 实时资源 vs values.yaml 期望态
- [ ] 展示漂移详情：谁改了什么，改了多少
- [ ] `kp doctor` 集成漂移检测，发现漂移输出告警

### force-sync（终态强权）
- [ ] `resources.yaml` 加 `force-sync: true` 字段
- [ ] Controller 定时扫描任务：发现漂移 → `helm upgrade --force` 强制对齐
- [ ] 漂移审计日志：记录每次漂移事件（谁改的/改了什么/何时被修复）
- [ ] `--no-sync` 白名单：允许部分字段豁免强制同步（如 HPA 管理的 replicas）

### 自愈策略增强
- [ ] `on-missing: recreate | rollback | scale-down | alert | custom`
- [ ] `custom` 策略支持执行用户自定义脚本
- [ ] OOMKilled 检测：自动调整 memory limits 并重新部署
- [ ] CrashLoopBackOff 分析：区分启动错误 vs 运行时错误
- [ ] HPA 集成：`components.yaml` 声明 `min_replicas/max_replicas/target_cpu`

---

## 🚨 v1.8.0 — 迁移原子性 + PVC 快照联动

> 前置：v1.5.1 PVC 备份（CSI 环境）已验证
> 目标："先有护身符，再动手术刀"——迁移前自动备份，失败自动 restore

### DB 迁移 + PVC 快照联动
- [ ] `kp migrate run` 执行前自动触发 `kp pvc backup`（如果有 CSI 支持）
- [ ] 迁移失败时自动触发 `kp pvc restore` 恢复到迁移前状态
- [ ] 同时触发 `helm rollback`（服务层 + 数据层双回滚）
- [ ] 整个链路原子性保证：backup → migrate → (成功/失败 → restore + rollback)
- [ ] `kp upgrade` 集成：自动检测 CSI 支持，有则开启快照保护，无则告警继续

### dirty 状态自动治理
- [ ] `kp migrate status` 检测到 `dirty=true` 时给出自动修复方案
- [ ] `kp migrate fix-dirty` — 交互式修复 dirty 状态
- [ ] 迁移超时自动回滚事务（利用 postgres 事务 DDL）

### Header-based Preview（蓝绿增强）
- [ ] `kp init` 模板预留 `virtualservice-preview.yaml`（Istio）和 `ingress-preview.yaml`（Nginx）
- [ ] `kp deploy --preview` — 填充 slot 标签和 Header 匹配（`x-kp-version: green`）
- [ ] 主流量不动，测试流量通过 Header 路由到新版本
- [ ] 验证通过后 `kp promote` 正式切换（符合"只保护，不越权"原则）

---

## 🚨 v1.9.0 — 多集群联邦 + 企业合规

> 目标：统一管理 dev/staging/prod 多套集群，满足出海合规要求

### 多集群管理
- [ ] `kp context add --name prod --kubeconfig ~/.kube/prod.yaml`
- [ ] `kp deploy --env prod` — 指定目标环境，自动切换 kubeconfig + namespace
- [ ] 跨集群部署状态统一视图：`kp status --all-envs`
- [ ] 环境间配置差异对比：`kp diff --from staging --to prod`

### 企业合规增强
- [ ] `kp audit` — 导出完整操作审计日志（满足 SOC2/ISO27001）
- [ ] `kp scan --sbom --sign` 供应链完整性验证（SLSA Level 2）
- [ ] OPA 策略引擎集成：部署前策略检查（`kp deploy` 自动触发）
- [ ] `kp policy add/list/remove` — 管理企业合规规则

### Secret 生命周期完善
- [ ] `kp secret audit` — 检测 Secret 引用完整性 + 过期时间
- [ ] `kp secret sync --from vault` — 从 Vault 同步密钥（v2.0.0 前的轻量版）
- [ ] Secret 轮转自动化：定时检查证书有效期，临期自动告警

---

## 🟢 v2.0.0 — 企业级插件平台

> 不内置，做插件，控制核心工具复杂度

- [ ] **Vault 深度集成**：完整密钥生命周期管理，动态 Secret
- [ ] **`kp self-update`**：CLI 自身版本升级
- [ ] **插件市场**：`kp plugin install <name>`，社区贡献插件
- [ ] **Web UI**：部署状态可视化大盘，实时 Drift 监控
- [ ] **Terraform Provider**：用 IaC 管理 KubePivot 项目配置

---

## 🟢 P2 — 长期愿景

- [ ] 服务级 FSM（目前是项目级）
- [ ] `kp ai-plan` 规则专家系统：确定性（Deterministic）永远优先于概率（Probabilistic）
  - 第一阶段：静态分析规则（发现 postgres 依赖 → 自动声明 statefulset）
  - 第二阶段：LLM 作为"建议层"，不直接写配置
- [ ] KWOK 万节点 CI 自动化压测流水线

---

## ✅ 已完成

### v1.5.1 蓝绿 e2e + P1 bug 修复
- [x] 蓝绿发布 e2e 验证（green slot 部署 + kp promote 切换 selector）
- [x] 蓝绿 chart 模板修复（Release.Name + skipService）
- [x] 状态机 bug：级联 rollback 后正确回到 RUNNING（DEPLOYING→ROLLING_BACK→RUNNING）
- [x] resources.yaml StatefulSet 名带项目前缀（修复 resume IDLE 误判）
- [x] `kp upgrade --service` 过滤完整实现
- [x] `kp pvc backup/restore/list` 完整实现（待 CSI 验证）

### v1.5.0 StatefulSet 状态同步
- [x] `kp doctor` etcd 健康检查（连通性/raft index/磁盘）
- [x] `kp doctor` VolumeSnapshot 环境检查
- [x] `kp status` StatefulSet pod 详情（ordinal/ready/version）
- [x] `kp deploy` StatefulSet rollout 路由

### v1.4.0 跨版本迁移
- [x] `kp migrate status/plan/run`
- [x] `kp compat check`（oasdiff）
- [x] `kp diff --migrate`
- [x] `kp upgrade` 全链路编排
- [x] 蓝绿发布 + `kp promote`

### v1.3.0 供应链安全
- [x] Trivy CVE + cosign + SBOM
- [x] 品牌焕新：KubePivot/kp

### v1.2.0 安全合规基线
- [x] `kp doctor` 安全检查
- [x] NetworkPolicy + SecurityContext + limits + Secret 管理

### v1.1.0 / v1.0.0 封神 🏆
- [x] DAG + Kahn 拓扑排序 + 级联 rollback
- [x] A2 Reconciliation Controller（~13s 自愈）
- [x] 143 单测全绿，CI -race

---

## 版本节奏总览

```
v1.5.2  Secret 轮转（先遣战）               ← 短平快，2-3天
v1.6.0  Controller HA + KWOK 压测           ← 硬骨头，1周
v1.7.0  Drift 治理（终态强权）              ← 硬骨头，1周
v1.8.0  迁移原子性 + Preview 发布           ← 硬骨头，1周
v1.9.0  多集群联邦 + 企业合规              ← 大版本，2周
v2.0.0  插件平台 + Vault + Web UI           ← 长期目标
```

---

> 乾枢不是名字，是承诺。
> 配得上这个名字，需要把企业真正卡住的硬骨头一块一块啃掉。
