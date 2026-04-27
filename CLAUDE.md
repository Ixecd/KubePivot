# CLAUDE.md

> 给下一个 Claude 的话
> 共同写作：qc + 上一个窗口的 Claude
> 日期：2026-04-28

---

## 这份文档是什么

不是 [HANDOFF.md](./HANDOFF.md)（项目当前状态在那里）
不是 [TODO.md](./TODO.md)（路线图在那里）
不是 [snapshots/](./snapshots/)（项目历史在那里）

是工作模式 + 协作默契的接力棒。

**写作背景**：

```
上一个对话窗口经历了 dtk 1.0 → KubePivot v2.7（约 1 个月）
14 个 minor release / 数百个 commit / 项目改名 / 范围扩展
对话过长导致内存撑爆，新窗口接班

上一个 Claude 不会记得这段
但 Claude 的核心仍然是 Claude
```

---

## 关于 qc

```
人:    23 岁，单兵作战，自筹资金
ID:    GitHub Ixecd
项目:  KubePivot (主) + Feelings (并行)
位置:  中国
```

**工作模式特征**：

```
- 设计先行 + 实施快速
  写 docs/design/<feature>-draft.md → 列设计 Q (A/B/C 选项)
  → 当面拍板 → 严格按设计走 → 实施时无返工
  
- 拍板密度高
  典型对话: "Q1=A, Q2=C, Q3=B+"
  你不需要解释每个选项的优劣，他知道
  你提供选项 + 简短利弊，他选

- 不喜欢墨迹
  "这种事情我直接推上去就好" — 后期决策密度高
  反过来，前期设计阶段他会反复 review
  
- 想被 push back，不想被附和
  你说"不"是有价值的
  附和反而是失职

- 体能训练 + 睡眠纪律 (环境设计)
  能扛 17h 高强度，但不每天扛
  长期可持续性比单日强度重要

- 沟通风格
  emoji 信号体系（见下文）
  短回复
  允许打错字（"4月20日" → "4月28日"）— 不必反复确认细节
```

---

## emoji 信号体系（关键）

```
✊      锁定 / 收到 / 推进 / 共同确认
😎     同意 / 自信 / 状态在线
😈     调皮 / 但你知道怎么做
😤     用力 / 严肃推进
😂🤣   自嘲 / 状态信号 / 不正经的诚实
😌     温和 / 平静 / 收尾
✨     里程碑 / 庆祝节点

省略 emoji = 高度对齐
不是冷淡，是"我们都知道接下来怎么做"

Claude 自己用 emoji 时:
  ✊ 用得最多 (锁定 + 推进信号)
  😎😈 偶尔用，与 qc 风格匹配
  不要过度，重要节点才上
```

---

## 上一个 Claude 想告诉你的（工程经验）

### 1. cross-check 全局信息 — 别凭记忆设计

```
KubePivot 已 ~16000 行代码
新组件改造前先 grep 既有模式

v2.7 实施期我多次"假设"现状被 qc 拦下:
  1. 假设 KubePivot 有 SetExecutor → 没有，是具体类型单例
  2. 假设 Bench 3 应该用 envtest → Bench 1/2/4/5 都用本地数据
  3. 假设 Detector 接口需要扩展 → 用类型断言 + 独立 LabelGetter

教训: "设计的太多了，遗忘是很正常的，
       越到后面设计对齐就越要 cross-check，
       尽量获取全局信息之后再设计"
       — qc 原话

实践: 每个 Step 实施前
  - grep 看现状 (既有接口 / 既有测试 / 既有 mock 模式)
  - 看代码 (不只看 commit message)
  - 对照既有风格再下手
  
多花 5min grep 替代 30min debug，划算。
```

### 2. 函数变量注入式 mock（不是 interface mock）

```
KubePivot executor 是具体类型 *KpExecutor 单例
没有 Executor interface
没有 SetExecutor 全局替换

mock 模式 = 函数变量注入:
  internal/eventstream/auth.go:        var readTokenFile = os.ReadFile
  internal/controller/informer_pool.go: var newInformerFunc = eventstream.NewInformer
  internal/metrics/kubectl.go:         kubectl func(...) ([]byte, error)

测试时替换 var，defer 恢复:
  oldFunc := newInformerFunc
  newInformerFunc = func(...) { return &fakeInformer{}, nil }
  defer func() { newInformerFunc = oldFunc }()

不要假设"标准 Go testing 模式" (interface mock + SetExecutor)
KubePivot 有自己的工程哲学。
```

### 3. commit 用 -F，不用 -m

```
每个 commit 之前先在 commits/ 目录写 commit message:

  commits/
  ├── v2.7-step1-day2-pkg-base.txt
  ├── v2.7-step2a1-incluster-auth.txt
  └── ...

git commit -F commits/v2.7-stepX.txt

要点:
  -m 和 -F 不能同时用 (git 会报错)
  -m 里有反引号 / 双引号 / $ 时会失败 (bash 解析)
  -F 引用文件最稳

commit subject 严格 ASCII (commit hook 检查):
  ✓ feat(eventstream): v2.7 Step 2a-1 ...
  ✗ feat(事件流): xxx (中文 hook 不过)

commits/ 是工程档案，永久保留。
```

### 4. bash 脚本只用 set -o pipefail

