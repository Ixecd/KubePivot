# TODO — KubePivot 路线图

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链。
> 乾为天、为尊，枢为核心枢纽。
> 当前：v1.7.0

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

## ⚠️ 已知技术债

| 优先级 | 描述 | 计划版本 |
|--------|------|---------|
| P1 | SSA `--field-manager=kubepivot`：helm v4 不支持此 flag，当前用 `--force-conflicts` 替代。待 helm v4 文档稳定后研究正确姿势 | v1.8.0 或按需 |
| P2 | 蓝绿 timing 统计为 `-`（走 deployBlueGreen 分支，未接入 deployTiming）| v1.8.0 顺手 |
| P2 | `kp upgrade --service` 过滤待 e2e 验证 | v1.8.0 |
| P3 | `kp pvc backup/restore/list` 待 CSI 集群验证 | 有 CSI 环境时 |
| P3 | drift force-sync Controller 审计日志目前只写 slog，etcd 写入路径已实现但待端到端验证 | v1.8.0 |

---

## ✅ 已完成

### v1.7.0 状态漂移治理（终态强权）
- [x] `kp diff --drift`：三级分层（❌ 硬冲突 / ⚠️ 受控偏离 / ℹ️ 已豁免）
- [x] `kp doctor` 集成 drift 告警（有 helm-diff 插件时自动运行）
- [x] `--force-conflicts`：helm v4 SSA 冲突强制解决
- [x] `resources.yaml` 加 `force-sync: true` / `no-sync-fields` 字段
- [x] Controller 30s drift 扫描 loop（StartDriftSyncLoop）
- [x] 漂移审计日志写入 etcd（降级 slog）
- [x] `on-missing: recreate | rollback | scale-down | alert | custom` 全策略
- [x] OOMKilled 检测 + 自动调整 memory limit（+25%，纯函数 bumpMemory，6 个单测）
- [x] CrashLoopBackOff 分析：startup vs runtime，classifyCrashLogs 纯函数，3 个单测
- [x] 运行时崩溃 restarts≥5 自动触发 rollback
- [x] HPA 集成：`components.yaml` 声明 min/max replicas/target_cpu，自动创建 autoscaling/v2 HPA
- [x] WorkQueue worker bug 修复（之前调 Add 而不是 reconcile）
- [x] 蓝绿 drift 检测按 slot 独立运行（blue/green 分别检测）

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
- [x] 双密码过渡期模式（DB 零宕机轮转）
- [x] `kp secret audit`：TLS 证书过期检测（30d warn，7d error）

### v1.5.1 ~ v1.5.0
- [x] 蓝绿 e2e + StatefulSet + etcd 健康监控

### v1.4.0 跨版本迁移
- [x] `kp migrate / compat / diff / upgrade` 全链路

### v1.0.0 ~ v1.3.0 封神 🏆
- [x] DAG + A2 Controller + 供应链安全 + 安全合规基线
- [x] 143 单测全绿，CI -race

---

> 乾枢不是名字，是承诺。
> 配得上这个名字，需要把企业真正卡住的硬骨头一块一块啃掉。
