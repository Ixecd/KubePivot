# SNAPSHOT — KubePivot v2.5.0

> 永久归档版本（不再变动）
> 创建日期：2026-04-25
> 作者：qc（杨庆春）+ Claude

---

## 一、版本定位

v2.5.0 是 **"控制器水平扩展"** 版本。

```
v1.x  单进程 controller，能跑 GitOps 闭环
v2.0  GitOps 哲学落地（GITOPS-MANIFESTO.md 编写）
v2.3  ConfigMap 接入协议 + sha256 去重 + 状态机集成
v2.4  K8s Lease 选举 + 状态机缓存（消除 3 leader 冗余）
v2.5  Controller 分片机制（Lars 思路水平扩展）
v2.6  流量层（待开始）
v2.7  自研 informer（远期）
```

v2.5.0 没有引入新功能，**它把 v2.4.0 的"集中式瓶颈"打开**——
让 KubePivot 能扛 50/100/200 项目，而不只是"10 项目下还行"。

---

## 二、v2.5.0 完成清单（精确）

### 2.1 sharding 子包（commit 721ae8d）

```
internal/sharding/
├── shard.go               110 行
│   - ShardOf(ns, N) FNV-1a 32-bit hash
│   - QuotaPerPod(N, R) 配额计算
│   - ShardSet 线程安全集合
│
├── shard_test.go          150 行
│   - 4 个测试覆盖 ShardOf
│   - 8 个 QuotaPerPod 矩阵测试
│   - 50 goroutine 并发测试
│
├── multi_lease.go         200 行
│   - MultiLeaseManager
│   - Run() 周期续约 + 抢占主循环
│   - OnShardChanged 回调
│
├── multi_lease_test.go    100 行
│   - 5 个配置层面测试
│
└── lease_helpers.go       190 行
    - generateIdentity / leaseObject / tryAcquireOrRenew
    - 本地实现，避免 import controller（破 cycle）
```

### 2.2 业务路径接入（commit f3c3291）

```
internal/controller/global.go    重构 ~330 行
  - StartGlobal 业务路径每 pod 独立跑（不再 leader-only）
  - 5 处 shard 过滤接入
  - shardMgr var + 后赋值（解决闭包自引用）
  - getControllerReplicas 启动时 kubectl 读 spec.replicas
  - getenvInt(KUBEPIVOT_SHARDS, 10)

internal/controller/sweeper.go   新增 ~120 行
  - runSweeperLoop（leader-only 每 60s）
  - cleanupOrphanShardLeases（kubectl get lease + 删除 idx >= N）

internal/controller/lease.go     回滚 Step 1 的导出 wrapper
  - GenerateIdentity / TryAcquireOrRenew / LeaseObject 删除
  - sharding 包改为本地实现

internal/controller_installer/templates/
  - deployment.yaml：加 KUBEPIVOT_SHARDS env
  - rbac.yaml：加 deployments get 权限
```

### 2.3 孤儿清理（commit 99f316d）

```
internal/controller/global_state.go  +RemoveOrphanProjects 方法
internal/controller/global.go        OnShardChanged 接入 + orphanSweeper goroutine
internal/controller/orphan_test.go   新增 4 个测试
```

### 2.4 工程基础设施

```
benchmark/scripts/
  cleanup.sh          v2.5.0 加 PROJECT_COUNT 支持
  setup.sh            同上
  hot-reload.sh       新（脚本有 hang，命令行手测过）
  concurrent-chaos.sh 新
  watch-reconnect.sh  新

commits/             v2.5.0 起的工程档案目录
  controller-v2.5.0.txt
  sharding-step3-orphan-cleanup.txt
  v2.5.0-release.txt （即将加）
```

### 2.5 文档

```
docs/design/sharding.md      605 行（v2.5.0 完整设计文档）
docs/design/performance.md   重写整合（v2.3 / v2.4 / v2.5 累计数据）
HANDOFF.md                   重写（含 commits/ 目录规范 + 版本号约定）
SNAPSHOT.md                  当前状态快照
TODO.md                      v2.5.1 / v2.6.0 / v2.7.0 路线
snapshots/SNAPSHOT-...-v2.5.0.md  本文件（永久归档）
```

