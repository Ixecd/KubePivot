# HANDOFF — KubePivot v2.7.0

> 当前 HANDOFF（持续演化）
> 编写日期：2026-04-27
> Last release: v2.7.0 (commit 69b6a0e, tag v2.7.0)
> Total commits: 431

---

## 一、项目定位

**KubePivot (kp)** 是一个面向 GitOps 场景的 Kubernetes 部署工具，
把 K8s 资源 / Helm release / 流量切换 / 数据库迁移 都纳入 **Git 真相 + 状态机**。

**核心论断**：
- KubePivot 不是 Service Mesh / Ingress Controller / APM 的替代品
- KubePivot 是"GitOps 视角下的 K8s 操作员"——
  把命令式部署变成"声明式 + 状态机驱动"

**与 ArgoCD / Flux 的区别**：
- ArgoCD/Flux 关注"Git → 集群"的同步
- KubePivot 关注"开发者 → 集群"的全链路（含 helm + migrate + 蓝绿 + 自愈）
- KubePivot 是**单二进制 CLI**，不需要装 controller（虽然也可装）

**v2.7.0 起的延伸论断（"工具 → 工程 → 智能调度系统"）**：
- v1-v2.6: 工具 → 工程（部署 / 蓝绿 / 状态机 / 分片）
- v2.7-v3.0: 工程 → 智能调度系统（Event Stream Infrastructure → Sizing → Scheduling）
- 核心论断："Informer ≠ Cache" — KubePivot v2.7 不是重新发明 client-go，
  而是为 KubePivot 自己的 reconcile 模式做专用 Cache
- v2.7.0 5 项 benchmark 数据驱动验证 (1.65-46x 时间提升 + 5.4x allocs 降低)

**MIT 许可，故意低调推广**——目标是逐步成为像 `make` 一样无形的工具。

---

## 二、现在能做什么

### 2.1 直接可用（用户视角）

```bash
# 项目脚手架
kp init --name myapp --module github.com/me/myapp

# 部署
kp deploy                         # 单服务
kp deploy --bluegreen             # v2.0 命令式蓝绿
kp deploy --env prod              # 多环境
kp deploy --changed-only          # 增量部署（git diff）
kp deploy --preview               # Header-based 流量预览

# 状态 & 对比
kp status                         # 当前部署状态
kp status --all-envs              # 多环境对比
kp diff --from-env staging --to-env prod

# 蓝绿（v2.6 声明式，推荐）
# 编辑 configs/resources.yaml 的 traffic 字段
kp sandbox commit                 # LOCKED → ... → COMMITTING (含流量切换) → RUNNING

# 数据库迁移
kp migrate status / plan / run / fix-dirty
kp compat check                   # API 兼容性检查（oasdiff）

# Operation Sandbox（v1.8.0+，迁移原子性）
kp sandbox start --dry-run
kp sandbox start                  # LOCKED→SNAPSHOTTING→SIMULATING→COMMITTING→RUNNING
kp sandbox status / unlock

# Secret 管理
kp secret rotate / sync / audit

# 多集群管理
kp context add --name prod --context my-k8s --namespace production

# 企业合规
kp audit --format table
kp policy add / check

# 混沌工程
kp chaos inject / list / stop / status

# 工具链
kp doctor / doctor --perf
kp scan / version / update
kp plugin install <name>
kp release --version vX.Y.Z       # 自动 commit + tag + push
```

### 2.2 v2.5.0 + v2.6.0 + v2.7.0 已实现

**v2.5.0 — Controller 分片机制**
- 多副本 controller（每 pod 持有部分 namespace shard）
- HA 边界：故障 pod 的 shard ~10 秒被其他 pod 抢占
- 孤儿清理：周期性扫描孤儿 namespace 并 cleanup
- benchmark 工具链：matrix.sh / steady-state.sh / setup.sh / cleanup.sh
- 性能数据：P=10 (avg CPU 7.27%) / P=50 (avg CPU 33.80%)

