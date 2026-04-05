# KubePivot 当前快照

> 版本：v2.1.0
> 日期：2026-04-05
> 状态：✅ 全绿，已发布

---

## 快速状态

```
go test ./... -race  → 全绿（239 个测试）
make dev             → build + test + install 一键完成
当前版本             → v2.1.0
companion            → github.com/Ixecd/web3-blitz
```

---

## 完整版本线

```
v1.0.0  生日当天 🏆  DAG + A2 Controller + 安全合规基线
v1.4.0              跨版本迁移（KubePivot 改名，乾枢）
v1.6.0              Controller HA（Leader Election + WorkQueue）
v1.7.0              状态漂移治理（终态强权）
v1.8.0              Operation Sandbox + Header Preview + Warmup
v1.9.0              多集群联邦 + 企业合规（audit + OPA + Vault）
v2.0.0  #329 🏆     插件平台 + Chaos + GitOps Manifesto
v2.1.0              脚手架适配性 + 扩展性 + GitOps 愿景落地
```

---

## v2.1.0 新增能力

### kp sync — 框架文件升级
```bash
kp sync             # 升级框架文件，永远不动业务代码
kp sync --dry-run   # 预览变更
kp sync --only scripts  # 只更新 make-rules
```

文件策略：
```
强制覆盖：Makefile / scripts/make-rules/*.mk / .githooks/
合并更新：configs/project.env（只追加新 key）
永远不动：cmd/ / internal/ / migrations/ / go.mod
提示用户：deployments/ / components.yaml / resources.yaml
```

### kp init 新增内容
```
.githooks/post-receive  → git push → kp deploy --changed-only（GitOps 落地）
.githooks/pre-push      → push 前自动跑测试
internal/pkg/code/      → ErrorCode + Error 类型（codegen 兼容注释格式）
internal/pkg/response/  → OK() / Fail() 统一 HTTP 响应
make gen                → 自动生成错误码文档
```

### kp deploy 改进
```
自动检测 Secret 是否存在
  ├── 存在 → 跳过
  └── 不存在 → 自动创建 namespace + dev Secret
               生产环境提示运行 ./scripts/create-secret.sh
```

### 其他修复
```
CRD 资源自愈：isCRDKind + healCRDApply（helm manifest → kubectl apply）
COMPONENT_NAMES：find cmd/ 实现，postgres/etcd 不参与 build
sed -i 跨平台：sed -i.bak + rm（macOS/Linux 兼容）
create-secret.sh：去除 web3-blitz 硬编码，通用模板
```

---

## 当前测试覆盖

```
cmd/kp：      70  个测试
controller：  46  个测试
state：       58  个测试
planner：     32  个测试
scaffold：    30  个测试
test/：        3  个测试
─────────────────────────
总计：        239  个测试，全部 -race 通过
```

---

## kp init 端到端验证

```bash
go install github.com/Ixecd/kubepivot/cmd/kp@latest
kp init --name myapp --module github.com/me/myapp
cd myapp
make build   ✅  只编译业务服务，postgres/etcd 跳过
make test    ✅  全包通过
make gen     ✅  错误码文档自动生成
kp deploy    ✅  自动创建 Secret + RUNNING
kp sync      ✅  框架升级，业务代码不动
git push     ✅  .githooks/post-receive 触发 kp deploy
```

---

## 架构一页纸

```
kp CLI（26 子命令）
  ├── 脚手架：kp init（含合规基线 + 错误码 + GitOps hooks）
  ├── kp sync（框架升级，不动业务代码）
  ├── 部署引擎：OPA → 迁移兼容 → CVE → AI规划 → DAG → 并行部署
  │            自动创建 dev Secret
  ├── 状态机：13 状态，etcd/本地持久化，零 K8s 依赖
  ├── A2 Controller：Leader Election + WorkQueue + Reconcile/Drift/GC 三 Loop
  │                  CRD 资源自愈（healCRDApply）
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
P3  Vault SDK / kp sync 真实用户验证 / kp init --type minimal/full
```
