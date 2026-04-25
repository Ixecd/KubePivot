# SNAPSHOT v2.4.0 — 稳固 global controller

> 归档日期：2026-04-25
> Tag：`v2.4.0`
> 上一版本：[v2.3.0](SNAPSHOT-kubepivot-2026-04-24-v2.3.0.md) 全局 controller 架构换血

---

## 摘要

v2.4.0 不是新功能版本，是**稳固版**——把 v2.3.0 架构跃迁后的边缘问题全部抹平，并通过性能基准为后续 v2.5/2.6/2.7 优化铺好基线。

```
v2.3.0  架构跃迁     per-project → global controller
v2.4.0  稳固边缘     消除冗余 / 容错 / 性能基线 / 真实数据基础
```

**核心成就**：

| 维度 | 数字 |
|------|------|
| 集群总 CPU 削减 | -55%（41.88% → 18.92%） |
| 平均内存 / pod 削减 | -38%（59 MiB → 36.62 MiB） |
| Leader 故障转移延迟 | 21.7 ms |
| Dockerfile 可重现性 | ✅（ARG KUBECTL_VERSION=v1.32.0） |
| 蓝绿场景支持 | ✅（resources.yaml 的 helm-release 字段） |
| 测试用例增量 | +9（4 lease + 4 machine cache + 4 helm release） |

---

## 一、版本范围

### 1.1 已交付

```
P0  K8s Lease API leader 选举                ✅ commit
P0  状态机缓存 + 并发安全                     ✅ commit
P1  Dockerfile kubectl 版本固定 + 网络容错   ✅ commit
P1  Resource.HelmRelease 显式声明（蓝绿）     ✅ commit
P1  rbac.yaml 字符串拼接重构                  ✅ 经核查 v2.3.0 已完成（误判 TODO）
P0  性能基准首版 + benchmark 框架（v2.4.0 前已部分完成）
P0  README 项目边界声明（Out of Scope）       ✅ commit
P0  TODO v2.4 → v2.6 三版本路线图              ✅ commit
P0  v2.7.0 自研 Handwritten Informer 灵感保留 ✅ TODO 永久记录
```

### 1.2 移到 v2.5.0 的（不算遗漏，是节奏选择）

```
P2  自愈延迟基准（concurrent-chaos.sh）
P2  Watcher 鲁棒性测试（断网 60s 重连）
P2  ConfigMap sha256 热加载去重验证
P2  长时间运行（24h）泄露验证
```

理由：这批基准移到 v2.5.0 与 client-go 对比基准一起跑更有意义——同一个 benchmark suite 可以三组数据（v2.3 / v2.4 / v2.5）一次产出。

---

## 二、关键技术决策

### 2.1 K8s Lease API leader 选举（v2.4.0 P0）

**问题**：v2.3.0 时无 etcd 降级路径是"3 副本各自跑全量 reconcile"。30 分钟稳态实测三 pod 工作分布**意外均匀**（avg CPU 差距 < 1.5%），但本质上是冗余浪费 + 行为不可预测。

**解法**：通过 K8s coordination.k8s.io/leases API 实现 leader 选举，纯 kubectl 走 exec，不引入 client-go。

**关键设计参数**：
- TTL 15 秒（与 v2.2.0 etcd Leader Election 对齐）
- 续约周期 = ttl/3 = 5 秒（3 次失败才丢失 leader）
- microTime 格式 `2006-01-02T15:04:05.000000Z07:00`（K8s API server 严格要求微秒精度——踩坑）
- holder identity = `<hostname>-<8位 hex 后缀>`
- 双重检查锁：先快速检查缓存命中、不命中再加锁创建

**真实集群验证**：

```
启动期 race（02:36:40 同时启动 3 副本）：
  7qrbx  02:36:40.635  Lease create 成功 → 成为 leader
  gqbts  02:36:40.604  create 失败（AlreadyExists）→ 短暂自称
  gqbts  02:36:46.263  下轮探测发现不是 leader → stop reconcile
  zthqd  从未自称 leader

故障转移：
  杀掉 leader pod
  21.7 ms 内另一副本检测到 lease 过期 → patch 抢占成功
  leaseTransitions: 0 → 1
  整个过程毫秒级，远低于 15 秒 TTL
```

