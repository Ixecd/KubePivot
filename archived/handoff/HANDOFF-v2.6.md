# HANDOFF — KubePivot v2.6.0

> 当前 HANDOFF（持续演化）
> 编写日期：2026-04-26
> Last release: v2.6.0 (commit 1f780c2, tag v2.6.0)
> Total commits: 404

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
```

### 2.2 v2.5.0 + v2.6.0 已实现

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
- 27 个 sub-cases 单测全 PASS
- `docs/example-blue-green/` 完整 demo 工程

### 2.3 已知不做

- ❌ Service Mesh / Ingress Controller / APM
- ❌ canary 渐进发布（v2.7+ 候选）
- ❌ 自研 K8s informer（v2.7+ 候选）
- ❌ 数据敏感资源保护机制（v2.8 候选）
- ❌ Web UI（KubePivot 故意保持 CLI-first）

---

## 三、开发约束（永久规范）

### 3.1 不引入 client-go

KubePivot 故意 **不依赖 K8s client-go 库**：
- 所有 K8s 操作通过 `kubectl` CLI（`internal/executor` 包封装）
- 单二进制编译产物 ~30MB（client-go 会让它涨到 ~100MB）
- 用户安装 KubePivot 不需要预装 K8s libraries

**唯一例外**：测试集成（`test/integration/`）可临时用 client-go 写测试断言。

### 3.2 commits/ 目录（v2.5.0 起的工程规范）

每个 commit 之前先在 `commits/` 目录写 commit message 草稿：

```
commits/
├── v2.5.0-step1-shard-base.txt
├── v2.5.0-step3-orphan-cleanup.txt
├── v2.6.0-step1-route-package.txt
├── v2.6.0-step2-sandbox-bluegreen.txt
├── v2.6.0-step3-example-blue-green.txt
├── v2.6.0-step4-docs.txt
└── ...
```

`git commit -F commits/v2.6.0-stepX.txt` 引用文件作为 commit message。

理由：
- 写代码时一边写一边记下"为什么这么改"
- commit message 不再是事后凑数的描述
- 历史 commits/ 目录本身是工程档案

### 3.3 版本号约定

```
v{major}.{minor}.{patch}

major 变更（v1 → v2）：项目名/范围根本性改变（如 dev-toolkit → KubePivot）
minor 变更（v2.5 → v2.6）：新功能 / 新接口
patch 变更（v2.6.0 → v2.6.1）：bug 修复 / 文档 / 内部优化（不打 tag）

实践：
- 真 tag 只打 minor：v2.0.0 / v2.5.0 / v2.6.0
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
```

**v2.6.0 案例**：traffic-layer-draft.md 14 个 Q 全部拍板后，
Step 1 实施 ~2 小时（990 行代码 + 530 行测试），无返工。

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

# ✗ 错误（"original_replicas）" 当变量名）
ok "Controller 已停（原副本数 $original_replicas）"
# 输出：  Controller 已停（原副本数

# ✓ 正确：用 ${} 显式定界
log "进度: ${total}，剩余 ${remaining} 个"
ok "Controller 已停（原副本数 ${original_replicas}）"
```

**实践规范**（v2.5.1 起）：
- 凡是 `$var` 后面紧贴非 ASCII 字符（中文标点、括号、特殊符号），一律 `${var}` 显式定界
- 简单情况 `"总数: $total"` 后面是空格 / 行尾不需要 `${}`
- 但建议 default 都用 `${var}` 风格，避免心智负担

历史踩坑：
- 2026-04-26 hot-reload.sh `$reload_delta（期望 ≤ 1）` 显示成空（中文括号问题）
- 2026-04-26 cleanup.sh `$total，先停 controller` 显示成 `??`（中文逗号问题）
  — 当天第二次踩同样的坑，触发本节扩展

#### Go 闭包自引用

```go
// ❌ 自引用闭包：编译报 "undefined: checkPod"
checkPod := func(name string) bool {
    if checked[name] {
        return checked[name]
    }
    return checkPod(name)   // 编译时 checkPod 还没赋值
}

// ✅ 用 var 先声明
var checkPod func(name string) bool
checkPod = func(name string) bool {
    if checked[name] {
        return checked[name]
    }
    return checkPod(name)
}
```

#### Go 包 import cycle

```
internal/sharding 想 import internal/state（用 state.Machine）
但 internal/state 又 import internal/sharding（用 sharding.ShardKey）
→ import cycle

解法：
1. 把共享类型抽到第三个包（如 internal/types）
2. 依赖反转：用 interface 在依赖方定义、实现方实现
3. 接受不优雅：把状态机的 shard 部分内联在 sharding 包内（v2.5.0 选择）
```

