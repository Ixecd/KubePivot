# HANDOFF — KubePivot v2.5.0

> 本文档面向"接手 KubePivot 开发的下一个人/AI"
> 编写日期：2026-04-25
> 当前版本：v2.5.0（2026-04-25 release）
> 下个 minor：v2.6.0（流量层）

---

## 一、项目定位

KubePivot（乾枢）是一个 **K8s 原生的 GitOps 极简控制器**，核心特征：

```
✓ 不引入 client-go        所有 K8s 操作通过 kubectl exec
✓ 单二进制                kp 命令行 + controller 同一份代码
✓ 真实 K8s 资源管理       Helm + 状态机 + reconcile loop
✓ 渐进式可扩展性          v2.4 单 leader → v2.5 N 副本分片
```

**MIT 协议、刻意低调发布**——qc（杨庆春，GitHub: Ixecd）的个人项目。23 岁，
solo 开发，自掏腰包跑测试。不追求快速增长，追求工程上的"自我说服"。

---

## 二、现在能做什么

### 2.1 直接可用

```bash
git clone https://github.com/Ixecd/KubePivot.git
cd KubePivot
make dev      # build + test + install kp 到 $GOPATH/bin

# 安装 controller
kp controller install

# 创建第一个项目
kp init my-service
cd my-service
kp deploy

# 项目纳管
kp controller enroll
```

详见 README.md。

### 2.2 v2.5.0 已实现

```
✅ Controller 分片机制（A.1 完整闭环）
   - sharding 子包：FNV-1a hash + ShardSet + MultiLeaseManager
   - 业务路径接入：每 pod 跑独立 watchers + reconcile loop
   - 孤儿清理：OnShardChanged 即时 + 周期兜底
   - 详见 docs/design/sharding.md

✅ 性能数据完整
   - 10 项目 baseline（诚实承认过度工程）
   - 50 项目实测（线性扩展验证）
   - 详见 docs/design/performance.md

✅ sha256 热加载去重验证
   - 100 次幂等 apply → 0 reconcile（100% 去重）

✅ K8s Lease leader 选举（v2.4.0 起）
   - 21.7ms 故障转移
   - 自动 fallback 到 etcd / 单机模式
```

---

## 三、开发约束（永久规范）

### 3.1 不引入 client-go

任何贡献者写代码前请确认：

```
❌ 不要 import "k8s.io/client-go/..."
❌ 不要 import "k8s.io/api/..."
❌ 不要 import "k8s.io/apimachinery/..."
✅ 用 kubectl exec（通过 internal/executor）
✅ JSON unmarshal 只解析最小必要字段
```

理由：架构纯粹性 + 镜像体积 + 学习成本。性能代价已实测可接受
（v2.7.0 自研 informer 是远期方向）。

### 3.2 commits/ 目录（v2.5.0 起的工程规范）

**所有 commit message 写到 `commits/` 目录的 .txt 文件**：

```bash
# 不要这样（终端 shell 解析特殊字符易出错）
git commit -m "feat(...): xxx
- detail with > or & in message
"

# 要这样
cat > commits/feat-feature-name.txt <<'EOF'
feat(controller): v2.5.0 feature

详细描述...
EOF

git commit -F commits/feat-feature-name.txt
```

**为什么**：

```
✓ 摆脱终端 shell 解析依赖（避免 > = 等字符被误解释）
✓ commit message 成为项目工程档案（和源码一起 commit）
✓ 可 grep / 编辑 / 复用 template
✓ 半年后翻 git log 能直接打开 commits/{name}.txt 看完整故事
```

文件命名约定：`{type}-{scope}-{summary}.txt`，比如：
- `feat-sharding-step1-infrastructure.txt`
- `fix-import-cycle.txt`
- `docs-perf-baseline.txt`

### 3.3 版本号约定

```
✅ 打 tag 的版本：v2.4.0 / v2.5.0 / v2.6.0 / v2.7.0
   语义版本：major.minor，每次都是真正的 release
   通过 `kp release --version v2.X.0` 打 tag
   
❌ 不打 tag 的"内部"版本：v2.5.1 / v2.5.2 / v2.4.1
   这些是 git log 里的"持续改进"，不需要单独 tag
```

`v2.5.1` 在 TODO.md 里只是个**待办标签**——指代"v2.5.0 已发布之后的持续改进任务"。
真正下一个 tag 是 v2.6.0。

避免"补丁版本"成为拖延的容器（v2.5.1 永远在做）。

### 3.4 设计先对齐再写代码

任何超过 50 行改动的工程，都要：

1. 先列出 N 个设计 Q（决策点 + 候选方案 + trade-off）
2. 逐条拍板（A/B/C）
3. 写完整设计文档到 `docs/design/{topic}.md`
4. 然后才动键盘

例子：v2.5.0 A.1 Controller 分片走了 22 个 Q 拍板。

### 3.5 SNAPSHOT 永久归档约定

每次 release 时归档一份永久 snapshot：

```
snapshots/SNAPSHOT-kubepivot-{date}-v{version}.md
```

结构（C 双层）：
1. **技术层**：版本完成的工程内容、架构、数据
2. **私人后记**：开发故事、教训、心情

