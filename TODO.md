# TODO — KubePivot 路线图

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链。
> 乾为天、为尊，枢为核心枢纽。
> 当前：v1.5.1

---

## 🚨 v1.5.2 — Secret 轮转（先遣战）

- [ ] `kp secret rotate --secret <name>` 一键密钥轮转
- [ ] `--strategy=graceful` 双密码过渡期模式（新旧密码并存，全量重启后提示禁用旧密码）
- [ ] Secret 引用追踪器：扫描 `deployments/` 找出所有引用该 Secret 的服务
- [ ] StatefulSet 按序号滚动重启，Deployment 并行 rollout restart
- [ ] 轮转后自动健康检查（/healthz 验证新连接）
- [ ] `kp doctor` 加 Secret 过期时间检查（TLS 证书类）
- [ ] 审计日志：记录轮转操作人/时间/涉及服务

---

## 🚨 v1.6.0 — Controller 高可用 + 大规模场景

### Controller 高可用（P0）
- [x] Leader Election：`client-go/leaderelection` + K8s `Leases` 资源
- [x] Controller 支持多副本（`replicas: 3`），任意节点故障不影响自愈
- [x] WorkQueue + Rate Limiter：`workqueue.NewRateLimitingQueue` 防事件风暴
- [x] Event Aggregation：大规模变更时聚合同类事件，防 Controller 死循环
- [x] `kp doctor` 深度检查 Controller 存活 + RBAC（含 `leases` 读写权）
- [x] `kp doctor --perf`：测 Apiserver P99 延迟，高延迟时自动降低 `--parallelism`
- [ ] `kp doctor` 加 `helm-diff` 插件检测
- [ ] Controller RBAC 模板自动补全 `leases` 权限（`kp init` 更新）

### 大规模部署性能
- [ ] `--parallelism` flag 并行度控制
- [ ] `--changed-only` 增量部署（基于 git diff）
- [ ] 部署耗时统计（各阶段表格输出）
- [ ] KWOK 万节点压测：DAG 性能 + 并行稳定性

### 多 namespace 依赖
- [ ] `components.yaml` 跨 namespace 依赖声明
- [ ] `kp deploy` 跨 namespace 拓扑排序
- [ ] NetworkPolicy 跨 namespace **只生成模板，不自动 apply**
- [ ] `kp doctor` 跨域嗅探：只读检测跨 namespace 依赖是否存在，缺失则标红提示

### 可观测性
- [ ] 结构化部署日志（JSON），支持 ELK/Loki
- [ ] `kp history --export` 导出 CSV/JSON

---

## 🚨 v1.7.0 — 状态漂移治理（终态强权）

### Drift 检测分层
- [ ] `kp diff --drift`：三级输出
  - ❌ 硬冲突（kp 拥有所有权，将强制同步）
  - ⚠️ 受控偏离（kp 已豁免，透明展示，如 HPA 管理的 replicas）
  - ℹ️ 外部注入（非 kp 字段，如 Istio sidecar，完全忽略）
- [ ] `kp doctor` 集成 drift 告警

### SSA FieldManager（精准所有权）
- [ ] kp 用 `--field-manager=kubepivot` 声明对 `image/env/ports/resources` 的所有权
- [ ] 只对 kp 拥有所有权的字段执行 force-sync
- [ ] 不干预 Istio/HPA/云厂商注入的字段，避免无限套娃

### force-sync
- [ ] `resources.yaml` 加 `force-sync: true` 字段
- [ ] Controller 定时扫描：发现硬冲突 → `helm upgrade --force` 强制对齐
- [ ] `--no-sync` 白名单豁免特定字段
- [ ] 漂移审计日志：谁改的/改了什么/何时被修复

### 自愈策略增强
- [ ] `on-missing: recreate | rollback | scale-down | alert | custom`
- [ ] OOMKilled 检测 + 自动调整 limits
- [ ] CrashLoopBackOff 分析：区分启动错误 vs 运行时错误
- [ ] HPA 集成：`components.yaml` 声明 min/max replicas

---

## 🚨 v1.8.0 — 迁移原子性 + Operation Sandbox + 蓝绿增强

### Operation Sandbox（核心架构）
状态机新增：`LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING`

