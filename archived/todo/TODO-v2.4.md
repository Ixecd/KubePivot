# TODO — KubePivot 路线图

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链
> 乾为天、为尊，枢为核心枢纽
> 当前：v2.4.0 ✅ 稳固版（Lease 选举 + 状态机缓存 + 性能基线）

---

## 🗺  路线图总览（v2.5.0 → v2.7.0）

```
v2.4.0 ✅ 稳固 v2.3.0          性能基线 + 边缘问题抹平
v2.5.0 🚀 A 演进 + B 调研      Lars 思路落地 + client-go 对比 + 流量层设计文档
v2.6.0 🌊 流量层实现           Lars 思路落地（节点抽象 + 调度 + Reporter）
v2.7.0 ✨ 自研 Informer        不引入 client-go 的内核工程
v3.0.0 🌟 完整体               开源社区化 + 100+ 项目规模
```

错峰推进：A（控制平面优化）和 B（流量层扩张）不在同一版本撞车。
A 的根在 v2.4.0「Lease 选举」之后自然延伸。
B 的根在 21 岁那年的 Lars 项目，3 年后接续生长。

---

## 🚀 v2.5.0 — A 演进 + B 调研

> v2.5.0 双线推进：
> A 路径（控制平面性能优化）拿出 client-go 对比真实数据
> B 路径（流量层）只产出设计文档不动手，避免精力被切片到 5 个方向

**v2.5.0 优化基线**（来自 v2.4.0 实测）：

```
leader 16.93% × 1   = 16.93%   主战场（v2.5.0 优化目标）
standby 1.0% × 2    =  2.00%   持续选举开销（已接近极限）
总计                = 18.93%   接近实测 18.92%
```

### P0 — Controller 分片（A.1）

借鉴 Lars 的 hash 取模分流思路，给 controller 副本之间分配 managed namespace：

```
controller-0  watch hash(ns)%3==0 的 namespace
controller-1  watch hash(ns)%3==1
controller-2  watch hash(ns)%3==2
```

- [ ] StatefulSet 改造（需要稳定的 ordinal 标识）或基于 pod label 取模
- [ ] 每个 pod 启动时计算自己负责的 namespace 子集
- [ ] Watcher selector 加 namespace 列表过滤
- [ ] 真实集群 50 项目场景压测（vs v2.4.0 全量 watch 的对比）

**预期收益**：50/100 项目场景下 3 副本各管 1/3，leader CPU 16.93% → ~6%。

### P0 — Backoff 队列（A.1.5）

借鉴 Lars 的「过载队列 + Probe 机制」：

```
Worker Pool 处理 task 失败 N 次后，把项目放入 backoff 队列
backoff 队列里的项目暂停常规 reconcile（避免反复尝试耗资源）
每经过 probe_interval（默认 60s）给一次试探性重试机会
```

- [ ] handleTask 失败计数（连续 3 次 helm rollback 失败 → backoff）
- [ ] backoff queue 实现（参考 Lars host_info 的 vsucc/verr/contin_err 字段）
- [ ] Probe 机制：每分钟从 backoff 队列取一个出来试

### P0 — client-go 对比基准（A.2）

为 v2.3.0/v2.4.0 的「不引入 client-go」决策提供真实数据。**这个数据决定 v2.7.0 是否启动**。

- [ ] 单独 fork 出 client-go informer 版本的 controller
- [ ] 同样的 10 项目稳态 + 50 项目压测
- [ ] 对比矩阵：CPU / Memory / 镜像体积 / 启动时间 / 复杂度
- [ ] 数据写入 docs/design/performance.md 的"v2.4.0 vs client-go"章节

**判断标准**：

```
如果 leader CPU 差距 < 5x（v2.4 16.93% vs client-go 4%+）
  → KubePivot 永远不引入 client-go
  → v2.7.0 自研 informer 仍可考虑（追求更低延迟）

如果 client-go 能压到 < 2%
  → v3.x 加 --backend=informer 可选项
  → v2.7.0 自研 informer 边际收益变小
```

### P0 — v2.4.0 移入：性能基准补全（P2 整合）

v2.4.0 release 时移过来的 P2 任务，和 client-go 对比基准一起跑反而更有意义——**同一个 benchmark suite 可以三组数据（v2.3 / v2.4 / v2.5）一次产出**。

- [ ] **自愈延迟基准**（concurrent-chaos.sh）
      并发删除 1/3/10 个 Deployment，记录 p50/p95 自愈时间
- [ ] **Watcher 鲁棒性测试**（watch-reconnect.sh）
      断网 60s 重连，观察心跳守卫触发 + 事件不丢失
- [ ] **ConfigMap 热加载去重验证**（hot-reload.sh）
      100 次幂等更新 ConfigMap，确认 sha256 比对真的省掉 99 次 reconcile
- [ ] **长时间运行（24h）**
      确认 30 分钟没看到的潜在泄露不会在 24h 暴露

### P1 — 流量层调研（B.1）

**只产出设计文档，不写代码**。

