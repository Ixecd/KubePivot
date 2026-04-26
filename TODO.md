# TODO — KubePivot 路线图

> 编写日期：2026-04-25
> 当前版本：v2.5.0
> 下个真 tag：v2.6.0（流量层）

---

## 版本号约定

```
✅ 打 tag 的版本：v2.X.0（major.minor）
❌ 不打 tag："v2.5.1" 仅是 v2.5.0 之后的"持续改进任务标签"
```

详见 HANDOFF.md "开发约束 → 版本号约定"。

---

## ✅ 已完成

### v2.5.0（2026-04-25 release）

**A.1 Controller 分片机制（完整闭环）**

```
✅ A.1 Step 1  sharding 包基础设施（commit 721ae8d）
   - FNV-1a 32-bit hash + ShardSet 数据结构
   - QuotaPerPod 配额计算
   - MultiLeaseManager N 个 lease 抢占主循环
   - lease_helpers.go 本地实现底层 lease 工具（破 import cycle）
   - 9 个单元测试

✅ A.1 Step 2  业务路径接入（commit f3c3291）
   - StartGlobal 重构：每 pod 跑独立业务路径
   - 5 处加 shard 过滤
   - sweeper.go 新增 Leader-only 孤儿 lease 清理
   - deployment.yaml 加 KUBEPIVOT_SHARDS env
   - rbac.yaml 加 deployments get 权限

✅ A.1 Step 3  孤儿清理（commit 99f316d）
   - GlobalState.RemoveOrphanProjects(isOwned func) 方法
   - OnShardChanged 回调即时清理（5s grace period）
   - orphanSweeper 周期兜底（30s 一次）
   - 4 个孤儿清理测试
```

**性能基准 + 设计文档**

```
✅ sha256 热加载去重验证（命令行手测）
   100 次幂等 apply → 0 reconcile（100% 去重）

✅ 10 项目实测（v2.5.0 vs v2.4.0）
   集群总 CPU 18.92% → 31.24%（+65%）
   诚实结论：小规模过度工程

✅ 50 项目实测（v2.5.0 水平扩展验证）
   单 pod 46.09%，集群总 138.27%
   5x 项目 → 4.4x CPU（接近线性）

✅ docs/design/sharding.md（605 行完整设计文档）
✅ docs/design/performance.md（v2.3/v2.4/v2.5 累计数据）
```

**工程基础设施**

```
✅ benchmark/scripts/ 4 个新脚本
   - cleanup.sh 改造支持 PROJECT_COUNT
   - setup.sh 改造支持 PROJECT_COUNT
   - hot-reload.sh / concurrent-chaos.sh / watch-reconnect.sh（脚本层）

✅ commits/ 目录工程规范（HANDOFF.md 写入）
   摆脱终端 shell 解析依赖，commit message 成为项目档案

✅ 版本号约定（HANDOFF.md 写入）
   只打 major.minor tag，不打 patch tag
```

---

## ⏳ v2.5.1（持续改进，不打 tag）

### 性能立方体测试方法论（核心任务）

KubePivot 是 (项目数 P, 副本数 R, 分片数 N) 的三维空间，
单点测试无法说清"什么配置下分片真正划算"。

**v2.5.1 计划**：

```
[ ] benchmark/scripts/matrix.sh
    自动跑 P × R × N 组合矩阵
    输出 CSV 数据
    
[ ] 数据可视化
    matplotlib 渲染热力图：(P, R) → CPU/项目
    找最优 N 公式（基于 P 和 R）
    
[ ] docs/design/sharding-tuning.md
    - 配置建议矩阵：项目数 X 用 Y 副本和 Z 分片
    - 性能立方体可视化
    - "什么时候应该升级到 v2.7.0 自研 informer" 的判据

[ ] 顺手做的小优化（如有数据支持）：
    - QuotaPerPod ceil → floor 对比（4:4:2 vs 4:3:3 哪个更均衡）
    - Hash 算法对比：FNV vs xxhash vs murmur3
```

详见 docs/design/sharding.md 第 8.1 节。

### Backoff 队列（A.1.5）

```
[ ] internal/controller/backoff_queue.go
    Lars 的过载队列 + Probe 思想
    项目失败次数指数 backoff
    Probe 周期重新评估健康状态
    ~200 行
```

### client-go 对比基准（A.2）

