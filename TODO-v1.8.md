# TODO — KubePivot 路线图

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链。
> 乾为天、为尊，枢为核心枢纽。
> 当前：v1.8.0

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

## ⚠️ 已知技术债

| 优先级 | 描述 | 计划版本 |
|--------|------|---------|
| P1 | SSA `--field-manager=kubepivot`：helm v4 不支持，当前用 `--force-conflicts` 替代 | helm v4 稳定后 |
| P2 | SIMULATING Job：待 K8s 集群 + postgres + golang-migrate 镜像验证 | v1.9.0 之前 |
| P2 | PVC 快照联动：待 CSI 环境验证 | 有 CSI 环境时 |
| P2 | patchIstioWeight / patchNginxWeight：待 Istio/Nginx 集群验证 | v1.9.0 之前 |
| P2 | sampleErrorRate：待 Prometheus + http_requests_total 验证 | v1.9.0 之前 |
| P2 | Controller GC 超期清理：待长时间运行集群验证 | v1.8.1 |
| P2 | 蓝绿 timing 统计为 `-`（deployBlueGreen 未接入 deployTiming）| v1.9.0 顺手 |
| P3 | `kp upgrade --service` 待 e2e 验证 | v1.9.0 |
| P3 | drift etcd 审计待端到端验证 | v1.9.0 |
| P3 | `kp pvc` 待 CSI 集群验证 | 有 CSI 环境时 |

---

## ✅ 已完成

### v1.8.0 迁移原子性 + Operation Sandbox + 蓝绿增强
- [x] 状态机新增：LOCKED/SNAPSHOTTING/SIMULATING/COMMITTING/RESTORING
- [x] `kp sandbox start`：完整 5 阶段流程
- [x] `kp sandbox start --dry-run`：执行计划预览
- [x] `kp sandbox status`：查看当前沙盒状态
- [x] `kp sandbox unlock`：force-unlock（COMMITTING 永远禁止）
- [x] LOCKED 状态阻止其他 `kp deploy`
- [x] SIMULATING：K8s Job 预跑迁移，失败降级 dry-run
- [x] SandboxSession 文件写入 `.kp/sandbox/`，Controller GC 超期清理
- [x] Controller GC Loop（5m 扫描，清理 sandbox-id label 资源）
- [x] `kp deploy --preview`：Istio/Nginx/降级三路生成 Header 路由模板
- [x] `kp warmup`：线性权重步骤 + error-rate 监控 + 自动回滚
- [x] `kp warmup --dry-run`：执行计划预览
- [x] patchTrafficWeight：Istio VirtualService + Nginx Ingress weight patch
- [x] sampleErrorRate：Prometheus ClusterIP 发现 + PromQL 5xx 错误率
- [x] `kp init` 预留 `virtualservice-preview.yaml` 注释模板
- [x] `kp migrate run` 前自动触发 PVC 快照（有 CSI 才执行）
- [x] 迁移失败双层回滚：pvc restore + helm rollback
- [x] `kp upgrade` 集成快照保护
- [x] `kp migrate fix-dirty`：交互式 dirty 状态修复指引
- [x] 5 个 Sandbox 状态机单测

### v1.7.0 状态漂移治理（终态强权）
- [x] `kp diff --drift`：三级分层（Hard/Managed/Exempted）
- [x] `kp doctor` 集成 drift 告警
- [x] `--force-conflicts`：helm v4 SSA 冲突解决
- [x] force-sync Controller 30s 扫描
- [x] no-sync-fields 豁免字段
- [x] 漂移审计日志（slog + etcd）
- [x] on-missing 全策略（recreate/rollback/scale-down/alert/custom）
- [x] OOMKilled 自动调整 memory limit
- [x] CrashLoopBackOff 分析（startup/runtime）
- [x] HPA 集成（autoscaling/v2）
- [x] kp doctor drift 告警

### v1.6.0 ~ v1.0.0
- [x] Controller HA + 可观测性 + 多 namespace + KWOK 压测
- [x] Secret 轮转 + StatefulSet + 蓝绿 e2e + 迁移全链路
- [x] DAG + A2 Controller + 供应链安全 + 安全合规基线

---

> 乾枢不是名字，是承诺。
> 配得上这个名字，需要把企业真正卡住的硬骨头一块一块啃掉。