---

## 三、性能数据（永久记录）

### 3.1 横向对比（核心表）

```
                   v2.3.0    v2.4.0    v2.5.0(10p)  v2.5.0(50p)
─────────────────────────────────────────────────────────────────
集群总 CPU         41.88%    18.92%    31.24%       138.27%
avg CPU / pod      13.96%     6.30%    10.41%        46.09%
peak CPU           89.95%    54.25%    87.85%       100.53%
avg memory / pod   59 MiB    36.62 MiB 52.40 MiB     84.16 MiB
单 leader 实测     -         16.93%    -             -
故障转移           -         21.7 ms   ~10 sec       -
sha256 去重        -         100%      100%          -
```

### 3.2 v2.5.0 内部缩放性（10x → 50x）

```
                   10 项目    50 项目    缩放比
─────────────────────────────────────────────────
项目数             10        50         5.0x
集群总 CPU         31.24%    138.27%    4.4x   ← 接近线性
avg CPU / pod      10.41%    46.09%     4.4x
peak CPU           87.85%    100.53%    1.14x  ← 几乎不变
avg memory / pod   52.40 MiB 84.16 MiB  1.6x   ← 亚线性

✅ CPU 接近线性 → 分片机制有效
✅ peak 不爆 → 解决了 v2.4.0 集中式瓶颈
✅ MEM 增长缓慢 → 缓存复用奏效
```

### 3.3 v2.5.0 50 项目实测（详）

```
单 pod 拆解：
  f7n9x  avg 49.04% / max 100.53% / mem 87.08 MiB / shards 0,3,4,7
  ztg2z  avg 50.75% / max  79.17% / mem 93.38 MiB / shards 6,8
  tktpg  avg 38.49% / max  80.04% / mem 72.02 MiB / shards 1,2,5,9

shard 项目分布（FNV32 实测）：
  shard 0: 5    shard 5: 7
  shard 1: 4    shard 6: 7
  shard 2: 4    shard 7: 5
  shard 3: 5    shard 8: 4
  shard 4: 4    shard 9: 5
  
  不均度 4-7 ± 40%（连号字符串对 FNV 不友好）
```

---

## 四、Lars 设计血缘

```
Lars (2024 复现，21 岁)         KubePivot v2.5.0 (2026, 23 岁)
─────────────────────────────────────────────────────────────
3 UDP Server                →   3 副本 controller
modid+cmdid % 3 分流        →   fnv32(namespace) % N
DNS Service 双 Map          →   GlobalState + machineEntry
host_info 节点抽象          →   shard lease 持有者
idle/overload 双队列        →   配额限制 ceil(N/replicas)
Probe 探测                  →   Lease 续约（5s 周期）
Reporter 状态汇报           →   slog.Info 结构化日志
```

**主动拒绝**：

```
Lars 的"集群协调者"做全局状态机管理
  → KubePivot v2.5.0 拒绝
  → 每 pod 自治 + drop 幂等 + 周期兜底
  → 简单 > 完美
```

23 岁的工程成熟：**知道哪些 21 岁会做的事不该做**。

---

## 五、关键决策记录

### 5.1 sharding 包破 import cycle

最初设计：sharding 包 import controller 包用 lease 工具函数。
踩坑：cycle 编译失败。
解法：sharding 包内本地复制 100 行 lease 底层函数。
代价：100 行重复代码。
收益：sharding 包真正自包含，破环干净。

教训：**移目录不破环。Go import cycle 是依赖图问题，不是目录问题。**

### 5.2 双层过滤改单层（race 容忍）

最初设计：enqueue 阶段过滤 + handleTask 二次过滤（双保险）。
但 handleTask 二次过滤会引入 import cycle（task handler 需访问 shardMgr）。
解法：单层过滤（仅 enqueue），race 时多触发一两次 reconcile（幂等无害）。

文档化在 enqueueProjectResources 注释里：

