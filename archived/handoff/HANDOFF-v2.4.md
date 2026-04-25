# HANDOFF — KubePivot

> 当前版本：**v2.4.0** ✅
> 上次更新：2026-04-25
> 目标读者：下一个接手 KubePivot 的开发者 / Claude 实例

---

## 你现在的位置

KubePivot 刚发完 **v2.4.0**——稳固版，没加新功能但每个角落更稳。

```
v1.0.0  多服务 DAG + A2 Controller + 安全合规基线
v1.4.0  跨版本迁移（KubePivot 改名）
v1.6.0  Controller HA（Leader Election + WorkQueue）
v1.7.0  状态漂移治理 + HPA + on-missing 全策略
v1.8.0  Operation Sandbox
v1.9.0  多集群联邦 + 企业合规
v2.0.0  插件平台 + Chaos Mesh
v2.1.0  脚手架适配性 + GitOps 愿景
v2.2.0  真实集群自愈 + scratch 容器化
v2.3.0  🌐 全局单一 HA Controller 架构跃迁
v2.4.0  ⚙  稳固边缘 + 性能基线（Lease 选举 / 状态机缓存 / -55% CPU）  ← 你在这
v2.5.0  🚀 A 演进 + B 调研（待开始）
v2.6.0  🌊 流量层落地（Lars 思路）
v2.7.0  ✨ 自研 Handwritten Informer
v3.0.0  🌟 KubePivot 完整体
```

---

## 起手三件事

```
1. 读这份 HANDOFF（你正在做）
2. 读 SNAPSHOT.md（v2.4.0 总览）+ snapshots/SNAPSHOT-...-v2.4.0.md（深度档案）
3. 读 TODO.md（v2.5/v2.6/v2.7 路线图）
```

读完这三份你大概知道项目当前状态、为什么走到这里、下一步要去哪里。

---

## v2.4.0 已交付清单

```
✅  K8s Lease API leader 选举（21.7ms 故障转移实测）
✅  状态机缓存 + 并发安全（集群总 CPU -55%）
✅  Dockerfile kubectl 版本固定 + 网络容错（v1.32.0）
✅  Resource.HelmRelease 显式声明（蓝绿场景支持）
✅  README 加「项目边界 Out of Scope」节
✅  TODO v2.4 → v2.7 路线图
✅  性能基准 v2.4.0 章节 + 数据对比
```

---

## v2.5.0 开工前必读

下面这些 context 在你开工前就要先确立。**不读直接动手 = 大概率走偏**。

### 1. KubePivot 不引入 client-go

**这是贯穿整个项目的设计决策**，不是技术债。

具体表现：
- 整个 controller 走 `kubectl exec`（list/get/watch/patch）
- v2.3.0 自研 KubectlWatcher（基于 `kubectl --watch` JSON 流）
- v2.4.0 K8s Lease 选举走 `kubectl create/patch lease`

为什么：
- 架构纯粹性 / 镜像体积 / 依赖最小化 / 可调试性
- 50 项目规模下 ~31 次 kubectl/秒 完全扛得住

v2.5.0 的 P0 client-go 对比基准是为了**用真实数据验证这个决策**，不是动摇它。

### 2. v2.4.0 的"消除冗余"哲学

```
v2.3.0  3 副本各 13.96% CPU         看起来 OK
v2.4.0  1 leader 16.93% + 2 standby 0.5%   真实成本结构

→ 集群总 CPU -55%
→ 但 leader 单 pod 反而升 3 个点（因为之前是错峰摊出来的假象）
```

**重点**：v2.4.0 性能优化不是"做得更快"，是"消除冗余"。这是分布式系统里更重要的事。

v2.5.0 优化基线由此明确：
```
leader 16.93% × 1   = 主战场
standby 1% × 2      = 已接近极限
```

v2.5.0 的两条路径都是冲 leader 的 16.93%：
- A.1 Controller 分片 → ~6%
- A.2 client-go 对比 → ~2-3%（如果数据支持）

### 3. v2.7.0 自研 informer 是真要做的事

不是吹水。但**前提是 v2.5.0 client-go 对比数据出来**：

```
如果 client-go 能压到 < 5%   → 自研 informer 边际收益小
如果 client-go 也只能 8%+   → 自研 informer 是真有内核价值
```

设计灵感和原文档见 TODO.md v2.7.0 章节。

### 4. 项目边界（Out of Scope）

KubePivot **不做**：
- K8s 集群本身的生命周期管理（→ 未来的 Cloud 项目）
- K8s NodePool 管理（→ 未来的 Cloud 项目）
- 裸金属 / 虚机 / 数据中心规划（→ 未来的 Cloud 项目）

**做**的边界停在"应用层"。

---

## 开发约束（不会过期）

```
1. 设计先对齐，再动手     大版本开工前列设计清单逐条拍板
2. 小步快跑               一个 commit 解决一件事
3. 不搞技术债             宁可 TODO + 完整设计也不临时方案
4. 真实集群验证不可跳过    单测绿 ≠ 能跑
5. 不为做而做             "经核查不需要"也是工程产出（v2.4.0 P1 rbac 那条）
6. 只保护，不越权         贯穿整个项目
```

---

## 命令速查

```
make dev                           编译 + 测试 + 安装本地
kp release --version v2.5.0       打 tag + push（dogfooding）

# 真实集群操作
kp controller install
kp controller enroll
kp controller status / projects

# benchmark
bash benchmark/scripts/setup.sh                      生成 10 个 mock 项目
bash benchmark/scripts/steady-state.sh -d 1800       30 分钟稳态采样
```

---

## 踩过的坑（重要 lessons）

### 1. K8s microTime 必须带微秒精度

```
普通 RFC3339   "2026-04-25T01:51:57Z"            ❌ K8s API server 拒绝
microTime     "2026-04-25T01:51:57.000000Z"     ✅ 必须的
```

`time.Format("2006-01-02T15:04:05.000000Z07:00")` 才对。

### 2. macOS bash 全角中文括号坑

bash 5.3 解析 `$VAR（中文`时，会把全角"（"和后面的中文当作变量展开后缀。
benchmark/scripts/setup.sh 当时全角括号导致 set -u 报"未绑定的变量"。
**约定**：所有 shell 脚本里只用半角括号 `()`，中文文本里用 `（）` 作为视觉差异。

### 3. PROJECT_NAME 空时 fallback 到 namespace

v2.3.0 时 controller pod 内 PROJECT_NAME env 默认为空，导致 `PROJECT_NAME-resource.name` 变成 `-Deployment`。
v2.3.0 fix 已经加了 `if project == "" { project = namespace }`。
v2.4.0 顺手在 helm-release 字段重构里巩固了这个 fallback。

### 4. zsh vs bash 数组下标差异

zsh 数组从 1 开始，`${arr[0]}` 在 set -u 下被判为未绑定。
**约定**：shell 脚本里硬编码具体值或用 `${arr[@]:0:1}` 兼容写法。

### 5. Docker Hub auth token 国内不稳定

```
解决方案：imagePullPolicy: IfNotPresent + 本地 build cache
长期方案：v2.4.0 TODO 移到 v2.5.0 之后，国内 registry 推送
```

---

## 联系信息（人 + 项目）

```
作者：     qc (杨庆春, GitHub: Ixecd)
位置：     中国，独立开发者，自费 runway
项目链接： https://github.com/Ixecd/KubePivot
许可：     MIT
```

KubePivot 是 qc 的判断的物化——"把工程能力固化为可复用框架，未来项目都在标准里跑"。

---

**v2.5.0 开工的话，从读 TODO.md 的 v2.5.0 章节开始。**

Good luck. 🍀
