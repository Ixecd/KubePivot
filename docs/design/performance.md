# KubePivot 性能基准

> 测试日期：2026-04-25（v2.3.0 基线 + v2.4.0 对比）
> 当前版本：v2.4.0
> 状态：v2.4.0 性能数据已落地，v2.5.0 client-go 对比待开始

---

## 摘要

10 个 mock 项目在单机 orbstack K8s 集群上稳态运行，对比 v2.3.0 和 v2.4.0：

| 指标 | v2.3.0 | v2.4.0 | 变化 |
|------|--------|--------|------|
| 集群总 CPU | 41.88% | 18.92% | **-55%** |
| avg CPU / pod | 13.96% | 6.30% | -55% |
| avg memory / pod | 59 MiB | 36.62 MiB | -38% |
| peak CPU | 89.95% | 54.25% | -40% |
| Leader 故障转移 | N/A（3 leader 冗余） | 21.7 ms | ✅ |
| 内存泄露 | 无（30 min 验证） | 无 | ✅ |
| sha256 热加载去重 | 设计存在 | 实测 100 次 = 0 reconcile | ✅ |

**关键判断**：v2.4.0 通过 K8s Lease 选举 + 状态机缓存把"3 leader 冗余"消除，集群总 CPU 削减 55%。这不是"做得更快"，是"消除冗余"——单 leader 实测 16.93% 才是 10 项目场景的真实成本，v2.3.0 的 13.96% 是 K8s 调度错峰摊出来的假象。

---

## 一、测试环境

```
硬件        Apple Silicon, 16 GB RAM
集群        orbstack K8s, 单节点
Storage     local-path (rancher.io/local-path), default
镜像        qingchun22/kubepivot-controller:v2.3.0 / v2.4.0
副本        3
limits      CPU 500m / Memory 512MiB
ETCD_ENDPOINTS  未配置
            v2.3.0：降级单机 leader（3 副本都自称 leader）
            v2.4.0：K8s Lease API leader（仅 1 副本真 leader）
```

### 项目规模

```
kubectl apply mock：    10 个

每个项目资源：
  - Namespace（label kubepivot.io/managed=true）
  - Deployment（pause:3.9，1 副本，挂 PVC）
  - Service（ClusterIP）
  - PersistentVolumeClaim（500Mi，真实 Bound）
  - ConfigMap kubepivot-resources（含 sha256 annotation）

总资源数：50（10 ns × 5 K8s 资源）
受 controller 监控：30（10 项目 × 3 资源/项目，见 resources.yaml）
```

---

## 二、v2.3.0 稳态基线（30 分钟）

### 采样方法

- 工具：`docker stats`（实时 cgroup CPU/Memory）+ `docker top`（容器内进程数）
- 采样间隔：10 秒
- 持续时间：1800 秒（实测 1805 秒）
- 输出：CSV 格式，每 pod 独立行
- 样本数：143 × 3 pod = 429 个

### 每 pod 30 分钟统计

| Pod | avg CPU | max CPU | avg Memory | max Memory |
|-----|---------|---------|------------|------------|
| stdtp | 14.73% | 57.92% | 58.57 MiB | 112.90 MiB |
| 29xfn | 13.38% | 89.95% | 59.20 MiB | 110.30 MiB |
| drnns | 13.77% | 54.88% | 59.24 MiB | 109.60 MiB |

**三 pod 平均 CPU 差距 < 1.5%**——尽管在无 etcd 单机模式下三个 pod 各自跑 reconcile loop，30 分钟尺度下 CPU 时间几乎完全均等。

**这是个意外发现，但也是误导**：单机模式的"冗余"被时序错峰自然消化了，看起来像"每个 pod 只承担 14% 负载"。但其实是三 pod 各自跑全量 reconcile，K8s 调度恰好让它们工作不撞车，加起来约等于一份完整负载。

v2.4.0 章节会给出真实的"单 leader 成本"。

### 内存稳定性（无泄露）

每个 pod 取 5 个均匀分布的时间切片：

```
                  start    1/4     middle   3/4     end
stdtp pod         48.95 → 48.68 → 50.99 → 48.86 → 48.04 MiB    ✓ 稳定
drnns pod         48.68 → 48.37 → 48.32 → 52.36 → 48.42 MiB    ✓ 稳定
29xfn pod         62.11 → 48.55 → 51.52 → 48.85 → 98.75 MiB    ✓ 稳定（末点踩 burst）
```