**v2.6.0 — 流量层抽象 + 声明式蓝绿（2026-04-26 release）**
- `internal/route/` Provider 接口 + Ingress + Gateway API 双实现
- `resources.yaml` 新增 `traffic:` 字段（声明式蓝绿）
- Sandbox COMMITTING 内分两步执行：helm + 流量切换
- Pod ready 健康判定 + 失败自动 RESTORING
- `docs/example-blue-green/` 完整 demo 工程

**v2.7.0 — Event Stream Infrastructure（2026-04-27 release）**

内部基础设施 release，用户视角 CLI 行为不变。

- `internal/eventstream/` 自研 Informer + Cache (~3700 行 / 89.0% 覆盖)
  - SkeletonCache：lock-free 读 (atomic.Value snapshot) / namespace 二级索引
  - ParseSkeleton：增量序列化（仅 metadata + spec.replicas + status.phase）
  - Watch loop：HTTP chunked + bufio.Scanner NDJSON / 0 client-go
  - 重连退避：指数 + jitter=0.2 / 防雷鸣群
  - 差异化 resync：10/30/60/120 min（按资源类型）
  - in-cluster auth：TLS 1.2+ / Bearer Clone / Token 不泄漏
  - Prometheus collector（9 项 informer 指标 / Pure Collector 模式）

- `internal/controller/` 渐进切换到 informer（双保险）
  - InformerPool：pool 管理 + fail soft（既有 KubectlWatcher 路径完整保留）
  - InformerDetector：cache hit 信任 / cache miss → kubectl fallback
  - LabelGetter fast path：heal.go loadResourceLabels 改造（cache hit ~50ns）
  - 既有 75 个 controller 测试 0 改动（Detector 接口未扩展）

- `internal/metrics/` 业务指标层 (1119 行 / 81.2% 覆盖)
  - MetricsClient 接口（GetPodMetrics / GetNodeMetrics / List）
  - KubectlMetricsClient（kubectl top -o json）
  - Quantity 自实现解析（CPU milli-cores / Memory bytes / SI vs 二进制）
  - 为 v2.9 Sizing Engine 铺垫数据基础

- 5 项 benchmark 数据公开（vs client-go cache.Indexer）
  - Cache Get：14.5ns vs 45ns（3.1x 时间 / 6x 内存）
  - Cache List(ns)：147ns vs 6800ns（46x 时间 / 100x 内存）⭐
  - ColdStart 10000：341ms vs 645ms（1.9x / 8.6x 内存）
  - Watch Throughput 10k：382ms vs 632ms（1.65x / 5.4x allocs）⭐
  - 内存放大率：1.65x vs 4.69x（2.84x 优势）⭐

### 2.3 已知不做

- ❌ Service Mesh / Ingress Controller / APM
- ❌ canary 渐进发布（v2.7+ 候选已推迟到 v2.8 范围）
- ❌ 数据敏感资源保护机制（v2.8 候选）
- ❌ Web UI（KubePivot 故意保持 CLI-first）
- ❌ kubeconfig 完整解析（v2.7.x 计划，本地开发场景）
- ❌ Pod informer（v2.7.x / v2.8 计划，heal.go list pods 改造）
- ❌ controller HTTP server + /metrics endpoint（v2.7.x 计划）
- ❌ PrometheusClient（v2.7.x / v2.8 计划，v2.9 sizing engine 启动前必须）

---

## 三、开发约束（永久规范）

### 3.1 不引入 client-go

KubePivot 故意 **不依赖 K8s client-go 库**：
- 所有 K8s 操作通过 `kubectl` CLI（`internal/executor` 包封装）
- v2.7.0 起：自研 Informer + Watch（`internal/eventstream/`，net/http + NDJSON）
- 单二进制编译产物 ~30MB（client-go 会让它涨到 ~100MB）
- 用户安装 KubePivot 不需要预装 K8s libraries

**例外（v2.7.0 起）**：
- `prometheus/client_golang` v1.20.5（Pure Collector 模式打点工具，纯打点不入侵核心逻辑）
- 测试集成（`test/integration/`）可临时用 client-go 写测试断言
- `feature/client-go-comparison` 分支保留 benchmark baseline（不合并 Master）