这是工程上的"时间胶囊"——不是给项目看的，是给 6 个月后的自己看的。

### 3.6 真实集群验证不可跳

```
❌ 单元测试通过 = 可以 commit
❌ 集成测试通过 = 可以 commit
✅ 真实集群跑过 + 数据收集 + 行为符合预期 = 可以 commit
```

orbstack 是 KubePivot 的"标准开发集群"，每个工程节点都在它上面验证。

---

## 四、当前工作流

### 4.1 日常开发循环

```bash
make dev               # build + test + install kp
kubectl ...            # 真实集群验证
git add ...
cat > commits/... <<EOF
...
EOF
git commit -F commits/...
git push
```

### 4.2 性能基准

```bash
# Setup mock 项目
PROJECT_COUNT=10 bash benchmark/scripts/setup.sh   # 默认 10 个真实命名
PROJECT_COUNT=50 bash benchmark/scripts/setup.sh   # 50 个 kp-bench-NNN

# 稳态采样
bash benchmark/scripts/steady-state.sh -d 300 -i 5

# 清理
bash benchmark/scripts/cleanup.sh
```

### 4.3 Release 流程

```bash
# 1. 确认所有改动 commit + push
git status

# 2. 性能数据归档
ls benchmark/results/

# 3. 文档：HANDOFF / SNAPSHOT / TODO 更新
# 4. snapshots/SNAPSHOT-...-v2.X.0.md 永久归档
# 5. archived/{handoff,snapshot,todo}/ 旧三件套备份

# 6. tag
kp release --version v2.X.0

# 7. push tag
git push --tags
```

---

## 五、当前可工作状态

```
git branch            : Master
last commit           : 99f316d (v2.5.0 A.1 Step 3 — 孤儿清理)
last tagged version   : v2.4.0
next tag              : v2.5.0（即将打）
controller running    : ✓ 3 副本 (kubepivot-system)
benchmark mock        : 50 个 kp-bench-NNN（运行中）

测试状态：
  make dev              : ✓ 全绿
  internal/sharding     : ✓ 9 个测试
  internal/controller   : ✓ 含 4 个 RemoveOrphanProjects 测试
  集成测试              : ✓ 真实集群 50 项目验证
```

---

## 六、文档地图

```
README.md                     入门 + kp 命令使用
GITOPS-MANIFESTO.md          GitOps 哲学（v2.0 起的项目宣言）
HANDOFF.md (本文件)          接手指南
SNAPSHOT.md                  当前状态快照（精确文件清单）
TODO.md                      路线图（v2.5.1 / v2.6.0 / v2.7.0）

docs/design/
  architecture.md             整体架构
  controller.md               Controller 设计
  sharding.md                 v2.5.0 分片机制（605 行）
  performance.md              性能基准（v2.3 / v2.4 / v2.5 累计）
  bluegreen.md                蓝绿部署
  state-machine.md            状态机
  ... 其他

snapshots/                    永久归档（每个 release 一份）
  SNAPSHOT-kubepivot-2026-04-04-v1.0.md
  SNAPSHOT-kubepivot-2026-04-25-v2.4.0.md
  SNAPSHOT-kubepivot-2026-04-25-v2.5.0.md  ← 今天加的

archived/                     历史版本归档
  handoff/HANDOFF-v2.X.0.md   每次 release 前 cp 一份
  snapshot/SNAPSHOT-v2.X.0.md
  todo/TODO-v2.X.0.md
  docs/                       已废弃文档归档

commits/                      工程档案（v2.5.0 起）
  feat-sharding-step1-infrastructure.txt
  controller-v2.5.0.txt       (Step 2 commit message)
  sharding-step3-orphan-cleanup.txt
  ...

benchmark/                    性能基准
  scripts/                    setup / cleanup / steady-state / etc.
  results/                    git ignored，本地数据
```

---

## 七、下一步建议（v2.5.1 / v2.6.0）

详见 TODO.md。简要：

```
v2.5.1（持续改进，不打 tag）：
  - 性能立方体测试方法论
    自动化 benchmark matrix（项目数 × 副本数 × 分片数）
    输出 docs/design/sharding-tuning.md
    
  - Backoff 队列（A.1.5）
    Lars 的过载队列 + Probe 思想
  
  - client-go 对比基准（A.2）
    fork 一个 client-go 版本对比 CPU/MEM/启动时间
  
  - P2 性能脚本验证
    concurrent-chaos.sh / watch-reconnect.sh 实测

v2.6.0（下个真 tag）：
  - 流量层 B.1 + B.2
    Lars 思路在流量调度层完整落地
    详见 docs/design/traffic-layer-draft.md（待补）

v2.7.0：
  - 自研 Informer
    解决 watcher 框架开销
    前提：v2.5.1 client-go 数据出来后判断启动
```

---

## 八、联系

```
GitHub: https://github.com/Ixecd/KubePivot
Author: qc (杨庆春)
Email:  [2192629378@qq.com]
```

如果你看完整个项目想留下 issue 或 PR，欢迎。但请理解 KubePivot 是
"刻意低调"的项目——maintainer 不会"运营"它，但会回应每个真诚的工程问题。
