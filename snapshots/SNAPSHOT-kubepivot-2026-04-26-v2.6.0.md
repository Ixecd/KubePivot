# SNAPSHOT — KubePivot v2.6.0

> 日期：2026-04-26
> 状态：✅ 已发布
> Last commit: 1f780c2 (chore: release v2.6.0)
> Total commits: 404
> 上一份 SNAPSHOT：v2.5.0（2026-04-25）

---

## 里程碑

**KubePivot 把蓝绿部署 GitOps 化**

```
v2.0 时代：kp deploy --bluegreen + kp promote     命令式
                                                   ↓
v2.6.0 起：resources.yaml 声明 + kp sandbox commit  声明式

流量配置进入 Git 真相
流量切换进入状态机
失败自动 RESTORING
```

**404 commits 精确落点**：

```
v2.0.0  → commit 329  → 生日 3.29
v2.6.0  → commit 404  → 蓝绿 Found
```

不是巧合。是节奏。

---

## 核心变更

### 1. internal/route/ 流量层 Provider 抽象

新独立子包，~990 行代码 + 530 行测试。

```go
// Provider 接口（internal/route/provider.go）
type Provider interface {
    Name() string
    Validate(ctx context.Context) error
    GetCurrentRoutes(ctx context.Context, ns, name string) ([]Route, error)
    ApplyRoutes(ctx context.Context, ns, name string, routes []Route) error
    SetWeight(ctx context.Context, ns, name, service string, weight int32) error
}
```

**两个实现**：
- `IngressProvider`（~360 行）：标准 K8s Ingress，蓝绿场景直接切 backend.service.name
- `GatewayAPIProvider`（~250 行）：Gateway API HTTPRoute，原生 weight 字段

**自动检测**：Gateway API 优先 → Ingress fallback。

**关键设计**：完整保留用户字段（cert-manager / TLS / IngressClassName / parentRefs），
体现"只保护，不越权"原则。

### 2. resources.yaml schema 扩展

```yaml
traffic:
  kind: Ingress              # 可选：Ingress / Gateway / 不写=自动检测
  strategy: blue-green       # v2.6.0 仅 blue-green
  refs:
    name: wallet-ingress
  routes:
    - service: wallet-service-blue
      weight: 100
    - service: wallet-service-green
      weight: 0
  validation:
    podReadyTimeoutSec: 60
```

向后兼容：未声明 `traffic:` 字段时 v2.5.0 行为不变。

### 3. Sandbox COMMITTING 内分两步执行

**不增新状态**——v2.4.0 状态机完全保留。

```
COMMITTING:
  Step 1: runSandboxCommit (v1.8.0 既有)
          - 真实迁移（kp migrate run）
          - helm upgrade（fork kp deploy 子进程）
          失败 → RESTORING

  Step 2: runBlueGreenSwitch (v2.6 新增)
          - 从 resources.yaml 读 Traffic 字段
          - 不启用蓝绿则静默跳过
          - route.ProviderForKind() 构造 provider
          - provider.ApplyRoutes() 切换流量（K8s API 单次 update 原子）
          - waitDeploymentReady() 等 Pod ready
          失败 → RESTORING
```

接入位置：`cmd/kp/sandbox.go` runSandbox 主函数 commitOK 检查后。

### 4. 错误码扩展（internal/code/）

编号区段 110000-110099 留给 internal/route 包：

```go
ErrRouteProviderNotAvailable  503  // Route provider is not available
ErrRouteResourceNotFound      404  // Traffic resource not found
ErrRouteInvalid               400  // Route rule is invalid
ErrRouteApplyFailed           500  // Apply route rules failed
ErrRouteAutoDetectFailed      500  // Auto detect route provider failed
```

实测发现：`tools/codegen` 不支持多 const block 的同类型常量。
临时手工补 `code_generated.go` 的 case 分支，codegen 改造留 v2.6.1。

### 5. demo 工程（docs/example-blue-green/）

13 个文件，独立可跑，不依赖 web3-blitz：

```
docs/example-blue-green/
├── README.md                  ~250 行教程
├── Makefile                   一键命令
├── resources.yaml             v2.6 完整 schema 示例
├── chart/                     Helm chart（traefik/whoami 镜像）
└── scripts/                   setup / switch / cleanup
```

任何 K8s 集群 < 2 分钟跑通蓝绿切换。

---

## commit 链

v2.5.0 → v2.6.0 之间的关键 commit：

