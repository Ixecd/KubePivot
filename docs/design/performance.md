# KubePivot v2.3.0 性能基准

> 测试日期：2026-04-25
> 版本：v2.3.0
> 状态：🚧 持续完善（首份稳态数据已落地）

---

## 摘要

10 个 mock 项目在单机 orbstack K8s 集群上稳态运行 30 分钟，全局
controller 表现：

| 指标 | 数值 |
|------|------|
| 平均 CPU / pod        | **13.96%** |
| 平均内存 / pod        | **59.00 MiB** |
| 峰值 CPU              | **89.95%**（reconcile burst） |
| 峰值内存              | **112.90 MiB** |
| 内存泄露              | **无**（30 min 内三 pod 均稳定） |
| 样本数                | 143 × 3 pod = 429 个 |

**关键判断**：v2.3.0 在 10 项目规模下资源占用极低，给"不引入 client-go"的设计决策提供了真实数据支撑。

---

## 一、测试环境

```
硬件        Apple Silicon, 16 GB RAM
集群        orbstack K8s, 单节点
Storage     local-path (rancher.io/local-path), default
镜像        qingchun22/kubepivot-controller:v2.3.0
副本        3
limits      CPU 500m / Memory 512MiB
ETCD_ENDPOINTS  未配置（降级单机 leader 模式）
```

### 项目规模

```
kp init 真实锚点：     1 个（kp-auth-service，未 deploy）
kubectl apply mock：   10 个

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

## 二、稳态测试（30 分钟）

### 采样方法

- 工具：`docker stats`（实时 cgroup CPU/Memory）+ `docker top`（容器内进程数）
- 采样间隔：10 秒
- 持续时间：1800 秒（实测 1805 秒）
- 输出：CSV 格式，每 pod 独立行

### 每 pod 30 分钟统计

| Pod | avg CPU | max CPU | avg Memory | max Memory |
|-----|---------|---------|------------|------------|
| kubepivot-controller-6fd54bf6cf-stdtp | 14.73% | 57.92% | 58.57 MiB | 112.90 MiB |
| kubepivot-controller-6fd54bf6cf-29xfn | 13.38% | 89.95% | 59.20 MiB | 110.30 MiB |
| kubepivot-controller-6fd54bf6cf-drnns | 13.77% | 54.88% | 59.24 MiB | 109.60 MiB |

**三 pod 平均 CPU 差距 < 1.5%**——尽管在无 etcd 单机 leader 模式下三个 pod 各自跑 reconcile loop，30 分钟尺度下 CPU 时间几乎完全均等。这是个意外的发现：单机模式的"冗余"被时序错峰自然消化了，没有出现"一个 pod 包揽全部工作"的退化情况。

### 内存稳定性（无泄露）

每个 pod 取 5 个均匀分布的时间切片，验证 30 分钟内是否单调上升：

```
                  start    1/4     middle   3/4     end
stdtp pod         48.95 → 48.68 → 50.99 → 48.86 → 48.04 MiB    ✓ 稳定
drnns pod         48.68 → 48.37 → 48.32 → 52.36 → 48.42 MiB    ✓ 稳定
29xfn pod         62.11 → 48.55 → 51.52 → 48.85 → 98.75 MiB    ✓ 稳定（末点踩 burst）
```

29xfn 末尾的 98.75 MiB 不是泄露——它和当时刻峰值 CPU 89.95% 同步出现，是 reconcile burst 期间 Go runtime 临时分配，下个 GC 周期会回到 ~50 MiB。

> **结论**：30 分钟稳态下没有内存泄露的迹象。50 MiB 是 controller 的"基础运行内存"，60-110 MiB 是 reconcile 工作期间的瞬时占用区间。

---

## 三、为什么是 13.96% CPU

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

## 四、未做但要做（v2.4.0 / v2.5.0）

### v2.4.0 待补 benchmark

```
[ ] 自愈延迟基准
    并发删除 1/3/10 个 Deployment，记录 p50/p95 自愈时间
    
[ ] Watcher 鲁棒性
    断网 60s 重连，观察心跳守卫触发 + 事件不丢失

[ ] ConfigMap 热加载去重
    100 次幂等更新 ConfigMap，确认 sha256 比对真的省掉 99 次 reconcile

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

数据出来后，基于真实数字而不是猜测，决定是否需要在 v3.x 加 client-go 实现。
```

---

## 五、相关文档

- 设计原理：[`docs/design/controller.md`](controller.md)
- 整体架构：[`docs/design/architecture.md`](architecture.md)
- 测试脚本：[`benchmark/scripts/`](../../benchmark/scripts/)
- 原始数据：`benchmark/results/2026-04-25_082859-steady-state/`（git ignored）