- [ ] `LOCKED` 状态：禁止其他 `kp deploy`，给所有沙盒资源打 `kubepivot.io/sandbox-id: <uuid>`
- [ ] 沙盒资源用 `ownerReferences` 挂在 `SandboxSession` CR 下，GC 时级联删除
- [ ] Controller 定时扫描超过 1h 的过期 Session，自动清理残余资源（Job/Secret/临时 PVC）
- [ ] `SIMULATING`：临时 Job 预跑迁移（postgres 事务 DDL 保证安全）
- [ ] `COMMITTING`：真实 migrate + helm upgrade
- [ ] 失败回滚：restore PVC 快照 + helm rollback
- [ ] `kp unlock --sandbox <uuid> --force --reason "..."` 紧急解锁
  - 允许阶段：LOCKED / SNAPSHOTTING / SIMULATING
  - 禁止阶段：COMMITTING（DB 正在迁移，禁止强制解锁）

### DB 迁移 + PVC 快照联动
- [ ] `kp migrate run` 前自动触发 `kp pvc backup`（有 CSI 才执行）
- [ ] 迁移失败自动 `kp pvc restore` + `helm rollback`（双层回滚）
- [ ] dirty 状态自动检测 + `kp migrate fix-dirty` 交互式修复
- [ ] `kp upgrade` 集成：有 CSI 则开启快照保护，无则告警继续

### Header-based Preview + 温和预热
- [ ] `kp init` 模板预留 `virtualservice-preview.yaml`（Istio）/ `ingress-preview.yaml`（Nginx）
- [ ] `kp deploy --preview`：填充 slot 标签和 `x-kp-version: green` Header 匹配
- [ ] `kp warmup --steps 10,50,100 --interval 2m,5m`：线性增量权重切换
- [ ] warmup 过程中自动监控新版本 error-rate，超阈值自动中断并回滚流量

---

## 🚨 v1.9.0 — 多集群联邦 + 企业合规

### 多集群管理
- [ ] `kp context add --name prod --kubeconfig ~/.kube/prod.yaml`
- [ ] `kp deploy --env prod`
- [ ] `kp status --all-envs` 跨集群统一视图
- [ ] `kp diff --from staging --to prod` 环境间配置对比

### 企业合规
- [ ] `kp audit` 导出完整操作审计日志（SOC2/ISO27001）
- [ ] OPA 策略引擎：`kp deploy` 前自动触发策略检查
- [ ] `kp policy add/list/remove`
- [ ] Secret 生命周期：`kp secret audit` + 过期告警
- [ ] `kp secret sync --from vault`（Vault 轻量版，v2.0.0 前过渡）

---

## 🟢 v2.0.0 — 企业级插件平台

- [ ] Vault 深度集成（动态 Secret）
- [ ] `kp self-update`
- [ ] 插件市场：`kp plugin install <n>`
- [ ] Web UI：部署状态可视化 + 实时 Drift 监控
- [ ] Terraform Provider
- [ ] `kp chaos --service <n>`：混沌工程（依托 Chaos Mesh）

---

## 🟢 P2 长期愿景

- [ ] 服务级 FSM（目前是项目级）
- [ ] `kp ai-plan` 规则专家系统（确定性优先于概率）
- [ ] KWOK 万节点 CI 自动化压测流水线

---

## ✅ 已完成

### v1.5.1
- [x] 蓝绿 e2e 验证（green slot + kp promote selector 切换）
- [x] 状态机 bug：级联 rollback 后 DEPLOYING→ROLLING_BACK→RUNNING
- [x] resources.yaml StatefulSet 名带项目前缀（修复 resume IDLE 误判）
- [x] `kp upgrade --service` 过滤完整实现
- [x] `kp pvc backup/restore/list` 完整实现（待 CSI 验证）
- [x] 蓝绿 chart 模板修复（Release.Name + skipService）

### v1.5.0
- [x] etcd 健康检查（连通性/raft index/磁盘）
- [x] VolumeSnapshot 环境检查
- [x] StatefulSet pod 详情展示
- [x] StatefulSet rollout 路由

### v1.4.0
- [x] `kp migrate status/plan/run`
- [x] `kp compat check`
- [x] `kp diff --migrate`
- [x] `kp upgrade` 全链路
- [x] 蓝绿发布 + `kp promote`

### v1.0.0 ~ v1.3.0 封神 🏆
- [x] DAG + A2 Controller + 供应链安全 + 安全合规基线

---

> 乾枢不是名字，是承诺。
