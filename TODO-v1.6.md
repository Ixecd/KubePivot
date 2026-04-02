# TODO — KubePivot 路线图

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链。
> 乾为天、为尊，枢为核心枢纽。
> 当前：v1.6.0

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

### force-sync（终态强权）
- [ ] `resources.yaml` 加 `force-sync: true` 字段
- [ ] Controller 30s 定时扫描：发现硬冲突 → `helm upgrade --force` 强制对齐
- [ ] `--no-sync` 白名单豁免特定字段
- [ ] 漂移审计日志写入 etcd

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
- [ ] Controller 定时扫描超过 1h 的过期 Session，自动清理残余资源
- [ ] `SIMULATING`：临时 Job 预跑迁移（postgres 事务 DDL 保证安全）
- [ ] `COMMITTING`：真实 migrate + helm upgrade
- [ ] 失败回滚：restore PVC 快照 + helm rollback（双层）
- [ ] `kp unlock --sandbox <uuid> --force --reason "..."`（COMMITTING 阶段禁止）

### DB 迁移 + PVC 快照联动
- [ ] `kp migrate run` 前自动触发 `kp pvc backup`（有 CSI 才执行）
- [ ] 迁移失败自动 `kp pvc restore` + `helm rollback`
- [ ] dirty 状态自动检测 + `kp migrate fix-dirty` 交互式修复
- [ ] `kp upgrade` 集成快照保护

### Header-based Preview + 温和预热
- [ ] `kp init` 模板预留 `virtualservice-preview.yaml`（Istio）
- [ ] `kp deploy --preview`：填充 slot 标签和 Header 匹配
- [ ] `kp warmup --steps 10,50,100 --interval 2m,5m`：线性增量权重
- [ ] warmup 监控 error-rate，超阈值自动中断回滚

---

## 🚨 v1.9.0 — 多集群联邦 + 企业合规

### 多集群管理
- [ ] `kp context add --name prod --kubeconfig ~/.kube/prod.yaml`
- [ ] `kp deploy --env prod`
- [ ] `kp status --all-envs` 跨集群统一视图
- [ ] `kp diff --from staging --to prod`

### 企业合规
- [ ] `kp audit` 导出完整操作审计日志（SOC2/ISO27001）
- [ ] OPA 策略引擎：`kp deploy` 前自动触发策略检查
- [ ] `kp policy add/list/remove`
- [ ] Secret 生命周期：`kp secret audit` + 过期告警（已完成 TLS 检测）
- [ ] `kp secret sync --from vault`（Vault 轻量版过渡）

---

## 🟢 v2.0.0 — 企业级插件平台

- [ ] Vault 深度集成（动态 Secret）
- [ ] `kp self-update`
- [ ] 插件市场：`kp plugin install <name>`
- [ ] Web UI：部署状态可视化 + 实时 Drift 监控
- [ ] Terraform Provider
- [ ] `kp chaos --service <name>`：混沌工程（依托 Chaos Mesh）

---

## 🟢 P2 长期愿景

- [ ] 服务级 FSM（目前是项目级）
- [ ] `kp ai-plan` 规则专家系统（确定性优先于概率）
- [ ] KWOK 万节点 CI 自动化压测流水线

---

## ✅ 已完成

### v1.6.0 Controller HA + 大规模场景 + 可观测性
- [x] Leader Election：etcd 分布式锁（TTL=15s，无 client-go）
- [x] WorkQueue 三集合去重（queue/dirty/processing，防事件风暴）
- [x] Controller RBAC 最小权限（含 leases 读写权）
- [x] `kp doctor --perf`：Apiserver P99 延迟 + 并发度建议
- [x] `kp doctor` helm-diff 插件检测
- [x] `make dev`：一键 build + test + install
- [x] `--parallelism` flag：semaphore 控制同层并发
- [x] `--changed-only`：git diff 增量部署（7 个单测）
- [x] 部署耗时统计（build/push/helm/rollout 各阶段表格）
- [x] KWOK 500 节点压测（DAG P99=40ms，Apiserver P99=562ms）
- [x] 跨 namespace 依赖（`other-ns/svc` 格式，只嗅探不参与 DAG）
- [x] `kp doctor` 跨域嗅探（只读标红，不自愈）
- [x] `kp network gen`：生成跨 ns NetworkPolicy 模板（不自动 apply）
- [x] 结构化 JSON 日志（`LOG_FORMAT=json`，ELK/Loki ready）
- [x] `kp history --export json/csv`

### v1.5.2 Secret 轮转
- [x] `kp secret rotate --strategy=immediate/graceful`
- [x] Secret 引用追踪器（secretKeyRef + Volume 挂载检测）
- [x] 双密码过渡期模式（DB 零宕机轮转）
- [x] `kp secret cleanup`：清理 *_OLD 字段
- [x] `kp secret audit`：TLS 证书过期检测（30d warn，7d error）
- [x] `gencerts.sh`：自签 CA + 证书生成

### v1.5.1 蓝绿 e2e + Bug 修复
- [x] 蓝绿 e2e 验证（green/blue slot + kp promote selector 切换）
- [x] 状态机 bug：DEPLOYING→ROLLING_BACK→RUNNING（修复级联回滚路径）
- [x] resources.yaml StatefulSet 名带项目前缀（修复 resume IDLE 误判）
- [x] `kp upgrade --service` 过滤完整实现
- [x] `kp pvc backup/restore/list` 完整实现（待 CSI 验证）
- [x] 蓝绿 chart 模板修复（Release.Name + skipService）

### v1.5.0 StatefulSet + etcd 健康监控
- [x] etcd 健康检查（连通性/raft index/磁盘）
- [x] VolumeSnapshot 环境检查
- [x] StatefulSet pod 详情展示
- [x] StatefulSet rollout 路由

### v1.4.0 跨版本迁移
- [x] `kp migrate status/plan/run`
- [x] `kp compat check`（oasdiff）
- [x] `kp diff --migrate`
- [x] `kp upgrade` 全链路
- [x] 蓝绿发布 + `kp promote`

### v1.0.0 ~ v1.3.0 封神 🏆
- [x] DAG + A2 Controller + 供应链安全 + 安全合规基线
- [x] 143 单测全绿，CI -race

---

> 乾枢不是名字，是承诺。
> 配得上这个名字，需要把企业真正卡住的硬骨头一块一块啃掉。