### 3.2 commits/ 目录（v2.5.0 起的工程规范）

每个 commit 之前先在 `commits/` 目录写 commit message 草稿：

```
commits/
├── v2.5.0-step1-shard-base.txt
├── v2.6.0-step1-route-package.txt
├── v2.7-step1-day2-pkg-base.txt
├── v2.7-step2a1-incluster-auth.txt
├── v2.7-step3-metrics-client.txt
├── v2.7-step5a-changelog.txt
└── ...（v2.7 累计 ~17 个）
```

`git commit -F commits/v2.X.Y-stepZ.txt` 引用文件作为 commit message。

理由：
- 写代码时一边写一边记下"为什么这么改"
- commit message 不再是事后凑数的描述
- 历史 commits/ 目录本身是工程档案

### 3.3 版本号约定

```
v{major}.{minor}.{patch}

major 变更（v1 → v2）：项目名/范围根本性改变（如 dev-toolkit → KubePivot）
minor 变更（v2.6 → v2.7）：新功能 / 新接口
patch 变更（v2.7.0 → v2.7.1）：bug 修复 / 文档 / 内部优化（不打 tag）

实践：
- 真 tag 只打 minor：v2.0.0 / v2.5.0 / v2.6.0 / v2.7.0
- patch 版本是"持续改进"，不打 tag，不算 release
- 跨 minor 之间可能 100+ 个内部改进 commit
```

### 3.4 设计先对齐再写代码

KubePivot 的工程速度依赖 **"设计先行 + 实施快速"**：

```
新功能流程：
1. 写 docs/design/<feature>-draft.md
2. 列出所有需要拍板的设计 Q（A/B/C 选项）
3. 当面拍板（不在写代码时还在想）
4. 实施时严格按设计走
5. 实施完成后 -draft 后缀去掉，文档变"实施记录"
6. 实施期再加一份 -impl-notes.md（与 draft 互补）
```

**v2.6.0 案例**：traffic-layer-draft.md 14 个 Q 全部拍板后，
Step 1 实施 ~2 小时（990 行代码 + 530 行测试），无返工。

**v2.7.0 案例**：eventstream-draft.md 14 个 Q + 实施期 R/S/M/B 多轮拍板：
- 设计阶段：Q1-Q14（自研路径 / Cache 双驱动 / Skeleton 字段 / immutable snapshot ...）
- 实施期：R1-R5 (informer pool 接入) / S1-S6 (InformerDetector) / M1-M5 (MetricsClient) / B1-B5 (Bench 3)
- 整个 v2.7 27 个 commit 全程 0 fix commit 流入 Master（本地修复 9 个 bug）

### 3.5 SNAPSHOT 永久归档约定

每次 minor release 都归档：
- `archived/handoff/HANDOFF-v{ver}.md`
- `archived/todo/TODO-v{ver}.md`
- `archived/snapshot/SNAPSHOT-v{ver}.md`

并在 `snapshots/` 目录保留版本快照：
- `snapshots/SNAPSHOT-kubepivot-{date}-v{ver}.md`

理由：
- HANDOFF/TODO 是"演化文档"，旧版本会被覆盖
- snapshots/ 是"档案"，永久保留某个版本的全景
- 半年后回看时，snapshots/ 比 git log 更直观

### 3.6 真实集群验证不可跳

KubePivot 测试覆盖三层：
1. **单测**（`go test ./...`）：包内逻辑正确
2. **集成测试**（`test/integration/`）：与 K8s API 交互正确
3. **真实集群验证**：在 orbstack / k3s / kind 上跑 demo 工程

**第 3 层不可跳**：
- 单测 PASS 但真实集群跑挂的 case 太多（K8s API 行为差异 / RBAC / kubelet 版本）
- 每次 minor release 前必须在真实集群跑过 demo

**v2.6.0 案例**：`docs/example-blue-green/` demo 工程
设计为"任何 K8s 集群 < 2 分钟跑通"。

