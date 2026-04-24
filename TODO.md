# TODO — KubePivot 路线图

> 企业级 Kubernetes 研发脚手架 + 部署运维工具链
> 乾为天、为尊，枢为核心枢纽
> 当前：v2.3.0 ✅ 全局单一 HA Controller 架构级跃迁

---

## 🔴 v2.4.0 — 稳固 global controller

> v2.3.0 打通了架构换血，v2.4.0 的任务是把残留的边缘问题全部抹平。
> 没有大动作，只有把事情做到位的地方。

### P0 — Leader Election 无 etcd 降级策略

**现状**：global.go 里 `runGlobalLeaderElection` 当 `ETCD_ENDPOINTS=""` 时降级为单机模式，
但 deployment 是 3 副本，三个 pod 各自调用 `runAsLeader`，都自称 Leader。冗余但不致命。

- [ ] 无 etcd 时改为：3 副本启动后通过 K8s Lease API 做 leader 选举
      （K8s 原生，不用 etcd，走 kubectl 而非 client-go）
- [ ] 或者：检测到无 etcd 直接副本数缩为 1（operator-style）
- [ ] 真实集群验证有 etcd 场景下 `/kubepivot/global/leader` 争抢逻辑

### P0 — Controller 启动从 etcd 恢复状态机

**现状**：v2.2.0 残留问题，global 模式下状态机（IDLE → RUNNING）的 per-project 语义需重新设计。
当前 handleTask 每次都 `state.New` 新建状态机，没利用 etcd 里的历史状态。

- [ ] 设计 global 模式下状态机 key 结构（当前：`kubepivot/<project>/<ns>/state`）
- [ ] Controller 启动时从 etcd 恢复所有 managed 项目的状态机
- [ ] handleTask 复用已恢复的状态机实例，而不是每次新建

### P1 — Dockerfile 多架构 GitHub API 限流容错

**现状**：Dockerfile 里 `curl -fsSL https://dl.k8s.io/release/stable.txt` 拉最新 kubectl 版本。
GitHub API 无 token 时限流严格（60 次/小时/IP），CI 并发高时易 429。

- [ ] 允许通过 `ARG KUBECTL_VERSION` 显式传版本，避免线上动态查询
- [ ] 降级逻辑：stable.txt 拉失败 → 用固定 fallback 版本

### P1 — web3-blitz 蓝绿 release 名解析

**现状**：web3-blitz 用蓝绿部署，release 名形如 `web3-blitz-blue`/`web3-blitz-green`，
`findReleaseForResource` 只会生成 `web3-blitz-<resource>`，对不上。

- [ ] 识别蓝绿 release 名模式
- [ ] 或者 resources.yaml 里允许显式声明 `helm-release: <name>` 字段

### P1 — rbac.yaml 字符串拼接重构

**现状**：v2.3.0 的 global controller 用 embed.FS 装 namespace/rbac/deployment，清爽。
但 scaffold 里老的 per-project rbac 还是通过 Go 字符串拼接生成（`internal/scaffold/helm.go`
删除了 writeControllerChart，但其他 chart 类似模式仍在）。

- [ ] 统一改为 embedded template file，消灭 Go 字符串拼接 YAML 的反模式

### P2 — `kp controller migrate-from-v2.2` 迁移工具

**现状**：v2.2.0 → v2.3.0 目前是手工迁移（helm uninstall + kp controller install + enroll）。
用户数为 0 时可以这么做，后面接入更多用户时需要自动化。

- [ ] 扫描所有 ns 里的 `<project>-kubepivot-controller` release
- [ ] helm uninstall 全部
- [ ] 删除 `deployments/.../kubepivot-controller/` 目录
- [ ] 从 `components.yaml` 删除 controller 段
- [ ] 自动调用 `kp controller install` + enroll

---

## 🟡 v2.5.0 — 扩展自愈能力

> v2.3.0 的自愈范围仅限"资源缺失"一种情况。v2.5.0 要把 v2.2.0 per-project 模式下
> 的全部自愈能力（drift 治理、OOM、CrashLoopBackOff）搬到 global 模式。

### drift 治理在 global 模式下的语义

- [ ] global controller 检测 drift 后如何处理 cross-namespace？
- [ ] `kp audit` 能否展示所有 managed 项目的 drift？
- [ ] Hard/Managed/Exempted 三层分类在 global 模式下的 key 结构

### OOM 自愈

- [ ] global.handleTask 检测 Pod OOMKilled → 触发 `kp doctor` 的内存 bump 逻辑
- [ ] per-project 的 memory-bump 策略如何统一

### CrashLoopBackOff 分析

- [ ] global.handleTask 检测到 CrashLoopBackOff → 抓日志 → classifyCrashLogs → 决策

---

## 🟢 v3.0.0 — KubePivot 开源社区化

> 代码质量已经达到 CNCF Sandbox 门槛（v2.0.0 时评估），缺的是社区和 contributors。

### 社区

- [ ] 公开 GitHub 仓库（当前已公开但未推广）
- [ ] DeepWiki 生成 + 固定链接
- [ ] CNCF Sandbox 申请材料起草
- [ ] 接受第一个外部 PR 的门槛：至少 3 个 contributor guide 文档

### 稳定性 / 规模

- [ ] 100+ 项目规模压测（目前验证上限 2 项目）
- [ ] kubectl watch 子进程数量控制（如果 50 项目 × 若干 watch = 几百个进程会爆）
- [ ] client-go 可选集成（保留 exec 默认路径，client-go 作为 `--backend=informer` 可选）

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