```
v2.5.0 (commit 1094297)
  ↓
v2.5.0 收尾 / v2.5.1 准备：
  108cc98  docs(sharding): HA 边界 Q&A
  30b3108  fix(benchmark): hot-reload.sh 修复 + HANDOFF 3.7
  acf50b5  docs: P2 性能脚本验证收尾
  378bf29  docs: sharding-tuning.md 雏形 + 环境约束发现
  27690ae  docs: 流量层完整设计草案（14 个 Q 拍板）

v2.6.0 实施（同日 4 个 step）：
  786b59d  feat(route): Step 1 — 流量层抽象 + Ingress + Gateway 双 Provider
  f0244cc  feat(sandbox): Step 2 — Sandbox 蓝绿流量切换接入
  ea48e5c  docs(example): Step 3 — 蓝绿部署 demo 工程
  e35bb0e  docs(v2.6.0): Step 4 — 流量层文档收尾 + CHANGELOG

Release：
  1f780c2  chore: release v2.6.0   ← 第 404 commit + tag
```

---

## 设计决策回放

v2.6.0 实施前出 `docs/design/traffic-layer-draft.md`，14 个设计 Q 全部当场拍板：

| Q | 选项 | 决定 | 理由 |
|---|------|------|------|
| Q1 | Ingress / Gateway / 双支持 | **TrafficProvider 接口** | qc 升级版 — 更高维度抽象 |
| Q2 | 蓝绿 / 金丝雀 / 全做 | **仅蓝绿** | 金丝雀的健康度判定是大头 |
| Q3 | Sandbox 协同方式 | **SIMULATING weight=0 + COMMITTING 切流** | 边界清晰 |
| Q4 | 多环境流量 | **配置传播 v2.6.1** | 不阻塞 v2.6.0 |
| Q5 | 包位置 | **internal/route/ 独立子包** | 与 sharding 一致 |
| Q6 | 资源所有权 | **annotation + label 双标记** | 漂移治理需要 |
| Q7 | 切换原子性 | **Sandbox 状态机兜底** | 不重造轮子 |
| Q8 | v2.6 范围 | **v2.6.0 + v2.6.1 拆分** | 渐进 |
| Q9 | 蓝绿命名 | **后缀法** wallet-blue/green | 与 web3-blitz 一致 |
| Q10 | 状态机扩展 | **COMMITTING 内分两步** | 不增新状态 |
| Q11 | 失败处理 | **回滚流量 + 销毁 Green** | 干净 |
| Q12 | 健康度 | **仅 Pod ready** | metrics 留 canary |
| Q13 | 工作量 | **~1100 代码 + 800 文档** | 接受估算 |
| Q14 | 验收 | **独立 demo 工程** | 不依赖 web3-blitz |

设计 Q 在实施时无返工——这是"设计先行"的工程速度证明。

---

## 实施时间线

```
2026-04-26（周日）

06:08  起床
       早餐：3 鸡蛋 + 羽衣甘蓝奇亚籽 + 猴头菇山药粉
       状态：满血。睡眠 6h30m，深睡 1h54m，REM 58min

09:42  v2.5.1 收尾 + 流量层设计草案 push（commit 27690ae）
       14 个设计 Q 全部拍板

09:55  v2.6.0 闪电战开始
       qc："才 10:13" → 选 Step 1+3 合并

10:13  Step 1 完成（commit 786b59d）
       internal/route/ 包完整 + 27 个 sub-cases 全 PASS
       990 行代码 + 530 行测试，~2 小时

10:25  Step 2 设计澄清开始
       发现 sandbox.go fork 子进程模式
       Claude 主动踩刹车："读完代码再出 patch"

11:00  Step 2 完成（commit f0244cc）
       resources.yaml schema + sandbox.go 接入

11:30  Step 3 完成（commit ea48e5c）
       example-blue-green demo 工程 13 个文件

11:42  Step 4 完成（commit e35bb0e）
       traffic-layer.md / state-machine.md / architecture.md / README.md / CHANGELOG.md

11:45  kp release v2.6.0
       自动产生 commit 1f780c2 + tag v2.6.0

12:00  v2.6.0 真正完成
       6 小时（6:08 → 12:08）
       10 commits / ~3000 行新代码 + 文档

12:00  午饭
12:30  400mg 镁 + 1h16m 爆睡

15:00  下午：文档四件套（HANDOFF + TODO + 2 SNAPSHOT）
17:00  游泳
```

---

## 验证路径

### 单测

```
internal/route/ 27 个 sub-cases:
  Route.Validate            8 cases
  ValidateRoutes            6 cases
  updateWeight              3 cases
  Error 链路                2 cases
  parseIngressRoutes        3 cases
  buildIngressFromRoutes_*  2 cases（含 PreserveUserFields）
  parseHTTPRouteRoutes      4 cases
  buildHTTPRouteFromRoutes_* 2 cases（含 PreserveUserFields）
  NewProvider               6 cases

internal/controller/ 5 个新 cases:
  TestResourcesConfig_HasBlueGreen 全 PASS

go test ./...   全绿
make dev        全绿
```