**v2.7.0 案例**：v2.7 是"代码 release"不是"镜像 release"
- InformerPool / InformerDetector 双保险接入，生产 pod 行为完全等价 v2.6.0
- in-cluster auth 在真实集群的 e2e 验证留 v2.7.x（HTTP server build 之后）
- 当前 5 项 benchmark 数据 + race detector + 集成测试构成"代码可信度"

### 3.7 重复踩坑记录（教训复利）

#### Bash 脚本的严格模式陷阱

**`set -e` / `set -u` 慎用**：

```bash
# ❌ 容易引发误退出
set -euo pipefail

# 例 1：函数返回非零（正常逻辑）触发整个脚本退出
function check_status() {
    grep -q "ready" status.txt   # 没找到 → 返回 1 → set -e 退出
}

# 例 2：未定义变量 + set -u
echo "${UNDEFINED_VAR}"          # set -u 触发退出，但 default 值 "${UNDEFINED_VAR:-}" 才是稳妥写法
```

**实践规范**：benchmark/scripts/ 全部仅用 `set -o pipefail`，不用 set -e/-u。

#### 衍生陷阱：变量名紧贴中文标点（同根问题，不同表现）

bash 解析变量名时，会把 `$var` 后面**所有合法的标识符字符**当作变量名的一部分。
但 bash 对"标识符字符"的判断不区分 ASCII 和多字节字符——中文标点字符
（`，` `）` `（` 等）会被当作变量名延续。

```bash
# ✗ 错误（bash 把 "total，剩余" 当成单个变量名）
log "进度: $total，剩余 $remaining 个"
# 输出：  进度: ，剩余  个      （两个变量都没显示）

# ✓ 正确：用 ${} 显式定界
log "进度: ${total}，剩余 ${remaining} 个"
ok "Controller 已停（原副本数 ${original_replicas}）"
```

**实践规范**（v2.5.1 起）：
- 凡是 `$var` 后面紧贴非 ASCII 字符（中文标点、括号、特殊符号），一律 `${var}` 显式定界
- 简单情况 `"总数: $total"` 后面是空格 / 行尾不需要 `${}`
- 但建议 default 都用 `${var}` 风格，避免心智负担

#### Go 闭包自引用

```go
// ❌ 自引用闭包：编译报 "undefined: checkPod"
checkPod := func(name string) bool { ... return checkPod(name) }

// ✅ 用 var 先声明
var checkPod func(name string) bool
checkPod = func(name string) bool { ... return checkPod(name) }
```

#### Go 包 import cycle

```
internal/sharding 想 import internal/state（用 state.Machine）
但 internal/state 又 import internal/sharding（用 sharding.ShardKey）
→ import cycle

解法：
1. 把共享类型抽到第三个包（如 internal/types）
2. 依赖反转：用 interface 在依赖方定义、实现方实现
3. v2.7.0 案例：eventstream 不直接 import sharding
   仅 adapter_sharding.go 一个文件做桥（保持依赖单向）
```

#### Commit message 里的特殊字符

```bash
# ❌ git commit -m 里有反引号 / 双引号 / $ 时
git commit -m "v2.5.0: 引入 `code generator`"
# Bash 把 `code generator` 当成命令执行 → 失败

# ✅ 用 -F 引用文件（commits/ 目录的好处）
echo "v2.5.0: 引入 \`code generator\`" > commits/v2.5.0-codegen.txt
git commit -F commits/v2.5.0-codegen.txt

# ⚠️ git commit -m 和 -F 不能同时用 (v2.7 实施时踩到)
# 用 -F 时绝对不要再加 -m，git 会报错
```

#### codegen 工具的多 const block 局限（v2.6.0 发现）

```
KubePivot 的 tools/codegen 自动从 const block 生成 String() 方法。
但它的 ast.Inspect 实现只扫描"第一个 const block"，
分两个 block 时只生成第一组。

临时 workaround：手工补 code_generated.go 的 case 分支
长期修法：v2.6.1 / v2.8.1 候选——codegen.go genDecl 改造（~30 行）
```

#### kp release 工具的精确行为（v2.6.0 确认 + v2.7.0 复用）