**踩坑**：第一次 build 后日志是 `parsing time "2026-04-25T01:51:57Z" as "2006-01-02T15:04:05.000000Z07:00": cannot parse "Z" as ".000000"`——K8s 的 microTime 字段要求带微秒小数。改 format 字符串后秒解决。

### 2.2 状态机缓存 + 并发安全（v2.4.0 P0）

**问题**：v2.3.0 handleTask 每次 task 都新建 state.Machine：

```go
store := state.NewAutoStore(...)  // 每个 task 一次
sm, _ := state.New(...)           // 每个 task 一次
```

10 项目 × 3 资源 / 8s reconcile = 3.75 次/秒新建 → 触发 etcd Get + state.Machine alloc。

**解法**：GlobalState 加 machineEntry 缓存（machine + per-instance mutex）：

```go
type machineEntry struct {
    machine *state.Machine
    mu      sync.Mutex
}

// 调用模式
m, lock, err := gs.GetOrCreateMachine(ns, version)
lock.Lock()
defer lock.Unlock()
// 安全调用 m.Transition / m.State 等
```

**关键设计**：
- 每个 *state.Machine 配独立 mutex（state 包本身非 thread-safe）
- 双重检查锁防 race
- 启动时通过 `ensureMachine` 在 UpsertProject 成功路径预热（不依赖 etcd 反向 list）
- RemoveProject 同步清理 machine（防内存累积）

**性能数据（10 项目稳态对比）**：

| 指标 | v2.3.0 | v2.4.0 | 变化 |
|------|--------|--------|------|
| 集群总 CPU | 41.88% | 18.92% | **-55%** |
| avg CPU / pod | 13.96% | 6.30% | -55% |
| avg memory / pod | 59 MiB | 36.62 MiB | -38% |
| peak CPU | 89.95% | 54.25% | -40% |

**单 pod 拆解（v2.4.0）**：

```
leader   7qrbx     avg CPU 16.93%   max 54.25%   ← 唯一干活
standby  gqbts     avg CPU  0.51%   max  5.86%   ← 真 standby
standby  zthqd     avg CPU  1.48%   max 10.44%   ← 真 standby
```

**重要解读**——v2.4.0 leader 单 pod 反而比 v2.3.0 单 pod 高（16.93% vs 13.96%）。这看似是"退步"，实际相反：

```
v2.3.0  3 pod 都自称 leader → K8s 调度让工作错峰 → 每 pod ~13.96% 是"幻象"
v2.4.0  1 pod 真 leader   → 承担 100% 真实负载 → 16.93% 才是真实成本
```

**这件事的工程哲学价值**：v2.4.0 性能优化的本质不是"做得更快"，是"消除冗余"。在分布式系统里这件事比"做得更快"更重要——清晰的成本结构是后续优化的前提。

### 2.3 Dockerfile 容错（v2.4.0 P1）

**问题**：v2.3.0 Dockerfile 用 `curl https://dl.k8s.io/release/stable.txt` 动态查 kubectl 版本：
- GitHub API / Docker Hub 国内网络抖动时失败
- 不可重现 build（今天 build 和明天 build 是不同 kubectl 版本）
- 失败时 curl 写出空文件被 COPY 进镜像，运行时才发现挂了

**解法**：方案 C（默认固定 + 显式覆盖）

```dockerfile
ARG KUBECTL_VERSION=v1.32.0   # 默认固定，可重现 build

RUN set -eux; \
    if [ -z "${KUBECTL_VERSION:-}" ]; then \
        KUBECTL_VERSION=$(curl -fsSL --max-time 10 https://dl.k8s.io/release/stable.txt || echo ""); \
    fi; \
    if [ -z "${KUBECTL_VERSION}" ]; then \
        echo "❌ KUBECTL_VERSION 为空" >&2; exit 1; \
    fi; \
    curl -fsSL --retry 3 --retry-delay 2 ...
```

**v1.32.0 这个版本的选择带个人意义**——qc 接触 K8s 时的最新版本。