#### Commit message 里的特殊字符

```bash
# ❌ git commit -m 里有反引号 / 双引号 / $ 时
git commit -m "v2.5.0: 引入 `code generator`"
# Bash 把 `code generator` 当成命令执行 → 失败

# ✅ 用 -F 引用文件（commits/ 目录的好处）
echo "v2.5.0: 引入 \`code generator\`" > commits/v2.5.0-codegen.txt
git commit -F commits/v2.5.0-codegen.txt
```

#### codegen 工具的多 const block 局限（v2.6.0 发现）

```
KubePivot 的 tools/codegen 自动从 const block 生成 String() 方法。
但它的 ast.Inspect 实现只扫描"第一个 const block"，
分两个 block 时（例如 ErrorCode 通用 + Route 域）只生成第一组。

实测案例（2026-04-26 v2.6.0 Step 1）：
  在 internal/code/error.go 加了 5 个 ErrRoute* 错误码
  分两个 const block（避免编号 100000 和 110000 之间跳跃）
  跑 go run tools/codegen/codegen.go 报 "no values defined for type ErrorCode"
  
临时 workaround：手工补 code_generated.go 的 case 分支
长期修法：v2.6.1 候选——codegen.go genDecl 改造（~30 行 ast.Inspect 改造，
        递归收集所有匹配 typeName 的 const block）
```

#### kp release 工具的精确行为（v2.6.0 确认）

```
kp release --version v{X}.{Y}.{Z} 会自动：
1. 检查工作区干净（不允许未 commit 的改动）
2. 检查 tag 不存在
3. 更新 configs/project.env 的 VERSION
4. 更新 cmd/kp/version.go 的 kpVersion 常量
5. git add + git commit -m "chore: release v{X}.{Y}.{Z}"
6. git tag v{X}.{Y}.{Z}

要点：
- kp release 会产生一个 commit（"chore: release"）
- tag 打在这个 commit 上（不是上一个）
- 想要"第 N 个 commit 是 release"的话，提前规划 N-1 个 commit

v2.6.0 案例：
  目标：404 个 commit 是 v2.6.0 release tag
  规划：400 起点 + 4 个 step commit (401/402/403/404)
        但 step4 是 docs commit (403)，kp release 自动产生 404
  实际：完美落地，404 = v2.6.0 ✨
```

#### sandbox.go 的 fork 子进程模式（v2.6.0 接入根因）

```
runSandboxCommit() 内部不直接调 helm，而是 fork 子进程跑 kp deploy。
意味着 v2.6 流量切换不能放在 deploy.go，必须放在 sandbox.go。

具体行为：
  COMMITTING 阶段：
  - sandbox.go 进入 COMMITTING 状态
  - runSandboxCommit() 内部：
      1. 跑 kp migrate run（如有）
      2. fork 子进程: kp deploy --namespace XXX
         子进程内部走 IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
         （子进程的状态机是独立的）
      3. 子进程退出后回到外层 sandbox
  - sandbox.go 在 commitOK 检查后判断成败

v2.6 接入位置：runSandboxCommit() 之后的下一行
  在 sandbox 的 COMMITTING 状态内追加流量切换逻辑
  与 sandbox 状态机协同（失败 → RESTORING）
  
为什么不能放在 deploy.go？
  - kp deploy 是给"普通部署"用的入口
  - 如果在 deploy.go 加蓝绿，所有 kp deploy 都会触发流量切换
  - 而蓝绿是 sandbox 模式独占的高级用法
  - 接入分离让普通部署不受影响
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
benchmark/scripts/
├── setup.sh          # 创建 mock 项目（PROJECT_COUNT=N）
├── steady-state.sh   # 稳态采样（5 分钟，每 5s 一次）
├── matrix.sh         # 自动化矩阵测试（v2.5.1 雏形）
├── hot-reload.sh     # 热加载幂等性测试
└── cleanup.sh        # 清理（≥30 项目自动停 controller）
```

数据归档：`benchmark/results/{date}-{test-name}/`

### 4.3 Release 流程

```
1. 全部代码合并到 Master，确保 working tree 干净
2. make dev 全绿
3. 真实集群跑 demo（最少 example-blue-green）
4. 写 commit message 草稿到 commits/
5. 写 docs/design/<feature>.md（去 -draft 后缀）
6. 更新 CHANGELOG.md
7. kp release --version v{X}.{Y}.{Z}
   → 自动 commit + tag + push（部分场景需手工 git push --tags）
8. 归档 archived/handoff/snapshot/todo/ 旧版本
9. 重写根目录 HANDOFF.md / TODO.md / SNAPSHOT.md（新 minor 视角）
10. 在 snapshots/ 创建新版本 SNAPSHOT-kubepivot-{date}-v{ver}.md
```