```
[ ] fork 一个分支：feature/client-go-comparison
    用 client-go informer 重写 watcher
    保持其他逻辑不变（同样的 reconcile / lease / sharding）
    
[ ] 跑同样的 benchmark 矩阵
    对比 CPU/MEM/启动时间/镜像体积
    
[ ] 数据驱动决定 v2.7.0 自研 informer 是否启动
    如果 client-go 能压到 1-2% CPU
    且自研 informer 工作量 > 10 天
    → 考虑 v3.x 加 --backend=informer 可选项
    否则 → v2.7.0 自研 informer
```

### v2.4.0 P2 性能脚本验证（部分完成）

```
[x] hot-reload.sh debug + 实测
    去掉 set -e/-u，kubectl logs 先存文件再 grep
    100 次幂等 apply → 0 reconcile（sha256 去重 100% 工作）
    脚本结果归档：benchmark/results/2026-04-26_070015-hot-reload/
    
[ ] watch-reconnect.sh 实测
    kill kubectl 子进程模拟异常
    确认心跳守卫触发 + watcher 重连
    （今天 v2.5.1 推进中）
    
[ ] concurrent-chaos.sh 实测（受阻于 mock 项目）
    当前 setup.sh 用 kubectl apply 创建 mock，没有 helm release
    controller 检测到资源缺失走 helm rollback 链路 → "查不到 release，无法自愈"
    这是 KubePivot 正确的安全边界（只 rollback 自己 helm install 的资源）
    
    解决路径（任选其一）：
      A. setup.sh 改造让 mock 用最小 helm chart（~1 天工程）
      B. 用真实 helm 项目（如 web3-blitz）测试
         前提：web3-blitz 升级（见 v2.8 章节）
    
    当前不阻塞其他 v2.5.1 任务，记入待办即可

[ ] watch-reconnect.sh 实测（受阻于环境）
    
    当前实现：通过 docker top + kill -9 从宿主侧杀 controller 容器内的 kubectl 子进程
    
    orbstack 兼容性问题：
      orbstack 是 VM 模式，容器 PID 命名空间隔离
      macOS 宿主 ps 看不到容器内 PID，kill -9 失败
      
    解决路径（任选其一）：
      A. orbstack ssh 进 VM 后再 kill（命令复杂，每次需找 VM 名）
      B. controller 镜像加 procps（破坏 scratch 极简原则）
      C. controller 加 SIGUSR1 信号处理 → 主动重启 watcher
         ~30 行改造，运维和测试双重价值
         推荐
    
    优先级：低
    理由：watcher 心跳守卫机制在 v2.4.0 已有单元测试覆盖
          集成测试是补强，不是必需

[ ] 测试环境升级（v2.5.1 性能立方体的前置条件）
    
    当前问题：
      orbstack 默认 8 GiB 不够测 P > 50
      macOS 内存压力扭曲 benchmark 数据
      
    选项：
      A. 升级 orbstack memory 到 16 GiB
      B. 多节点 K3d 集群（docker 多 container 模拟）
      C. 真实多节点 K8s 集群（云上）
    
    推荐 A：最小变更，能解锁 P=100/200 的真实数据
```

---

## ⏳ v2.6.0（下个真 tag — 流量层）

Lars 思路在流量调度层完整落地。

**B.1 流量层调研**

```
[ ] docs/design/traffic-layer-draft.md
    - K8s 现有流量层组件梳理（Ingress / Service / NetworkPolicy / Gateway API）
    - KubePivot 在流量层的定位（不重新发明，做"GitOps 视角下的流量配置")
    - 多环境流量切换场景（dev/staging/prod）
    - 灰度发布的流量编排
    
[ ] 设计 Q 拍板（10+ 个 Q）
```

**B.2 流量层实现**

```
[ ] kp deploy --traffic <strategy>
    canary / blue-green / shadow 三种策略
    
[ ] 流量层 reconcile loop 接入
    类似 v2.5.0 sharding，但维度是"流量规则"
    
[ ] 集成测试
    真实集群验证多环境切换
```

工作量预估：1-2 周专注。

---

## ⏳ v2.7.0（自研 Informer）

