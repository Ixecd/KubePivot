# KubePivot 分片调优指南（雏形）

> 编写日期：2026-04-26（v2.5.1 持续改进）
> 状态：🚧 进行中 — 雏形已建，性能立方体数据待补
> 适用版本：v2.5.0+

---

## 摘要

KubePivot v2.5.0 引入分片机制后，系统配置维度变成三维：

```
P  = 项目数      实际业务规模
R  = 副本数      controller 副本数
N  = 分片数      KUBEPIVOT_SHARDS
```

不同 (P, R, N) 组合下系统表现差异巨大。这份文档记录**真实测试**得出的
最优配置建议、环境约束、以及测试方法论。

**当前状态**：环境约束章节已完成（来自 2026-04-26 实测发现），
最优配置矩阵待补充（性能立方体测试规划中）。

---

## 一、环境约束（来自实测发现）

性能立方体测试本身受测试环境影响。这一节记录单节点 K8s 集群上
发现的真实约束，为后续测试规划提供依据。

### 1.1 单节点 Pod 容量上限

```
KubePivot benchmark 单节点 K8s 集群 P_max 公式：

  P_max ≈ node.pods - controller_replicas - system_pods

  orbstack 默认 K8s：
    node.pods           = 110
    controller_replicas = 3
    system_pods         ≈ 9（kube-system + dashboard 等）
    → P_max ≈ 98
```

实测：尝试 setup P=100，第 100 个 mock pod 进 Pending 状态：

```
Events:
  Warning  FailedScheduling  default-scheduler
    0/1 nodes are available: 1 Too many pods.
    preemption: 0/1 nodes are available: 1 No preemption victims found
```

要测 P > 100，需要：
- 多节点 K8s 集群（每节点 110 上限不变，水平扩展）
- 或调高 kubelet `--max-pods` 到 200+
- 或减少 controller 副本数 / 不安装 kubernetes-dashboard

### 1.2 macOS + orbstack 的内存约束

orbstack 默认给 K8s VM 8 GiB memory。当集群 K8s memory limits 超过
80% allocatable 时，**性能数据失真**：

```
触发条件：
  K8s memory limits 使用率 > 80%
  → macOS swap 启动
  → load avg 突破 CPU 核数
  → controller 进程被节流，CPU 数据不真实

实测案例（2026-04-26 08:30 P=99 跑稳态）：
  K8s allocated:  memory limits 6644Mi / 8186Mi = 83%
  macOS load:     8.54 (10 核环境，已开始排队)
  macOS CPU sys:  40.54% (典型内存压力 / 调度抖动)
  
  controller 实测 CPU:
    avg 23.79% / pod   ← 比 P=50（33.80%）还低 30%
    peak 63.45%        ← 想冲但被节流
    
  → 数据反向、不可信，废弃
```

**安全经验值**：

```
8 GiB orbstack 默认配置下：
  P_safe_for_benchmark = ~50
  
要测 P > 50 需要：
  - 升 orbstack VM memory 到 16 GiB
  - 或使用真实多节点 K8s 集群
  - 或减小 mock 项目的 memory request
```

### 1.3 测试方法论：监控环境健康度

任何性能数据都需要**环境健康度作为前提条件验证**。
建议在 benchmark 脚本里加入：

```bash
# 测试前预检
load_avg=$(top -l 1 | grep "Load Avg" | awk '{print $3}' | tr -d ',')
mem_pct=$(kubectl describe node | grep -A 4 "Allocated resources" \
          | grep memory | awk '{print $4}' | tr -d '%()')

if (( $(echo "$load_avg > 5.0" | bc -l) )); then
    err "load avg $load_avg 偏高，benchmark 数据不可信"
    exit 1
fi

if (( mem_pct > 75 )); then
    err "memory limits $mem_pct% 接近上限，benchmark 数据失真风险"
    exit 1
fi
```

→ 这是 v2.5.1 性能立方体测试脚本（matrix.sh）应该具备的能力。

---

## 二、运维注意事项

### 2.1 大规模 cleanup 的"reconcile 死锁螺旋"

实测发现：当 ≥50 个 namespace 同时进入 Terminating 时，
controller 会在每个 ns 上跑 reconcile + 自愈 + 失败 + 重试。
叠加内存压力，形成"reconcile vs cleanup"的死锁螺旋。