---

## 五、当前可工作状态

```
working tree:    干净
HEAD:            1f780c2 (chore: release v2.6.0)
last tag:        v2.6.0
total commits:   404
go module:       github.com/Ixecd/kubepivot

internal/ 包：
  ai / bluegreen / code / controller / controller_installer
  executor / logger / planner / route / scaffold / sharding
  state

cmd/kp/ 命令：
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
  CHANGELOG.md               变更日志（v2.6.0 起）
  GITOPS-MANIFESTO.md        "只保护，不越权"原则

docs/design/                 设计文档
  architecture.md            整体架构
  controller.md              Controller 设计
  state-machine.md           状态机设计
  sharding.md                v2.5.0 分片机制
  sharding-tuning.md         v2.5.1 性能调优雏形
  traffic-layer.md           v2.6.0 流量层
  performance.md             性能数据归档

docs/example-blue-green/     v2.6 demo 工程

snapshots/                   版本快照档案
  SNAPSHOT-kubepivot-{date}-v{ver}.md   各 minor 一份

archived/                    历史归档
  handoff/   历史 HANDOFF
  snapshot/  历史 SNAPSHOT  
  todo/      历史 TODO
  plan/      历史规划文档
  docs/      历史 gotchas / quickstart 等

commits/                     commit message 草稿（永久保留）
benchmark/                   性能测试工具链
test/integration/            集成测试
tools/codegen/               代码生成工具
```

---

## 七、下一步路线图（v2.6.1 / v2.7+）

### 7.1 v2.6.1（持续改进，不打 tag）

```
[ ] 多环境流量配置传播
    kp deploy --env prod --from-env staging
    从 staging 读"已验证的 traffic 配置"应用到 prod
    
[ ] tools/codegen 多 const block 改造
    实测发现：v2.6.0 添加 ErrRoute* 时只识别第一个 const block
    修法：~30 行 ast.Inspect 改造
    
[ ] codegen 错误码 doc 自动生成
    -doc 输出 markdown 错误码表
    与 internal/code/error.go 同步
```

### 7.2 v2.5.1（性能立方体，前置依赖）

```
[ ] 测试环境升级
    当前 8 GiB orbstack 限制 P_max ≈ 50
    需升 16 GiB 或多节点 K3d 才能测 P > 50
    
[ ] benchmark/scripts/matrix.sh 完整跑通
    阶段 1: P 维度（已 P=10 / P=50，待 P=100）
    阶段 2: R 维度（待 R=5）
    阶段 3: N 维度（待 N=3 / N=15）
    
[ ] sharding-tuning.md 性能立方体章节补完
    最优配置公式 + 配置建议
```

### 7.3 v2.7（自研 Informer）

```
[ ] internal/informer/ 新独立包
    解决 watcher 框架开销（v2.5.0 实测有边际成本）
    
[ ] benchmark：自研 informer vs client-go
    fork 一个 feature/client-go-comparison 分支
    跑同样 benchmark 矩阵
    数据驱动决定是否上 client-go
    
[ ] canary 完整实现（与自研 informer 一起）
    含 metrics 健康度判定（5xx rate / p99 latency）
    依赖 informer 的高频事件流
```

### 7.4 v2.8（数据保护 + web3-blitz 升级）

```
[ ] 数据敏感资源保护
    resources.yaml 加 protect: true 标记
    reconcile 检测到删除/重建意图时阻断
    kp confirm <ns>/<resource> 显式确认
    
[ ] 多集群管理增强
    跨集群 deployment 同步
    集群健康度聚合
    
[ ] web3-blitz 升级到 v2.6
    改造为声明式蓝绿（resources.yaml + sandbox commit）
    作为 KubePivot 的"真实生产案例"
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
2026-04-26  v2.6.0 release 后大改（404 commits）
            归档前一版到 archived/handoff/HANDOFF-v2.5.md
            主要变化：
            - 第 2.2 节 v2.5.0 + v2.6.0 合并
            - 第 3.7 节加 3 条新教训（codegen / kp release / sandbox fork）
            - 第 7 节路线图全重写（v2.6.1 / v2.5.1 / v2.7 / v2.8）
            - 第 4.3 节 release 流程精确化（kp release 行为）
```
