# SNAPSHOT — dev-toolkit

**里程碑**：战略定位确认 + v1.2.0 安全合规基线规划
**日期**：2026-03-30
**版本**：v1.1.0 → v1.2.0 启动

---

## 战略定位（一锤定音）

**dev-toolkit = 企业级 Kubernetes 研发脚手架 + 部署运维工具链**

面向：出海业务、中小团队/企业客户、合规强要求场景
核心价值：让用户零成本获得合规基线，不用手动补安全短板

设计原则：
- 默认安全：用户不配置，也能获得合规能力
- 最小侵入：不强制改造业务代码，无学习成本
- 海外合规优先：对齐 GDPR、SOC2 最常用要求
- 插件化架构：企业功能不污染核心工具

不内置（做插件）：Vault、OPA、Falco、专业审计平台

---

## 版本路线图

```
v1.1.0  controller e2e + status 多 release + rollback 进度 + init dry-run  ✅ 主体完成
v1.2.0  安全合规基线（Secret 管理 + Pod 安全 + Network Policy + doctor 安全检查）
v1.3.0  供应链安全（Trivy CVE 扫描 + cosign 签名 + SBOM）
v2.0.0  企业级插件（Vault + 审计日志 + OPA）
```

---

## v1.2.0 详细设计

### 开发顺序

1. `dtk doctor` 安全检查（改动小，验证快）
2. `dtk init` 模板强化（Pod Security + Network Policy + 资源 limits）
3. controller RBAC 最小权限
4. Secret 管理（`dtk init` 生成脚本 + `dtk deploy` 检查）

### Secret 管理方案

不引入 Vault，用"脚本 + 检查"覆盖 80% 场景：

- `dtk init` 生成 `scripts/create-secret.sh`（幂等，从 .env 或环境变量读）
- `dtk deploy` 前查 K8s 是否有对应 secret，没有就警告
- `dtk doctor` 扫 values.yaml 明文密码字段

### Network Policy 模板

```yaml
# 默认拒绝所有入站，只开放业务端口 + namespace 内互通
ingress:
  - from:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: {{ .Release.Namespace }}
    ports:
    - port: {{ .Values.service.port }}
```

### Pod Security Context 模板

```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 1000
  readOnlyRootFilesystem: true
  allowPrivilegeEscalation: false
  capabilities:
    drop: ["ALL"]
```

### controller RBAC 最小权限

从现有全权限收紧到：
- secrets：get/list/watch（helm history 需要）
- deployments/statefulsets：get/list/watch/patch/update（rollback 需要）
- pods：get/list/watch（健康检查需要）

---

## 快照归档

```
snapshots/
├── SNAPSHOT-dtk-2026-03-29-v1.0.0-final.md
├── SNAPSHOT-dtk-2026-03-30-v1.1.0.md
└── SNAPSHOT-dtk-2026-03-30-v1.2.0-plan.md  ← 本次
```
