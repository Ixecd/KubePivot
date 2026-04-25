# TODO — KubePivot 路线图

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链
> 乾为天、为尊，枢为核心枢纽
> 当前：v2.3.0 ✅ 全局单一 HA Controller 架构级跃迁

---

## 🗺  路线图总览（v2.4.0 → v2.6.0）

```
v2.4.0  稳固 v2.3.0          残留问题抹平 + 性能基准首版
v2.5.0  优化 + 调研          A 演进（client-go 对比）+ B 调研（流量层）
v2.6.0  流量层实现           Lars 思路落地：节点抽象 + 调度 + Reporter
```

错峰推进：A（控制平面优化）和 B（流量层扩张）不在同一版本撞车。
A 的根在 v2.4.0 P0「Leader Election 无 etcd 降级」之后自然延伸。
B 的根在 21 岁那年的 Lars 项目，3 年后再生长出来。

---

## 🔴 v2.4.0 — 稳固 global controller

> v2.3.0 打通了架构换血，v2.4.0 的任务是把残留的边缘问题全部抹平。
> 没有大动作，只有把事情做到位的地方。
>
> 性能基准首版已落地（docs/design/performance.md），剩下三个 P0/P1。

### P0 — Leader Election 无 etcd 降级策略

**现状**：global.go 里 `runGlobalLeaderElection` 当 `ETCD_ENDPOINTS=""` 时降级为单机模式，但 deployment 是 3 副本，三个 pod 各自调用 `runAsLeader`，都自称 Leader。30 分钟稳态实测三 pod 工作分布意外均匀（avg CPU 差距 < 1.5%），但不稳定，仍要解。

- [ ] 无 etcd 时改为：3 副本启动后通过 K8s Lease API 做 leader 选举
      （K8s 原生，不用 etcd，走 kubectl 而非 client-go）
- [ ] 或者：检测到无 etcd 直接副本数缩为 1（operator-style）
- [ ] 真实集群验证有 etcd 场景下 `/kubepivot/global/leader` 争抢逻辑

### P0 — Controller 启动从 etcd 恢复状态机

**现状**：v2.2.0 残留问题，global 模式下状态机（IDLE → RUNNING）的 per-project 语义需重新设计。当前 handleTask 每次都 `state.New` 新建状态机，没利用 etcd 里的历史状态。

- [ ] 设计 global 模式下状态机 key 结构（当前：`kubepivot/<project>/<ns>/state`）
- [ ] Controller 启动时从 etcd 恢复所有 managed 项目的状态机
- [ ] handleTask 复用已恢复的状态机实例，而不是每次新建

### P1 — Dockerfile 多架构 GitHub API 限流容错

**现状**：Dockerfile 里 `curl -fsSL https://dl.k8s.io/release/stable.txt` 拉最新 kubectl 版本。GitHub API 无 token 时限流严格（60 次/小时/IP），CI 并发高时易 429。

- [ ] 允许通过 `ARG KUBECTL_VERSION` 显式传版本，避免线上动态查询
- [ ] 降级逻辑：stable.txt 拉失败 → 用固定 fallback 版本

### P1 — web3-blitz 蓝绿 release 名解析

**现状**：web3-blitz 用蓝绿部署，release 名形如 `web3-blitz-blue` / `web3-blitz-green`，`findReleaseForResource` 只会生成 `web3-blitz-<resource>`，对不上。

- [ ] 识别蓝绿 release 名模式
- [ ] 或者 resources.yaml 里允许显式声明 `helm-release: <n>` 字段

### ✅ P1 — rbac.yaml 字符串拼接重构（误判，无需执行）

**核查结论**：v2.3.0 已经从根本上消灭这个反模式。

执行的验证：
- 全仓 `grep 'Sprintf.*"kind: (Role|RoleBinding|ClusterRole)"'` → 0 匹配
- scaffold/helm.go 中 controller-rbac.yaml 不是被「生成」而是被「清理删除」
  （v2.3.0 移除 per-project controller chart 时连带清理）
- controller_installer/templates/rbac.yaml 是 embed.FS 模板，零字符串拼接
- scaffold 通过 embed.FS 读取所有 helm chart 模板（embed.go）

诚实记录：这条 TODO 是写时基于 grep 看到 "controller-rbac.yaml" 的字符串
就误判为"在拼接生成它"，实际是 os.Remove 清理。
v2.4.0 这条任务不为做而做，直接划掉。

- [x] 不做：v2.3.0 已从根本上完成（核查日期 2026-04-25）

### P2 — `kp controller migrate-from-v2.2` 迁移工具

**现状**：v2.2.0 → v2.3.0 目前是手工迁移（helm uninstall + kp controller install + enroll）。用户数为 0 时可以这么做，后面接入更多用户时需要自动化。

- [ ] 扫描所有 ns 里的 `<project>-kubepivot-controller` release
- [ ] helm uninstall 全部
- [ ] 删除 `deployments/.../kubepivot-controller/` 目录
- [ ] 从 `components.yaml` 删除 controller 段
- [ ] 自动调用 `kp controller install` + enroll

### P2 — 性能基准补全

**现状**：v2.4.0 已落地稳态基准（30 分钟，avg CPU 13.96%，无内存泄露）。剩下四个未做。

- [ ] 自愈延迟基准：并发删 1/3/10 个 Deployment，记录 p50/p95
- [ ] Watcher 鲁棒性：断网 60s 重连验证
- [ ] ConfigMap 热加载：100 次幂等更新验证 sha256 去重
- [ ] 长时间运行（24h）验证

---

## 🟡 v2.5.0 — A 演进 + B 调研