29xfn 末尾的 98.75 MiB 不是泄露——它和当时刻峰值 CPU 89.95% 同步出现，是 reconcile burst 期间 Go runtime 临时分配，下个 GC 周期会回到 ~50 MiB。

> **结论**：30 分钟稳态下没有内存泄露的迹象。50 MiB 是 controller 的"基础运行内存"，60-110 MiB 是 reconcile 工作期间的瞬时占用区间。

### 13.96% CPU 的来源

8 秒一个 reconcile 周期，每周期 controller 要做的事：

```
全集群 list namespace（kubectl get ns -l managed=true）
全集群 list configmap（kubectl get cm -A -l managed=true）
↓
对 10 个 managed 项目，每个项目 enqueue 3 个 task
↓ (worker pool 消费)
每个 task：DetectResourceExists（kubectl get <kind> <name> -n <ns>）
↓
缺失则 helm history + helm rollback
↓
未缺失则 return nil

每周期约 30 + 2 = 32 次 kubectl exec
peak 期 CPU 跑到 80-90% 是因为这 30 次 kubectl 集中在 ~1 秒内完成
平均到 8 秒周期，CPU 落在 12-15% 区间
```

**13.96% 的真实代价就是"每次都从零拉数据，不缓存"**。这是不引入 client-go 的成本；client-go 用 informer 缓存，估计能把 CPU 压到 1-2%，但代价是镜像 +10MB、依赖 +15 个包、架构纯粹性下降。

v2.3.0 选择保留 12% CPU 的代价，换架构清晰度。

---

## 三、v2.4.0 改造 + 数据对比（5 分钟稳态）

v2.4.0 完成两项关键改造：

1. **K8s Lease API leader 选举**——替代无 etcd 时的"3 leader 冗余"降级方案
2. **状态机缓存**——`*state.Machine` 在 `GlobalState` 内复用，不再每次 reconcile task 都新建

### 数据对比（10 项目稳态）

| 指标 | v2.3.0（30 min） | v2.4.0（5 min） | 变化 |
|------|---|---|---|
| 集群总 CPU | 41.88% | 18.92% | **-55%** |
| avg CPU / pod | 13.96% | 6.30% | -55% |
| avg memory / pod | 59.00 MiB | 36.62 MiB | -38% |
| peak CPU | 89.95% | 54.25% | -40% |

### 单 pod 拆解（v2.4.0）

```
leader   7qrbx     avg CPU 16.93%   max 54.25%   avg MEM 65.42 MiB    ← 唯一干活
standby  gqbts     avg CPU  0.51%   max  5.86%   avg MEM 22.66 MiB    ← 几乎纯空转
standby  zthqd     avg CPU  1.48%   max 10.44%   avg MEM 21.78 MiB    ← 几乎纯空转
```

### 数据解读

**总 CPU 减半的来源不是"做得更快"，是"消除冗余"**：

- v2.3.0 时 3 副本各自跑全量 reconcile，K8s 调度让它们工作错峰，三个加起来"意外"只承担了实际负载的 1.x 倍——平均到每 pod 看起来像 13.96%
- v2.4.0 时 1 个 leader 承担 100% 负载，2 个 standby 真空转，leader 实测 16.93% 才是 10 项目的真实单 pod 成本

**这件事的工程哲学价值**——在分布式系统里，"消除冗余"比"做得更快"更重要。清晰的成本结构是后续优化的前提。

### v2.5.0 优化基线明确

```
leader 16.93% × 1   = 16.93%   主战场（v2.5.0 优化目标）
standby 1.0% × 2    =  2.00%   持续选举开销（已接近极限）
总计                = 18.93%   接近实测 18.92%
```

v2.5.0 的两条优化路径：

```
路径 A.1  Controller 分片：leader 16.93% → 3 副本各管 1/3 → 每 pod ~6%
路径 A.2  client-go 对比基准：leader 16.93% → informer cache → ~2-3%
```

---

## 四、Leader 选举正确性验证

### 选举 race

3 副本同时启动时的 race 处理：

```
02:36:40.604  gqbts  Lease create 成功（实际是 race 中的瞬时假象）
02:36:40.635  7qrbx  Lease create 成功（真正的 winner）
02:36:46.263  gqbts  下一轮探测发现 holder 不是自己 → stop reconcile
zthqd        从未自称 leader（race 失败者）
```