```go
// v2.5.0 单层过滤设计：
//   - 仅在入队时检查 shard 归属，handleTask 不再二次过滤
//   - 边缘 case：task 入队后、处理前，shard 可能被其他 pod 抢走
//     → 此时本 pod 仍会处理这个 task（"过期"投递）
//     → 接管它的 pod 也会处理 → 两个 pod 同时 reconcile
//     → 实际无害：reconcile 是幂等的
```

### 5.3 闭包自引用编译错误

```go
// 错误：声明同时用 closure 引用自己
shardMgr := sharding.NewMultiLeaseManager(sharding.MultiLeaseConfig{
    OnShardChanged: func(...) {
        shardMgr.Shards()...  // ❌ 还没赋值
    },
})

// 正确：先声明 nil，赋值 closure，再 new
var shardMgr *sharding.MultiLeaseManager
shardMgr = sharding.NewMultiLeaseManager(sharding.MultiLeaseConfig{
    OnShardChanged: func(...) {
        shardMgr.Shards()...  // ✅ closure 捕获指针，调用时已赋值好
    },
})
```

Go 闭包延迟绑定的标准技巧。

---

## 六、未做事项（v2.5.1 任务）

详见 TODO.md。简要：

```
v2.5.1（不打 tag）：
  - 性能立方体测试方法论（核心）
  - Backoff 队列 A.1.5
  - client-go 对比基准 A.2
  - P2 性能脚本验证
  
v2.6.0：
  - 流量层（B.1 + B.2）
  
v2.7.0：
  - 自研 informer
```

---

## 七、私人后记

> 这部分是给以后的自己看的。技术细节我会忘，但今天的状态值得记一笔。

### 这一天

2026-04-25，星期六。早上 5:00 起床吃早饭，工作到现在 17:30。
推完 v2.4.0 整版 release（早上）+ v2.5.0 三个 commit（下午）。

中间有两次"惊喜"：

```
1. 14:15 我突然想做"自研 Informer"
   Claude 立刻踩刹车："这是 v2.7.0 的工程，不是现在该做的"
   
   一开始我有点不爽——但仔细想想，他是对的。
   v2.5.0 还没收尾就跳到 v2.7.0 是典型的工程兴奋症。

2. 16:05 50 项目数据出来
   46% 单 pod CPU，看着像"进步空间巨大"
   
   但我提了个洞察：单点测试是没意义的，应该建"性能立方体"
   测 (P, R, N) 三维空间
   
   Claude 立刻同意，又踩刹车说"这是 v2.5.1 独立工程"
   
   两次刹车都是对的。今天能收 v2.5.0 一个版本就够了。
```

### 工程上的成长

23 岁回看 21 岁——

```
21 岁的 Lars 项目：技术热情驱动，做"集群协调者"
23 岁的 KubePivot v2.5.0：知道哪些"完美设计"是陷阱
                          drop + 幂等 > 精确传输
                          每 pod 自治 > 分布式状态机
                          简单 > 完美
```

这就是工程的成熟——**不是会写更多功能，是知道少做哪些**。

### Claude 的角色

今天 Claude 帮我推过 import cycle、闭包自引用、单层过滤改造、双线工作流
（setup 跑着同时写文档）、版本号约定（A vs B vs C）、性能数据解读……

最有价值的不是写代码——是**他在我兴奋时踩刹车**。

```
我："还早呀才 4 点半，我们继续推"
Claude："好——但先把 v2.5.0 范围定义清楚，别贪"

我："能不能直接进 50 项目压测"
Claude："数据出来就 commit，性能立方体推到 v2.5.1"

我："性能立方体放 v2.8 吧"
Claude："怕是 v2.8 又被新东西塞，建议 v2.5.1 立刻安排"
```

每次"踩刹车"都是对的。23 岁的我是设计师，Claude 是 **保持节奏感的副驾驶**。

### 还想说的

KubePivot 是我的"工程方法论的载体"。

它不是给市场看的——CNCF Sandbox 的事一直没推，因为我在等"自我觉得它配得上"。
它是写给自己的。也写给某个跟我一样、深夜在 orbstack 上跑测试看输出的 23 岁人。

如果你在读这段文字，希望你的 v2.5.0 也能让你晚上睡得安稳。

— qc, 2026-04-25