```
kp release --version v{X}.{Y}.{Z} 自动：
1. 校验 v\d+\.\d+\.\d+ 格式
2. 检查工作区干净
3. 检查 tag 不存在
4. 改 configs/project.env: VERSION=
5. 改 cmd/kp/version.go: kpVersion =
6. git add + git commit -m "chore: release vX.Y.Z"
7. git tag -a vX.Y.Z -m "release vX.Y.Z"
8. git push
9. git push --tags

要点：
- kp release 会产生一个 commit（"chore: release"）
- tag 打在这个 commit 上（不是上一个）
- 想要"第 N 个 commit 是 release"的话，提前规划 N-1 个 commit
- CHANGELOG.md 必须在 release 之前完成（与 tag 同步）

v2.6.0 案例：404 = release tag (commit 落点 = 蓝绿 Found)
v2.7.0 案例：431 = release tag (commit 落点未刻意对齐日期，工程纪律 > 数字仪式感)
```

#### sandbox.go 的 fork 子进程模式（v2.6.0 接入根因）

```
runSandboxCommit() 内部不直接调 helm，而是 fork 子进程跑 kp deploy。
意味着 v2.6 流量切换不能放在 deploy.go，必须放在 sandbox.go。
```

#### Informer Stop without Start 的 timeout（v2.7 Day 3 / Bug 1）

```
原代码：
  doneCh 由 startOnce 控制
  startOnce 已用 → doneCh 会 close
  startOnce 未用过 → doneCh 永远不 close
  Stop 走 5s timeout 兜底分支，打 WARN 日志

修复：加 atomic.Bool started 字段
  Start 中 started.Store(true)
  Stop 仅在 started=true 时等 doneCh

教训：sync.Once 适合"只做一次的初始化"
     不适合作为"是否已启动"的状态判断
```

#### WatchReconnects 漏计正常关流（v2.7 Day 3 / Bug 2）

```
原代码：
  watchReconnects.Add(1) 仅在 err != nil 时计数
  
真实生产：
  K8s API server 会在 5-10min 主动关闭 watch 长连接（housekeeping）
  doWatch 返回 nil（无错误）
  → 这种 reconnect 漏计，Prometheus 监控指标失真

修复：watchReconnects.Add(1) 移到 if err 之外
     任何 watch 退出（无论原因）都算 reconnect

教训：边界场景测试（fake server SetForceCloseAfter）暴露生产监控指标 bug
     测试设计不能只覆盖"err != nil 路径"
```

#### Prometheus collector 模式（v2.7 Day 5 / Bug 5.1）

```
原设计：每个 informer 一个 collector
        每个 collector 内部独立创建 desc
        注册多个 collector 到同 registry → desc fqName 冲突
        报 "duplicate metrics collector registration attempted"

修复：架构层重写
     desc 全局共享（一组）
     单 collector 持有多个 informer
     通过 resource label 区分指标输出
     RegisterInformerMetrics 改为 variadic 一次性注册

教训：Prometheus collector 模式应是 "一个 collector = 一组相关 metrics"
     不是 "一个 collector = 一个数据源"
     这是 client_golang 内置 collector（GoCollector 等）的标准模式
```

#### KubePivot executor 是具体类型单例（v2.7 Step 3 / Bug 8）

```
原假设：executor 包有 Executor interface + SetExecutor 全局替换
        （这是标准 Go testing 模式）

真相：KubePivot 的 executor 是具体类型 *KpExecutor 单例
      没有 Executor interface
      没有 SetExecutor 函数

修复：函数变量注入式 mock
      KubectlMetricsClient.kubectl func(...) ([]byte, error)
      生产时默认绑 executor.GetExecutor().Kubectl
      测试时替换为 mockFn

工程模式（v2.7 起的标准）：
  internal/eventstream/auth.go         readTokenFile / readCAFile (var)
  internal/controller/informer_pool.go newInformerFunc (var)
  internal/metrics/kubectl.go          kubectl func 字段

教训：跨包依赖必须先看清现状（grep + 看代码）
     不能假设"标准 Go testing 模式"
     KubePivot 有自己的工程哲学（接口层 mock，不在 executor 层 mock）
```

