# KubePivot 当前快照

> 版本：v2.0.0
> 日期：2026-04-04
> commit：#329（生日数字）
> 状态：✅ 全绿，已发布

---

## 快速状态

```
go test ./... -race  → 全绿（236 个测试）
make dev             → build + test + install 一键完成
当前版本             → v2.0.0（已 tag，已推送）
companion            → github.com/Ixecd/web3-blitz
```

---

## 完整版本线

```
v1.0.0  生日当天 🏆  DAG + A2 Controller + 安全合规基线
v1.1.0              status 多 release + e2e 验证
v1.2.0              安全合规基线（trivy + RBAC + Network Policy）
v1.3.0              供应链安全（cosign + SBOM）
v1.4.0              跨版本迁移（KubePivot 改名，乾枢）
v1.5.0              StatefulSet + etcd 健康监控
v1.5.1              蓝绿 e2e + P1 状态机 bug 修复
v1.5.2              Secret 轮转（双密码过渡期）
v1.6.0              Controller HA（Leader Election + WorkQueue）+ 可观测性
v1.7.0              状态漂移治理（终态强权）
v1.8.0              Operation Sandbox + Header Preview + Warmup
v1.9.0              多集群联邦 + 企业合规（audit + OPA + Vault）
v2.0.0  #329 🏆     插件平台 + Chaos + GitOps Manifesto
```

---

## v2.0.0 新增能力

```bash
kp version                    # 查看版本（kpVersion 常量，由 kp release 自动更新）
kp update                     # 自动更新到最新版本（GitHub releases API）
kp plugin install <name>      # 安装插件（go install + ~/.kp/plugins/ 包装脚本）
kp plugin list/remove
kp <unknown>                  # 未知命令自动转发到插件（execPlugin 兜底）

kp chaos inject --service wallet-service --kind pod-kill --dry-run
kp chaos inject --service wallet-service --kind network-delay --latency 200ms
kp chaos list / stop / status  # Chaos Mesh API

kp release --version v2.0.0   # 自动同步 kpVersion 常量 + project.env
```

**代码质量修复**：

- OPA stdin pipe：`exec.Command` + `bytes.NewReader(inputJSON)`，input 正确传递
- drift etcd 审计：`clientv3.WithPrefix` 读 `/kubepivot/<project>/<ns>/drift/`
- Vault `fetchVaultKV`：`net/http` 替换 curl，无外部依赖
- `extractTableName`：保留原始大小写（修复 CI 失败）

---

## 当前测试覆盖

```
cmd/kp：      67  个测试（chaos/migrate/image/plugin/preview/cert）
controller：  46  个测试（heal/workqueue/leader/sandbox/OOMKilled/CrashLoop）
state：       58  个测试（FSM/Sandbox/ForceState/持久化/幂等）
planner：     32  个测试
scaffold：    30  个测试
test/：        3  个测试
─────────────────────────
总计：        236  个测试，全部 -race 通过
```

---

## 架构一页纸

```
kp CLI（25 子命令）
  ├── 脚手架：kp init（含合规基线）
  ├── 部署引擎：OPA → 迁移兼容 → CVE → AI规划 → DAG → 并行部署
  ├── 状态机：13 状态，etcd/本地持久化，零 K8s 依赖
  ├── A2 Controller：Leader Election + WorkQueue + Reconcile/Drift/GC 三 Loop
  ├── Operation Sandbox：LOCKED→SNAPSHOTTING→SIMULATING→COMMITTING→RUNNING
  ├── 企业工具链：audit + OPA + Vault + 多集群 + chaos
  └── 插件市场：~/.kp/plugins/，未知命令自动转发

核心原则：只保护不越权 / 降级不阻断 / 确定性优先
```

---

## 技术债（诚实）

```
P1  --field-manager：helm v4 不支持，用 --force-conflicts
P2  SIMULATING Job / PVC 快照 / Istio weight / Prometheus：待对应环境验证
P2  drift etcd 审计 / Controller GC：待端到端验证
P3  Vault SDK 深度集成（当前 net/http 实现已可用）
```

---

## 项目文档

```
README.md           → 项目介绍 + 完整命令速查
GITOPS-MANIFESTO.md → "CD 从 Pipeline 退化回 Git Commit"
TODO.md             → 路线图 + 技术债
SNAPSHOT.md         → 本文件
handoff/HANDOFF.md  → 给下一个 Claude 的交接文档
docs/design/        → 架构/状态机/Sandbox/Preview 设计文档
docs/guide/zh-CN/   → quickstart/commands/gotchas
snapshots/          → 完整历史快照（从 dtk v0.x 到 KubePivot v2.0.0）
```