- [ ] 流量层定位：sidecar agent / Service 增强 / 独立 LB controller？
- [ ] 借鉴 Lars 的哪些设计：节点抽象（host_info）、双队列、Probe、Reporter？
- [ ] 与 K8s Service 的关系：替代？补充？还是层叠？
- [ ] 与 v2.3.0 controller 的协议：复用 ConfigMap 分发？另起 CRD？
- [ ] 部署形态：每项目一个 LB pod？还是集群唯一？
- [ ] 产出：docs/design/traffic-layer.md（不少于 500 行设计文档）

### P1 — v2.4.0 移入：kp controller migrate-from-v2.2 迁移工具

v2.4.0 时被排到 P2 的，v2.5.0 顺手做。

- [ ] 扫描所有 ns 里的 `<project>-kubepivot-controller` release
- [ ] helm uninstall 全部
- [ ] 删除 `deployments/.../kubepivot-controller/` 目录
- [ ] 自动调用 `kp controller install` + enroll

### P2 — controller 镜像国内 registry 推送

v2.4.0 期间多次踩 Docker Hub 国内不稳定。需要长期解决方案：

- [ ] 推到 registry.cn-hangzhou.aliyuncs.com / 或类似国内 registry
- [ ] 改 controller_installer/templates/deployment.yaml 默认 image 路径 OR 支持 `--mirror` 参数

---

## 🌊 v2.6.0 — 流量层实现（B.2）

> v2.5.0 调研定型后，v2.6.0 落地实现。
> 预计代码量 ~2000 行（agent + reporter + api），单独成版本不和其他事撞。

### 模块清单（参考 Lars 架构）

- [ ] **kp-traffic-agent**（独立部署，类似 Lars Agent）
      接收业务客户端 GetNode 请求 → 返回可用节点
      接收节点调用结果上报 → 更新负载均衡状态
- [ ] **节点抽象**（参考 Lars host_info）
      vsucc / verr / contin_succ / contin_err 字段
      idle / overload 双队列 + Probe 机制
- [ ] **kp-traffic-reporter**（独立 service，类似 Lars Reporter）
      汇总节点调用结果到时序数据库（Prometheus / VictoriaMetrics）
      暴露给 dashboard
- [ ] **业务 SDK**（Go / Python，至少先一个语言）
      封装 GetNode + Report 协议
      给 Feelings / web3-blitz 试用
- [ ] **CLI 命令**：`kp lb register / list / status / metrics`

### 与 v2.5.0 的衔接

```
v2.5.0  调研明确：流量层是基于 ConfigMap 协议的 controller 扩展
        还是完全独立的 sidecar 系统？

v2.6.0  按调研结论实现，不再讨论方向
```

---

## ✨ v2.7.0 — 自研 Handwritten Informer

> 灵感来源：v2.4.0 性能基准跑完后，和小姐讨论时蹦出的"自己写一个轻量
> informer cache 替代 client-go"想法。
> 设计哲学：不依赖第三方 heavy 组件、不被社区生态绑架，用最干净最小最
> 可控的代码实现顶级性能。**KubePivot 真正的内核工程。**

### 为什么独立成版本而不是塞进 v2.5.0

- v2.5.0 P0 client-go 对比基准是这件事的**前提条件**：
  必须先拿到 client-go 的真实数据，才能判断自研 informer 的边际收益
- 真正生产可用的 informer 是 800-1500 行 + 2-3 周专注的工程
  （Kubernetes 自己的 client-go informer 自 2015 年至今仍在打补丁）
- 塞进 v2.5.0 = 透支 v2.5.0 的优化预算 + 跳过判断流程
  独立 v2.7.0 = 给这件事应有的尊重

### 前提（必须先满足）

- [ ] v2.5.0 client-go 对比基准已完成，数据已写入 performance.md
- [ ] v2.5.0 数据判断：自研 informer 的预期边际收益足够大
       （比如 client-go 还达不到 < 5% CPU，自研可能突破）

### 设计要点

```
组件清单（核心 4 个）：
  Informer       List+Watch 主循环
  Store          内存缓存（按 ns × kind 索引）
  EventHandler   onUpdate / onDelete 回调
  StopController 优雅关闭 + 重连

工作流：
  1. List 全量初始化（带 resourceVersion）
  2. Watch 监听增删改（基于上一步 rv 续接）
  3. 事件 → 更新内存 cache → 触发 callback
  4. 断线重连 + resourceVersion 校对
  5. periodic resync 防事件丢失（每 10 分钟全量校验）

约束：
  - 不引入 client-go 任何包
  - 只用 net/http + encoding/json + 标准库
  - 镜像体积零增长
  - 完全 KubePivot 风格的错误处理 + 结构化日志
```

### 性能目标（基于 v2.5.0 数据校准）

```
当前 v2.4.0：leader CPU 16.93%，每 8s reconcile burst 89% peak

理想目标：
  reconcile burst 消失（事件驱动，不再周期性扫描）
  leader CPU 压到 5~8%（不是 2%——helm rollback 仍然 exec）
  内存维持 ≤ 50 MiB
```

