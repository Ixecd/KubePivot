# KubePivot 当前快照

> 版本：v2.2.0
> 日期：2026-04-23
> 状态：✅ 全绿，真实集群自愈闭环通过

---

## 快速状态

```
go test ./... -race  → 全绿（239+ 个测试）
make dev             → build + test + install 一键完成
当前版本             → v2.2.0
companion            → github.com/Ixecd/web3-blitz
                       github.com/Ixecd/Feelings-Server
自愈验证             → 2026-04-23 真实集群端到端 <10 秒闭环
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
v2.2.0  🔱          真实集群自愈闭环 + scratch 容器化全量改造
```

---

## v2.2.0 核心成就

### 1. 真实集群自愈闭环第一次跑通

```
2026-04-23 15:43:44  kubectl delete deployment feelings-server
2026-04-23 15:43:44  controller 检测缺失 → auto-heal
2026-04-23 15:43:44  helm rollback revision=4
2026-04-23 15:43:51  Deployment 恢复 Running

耗时 <10 秒，零告警，零人工介入，日志零 ERROR
```

这是从 v1.0.0 到今天第一次证明整条设计链路成立：状态机 + Sandbox + Drift 治理 + Reconciliation Controller + 自愈。

### 2. KpExecutor 统一子进程执行层

`internal/executor/executor.go` 新增模块：

- 单例 + 信号量限流（sem=5，防 fork bomb OOM）
- `resolveBin()` 自动识别 scratch 容器 / 本机开发环境路径
- `Kubectl / Helm / Generic / CmdKubectl` 四接口
- kubeconfig 自动注入，调用方零感知

### 3. 全量去裸 exec.Command

scratch 容器里所有 `exec.Command("kubectl", ...)` 都会失败（无 PATH 查找机制）。v2.2.0 全量替换：

```
必改（controller 运行在 scratch 容器）：
  internal/controller/heal.go / drift_sync.go / resources.go / sandbox_gc.go
  cmd/kp/deploy.go ensureSecret / cmd/kp/runner.go runOutput

不改（本机 CLI，PATH 必然有）：
  cmd/kp/version.go / doctor*.go / plugin.go / policy.go / secret.go
```

### 4. etcd key 统一：dtk/ → kubepivot/

KubePivot 最早叫 dev-toolkit，v1.4.0 改名后 etcd 路径一直是遗留。v2.2.0 直接破坏性升级（用户数 0，成本最低）。

### 5. RBAC 权限补全

核心修复：**secrets 完整 CRUD**（helm v3 release state 存在 Secret 里，rollback 需完整权限）。

补全内容：deployments/statefulsets/services/configmaps/namespaces/networking/RBAC 全部 CRUD。

### 6. Dockerfile 升级

```
Go 1.25.x → 1.26        消除 stdlib HIGH CVE
helm 硬编码 v3.17.1      避免 GitHub API 限流 404
kubectl 动态获取          dl.k8s.io/stable.txt 稳定
scratch runtime          零 OS 漏洞
多架构 manifest list     amd64 + arm64，tag 不带架构后缀
upx 只压 kp              kubectl/helm 不压（避免解压 OOM）
```

### 7. syncStateRunning 尊重状态机契约

controller 自愈成功**不再**强行推 IDLE → RUNNING。状态机应该由 kp deploy 驱动，controller 只管资源层。消除 ERROR 日志噪音。

---

## 当前测试覆盖

```
cmd/kp：      70+ 个测试
controller：  46+ 个测试
state：       58+ 个测试
planner：     32  个测试
scaffold：    30  个测试
test/：        3  个测试
─────────────────────────
总计：        239+ 个测试，全部 -race 通过
```

---

## kp init 端到端验证

```bash
go install github.com/Ixecd/kubepivot/cmd/kp@latest
kp init --name myapp --module github.com/me/myapp
cd myapp
make build   ✅  只编译业务服务
make test    ✅  全包通过
make gen     ✅  错误码文档自动生成
kp deploy    ✅  自动创建 Secret + RUNNING
              ✅  kubepivot-controller 三副本 Leader Election
              ✅  RBAC secrets CRUD 权限就位
              ✅  真实自愈可触发
```

---

## 架构一页纸

```
kp CLI（26+ 子命令）
  ├── 脚手架：kp init（含合规基线 + 错误码 + GitOps hooks）
  ├── kp sync（框架升级，不动业务代码）
  ├── 部署引擎：OPA → 迁移兼容 → CVE → AI规划 → DAG → 并行部署
  │            自动创建 dev Secret
  ├── 状态机：13 状态，etcd/本地持久化，key: kubepivot/<p>/<ns>/state
  ├── A2 Controller：Leader Election + WorkQueue + Reconcile/Drift/GC 三 Loop
  │                  scratch 容器，kp + kubectl + helm 三二进制
  │                  ⭐ v2.2.0 真实集群自愈通过
  ├── Operation Sandbox：LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
  ├── 企业工具链：audit + OPA + Vault + 多集群 + chaos
  └── 插件市场：~/.kp/plugins/，未知命令自动转发

统一子进程层：
  internal/executor/
    ├── 单例 + sem=5 限流
    ├── scratch/本机路径自动解析
    └── kubectl / helm / generic / CmdKubectl

核心原则：只保护不越权 / 降级不阻断 / 确定性优先
```

---

## 技术债（诚实）

```
P0  controller 启动从 etcd 恢复状态机（根治 IDLE → RUNNING）→ v2.3.0
P0  全局单一 HA controller（cluster-scoped）→ v2.3.0
P0  helm --history-max 限制 revision 堆积 → v2.3.0
P1  rbac.yaml 迁移到 embedded template file → v2.3.0
P1  Dockerfile 多架构 GitHub API 容错 → v2.3.0
P1  helm v4 --field-manager：当前用 --force-conflicts 替代
P2  SIMULATING Job / PVC 快照 / Istio weight / Prometheus：待对应环境
P2  web3-blitz 蓝绿 release 名解析（-blue/-green 后缀）
P3  Vault SDK / kp sync 真实用户验证 / kp init --type minimal/full
```
