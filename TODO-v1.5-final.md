# TODO — KubePivot 路线图

> 当前：v1.5.0 + 蓝绿 e2e 验证完成

---

## 🚨 v1.5.2 — Bug 修复

- [ ] [P1] 蓝绿失败后状态机停在 DEPLOYING（`multi_deploy.go:103` cascadeOK 路径）
- [ ] [P1] `kp resume` IDLE 判断未检查 Deployment，蓝绿场景误判重新部署
- [ ] [P2] `kp upgrade --service` 过滤已实现，待 e2e 验证

---

## 🚨 v1.5.1 — PVC 备份完整实现（需要 CSI 集群）

- [ ] `kp pvc backup` — label 发现 PVC，创建 VolumeSnapshot，等待 readyToUse
- [ ] `kp pvc restore` — 缩容→删旧 PVC→从 Snapshot 创建新 PVC→扩容
- [ ] `kp pvc list` — kp 标签过滤，NAME/PVC/SERVICE/CREATED AT/READY 展示

---

## 🚨 v1.6.0 — 大规模场景支持

> KWOK 万节点压测验证

### 部署性能
- [ ] `--parallelism` flag 并行度控制
- [ ] `--changed-only` 增量部署（基于 git diff）
- [ ] 部署耗时统计（build/push/helm/rollout 各阶段）

### 多 namespace 依赖
- [ ] `components.yaml` 支持跨 namespace 依赖声明
- [ ] `kp deploy` 跨 namespace 拓扑排序

### 可观测性
- [ ] 结构化部署日志（JSON），支持接入 ELK/Loki
- [ ] `kp history --export` 导出 CSV/JSON

---

## 🟡 v1.7.0 — 真正的自愈

- [ ] `resources.yaml` 自定义自愈策略（on-missing: recreate/rollback/scale-down/alert/custom）
- [ ] OOMKilled 检测 + 自动调整 limits
- [ ] CrashLoopBackOff 分析
- [ ] HPA 集成

---

## 🟢 v2.0.0 — 企业级扩展

- [ ] Vault 集成
- [ ] 审计日志（SOC2）
- [ ] OPA 策略引擎
- [ ] 多集群管理
- [ ] `kp self-update`

---

## ✅ 已完成

### v1.5.0 StatefulSet 状态同步 + 蓝绿 e2e
- [x] `kp doctor` etcd 健康检查（连通性/raft index/磁盘）
- [x] `kp doctor` VolumeSnapshot 环境检查
- [x] `kp deploy` StatefulSet rollout 路由
- [x] `kp status` StatefulSet pod 详情（ordinal/ready/version）
- [x] `kp pvc` 完整实现骨架（待 CSI 验证）
- [x] 蓝绿发布 e2e 验证（green slot 部署 + kp promote 切换 selector）
- [x] 蓝绿 chart 模板修复（Release.Name + skipService）
- [x] `kp upgrade --service` 过滤实现

### v1.4.0 跨版本迁移
- [x] `kp migrate status/plan/run`
- [x] `kp compat check`
- [x] `kp diff --migrate`
- [x] `kp upgrade` 全链路编排
- [x] 蓝绿发布 + `kp promote`

### v1.3.0 供应链安全
- [x] Trivy CVE 扫描 + cosign + SBOM
- [x] 品牌焕新：KubePivot/kp

### v1.2.0 安全合规基线
- [x] `kp doctor` 安全检查
- [x] NetworkPolicy + SecurityContext + limits

### v1.1.0 / v1.0.0 封神 🏆
- [x] controller e2e，多 release status，rollback 进度
- [x] DAG + Kahn 拓扑排序 + A2 Controller + 143 单测

---

> 乾枢不是名字，是承诺。
