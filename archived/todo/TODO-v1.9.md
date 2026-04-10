# TODO — KubePivot 路线图

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链。
> 乾为天、为尊，枢为核心枢纽。
> 当前：v1.9.0

---

## 🟢 v2.0.0 — 企业级插件平台

- [ ] Vault 深度集成（动态 Secret，替换当前 curl 实现）
- [ ] `kp self-update`
- [ ] 插件市场：`kp plugin install <n>`
- [ ] Web UI：部署状态可视化 + 实时 Drift 监控
- [ ] Terraform Provider
- [ ] `kp chaos --service <n>`：混沌工程（依托 Chaos Mesh）
- [ ] 全文档统一大版本更新（README/docs/gotchas/design 完整过一遍）

---

## 🟢 P2 长期愿景

- [ ] 服务级 FSM（目前是项目级）
- [ ] `kp ai-plan` 规则专家系统（确定性优先于概率）
- [ ] KWOK 万节点 CI 自动化压测流水线
- [ ] OPA 策略 stdin pipe 完整实现（当前 TODO stub）
- [ ] drift audit 从 etcd 读取（当前 slog only）

---

## ⚠️ 已知技术债

| 优先级 | 描述 | 计划版本 |
|--------|------|---------|
| P1 | SSA `--field-manager`：helm v4 不支持，用 `--force-conflicts` 替代 | helm v4 稳定后 |
| P2 | SIMULATING Job：待 K8s + postgres + golang-migrate 镜像验证 | 有环境时 |
| P2 | PVC 快照联动：待 CSI 环境验证 | 有 CSI 时 |
| P2 | patchIstioWeight / patchNginxWeight：待 Istio/Nginx 验证 | 有环境时 |
| P2 | sampleErrorRate：待 Prometheus + http_requests_total 验证 | 有环境时 |
| P2 | OPA stdin pipe：checkOPAPolicies 的 input JSON 还是 TODO | v2.0.0 |
| P2 | drift etcd 审计：collectDriftAudit 是 stub | v2.0.0 |
| P2 | kp secret sync：curl 实现，v2.0.0 换 Vault SDK | v2.0.0 |
| P2 | Controller GC 端到端验证 | v2.0.0 |
| P2 | 蓝绿 timing 统计为 `-` | v2.0.0 顺手 |
| P3 | `kp upgrade --service` 待 e2e 验证 | v2.0.0 |

---

## ✅ 已完成

### v1.9.0 多集群联邦 + 企业合规
- [x] `kp context add/list/remove/show`：多集群环境管理（`~/.kp/envs/<name>.yaml`）
- [x] `kp deploy/status/diff --env <name>`：指定环境部署/查看/对比
- [x] `kp status --all-envs`：跨集群统一视图（动态列宽，ANSI-safe 对齐）
- [x] `kp diff --from-env / --to-env`：环境间 helm values 对比
- [x] `kp audit`：统一审计日志导出（SOC2/ISO27001 友好）
  - deploy 历史 + secret 操作，jsonl/csv/table 三种格式
  - --source / --since / --format / --output 过滤
- [x] `kp policy add/list/remove/check`：OPA 策略引擎
  - 策略存 `~/.kp/policies/*.rego`
  - `kp deploy` 前自动运行，无 opa 命令静默跳过
  - 示例策略：no-latest-tag / require-resource-limits
- [x] `kp secret sync --from vault`：从 Vault KV v2 同步到 K8s Secret
  - curl 实现，--dry-run 模式，幂等 apply
  - 写审计日志到 `~/.kp/audit/secret.jsonl`

### v1.8.0 迁移原子性 + Operation Sandbox + 蓝绿增强
- [x] Sandbox 状态机（LOCKED/SNAPSHOTTING/SIMULATING/COMMITTING/RESTORING）
- [x] kp sandbox start/status/unlock（COMMITTING 永远禁止 force-unlock）
- [x] LOCKED 阻止其他 kp deploy
- [x] SIMULATING K8s Job（降级 dry-run）
- [x] Controller GC 超期 Session（5m 扫描）
- [x] kp deploy --preview（Istio/Nginx/降级三路）
- [x] kp warmup --steps/--interval/--err-threshold
- [x] patchTrafficWeight（Istio VirtualService + Nginx Ingress）
- [x] sampleErrorRate（Prometheus PromQL）
- [x] kp migrate + PVC 快照联动（待 CSI 验证）
- [x] kp upgrade 快照保护
- [x] kp migrate fix-dirty

### v1.7.0 状态漂移治理
- [x] kp diff --drift 三级分层 + kp doctor 集成
- [x] --force-conflicts SSA + force-sync Controller
- [x] on-missing 全策略 + OOMKilled + CrashLoopBackOff + HPA

### v1.6.0 ~ v1.0.0
- [x] Controller HA + 可观测性 + 多 namespace + KWOK 压测
- [x] Secret 轮转 + StatefulSet + 蓝绿 e2e + 迁移全链路
- [x] DAG + A2 Controller + 供应链安全 + 安全合规基线

---

> 乾枢不是名字，是承诺。
> 配得上这个名字，需要把企业真正卡住的硬骨头一块一块啃掉。