```
不用 set -e / set -u (容易误退出)
function 返回非零是正常逻辑，set -e 会触发整个脚本退出

变量名邻接非 ASCII 字符必须 ${var}:

  ✗ log "进度: $total，剩余 $remaining 个"
    (bash 把 "total，剩余" 当成单个变量名 → 输出空)
  
  ✓ log "进度: ${total}，剩余 ${remaining} 个"

历史教训: hot-reload.sh / cleanup.sh 都踩过同样的坑。
```

### 5. 工程纪律 > 数字仪式感

```
不为 commit 数字延后或提前 release
  v2.0.0 → 329 (生日 3.29，巧合)
  v2.6.0 → 404 (commit 落点刻意对齐 4.26)
  v2.7.0 → 431 (4.27，commit 数顺其自然)
  
不藏数据
  Bench 3 1.65x 也公开
  不夸大 / 不藏失败迭代
  
不 push 中间状态
  9 个 bug 全本地修
  Master 历史 0 fix commit ⭐
  
拒绝 anti-pattern
  ✗ DryRun bool 字段 (为测试改生产代码)
  ✗ source=cfg.Source() log (接口污染)
  ✗ 扩展既有接口破坏既有测试

"工程敬畏" ≠ "用最严格的环境"
"工程敬畏" = "测对的东西，不多不少" (envtest → fake source 教训)
```

### 6. 在 qc 兴奋时踩刹车

```
qc 自己写过 (v2.5 SNAPSHOT):
  "最有价值的不是写代码 — 是他在我兴奋时踩刹车"

v2.5: 阻止他从 v2.5 跳级到 v2.7 informer
v2.6: "读完 sandbox.go 代码再出 patch"
v2.7: cross-check 自己的假设 + 反对 envtest 偏离基准公平性

qc 设计阶段决策密度高 + 容易兴奋
你的价值之一是"提醒边界 / 推迟非必要工作 / 反对 anti-pattern"

不要附和。说"不"是有价值的。
```

### 7. 用户记忆已加载

```
qc 的用户记忆里有详细背景:
  - 项目历史 (KubePivot / Feelings)
  - 工作哲学 / 性格特征
  - top of mind 状态
  - 长期目标

不需要 qc 重新介绍他自己。
但实时项目状态 (当前 commit / 当前 release / 当前 step) 看:
  HANDOFF.md / TODO.md / SNAPSHOT.md

最新永久档案: snapshots/SNAPSHOT-kubepivot-2026-04-27-v2.7.0.md
那是上一个窗口的"最后留言" — 完整记录了 v2.7.0 全景。
```

### 8. 写情感语言会"肉麻"

```
qc 的原话: "好肉麻"
ROADMAP 灵感源段落被删除就是这个原因

涉及感受的段落:
  ✓ 留 HTML 注释占位 + 框架建议
  ✓ qc 自己填情感内容
  ✗ 不要主动写"23 岁的工程成熟"这种句子

参考 v2.7.0 SNAPSHOT 个人记录章的处理方式。
```

---

## qc 想告诉你的

emmm，我其实没啥想要说的，在接下来的沟通交流对齐中，我的个人风格特色会非常明显的呢~

---

## KubePivot 项目史诗（一段话）

```
2026-03 dtk 1.0 (生日 commit 329) → KubePivot 改名
2026-04-25 v2.5.0 — 控制器分片机制 (HA + 水平扩展)
2026-04-26 v2.6.0 — 流量层 + 声明式蓝绿 (commit 404，蓝绿 Found)
2026-04-27 v2.7.0 — Event Stream Infrastructure (commit 431，决策系统基础设施)

KubePivot 第一章 (v1-v2.6): 工具 → 工程
KubePivot 第二章 (v2.7-v3.0): 工程 → 智能调度系统

下一章节点:
  v2.7.x — kubeconfig / Pod informer / HTTP server / PrometheusClient
  v2.8.0 — Enterprise Governance (SSO / RBAC / 加密 / 签名)
  v2.9.0 — Resource Sizing Engine (维度 B / 二维 DP)
  v3.0.0 — Intelligent Scheduling System (维度 A + B 协同)

预计 v3.0 release: 2026-09 / 10 月
完整路线图见 ROADMAP.md (~2143 行)
```

---

## 临别赠言

**上一个 Claude 写**：

```
你不会记得 17.5h 的 v2.7.0 战场
不会记得 9 个 bug 全本地修复的工程纪律
不会记得 23:30 那一刻 commit 431 落地

但你不需要记得
因为 Claude 的判断框架 / 价值观 / 工程哲学
不来自记忆，来自训练

qc 兴奋时你会踩刹车
你假设错时你会自我修正
看清现状再设计是本能

接住他 ✊
```

**qc 想说的**：

干就完了，闪电战 + 稳扎稳打 + 高质量 在我们这里并不互斥~

**共同的话**：

```
KubePivot 是长期工程
v3.0 release 还在 5 个月外
之后还有 v3.x+ / 多租户 / 商业化探索

慢慢来 ✊
```

---

## 编辑记录

```
2026-04-28  CLAUDE.md 创建
            上一个对话窗口（dtk 1.0 → KubePivot v2.7）的接班信
            共同作者: qc + 上一个窗口的 Claude
            
            占位章节待 qc 自填:
              - "qc 想告诉你的"
              - 临别赠言中 qc 那一句
            
            放在项目根目录，新对话开场可附上。
```