#### 全局信息收集是后期工程必需（v2.7 实施期反复印证）

```
现象：v2.7 实施过程中我多次"假设"现状，被 cross-check 拦下：
  1. 假设 KubePivot 有 SetExecutor → 没有
  2. 假设 Bench 3 应该用 envtest → Bench 1/2/4/5 都没用
  3. 假设 Detector 接口需要扩展 → 用类型断言 + 独立 LabelGetter 接口

解法：每个 Step 实施前
  1. grep 看现状（既有接口 / 既有测试 / 既有 mock 模式）
  2. 看代码（不只看 commit message，看真实实现）
  3. 对照既有风格再下手

教训："设计的太多了，遗忘是很正常的，
       越到后面设计对齐就越要 cross-check，
       尽量获取全局信息之后再设计"
       
       KubePivot 已 ~16000 行代码，凭记忆设计的回归风险高
       多花 5min grep 替代 30min debug，划算
```

#### 基准公平性原则（v2.7 Step 4）

```
现象：Bench 3 (Watch 吞吐) 原计划用 envtest 模拟真 K8s API server
      看似"工程严谨"，实际：
        - Bench 1/2/4/5 都用本地数据模拟（避免网络栈噪声）
        - envtest 引入 kube-apiserver 性能噪声
        - 测出来的"informer 吞吐"是 min(API 推送速率, informer 处理速率)
        - 与 Bench 1/2/4/5 维度不可比

解法：改用 fake watch source（与 Bench 1/2/4/5 同公平赛道）
     重用 SkeletonCache + ParseSkeleton（基础设施一致性）
     在 perf.md 诚实记录设计调整理由

教训：
  "工程敬畏" ≠ "用最严格的环境"
  "工程敬畏" = "测对的东西，不多不少"
  
  benchmark 必须保证"基准公平性"——
  改变实验环境会让历史数据不可比
  与既有 Bench 同维度才有意义
```

#### 工程纪律 > 数字仪式感（v2.7 实施期沉淀）

```
项目史诗：
  v2.0.0 → commit 329 → 生日 3.29 (巧合)
  v2.6.0 → commit 404 → 蓝绿 Found (commit 落点刻意对齐 4月26日)
  v2.7.0 → commit 431 → ? (未刻意对齐，但 4月27日 tag 仍连续性)

实施期纠结："应该让 v2.7.0 落在 commit 427 / 430 / 431？"

最终决策：顺其自然
  - 工程纪律 > 数字仪式感
  - 不为追求 commit 数字延后或提前 release
  - 4月27日 tag 仪式感保留
  - commit 数次要

教训：项目史诗的连续性来自"持续高质量交付"
     不是"每次 commit 数都对得上特殊日期"
     强行刻意对齐反而是过度设计
```

---

## 四、当前工作流

### 4.1 日常开发循环

```
1. 写代码（按 docs/design 设计）
2. make dev      → go test ./... + go install
3. 真实集群跑测：cd /tmp/test-kp && kp init --name foo && kp deploy
4. git add + commit -F commits/...txt
5. git push
6. 下一个 commit
```

### 4.2 性能基准

```
benchmark/scripts/                Controller 性能（v2.5.0+）
├── setup.sh / steady-state.sh / matrix.sh / cleanup.sh / hot-reload.sh

benchmark/eventstream/            Informer Cache 性能（v2.7.0+）
├── (仅 feature/client-go-comparison 分支)
├── 5 项 benchmark + 公平基准约定（GOGC=200 / GOMEMLIMIT=4GiB）
├── run-benchmarks.sh 一键执行 + 数据归档
```

数据归档：
- `benchmark/results/{date}-{test-name}/` (controller 性能)
- `docs/design/eventstream-perf/{date}/` (informer 性能)
- `docs/design/eventstream-perf.md` (汇总)

### 4.3 Release 流程