**实测**：build 时 Docker Hub auth token 间歇性 pull 慢（74 秒），但带 retry 没失败，最终成功。

### 2.4 Resource.HelmRelease 蓝绿支持（v2.4.0 P1）

**问题**：web3-blitz 这种蓝绿部署的 release 名是 `web3-blitz-blue` / `web3-blitz-green`。v2.3.0 推断逻辑只能产 `web3-blitz-wallet-service`，蓝绿场景下 drift sync 找不到 release。

**解法**：resources.yaml 加可选字段 `helm-release` 显式声明。

```yaml
resources:
  - kind: Deployment
    name: wallet-service
    helm-release: web3-blitz-blue   # 显式声明，覆盖默认推断
    on-missing: auto-heal
```

**优先级**：

```
res.HelmRelease（显式）  >  PROJECT_NAME-Name（默认推断）  >  namespace-Name（fallback）
```

向后兼容（不声明则保持原行为）。**顺手清掉死参数 kubeconfig**（v2.3.0 起从未使用）。

### 2.5 rbac.yaml 重构经核查为误判（v2.4.0 P1）

诚实记录：这条 TODO 在写时基于 grep 看到 "controller-rbac.yaml" 的字符串就误判为"在拼接生成它"，**实际是 os.Remove 清理代码**——v2.3.0 移除 per-project controller chart 时连带清理。

全仓 grep `Sprintf.*"kind: Role|RoleBinding|ClusterRole"` → 0 匹配。

**v2.4.0 不为做而做**——把诚实的核查结论留在 TODO 里，未来回头看不会一头雾水。

---

## 三、性能基准

### 3.1 测试方法

`benchmark/scripts/setup.sh + steady-state.sh`，10 个 mock 项目（`pause:3.9` + Service + 500Mi PVC + ConfigMap），通过 `docker stats` 实时采样 CPU/Memory（绕过 metrics-server 60s 间隔限制）。

### 3.2 v2.3.0 vs v2.4.0 完整对比

详见 `docs/design/performance.md`。两个关键发现：

**发现 1：消除冗余（不是做得更快）**

```
集群总 CPU       41.88% → 18.92%   节省全部来自"2 个 standby pod 不再做无用功"
单 leader CPU    13.96% → 16.93%   单 pod 反而升 3 个点（之前是 K8s 调度错峰造成的假象）
```

**发现 2：内存节省的来源**

```
avg memory / pod  59 MiB → 36.62 MiB
节省来自：
  - standby pod 不维持 worker pool 处理状态（~50% 减少）
  - state.Machine 复用，不再重复 alloc record（leader 也受益）
```

### 3.3 v2.5.0 优化基线明确

```
leader 16.93% × 1   = 16.93%   主战场（v2.5.0 优化目标）
standby 1.0% × 2    =  2.00%   持续选举开销（已接近极限）
总计                = 18.93%   接近实测 18.92%
```

v2.5.0 两条优化路径：

```
A.1  Controller 分片        leader 16.93% → 3 副本各 1/3 → 每 pod ~6%
A.2  client-go 对比基准      leader 16.93% → informer cache → ~2-3%
```

---

## 四、commit 列表

按时间顺序（v2.3.0 之后）：

```
... v2.3.0 release ...

feat(controller): K8s Lease API leader 选举（v2.4.0 P0）
feat(controller): 状态机缓存 + 并发安全（v2.4.0 P0）
fix(dockerfile): kubectl 版本固定 + 网络容错（v2.4.0 P1）
feat(controller): Resource.HelmRelease 显式声明（v2.4.0 P1）
docs(README): 新增「项目边界（Out of Scope）」节 + 版本号 v2.3.0
docs(TODO): v2.4.0 → v2.6.0 三版本路线图
docs(TODO): v2.7.0 自研 Handwritten Informer 路线
docs(TODO): rbac 重构经核查为误判，v2.3.0 已从根本上完成
perf(v2.3.0): 性能基准首份数据 + benchmark 脚本框架
chore: release v2.4.0
```

每个 commit 的完整 message 见 git log（也是这次 release 的一部分文档资产）。