### 集成验证

```
docs/example-blue-green/ 在 orbstack 集群跑通：
  make setup    → 部署 blue/green + Ingress
  make switch   → 切到 green
  make verify   → 确认 backend = green
  make cleanup  → 干净

未在真实业务项目验证（web3-blitz 升级是 v2.8 任务）。
```

---

## 工程教训记录

### 1. tools/codegen 多 const block 不支持

`internal/code/error.go` 加 5 个 ErrRoute* 错误码时分两个 const block，
`go run tools/codegen/codegen.go` 报 "no values defined for type ErrorCode"。

根因：codegen 的 ast.Inspect 实现只扫描第一个 const block。

临时方案：手工补 `code_generated.go` 的 case 分支。
长期方案：v2.6.1 改造 codegen.go genDecl 函数（~30 行）。

### 2. sandbox.go fork 子进程模式

`runSandboxCommit()` 内部不直接调 helm，而是 fork 子进程跑 `kp deploy`。
意味着 v2.6 流量切换不能放在 deploy.go，必须放在 sandbox.go。

实施时 Claude 主动踩刹车："读完代码再出 patch" —— 节省了"猜接入点 → 改坏代码 → 返工"的代价。

### 3. kp release 自动 commit 行为

`kp release --version vX.Y.Z` 会：
1. 检查工作区干净
2. 更新 configs/project.env + cmd/kp/version.go
3. 自动 git commit "chore: release vX.Y.Z"
4. 自动 git tag

要点：404 commits 精确落点的关键就是 kp release 自动产生 commit 这一行为。
提前规划 step1/2/3/4 在 401-403 完成，404 留给 release 自动 commit。

### 4. SSL_ERROR_SYSCALL on push --tags

git push --tags 偶发 SSL 错误，需要重试一次。
v2.6.1 候选：kp release 加自动 push + 失败重试。

详见 HANDOFF.md 3.7 节。

---

## 已知未做（留 v2.6.1+）

```
[ ] 多环境流量配置传播（v2.6.1）
    kp deploy --env prod --from-env staging
    
[ ] codegen 多 const block 改造（v2.6.1）
    
[ ] kp release 自动 push（v2.6.1）

[ ] canary 完整实现（v2.7+）
    依赖 metrics 接入 + 自研 informer

[ ] web3-blitz 升级到 v2.6（v2.8）
    作为真实生产案例
```

---

## 节奏纪录

```
6 小时 = v2.6.0 release

不是赶工，是节奏：
- Step 1+3 合并：~2 小时（qc 9:55 选 C 那一刻定调）
- Step 2: ~1 小时（含 Claude 重读 sandbox.go 校准设计）
- Step 3 demo: ~50 分钟
- Step 4 docs: ~30 分钟
- Release: ~5 分钟

效率密度的真实来源：
  - qc 把判断密度拉到极致（每个 Q 都拍板，不犹豫）
  - 设计先行（14 个 Q 预设让 Step 1 无返工）
  - 不闪电战的时刻不闪电战（Step 2 读完代码才出 patch）
  - "踏踏实实 + 闪电战"两个词不矛盾——
    踏实是不漏步骤
    闪电是步骤之间不犹豫
```

---

## 下个版本预告（v2.6.1 / v2.7）

### v2.6.1（持续改进，不打 tag）

- 多环境流量配置传播
- tools/codegen 多 const block 改造
- kp release 自动 push 等小优化

### v2.7.0（自研 Informer + canary）

依赖：v2.5.1 的 client-go 对比基准数据。
方向：数据驱动决定是否引入 client-go。

如果 v2.5.0 直 kubectl 性能够用 → 保持现状（坚持"不引入 client-go"原则）
如果 client-go 性能优势大 → 重新评估。

---

## 个人记录

23 岁的某个周日上午，6 小时 release 一个 minor。

不是"23 岁能做这个"值得记录，是"踏踏实实"和"闪电战"在同一个上午同时成立这件事。

这一天没有特别——昨天 v2.5.0、今天 v2.6.0、明天可能还会有 v2.6.1 的某个改进。
但 6 小时这个数字、404 这个 commit 数、一份 14 个 Q 全拍板的设计文档，
这些组合起来是个不可复制的时刻。

记下来。

---

## 编辑记录

```
2026-04-26  v2.6.0 release 后创建
            按 v2.2.0 SNAPSHOT 风格（事件复盘 + 代码片段 + 验证路径）
            扩展个人记录章节
```