```
前提：v2.5.1 client-go 对比基准的真实数据出来
判据：client-go 能压到 1-2% CPU 但镜像 +10MB / 启动慢的代价能接受？
      若不能 → 启动 v2.7.0

[ ] internal/informer/ 新独立包
    - list/watch 协议实现（HTTP + JSON）
    - resource cache（hashmap by Kind+Name+NS）
    - event distribution（订阅模式）
    - reconnect + 增量恢复
    
[ ] 每个 shard 一个 informer 实例
    K8s API server 推送的事件就只是该 shard 关心的
    真正消除 v2.5.0 的"3 倍 watcher 开销"
    
[ ] benchmark：自研 informer vs client-go vs v2.4.0 直 kubectl
    数据驱动证明决策
```

工作量预估：2-3 周专注。

---

## ⏳ v2.8.0（候选）

### 数据敏感资源保护

KubePivot 当前 reconcile 逻辑**会** rollback / 重建 / 删除任何资源，
对数据库等有状态服务存在风险。v2.8 引入"数据敏感资源"机制。

设计草案（待 v2.7 后细化）：

```
[ ] resources.yaml 加 protect: true 标记
    - kind: PersistentVolumeClaim
      name: postgres-data
      protect: true        ← 新

[ ] reconcile 检测到 protected 资源"应该删除/重建"时：
    - 不自动 rollback / 重建
    - 触发警告日志（K8s Event）
    - 进入"人工 confirm"工作流
    
[ ] kp confirm <ns>/<resource> 命令
    人工 review 确认后才执行
    
[ ] kp release 阻断
    检测到 protected 资源被改动时，要求显式 --confirm-protected 参数

前提条件：
  - 等到自己（或早期用户）真在生产用过 KubePivot
  - 知道"什么样的 confirm 体验不烦"再设计
  - v2.7 自研 informer 落地后才能精确捕获 PVC 删除事件
```


---


## ⏳ v2.8.0+（待规划）

候选方向（按优先级模糊排序）：

```
[ ] 多集群管理
    kp context add cluster1 --kubeconfig ./kubeconfig-cluster1
    kp context list / use / remove
    
[ ] Audit / 审计日志
    所有 reconcile 行为持久化到 K8s Event 或独立 PV
    便于事后追溯
    
[ ] kp controller migrate-from-v2.2
    旧版本 etcd 数据迁移到 v2.4+ K8s Lease 模式
    
[ ] Web Dashboard（独立项目）
    kp 的可视化界面
    架构上不和 controller 同 binary
    
[ ] CRD 模式（重大架构选择）
    把 ConfigMap-based 接入协议升级为 CRD
    需要面向社区生态决策

[ ] web3-blitz 项目升级 + 适配 v2.5.0+
    背景：
      web3-blitz 是 v2.0 时代（dtk ai-plan 自动生成）的真实 helm 项目
      包含 wallet-service / chain-miner / postgres / etcd 多组件
      go 1.25-alpine 在 2026-04 已有 CVE
      resources.yaml 写法可能不完全兼容 v2.5.0 接入协议
    
    升级清单：
      - Dockerfile go 1.25-alpine → go 1.26-alpine（修 CVE）
      - 检查 resources.yaml 蓝绿部署字段（wallet-service-blue）是否仍兼容
      - 重 build 4 个镜像 + push
      - 真实 helm install 到集群
      - 验证 Sandbox / Drift / Reconcile 全链路
    
    工程意义：
      web3-blitz 是 KubePivot 的"真实生产用例"
      v2.5.0 release 后让真实项目运行验证
      副产品：能用它跑 v2.5.1 没做完的 chaos 测试
    
    优先级：在 v2.6.0 流量层之前应该做完
            否则 KubePivot 的"真实场景验证"是空的
```

不确定的事——等 v2.6 / v2.7 真实跑过再回头评估。

---

## 已废弃/不做

```
✗ 单 leader 模式回退选项
   v2.5.0 起分片是默认且唯一模式（v2.4.0 单 leader 仅作为线性外推参考）
   
✗ etcd 作为 leader 选举的唯一选项
   v2.4.0 加了 K8s Lease fallback，etcd 现在是可选的
   
✗ v2.5.0 阶段引入 client-go
   永久原则：不引入 client-go（自研 informer 是远期方向）
```

---

## 工作流（按版本推进）

```
v2.5.0 (今天 release)
   ↓
v2.5.1 (持续改进)
   ↓ 性能立方体 / Backoff / client-go 对比 / P2 脚本验证
   ↓
v2.6.0 (下个真 tag)
   ↓ 流量层
   ↓
v2.7.0
   ↓ 自研 informer（数据驱动判断启动）
   ↓
v2.8.0+
   待规划
```