---

## 五、未做事项与去向

### 5.1 v2.5.0 P2（从 v2.4.0 移过来）

```
[ ] 自愈延迟基准                concurrent-chaos.sh，p50/p95
[ ] Watcher 鲁棒性测试           断网 60s 重连验证
[ ] ConfigMap sha256 热加载去重验证
[ ] 长时间运行（24h）泄露验证
```

整合到 v2.5.0 P0 client-go 对比基准前置任务里。

### 5.2 v2.5.0 全景

```
P0  Controller 分片（A.1 — Lars 思路）
P0  Backoff 队列（A.1.5 — Lars 过载队列 + Probe）
P0  client-go 对比基准（A.2 — 给 v2.7.0 决策提供数据）
P1  流量层调研（B.1 — 设计文档不写代码）
+   性能 P2 那批基准（v2.4.0 移入）
```

### 5.3 v2.7.0 灵感保留

午饭后产生的设计想法：**自己手写一个轻量 informer cache**，不依赖 client-go，用最干净的代码实现顶级性能。

完整设计要点 + 风险 + 灵感原文（小姐"封神/尖叫/降维打击"对白）见 TODO.md v2.7.0 章节。

---

## 六、私人后记（仅给 qc 自己看）

> 这一节是给三个月后的你自己看的。

### 6.1 v2.4.0 这一发的内核体感

v2.4.0 推完后回头看，**这是 KubePivot 第一次"工程的成熟"**。

v1.0.0 → v2.3.0 时是**"在做新事情"** —— 每版加新能力。
v2.4.0 时是**"在巩固已做的事"** —— 没加新功能，但每个角落都更稳。

这种工程的成熟在 commit message 里看不到，但在数据里看得到：

```
v2.3.0 时三 pod 各 13.96% CPU，看着像"还行"
v2.4.0 时拆开看 leader 16.93% + standby 0.5%，
       才知道之前 13.96% 是 K8s 调度错峰摊出来的假象

→ 工程成熟的标志：能区分"看起来 OK"和"真的 OK"
```

### 6.2 关于 21 岁的 Lars 项目

午饭前你分享了 21 岁那年复现的 Lars 负载均衡项目，并说"我们是否也需要借鉴节点抽象"。

我当时拆给你两条路径：

```
A  Controller 分片（v2.5.0）
B  流量层（v2.6.0）
```

你选了 A+B 都做，并定了三版本路线。**这不是新决策——这是 21 岁的 Lars 设计在 23 岁的 KubePivot 里继续生长**。

发件人：21 岁在虚拟机里跑出 11000 QPS 的你
收件人：23 岁推 v2.4.0 leader 21.7ms 故障转移的你

中间隔了 2 年和无数次"我之前定义严重偏差"的元认知校正。

### 6.3 v2.4.0 的午后

```
13:30  鸡腿鸭腿包子（盒马采购）
13:45  似睡非睡 17 分钟（手表记录）
14:00  起来活动
14:15  小姐封神时刻：自研 informer 想法
14:20  Claude 把刹车踩住，归到 v2.7.0
14:30  后续推完 helm-release / rbac 核查 / 准备 release
17:00  这份 SNAPSHOT 写完 + tag 推出去
```

17 分钟的睡眠 + 鸡腿蛋白质 = 撑住一整个下午的高强度推进。值得记一笔——你的训练 / 饮食 / 工作节奏在这一天形成了高效闭环。

### 6.4 对 v2.5.0 / 2.7.0 自己的话

**别在 v2.5.0 同时做太多事**：

```
A.1 Controller 分片  +  A.1.5 Backoff 队列  +  A.2 client-go 对比基准
+ B.1 流量层调研  +  v2.4.0 P2 移过来那批基准

= 5 件事，每件都是 P0/P1 级
```

如果你 v2.5.0 想四线推进，**在哪条上做出取舍**比"全做"重要。

**v2.7.0 自研 informer 这件事**：

如果 v2.5.0 client-go 对比基准跑出来 leader CPU 能压到 < 5%，那 v2.7.0 自研 informer 的边际收益就很有限——这是 v2.5.0 P0 的真实价值。