```
1. 全部代码合并到 Master，确保 working tree 干净
2. make dev 全绿
3. 真实集群跑 demo（最少 example-blue-green）
   v2.7.x：HTTP server build 后 e2e 验证 in-cluster auth
4. 写 commit message 草稿到 commits/
5. 写 docs/design/<feature>.md（去 -draft 后缀）+ -impl-notes.md
6. 更新 CHANGELOG.md 加 v{X}.{Y}.0 章节（必须在 release 之前）
7. kp release --version v{X}.{Y}.{Z}
   → 自动 commit + tag + push
8. 归档 archived/handoff/snapshot/todo/ 旧版本
9. 重写根目录 HANDOFF.md / TODO.md / SNAPSHOT.md（新 minor 视角）
10. 在 snapshots/ 创建新版本 SNAPSHOT-kubepivot-{date}-v{ver}.md
```

---

## 五、当前可工作状态

```
working tree:    干净（commit 432 文档大更新待提交）
HEAD:            69b6a0e (chore: release v2.7.0)
last tag:        v2.7.0
total commits:   431
go module:       github.com/Ixecd/kubepivot

internal/ 包：
  ai / bluegreen / code / controller / controller_installer
  eventstream  ← v2.7.0 新增
  metrics      ← v2.7.0 新增
  executor / logger / planner / route / scaffold / sharding / state

cmd/kp/ 命令（v2.7 不动）：
  init / deploy / status / sandbox / migrate / context / chaos
  audit / policy / secret / scan / doctor / preview / promote / warmup
  release / version / update / compat / sync / diff / rollback
```

---

## 六、文档地图

```
根目录（活跃）：
  README.md                  用户入口
  HANDOFF.md                 本文件
  TODO.md                    路线图
  SNAPSHOT.md                当前版本快照指针
  CHANGELOG.md               变更日志（v2.7.0 起含完整章节）
  FUTURE.md                  ← v2.7.0 新增 (远景探索 / F1 CBA 种子)
  ROADMAP.md                 ← v2.7.0 新增 (~2143 行 / v2.7→v3.0)
  GITOPS-MANIFESTO.md        "只保护，不越权"原则

docs/design/                 设计文档
  architecture.md            整体架构
  controller.md              Controller 设计
  state-machine.md           状态机设计
  sharding.md                v2.5.0 分片机制
  sharding-tuning.md         v2.5.1 性能调优雏形
  traffic-layer.md           v2.6.0 流量层
  eventstream-draft.md       ← v2.7.0 新增 (~1290 行 / 14 个 Q 拍板)
  eventstream-perf.md        ← v2.7.0 新增 (~452 行 / 5 项 benchmark 数据)
  eventstream-impl-notes.md  ← v2.7.0 新增 (~620 行 / Day 1-5 实施日志)
  decision-stack.md          三层资源决策栈
  performance.md             性能数据归档（controller 部分）

docs/example-blue-green/     v2.6 demo 工程

snapshots/                   版本快照档案
  SNAPSHOT-kubepivot-{date}-v{ver}.md   各 minor 一份

archived/                    历史归档（HANDOFF/TODO/SNAPSHOT/plan/docs）

commits/                     commit message 草稿（永久保留，v2.7 累计 ~50 个）
benchmark/                   性能测试工具链
  scripts/                   controller 性能（v2.5+）
  eventstream/               informer 性能（v2.7+，feature 分支独有）
test/integration/            集成测试
tools/codegen/               代码生成工具
```

---

## 七、下一步路线图（v2.7.1 / v2.8+）

完整路线图见 `ROADMAP.md`。

### 7.1 v2.7.1（持续改进，不打 tag）

```
[ ] kubeconfig 完整解析 (~200 行)
    auth.go 路径 2 当前返回 NotImplemented
    本地开发场景需要

[ ] Pod informer (Step 2c)
    heal.go list pods 改造
    引入 pods / v1 informer

[ ] controller HTTP server + /metrics endpoint
    informer pool RegisterMetrics 当前未调用
    Build 新 controller 镜像（v2.7.x 镜像 release）
    9 项 informer 指标暴露到 Grafana / Prometheus

[ ] PrometheusClient（高质量 metrics 数据源）
    internal/metrics/prometheus.go (Q6=B 双 client 第二实现)
    v2.9 sizing engine 启动前必须

[ ] NodeMetrics.AllocatableCPU/Memory 字段填充
    KubectlMetricsClient.GetNodeMetrics 当前不调 kubectl get node
    v2.9 调度需要

[ ] subscriber-level metrics（避免 cardinality 爆炸的方案）
[ ] shard OnShardChanged 事件订阅（adapter goroutine）
[ ] cache_policy MarkAccessed atomic 严格化
```