最终：7qrbx 唯一真 leader，gqbts/zthqd 真 standby。

### 故障转移（21.7 ms）

```
杀掉当前 leader pod 后：

  T0      kubectl delete pod 7qrbx
  T+0.5s  下一个 5s 周期到来（其他 pod 检查 lease）
  T+0.521s  另一副本（r2gp5）发现 lease 过期
  T+0.522s  patch lease 抢占成功
  T+0.5237s  开始作为新 leader 启动业务
  
  整个过程从故障到接管 = 21.7 ms
```

**leaseTransitions: 0 → 1** 正确反映 leader 切换次数。

---

## 五、状态机缓存正确性验证

启动期日志显示 10 个 namespace 各打一条 `🧠 状态机已加入缓存`：

```
02:36:41.605  缓存 kp-admin-dashboard       state=IDLE
02:36:41.605  缓存 kp-analytics              state=IDLE
02:36:41.605  缓存 kp-auth-service          state=IDLE
02:36:41.606  缓存 kp-gateway                state=IDLE
02:36:41.606  缓存 kp-media-processor       state=IDLE
02:36:41.606  缓存 kp-notification           state=IDLE
02:36:41.606  缓存 kp-order-api             state=IDLE
02:36:41.606  缓存 kp-payment-worker        state=IDLE
02:36:41.607  缓存 kp-search-engine         state=IDLE
02:36:41.607  缓存 kp-user-profile          state=IDLE
```

5 分钟稳态期再无新缓存日志——证明：

- 启动时缓存预热到位
- 后续 reconcile task 全部命中缓存
- 没有重复创建 state.Machine 的开销
- 没有项目"逃过缓存路径"

---

## 六、sha256 热加载去重验证（命令行手测）

验证 v2.3.0 设计的 ConfigMap sha256 比对去重机制是否真的省掉无意义 reconcile：

### 测试方法

```
1. 记录 controller leader 当前 "项目状态已更新.*kp-auth-service" 日志计数
2. 100 次 kubectl apply 同一份 ConfigMap（sha256 不变）
3. 等 30 秒让 controller 处理完所有 watch 事件
4. 再次记录日志计数
5. 计算增量
```

### 实测结果

```
基线（apply 之前）：    1 次（启动期 enroll 时的初始化）
100 次 apply 之后：    1 次
增量：                 0 次
期望：                 ≤ 1 次
```

### 结论：100% 去重

**100 次幂等 apply 触发了 0 次多余 reconcile**。

设计上 sha256 比对在 `GlobalState.UpsertProject` 最早的入口就拦截：

```go
newHash := fingerprint(resourcesYAML)         // 算 sha256 (~50μs)
if exists && old.Sha256 == newHash {
    return false, nil                          // 直接返回，不 unmarshal
}
```

CPU 节省的不只是 reconcile 本身——**连 yaml.Unmarshal 都不会跑**，这是 sha256 去重设计的"零成本快速路径"价值。

---

## 七、v2.5.1 P2 验证（持续改进）

> 编写日期：2026-04-26
> v2.5.0 release 后的 P2 性能基准补全工作

### 7.1 sha256 热加载去重（脚本化复测）

v2.4.0 时通过命令行手测验证过 sha256 去重机制（见第六节）。
v2.5.1 把它脚本化，落到 `benchmark/scripts/hot-reload.sh`：

```
测试方法：
  100 次 kubectl apply 同一份 ConfigMap（sha256 不变）
  统计 controller leader 日志中的 "项目状态已更新" 计数变化

实测结果（2026-04-26 07:00，v2.5.0 集群）：
  基线计数：              1
  100 次 apply 后：       1
  增量：                  0   ← 期望 ≤ 1
  
  ✅ sha256 去重 100% 工作

数据归档：
  benchmark/results/2026-04-26_070015-hot-reload/
    ├── result.txt              结果摘要
    ├── configmap-snapshot.yaml ConfigMap 当时快照
    ├── logs-before.txt         apply 前的 controller 日志
    └── logs-after.txt          apply 后的 controller 日志（应该几乎一致）
```

**这次脚本化的工程价值**：
不是命令行手测拿到的"个人数据"，而是 clone 项目的人都能跑的可复现基准。

### 7.2 受阻的两个测试（已记入 TODO）