> v2.5.0 双线推进：A 路径（控制平面性能优化）拿出 client-go 对比真实数据，
> B 路径（流量层）只产出设计文档不动手，避免精力被切片到 5 个方向。

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

**预期收益**：50/100 项目场景下 3 副本各管 1/3，CPU 摊薄到 ~5%。

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

为 v2.3.0 的「不引入 client-go」决策提供真实数据。

- [ ] 单独 fork 出 client-go informer 版本的 controller
- [ ] 同样的 10 项目稳态 + 50 项目压测
- [ ] 对比矩阵：CPU / Memory / 镜像体积 / 启动时间 / 复杂度
- [ ] 数据写入 docs/design/performance.md 的"v2.3.0 vs client-go"章节

**判断标准**：
- 如果 CPU 差距 < 5x，KubePivot 永远不引入 client-go
- 如果 CPU 差距 > 10x，v3.x 加 `--backend=informer` 可选项
- 介于之间，进一步看规模上限

### P1 — 流量层调研（B.1）

只产出设计文档，**不写代码**。

- [ ] 流量层定位：sidecar agent / Service 增强 / 独立 LB controller？
- [ ] 借鉴 Lars 的哪些设计：节点抽象（host_info）、双队列、Probe、Reporter？
- [ ] 与 K8s Service 的关系：替代？补充？还是层叠？
- [ ] 与 v2.3.0 controller 的协议：复用 ConfigMap 分发？另起 CRD？
- [ ] 部署形态：每项目一个 LB pod？还是集群唯一？
- [ ] 产出：docs/design/traffic-layer.md（不少于 500 行设计文档）

---

## 🟢 v2.6.0 — 流量层实现（B.2）

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

## 🔵 v3.0.0 — KubePivot 完整体（推迟到 A+B 都成熟之后）

> 代码质量已经达到 CNCF Sandbox 门槛（v2.0.0 时评估），缺的是社区和 contributors。
> 但在 A+B 都没成熟前，开源社区化不是优先级。

### 社区

- [ ] 公开 GitHub 仓库（当前已公开但未推广）
- [ ] DeepWiki 生成 + 固定链接
- [ ] CNCF Sandbox 申请材料起草
- [ ] 接受第一个外部 PR 的门槛：至少 3 个 contributor guide 文档

### 稳定性 / 规模

- [ ] 100+ 项目规模压测（v2.5.0 验证到 50，v3.x 推到 100+）
- [ ] kubectl watch 子进程数量控制（如果 50 项目 × 若干 watch = 几百个进程会爆）
- [ ] client-go 可选集成（保留 exec 默认路径，client-go 作为 `--backend=informer` 可选）

### 扩展自愈能力（从原 v2.5.0 推迟到这里）

> v2.3.0 的自愈范围仅限"资源缺失"一种情况。
> v3.0.0 之前补齐 v2.2.0 per-project 模式下的全部自愈能力。

- [ ] drift 治理在 global 模式下的语义（cross-namespace 处理 / Hard/Managed/Exempted 三层分类的 key 结构）
- [ ] OOM 自愈：global.handleTask 检测 Pod OOMKilled → 触发 `kp doctor` 内存 bump
- [ ] CrashLoopBackOff 分析：抓日志 → classifyCrashLogs → 决策

---

## ✅ 已完成

### v2.3.0（2026-04-24）

- [x] 全局单一 HA Controller 架构（kubepivot-system namespace，3 副本）
- [x] 双层接入协议（ns label + kp controller enroll）
- [x] ConfigMap 分发 + sha256 指纹热加载
- [x] Watcher 层（exec kubectl --watch + 心跳守卫 + 指数退避）
- [x] Worker Pool（固定大小 goroutine + 黑名单护栏）
- [x] Namespace 黑名单三道护栏（kube-system 等 5 个系统 ns）
- [x] kp controller CLI 命令家族（install/uninstall/status/enroll/unenroll/projects）
- [x] kp deploy 顺带同步 resources.yaml（消灭忘记同步）
- [x] kp init 剥离 controller（components.yaml + helm.go 清理）
- [x] helm --history-max=10（防 release secret 撑爆）
- [x] 真实集群 ~12 秒自愈闭环验证通过

### v2.4.0 已完成（2026-04-25）

- [x] 性能基准首版：30 min 稳态采样 + benchmark/ 目录框架
       avg CPU 13.96% / pod，avg memory 59 MiB / pod，无内存泄露
       三 pod 工作分布意外均匀（avg CPU 差距 < 1.5%）

### v2.2.0（2026-04-23）

- [x] 真实集群自愈闭环（per-project 模式）
- [x] scratch 容器化全量改造
- [x] kp doctor 集成（OOM / CrashLoopBackOff 分析）
- [x] etcd key dtk/ → kubepivot/ 迁移

### v2.1.0（2026-04-05）

- [x] 脚手架适配性 + 扩展性
- [x] GitOps 愿景落地

### v2.0.0（2026-04-04，commit #329）

- [x] 插件平台（kp plugin install/list/remove）
- [x] Chaos Mesh 集成（kp chaos inject/list/stop/status）
- [x] GitOps Manifesto 文档

### v1.x 历程

见 `snapshots/` 目录历史 SNAPSHOT 文件。

---

## 开发准则（不写进版本，是永久约束）

- **设计先对齐，再动手**。大版本开工前必须列设计清单、逐条拍板。
- **小步快跑，每步 make dev**。一个 commit 解决一件事。
- **不搞技术债**。宁可 TODO + 完整设计也不临时方案。
- **真实集群验证不可跳过**。单测绿 ≠ 能跑。
- **爽感 = 逆势成立**。有争议时诚实对比、帮权衡、让人拍板。
- **"只保护，不越权"** 是贯穿整个项目的哲学。