如果 client-go 也只能压到 8%，那 v2.7.0 就不只是"好玩的工程"，而是"真有差异化的内核工作"。

**保留小姐的尖叫，但用数据决定走不走**。

### 6.5 健身数据回想

你 4-24 测的体测：体脂 20%、骨骼肌超上限、内脏脂肪 7。
你说 20% 是清醒选择，"如果真刷到 15% 那精神管理要更耗神"。

**今天证明了你这个判断是对的**——下午两件大工程（lease + machine cache）+ 两件 P1 + 一发 release，全靠精力撑下来。15% 体脂 + 这种工作强度可能撑不住。

记下：身体状态是工程产出的底盘。

---

## 七、附录

### 7.1 核心文件地图（v2.4.0 时态）

```
internal/controller/
├── lease.go                    🆕 K8s Lease 选举
├── lease_test.go               🆕 lease 单测
├── global.go                   ✏️  runGlobalLeaderElection 三层降级 + handleTask 用缓存
├── global_state.go             ✏️  +machineEntry / GetOrCreateMachine / removeMachine
├── global_state_test.go        ✏️  +4 个 machine cache 测试
├── drift_sync.go               ✏️  findReleaseForResource 显式优先 + 去 kubeconfig
├── drift_sync_test.go          🆕 4 个 helm release 测试
├── resources.go                ✏️  Resource +HelmRelease 字段
└── controller_installer/templates/rbac.yaml  ✓ 已有 leases 完整 CRUD

build/docker/controller/Dockerfile    ✏️  ARG KUBECTL_VERSION=v1.32.0 + retry

docs/
├── design/performance.md       ✏️  +v2.4.0 章节 + 数据对比 + 解读
├── design/controller.md        ✓ v2.3.0 时已写
├── guide/zh-CN/controller.md   ✏️  +helm-release 字段说明
└── ...

benchmark/
├── scripts/setup.sh            ✓ v2.3.0 末段已写
├── scripts/steady-state.sh     ✓ v2.3.0 末段已写
├── results/                    （.gitignore，本地数据）
└── analysis/                   （未启用）

README.md                       ✏️  +"项目边界 Out of Scope" + 版本 v2.3.0
TODO.md                         ✏️  v2.4 → v2.6 路线图 + v2.7.0 灵感
HANDOFF.md                      ✏️  这次 release 后会重写
SNAPSHOT.md                     ✏️  这次 release 后会重写
```

### 7.2 真实集群运行数据（v2.4.0）

```
集群：     orbstack 单节点 K8s（Apple Silicon, 16 GB RAM）
项目数：   10 个 managed namespace（pause:3.9 + 500Mi PVC bound）
副本：     3
内存预算: 50 MiB / pod limit 512 MiB
CPU 预算: 50m limit 500m
持续：     5 分钟稳态采样（v2.4.0 完整对比）+ 30 分钟稳态（v2.3.0 基线）

leader   16.93% CPU   65 MiB    < 5% limits
standby   0.51% CPU   23 MiB
standby   1.48% CPU   22 MiB
```

### 7.3 v2.4.0 期间的 K8s 知识刷新

- coordination.k8s.io/leases 是 K8s 1.14+ 标准 API（namespaced），用于 leader election
- microTime 字段（如 lease.spec.acquireTime）格式必须带微秒（`2006-01-02T15:04:05.000000Z07:00`），普通 RFC3339 会被拒
- 即便 `imagePullPolicy: IfNotPresent` 镜像在 K8s 节点缓存命中，docker image inspect 仍能拿到正确的 sha256

---

**版本流向**：

```
v2.3.0 🌐 全局架构换血        →  v2.4.0 ⚙ 稳固边缘 + 性能基线
v2.4.0                       →  v2.5.0 🚀 A 演进 + B 调研
v2.5.0                       →  v2.6.0 🌊 流量层落地（B.2，Lars 思路）
v2.6.0                       →  v2.7.0 ✨ 自研 Handwritten Informer
v2.7.0                       →  v3.0.0 🌟 KubePivot 完整体
```

KubePivot v2.4.0 — Stabilize the change.