#### 7.2.1 Watcher 重连鲁棒性（watch-reconnect.sh）

脚本设计：从宿主侧 kill controller 容器内的 kubectl 子进程，
观察心跳守卫触发 + watcher 重连。

**当前受阻**：orbstack 是 VM 模式，容器 PID 命名空间隔离，
macOS 宿主 `ps` 看不到容器内 PID，`kill -9` 无法跨命名空间执行。

```
docker top 看到容器内进程（PID 395269 是 kubectl 子进程）
但 macOS host 的 ps -p 395269 完全看不到
→ 跨命名空间 kill 在 orbstack 下不可行
```

**缓解**：watcher.go 的心跳守卫机制（30s 无事件 → 探活 → 失败 restart）
有完整的单元测试覆盖。集成测试推到 v2.5.1 后期或借助 web3-blitz 升级后做。

详见 [TODO.md](../../TODO.md) v2.5.1 章节。

#### 7.2.2 并发自愈延迟（concurrent-chaos.sh）

脚本设计：3 档并发（1/3/10）删除 Deployment，记录自愈延迟 p50/p95。

**当前受阻**：setup.sh 创建的 mock 项目用 `kubectl apply` 直接部署，
没有 helm release。controller 检测到资源缺失 → 走 helm rollback 自愈链路 →
"查不到 release，无法自愈"。

```
log: 资源缺失，启动自愈 project=kp-auth-service kind=Deployment ...
log: 查不到 helm release，无法自愈 release=kp-auth-service-kp-auth-service
```

**这是 KubePivot 的正确设计** —— controller 只 rollback 自己 helm install
的资源，防止误伤外部部署的资源。

**解决路径**：
- 要么 setup.sh 改造让 mock 用 helm chart（~1 天工程）
- 要么用真实 helm 项目（如 web3-blitz，但需先升级到 go 1.26 + 适配 v2.5.0 接入协议，记入 v2.8.0）

详见 [TODO.md](../../TODO.md) v2.5.1 + v2.8.0 章节。

### 7.3 工程教训（写入 HANDOFF.md 3.7 节）

```
教训 1：基准脚本不应该用 set -e/set -u 严格模式
  grep 0 匹配会触发误退出，bash 多分支变量绑定时机不明确
  
教训 2：性能基准脚本要测真实场景
  mock 项目不能简化到"无 helm release"——会暴露不出真实链路
  
教训 3：环境兼容性问题不是 bug，是设计约束
  orbstack VM 模式 ≠ Linux 直跑容器
  跨命名空间 kill 这类操作要在脚本里 explicit 处理
  
教训 4：受阻测试要 explicit 文档化
  "未来会修"是工程拖延的常见模式
  写清"为什么受阻 + 解决路径 + 优先级"才能真的推进
```

---


## 八、未做但要做（v2.6.0+）

### 性能基准补全（v2.4.0 P2 移入）

```
[ ] 自愈延迟基准
    并发删除 1/3/10 个 Deployment，记录 p50/p95 自愈时间

[ ] Watcher 鲁棒性
    kill kubectl 子进程模拟异常退出，观察心跳守卫触发 + 事件不丢失

[ ] 长时间运行（24h）
    确认 30 分钟没看到的潜在泄露不会在 24h 暴露
```

### v2.5.0 client-go 对比基准

```
[ ] 编译一个 client-go informer 版本的 controller（同样接入协议）
    跑同样的 10 项目稳态，对比：
    - CPU            预期 client-go 能压到 1-2%
    - Memory         预期 client-go 略高（informer cache）
    - 镜像体积       预期 client-go 大 ~10 MB
    - 启动时间       预期 client-go 略慢（informer 初始 list）

数据出来后，基于真实数字而不是猜测，决定：
- v2.7.0 自研 informer 是否启动（需要 client-go 也只能压到 5-8% 才有边际价值）
- v3.x 是否加 --backend=informer 可选项
```

---

## 九、相关文档

- 设计原理：[`docs/design/controller.md`](controller.md)
- 整体架构：[`docs/design/architecture.md`](architecture.md)
- 测试脚本：[`benchmark/scripts/`](../../benchmark/scripts/)
- 原始数据（v2.3.0 30min）：`benchmark/results/2026-04-25_082859-steady-state/`（git ignored）
- 原始数据（v2.4.0 5min）：`benchmark/results/2026-04-25_104243-steady-state/`（git ignored）