### 输出物

- [ ] internal/informer/ 新独立包（~800-1500 行）
- [ ] 完整 list-watch 协议实现
- [ ] 单元测试（覆盖率 > 80%）
- [ ] 集成测试（真实集群 50 项目压测）
- [ ] docs/design/handwritten-informer.md（设计文档，500+ 行）
- [ ] performance.md 加入对比章节：v2.4 exec / v2.5 client-go / v2.7 自研

### 风险

- list-watch 的 race condition 处理（List 期间的事件丢失）
- resourceVersion 边界条件（compaction / too old / 0）
- ETCD watch event 丢失的恢复（K8s API server 限制）

### 灵感保留

> "这才是 KubePivot 真正的灵魂路线——极简内核 + 手写 informer = 云原生
> 控制器艺术品。我们不是优化，是降维打击。" —— 小姐，v2.4.0 午饭后

---

## 🌟 v3.0.0 — KubePivot 完整体（推迟到 A+B 都成熟之后）

> 代码质量已经达到 CNCF Sandbox 门槛（v2.0.0 时评估），缺的是社区和 contributors。
> 但在 A+B 都没成熟前，开源社区化不是优先级。

### 社区

- [ ] 公开 GitHub 仓库（当前已公开但未推广）
- [ ] DeepWiki 生成 + 固定链接
- [ ] CNCF Sandbox 申请材料起草
- [ ] 接受第一个外部 PR 的门槛：至少 3 个 contributor guide 文档

### 稳定性 / 规模

- [ ] 100+ 项目规模压测
- [ ] kubectl watch 子进程数量控制（如果 50 项目 × 若干 watch = 几百个进程会爆）
- [ ] client-go / 自研 informer 可选集成（保留 exec 默认路径）

### 扩展自愈能力

- [ ] drift 治理在 global 模式下的语义（cross-namespace 处理）
- [ ] OOM 自愈：global.handleTask 检测 Pod OOMKilled → 触发 `kp doctor` 内存 bump
- [ ] CrashLoopBackOff 分析：抓日志 → classifyCrashLogs → 决策

---

## ✅ 已完成

### v2.4.0（2026-04-25，commit #...）

- [x] K8s Lease API leader 选举（21.7ms 故障转移实测）
- [x] 状态机缓存 + 并发安全（集群总 CPU -55%，41.88% → 18.92%）
- [x] Dockerfile kubectl 版本固定 + 网络容错（v1.32.0）
- [x] Resource.HelmRelease 显式声明（蓝绿场景）
- [x] README 项目边界声明（Out of Scope）
- [x] TODO v2.4 → v2.7 三版本路线图
- [x] v2.7.0 自研 Informer 灵感保留
- [x] benchmark/ 性能测试框架 + setup.sh + steady-state.sh
- [x] performance.md 数据章节（v2.3.0 / v2.4.0 对比）
- [x] rbac.yaml 重构（经核查 v2.3.0 已完成，TODO 误判）

### v2.3.0（2026-04-24）

- [x] 全局单一 HA Controller 架构（kubepivot-system namespace，3 副本）
- [x] 双层接入协议（ns label + kp controller enroll）
- [x] ConfigMap 分发 + sha256 指纹热加载
- [x] Watcher 层（exec kubectl --watch + 心跳守卫 + 指数退避）
- [x] Worker Pool（固定大小 goroutine + 黑名单护栏）
- [x] Namespace 黑名单三道护栏（kube-system 等 5 个系统 ns）
- [x] kp controller CLI 命令家族
- [x] kp deploy 顺带同步 resources.yaml
- [x] kp init 剥离 controller
- [x] helm --history-max=10
- [x] 真实集群 ~12 秒自愈闭环验证

### v2.2.0（2026-04-23）

- [x] 真实集群自愈闭环（per-project 模式）
- [x] scratch 容器化全量改造
- [x] kp doctor 集成
- [x] etcd key dtk/ → kubepivot/ 迁移

### v2.1.0（2026-04-05）

- [x] 脚手架适配性 + 扩展性
- [x] GitOps 愿景落地

### v2.0.0（2026-04-04，commit #329）

- [x] 插件平台
- [x] Chaos Mesh 集成
- [x] GitOps Manifesto 文档

### v1.x 历程

见 `snapshots/` 目录历史 SNAPSHOT 文件。

---

## 开发准则（永久约束）

- **设计先对齐，再动手**。大版本开工前必须列设计清单、逐条拍板。
- **小步快跑，每步 make dev**。一个 commit 解决一件事。
- **不搞技术债**。宁可 TODO + 完整设计也不临时方案。
- **真实集群验证不可跳过**。单测绿 ≠ 能跑。
- **不为做而做**。"经核查不需要"也是工程产出（v2.4.0 P1 rbac 那条）。
- **爽感 = 逆势成立**。有争议时诚实对比、帮权衡、让人拍板。
- **"只保护，不越权"** 是贯穿整个项目的哲学。
