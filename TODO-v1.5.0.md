# TODO — KubePivot 路线图

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链。
> 乾为天、为尊，枢为核心枢纽。
> 名字是要配得上的。
> 当前：v1.5.0

---

## 🚨 v1.5.1 — PVC 备份完整实现

> 前置：集群需支持 CSI VolumeSnapshot（local-path 不支持）
> 目标：kp pvc backup/restore/list 完整落地

- [ ] `kp pvc backup` — 按 kp.io/service 标签发现 PVC，创建 VolumeSnapshot，等待 readyToUse
- [ ] `kp pvc restore` — 缩容→删旧 PVC→从 Snapshot 创建新 PVC→扩容，强制确认机制
- [ ] `kp pvc list` — 按 kp 标签过滤，展示 NAME/PVC/SERVICE/CREATED AT/READY TO USE
- [ ] `--snapshot-class` 指定 VolumeSnapshotClass，默认用集群默认 class

---

## 🚨 v1.6.0 — 大规模场景支持

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

## 🟡 v1.7.0 — 真正的自愈

> 目标：controller 从"重启型自愈"升级到"智能型自愈"

### 自愈策略插件化
- [ ] `resources.yaml` 支持自定义自愈策略：`on-missing: recreate | rollback | scale-down | alert | custom`
- [ ] `custom` 策略支持执行用户自定义脚本
- [ ] 自愈动作审计日志

### 异常模式识别
- [ ] OOMKilled 检测：自动调整 memory limits 并重新部署
- [ ] CrashLoopBackOff 分析：区分启动错误 vs 运行时错误
- [ ] ImagePullBackOff 自动重试 + 告警

### 容量自愈
- [ ] HPA 集成：`components.yaml` 声明 `min_replicas` / `max_replicas` / `target_cpu`
- [ ] 自动生成 HorizontalPodAutoscaler 模板
- [ ] `kp status` 展示 HPA 当前副本数 vs 目标副本数

---

## 🟢 v2.0.0 — 企业级扩展插件

- [ ] **Vault 集成**：集中式密钥管理
- [ ] **审计日志**：操作/部署/权限日志持久化，满足 SOC2
- [ ] **OPA 策略引擎**：自定义企业合规规则
- [ ] **多集群管理**：统一管理 dev/staging/prod
- [ ] **RBAC 最小权限自动生成**

---

## 🟢 P2 — 长期愿景

- [ ] 服务级 FSM（v2.0，目前是项目级）
- [ ] `kp ai-plan` 规则专家系统优先于大模型
- [ ] Web UI：部署状态可视化大盘
- [ ] Terraform provider

---

## ✅ 已完成

### v1.5.0 StatefulSet 状态同步
- [x] `kp doctor` etcd 健康检查（连通性 + raft index 差值 + 磁盘使用率）
- [x] `kp doctor` VolumeSnapshot 环境检查（CRD + VolumeSnapshotClass）
- [x] `kp deploy` StatefulSet rollout 路由（statefulset/ vs deployment/）
- [x] `kp deploy` StatefulSet 不覆盖 replicaCount
- [x] `kp status` StatefulSet pod 详情（ordinal/ready/version，按 ordinal 排序）
- [x] `kp pvc` 命令骨架 + 完整设计 TODO（待 CSI 环境验证，v1.5.1 实现）
- [x] etcdctl ETCDCTL_* 环境变量冲突修复

### v1.4.0 跨版本迁移
- [x] `kp migrate status/plan/run` — DB 迁移感知全链路
- [x] 部署前自动迁移兼容性检查
- [x] `kp compat check` — oasdiff API 兼容性检测
- [x] `kp diff --migrate` — values diff + 迁移建议
- [x] `kp upgrade` — 跨版本全链路升级编排
- [x] 蓝绿发布 + `kp promote`
- [x] `components.yaml` 加 `api_version` 字段

### v1.3.0 供应链安全
- [x] `kp scan` CVE 扫描（Trivy），cosign 签名，SBOM
- [x] 品牌焕新：dev-toolkit → KubePivot，dtk → kp

### v1.2.0 安全合规基线
- [x] `kp doctor` 安全检查
- [x] `kp init` 模板天生合规：NetworkPolicy + SecurityContext + limits
- [x] `kp deploy` 前检查 Secret 存在性

### v1.1.0
- [x] controller 自愈 e2e 验证（~13s 恢复）
- [x] `kp status` 多 release 独立展示
- [x] `kp rollback` 拓扑逆序

### v1.0.0 封神 🏆
- [x] DAG + Kahn 拓扑排序 + 级联 rollback
- [x] A2 Reconciliation Controller
- [x] 143 个单测全绿，CI -race

---

> 乾枢不是名字，是承诺。