```
症状：
  - kubectl delete namespace 卡住
  - namespace 状态长时间 Terminating
  - controller CPU 持续高
  - cleanup.sh 报告"X 个 namespace 仍未删除"

实测案例（2026-04-26 08:37）：
  100 个 kp-bench-NNN 同时 cleanup
  90 秒后只清了 6/100
  controller 持续 reconcile 残余 ns
  K8s namespace finalizer 卡死

解法：先停 controller 再 cleanup
  kubectl scale deployment kubepivot-controller \
      -n kubepivot-system --replicas=0
  
  # 等几分钟 K8s 自然清理 finalizer
  
  # 全清完后恢复
  kubectl scale deployment kubepivot-controller \
      -n kubepivot-system --replicas=3
```

### 2.2 Namespace 强删 API（最后手段）

如果 namespace 卡 Terminating 超过 5 分钟未自然清理：

```bash
# 用 K8s 官方支持的强删 namespace 方式
for ns in $(kubectl get ns -o name | grep kp-bench); do
    ns_name="${ns#namespace/}"
    kubectl get ns "$ns_name" -o json | \
        jq '.metadata.finalizers = [] | .spec.finalizers = []' | \
        kubectl replace --raw "/api/v1/namespaces/${ns_name}/finalize" -f - 2>/dev/null
done
```

⚠ 这是底层 API，跳过 finalizer 链路 — **生产环境不要用**。
仅适用于 benchmark 测试集群。

---

## 三、性能立方体（待补充）

> 这一节将记录 (P, R, N) 三维空间下的实测数据，
> 找最优配置公式。

### 3.1 测试矩阵（计划）

```
阶段 1：固定 R=3 N=10，扫 P {10/50/100}
阶段 2：固定 P=50 N=10，扫 R {3/5}
阶段 3：固定 P=50 R=3，扫 N {3/10/15}
```

### 3.2 当前已有数据点

```
P=10 R=3 N=10:    avg CPU 7.27%   peak 87.46%   MEM 50.75 MiB
P=50 R=3 N=10:    avg CPU 33.80%  peak 82.27%   MEM 74.85 MiB
P=99 R=3 N=10:    [废弃 — 环境受限，详见 1.2]
```

### 3.3 缩放性观察（P=10 → P=50）

```
项目数:        5x
集群总 CPU:    4.65x   ← 接近线性
peak CPU:      ~ 不变  ← 单 pod 仍未被压垮
MEM:           1.47x   ← 亚线性，缓存复用
```

→ 在 P=50 范围内，v2.5.0 分片机制水平扩展正常工作。

### 3.4 待跑数据点（在 16 GiB orbstack 或多节点环境下）

```
[ ] P=100 R=3 N=10    要等 orbstack 升级或多节点
[ ] P=50 R=5 N=10     阶段 2 第一组合
[ ] P=50 R=3 N=3      阶段 3 第一组合
[ ] P=50 R=3 N=15     阶段 3 第二组合
```

---

## 四、配置建议（待数据驱动补完）

### 4.1 最优 N 公式（猜测，待验证）

```
N_optimal ≈ R × ceiling(P / 30)

理由（推测）：
  - 每 shard 容纳 ~30 项目时 reconcile 不会被压垮
  - N 太小 → 单 pod 持有过多 → CPU 集中
  - N 太大 → lease 协议开销 ↑ + 配额碎片化

  R=3, P=10:   N ≈ 3 × 1 = 3
  R=3, P=50:   N ≈ 3 × 2 = 6
  R=3, P=100:  N ≈ 3 × 4 = 12
  R=5, P=200:  N ≈ 5 × 7 = 35
```

→ 待 v2.5.1 性能立方体数据验证。

### 4.2 副本数选择

```
项目数         推荐副本数 R
< 30           3 副本足够（v2.4.0 单 leader 也能扛）
30-100         3-5 副本（分片真正派上用场）
100-500        5-10 副本（接近 K8s controller 推荐范围）
> 500          需要 v2.7.0 自研 informer
```

→ 待 v2.5.1 性能立方体数据验证。

---

## 五、相关文档

- [架构总览](architecture.md)
- [Controller 设计](controller.md)
- [分片机制设计](sharding.md) — v2.5.0 完整设计
- [性能基准](performance.md) — 累计性能数据
- [HANDOFF.md](../../HANDOFF.md) — 开发约束与教训
- [TODO.md](../../TODO.md) — v2.5.1 性能立方体规划

---

## 编辑记录

```
2026-04-26  雏形创建（qc + Claude）
            含环境约束章节（来自当日实测发现）
            性能立方体待补
```
