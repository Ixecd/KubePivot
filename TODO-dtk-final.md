# TODO — dev-toolkit 路线图

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链。
> 面向：出海业务、中小团队/企业客户、合规强要求场景。
> 核心价值：让用户零成本获得合规基线，不用手动补安全短板。
> 设计原则：默认安全 · 最小侵入 · 海外合规优先 · 插件化架构
> 当前：v1.1.0（进行中）

---

## 🚨 v1.2.0 — 安全合规基线

> 目标：脚手架生成的项目，天生满足 80% 合规基础要求

### Secret 全生命周期管理（P0）
- [ ] dtk init 自动生成 scripts/create-secret.sh 模板（含占位变量，幂等）
- [ ] dtk deploy 前检查 secret 是否存在，不存在提示运行 create-secret.sh
- [ ] dtk doctor 扫描 values.yaml 是否含明文密码（匹配 password/secret/token/key/seed 字段）

### dtk init 模板强化（P0）
- [ ] 自带 Network Policy（默认拒绝所有入站，仅开放业务端口）
- [ ] 自带 Pod Security Context（非 root 用户、只读根文件系统、降权）
- [ ] 自带资源 limits（禁止无限制占用）
- [ ] controller RBAC 最小权限模板（替换现有全权限）

### dtk doctor 安全检查（P0）
- [ ] 检测 values.yaml 明文密码字段
- [ ] 检测 Pod 是否缺少 Security Context
- [ ] 检测 RBAC Role 是否存在 * 通配符（过度权限告警）
- [ ] 检测 Network Policy 是否存在

---

## 🟡 v1.3.0 — 供应链安全 + 镜像防护

- [ ] 集成 Trivy：dtk deploy 自动扫描镜像 CVE（CRITICAL 阻断，HIGH/MEDIUM 告警）
- [ ] 可选：cosign 镜像签名校验（--verify-signature flag）
- [ ] 自动生成 SBOM 物料清单（满足海外合规审计）

---

## 🟡 v1.1.0（剩余）

- [ ] REGISTRY_PREFIX 支持阿里云 ACR 格式
- [ ] 统一进度输出带颜色（终端支持时）
- [ ] 灰度发布支持

---

## 🟢 v2.0.0 — 企业级扩展插件（不内置，做插件）

- [ ] Vault 集成插件：集中式密钥管理，满足金融/出海合规
- [ ] 审计日志插件：操作/部署/权限日志持久化
- [ ] OPA 策略引擎对接：自定义企业合规规则
- [ ] RBAC 最小权限自动生成

---

## 🟢 P2 — 长期

- [ ] 服务级 FSM（v2.0，目前是项目级）
- [ ] 跨 namespace 依赖支持
- [ ] dtk ai-plan 接入私有化 LLM 最佳实践文档

---

## ✅ 已完成

### v1.1.0 主体完成

- [x] 构建 dev-toolkit-controller:v1.0.0 镜像并推送
- [x] web3-blitz controller chart 迁移到独立 chart
- [x] controller 自愈 e2e 验证（~13s 恢复）
- [x] controller SSA 冲突处理（检测 → 清除 managedFields → 重试）
- [x] dtk status 展示每个 helm release 独立状态和 revision
- [x] dtk rollback 按拓扑逆序逐层并行打印进度
- [x] dtk init --dry-run 打印目录结构，不执行文件写入
- [x] gotchas.md 补充 controller 章节
- [x] 修复 controller 三个 bug（RealHelmClient/release 命名/策略字段）

### v1.0.0 封神 🏆

- [x] 多服务独立 helm release（每个服务 {project}-{service}）
- [x] components.yaml 支持 type（deployment/statefulset）和 depends_on
- [x] planner：DAG + Kahn 拓扑排序 + Downstream（32个单测）
- [x] scaffold：四个独立 chart
- [x] deploy：同层并行，层间串行，级联 rollback
- [x] controller 统一命名，共用镜像
- [x] e2e 验证（e2e 3层 + web3-blitz 2层）全部跑通
- [x] 全量文档更新

### v0.9.0

- [x] dtk ai-plan（Grok/Claude/OpenAI/豆包）
- [x] 统一进度输出（带时间戳和耗时）

### v0.8.x 及之前

- [x] dtk init / deploy / resume / rollback / release / down
- [x] dtk doctor / status / history / diff
- [x] 部署状态机（57个单测）+ A2 Controller（20个单测）
- [x] CI -race，143个单测全绿
- [x] 全量文档 + quickstart + gotchas + AI 使用手册

---

> v1.2.0 安全合规基线开干 🚀
> 不内置 Vault/OPA/Falco，做插件化扩展