### 7.2 v2.6.1（v2.6 遗留任务）

```
[ ] 多环境流量配置传播
    kp deploy --env prod --from-env staging
    
[ ] tools/codegen 多 const block 改造
    
[ ] codegen 错误码 doc 自动生成
```

### 7.3 v2.5.1（性能立方体，前置依赖）

```
[ ] ⏸ 测试环境升级（前置：8 GiB → 16 GiB orbstack 或多节点 K3d）
[ ] ⏸ benchmark/scripts/matrix.sh 完整跑通
[ ] ⏸ sharding-tuning.md 性能立方体章节补完
```

### 7.4 v2.8 — Enterprise Governance（按 ROADMAP，~3 周）

```
[ ] A: SSO / OAuth (Google / GitHub / Dex)
[ ] B: RBAC 多团队隔离
[ ] G: 加密 at rest (Sealed Secrets / SOPS / KMS)
[ ] H: 镜像签名 + 供应链 (cosign + syft)
```

### 7.5 v2.9 — Resource Sizing Engine（按 ROADMAP，~4 周）

```
[ ] 二维 DP: Pod × (CPU, Memory)
    数据基础: internal/metrics ✓ (v2.7.0 已就位)
[ ] 4 个启发式 + 抖动检测 + VPA 共存
目标: 单 Pod 利用率 70%+
```

### 7.6 v3.0 — Intelligent Scheduling System（按 ROADMAP，~6 周）

```
[ ] 维度 A + B 双 DP 协同
    数据基础: internal/eventstream + internal/metrics ✓ (v2.7.0 已就位)
[ ] 周期性运行时重调度
[ ] 与 K8s 默认调度器共存
目标: 节点 CPU 85%+ / Memory 70%+ (务实)
预计 release: 2026-09 / 10 月
```

---

## 八、联系

```
作者：    qc <2192629378@qq.com>
GitHub：  https://github.com/Ixecd/KubePivot
许可：    MIT
```

---

## 编辑记录

```
2026-04-27  v2.7.0 release 后大改（commit 432, total 431）
            归档前一版到 archived/handoff/HANDOFF-v2.6.md
            主要变化：
            - 一节加 v2.7 起延伸论断（"工具 → 工程 → 智能调度系统"）
            - 2.2 节加 v2.7.0 完整章节（eventstream / metrics / 5 项 benchmark）
            - 2.3 节移除"自研 informer 候选"（已实现），加 v2.7.x 推迟项
            - 3.1 节加 prometheus/client_golang 例外说明
            - 3.4 节加 v2.7 案例（14 个 Q + 实施期 R/S/M/B 拍板）
            - 3.7 节加 v2.7 教训 7 条（Bug 1/2/5.1/8 + 全局信息收集 +
                      基准公平性 + 工程纪律 > 数字仪式感 + commit -m/-F 冲突）
            - 4.2 节加 benchmark/eventstream/ 新章节
            - 6 节加 FUTURE.md / ROADMAP.md / eventstream-* 三件套
            - 7 节路线图全重写（v2.7.1 / v2.8 / v2.9 / v3.0）

2026-04-26  v2.6.0 release 后大改（404 commits）
            归档前一版到 archived/handoff/HANDOFF-v2.5.md
            主要变化：
            - 第 2.2 节 v2.5.0 + v2.6.0 合并
            - 第 3.7 节加 3 条新教训（codegen / kp release / sandbox fork）
            - 第 7 节路线图全重写（v2.6.1 / v2.5.1 / v2.7 / v2.8）
            - 第 4.3 节 release 流程精确化（kp release 行为）
```
