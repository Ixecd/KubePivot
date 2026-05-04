# KubePivot 分片调优指南

> 编写日期：2026-04-26 / 更新 2026-05-04
> 状态：✅ 自适应分片已实现（v3.0）
> 适用版本：v3.0.0+

---

## 摘要

KubePivot v2.5.0 引入分片机制后，系统配置维度变成三维：

```
P  = 项目数      实际业务规模
R  = 副本数      controller 副本数
S  = 分片数      KUBEPIVOT_SHARDS
```

v3.0 已实现 **自适应分片规模推荐**（`kp controller update`），基于 P→S→C 公式
自动推导最优配置，不再需要手动查表调优。

**自动推导公式**：
```
S = clamp(ceil(P/4), 3, 50)
C = clamp(ceil(S/3), 2, 10)
```

本章记录环境约束（来自实测发现）和自适应分片机制的使用方式。

---

## 一、自适应分片（v3.0）

### 1.1 使用方式

```
kp controller update                 # dry-run 预览推荐配置
kp controller update --apply         # 应用推荐配置
kp controller update --force-downscale --apply  # 允许缩减副本数
```

### 1.2 推荐算法逻辑

```
P→S→C 三维推导：
  S = clamp(ceil(P/4), min=3, max=50)
  C = clamp(ceil(S/3), min=2, max=10)

不降配策略：默认不降低 replicas（防止运维事故）
重平衡预警：ΔS≥50% 且 P≥50 时提示低峰期执行
超载保护：P/S > 10 时 🔴 警告 + 建议多集群

详见：controller-update-sizing-draft.md
```

### 1.3 代码位置

```
internal/controller_installer/sizing.go    Recommend() / GetSizingInfo()
internal/controller_installer/apply.go     ApplySizing() / rollbackSizing()
internal/controller_installer/cleanup.go   孤儿 Lease 清理
internal/controller_installer/auth.go      权限预检 + etcd 健康度
cmd/kp/controller.go                       kp controller update CLI
```

---

## 二、环境约束（来自实测发现）

以下约束来自 2026-04-26 在单节点 K8s 集群上的实测，对 benchmark 和
小规模部署有参考价值。

### 2.1 单节点 Pod 容量上限

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
```

### 2.2 macOS + orbstack 的内存约束

orbstack 默认给 K8s VM 8 GiB memory。当集群 K8s memory limits 超过
80% allocatable 时，性能数据失真：

```
触发条件：
  K8s memory limits 使用率 > 80%
  → macOS swap 启动
  → load avg 突破 CPU 核数
  → controller 进程被节流，CPU 数据不真实

安全经验值：
  8 GiB orbstack 默认配置下：
    P_safe_for_benchmark = ~50
```

### 2.3 测试方法论：监控环境健康度

```bash
# 测试前预检
load_avg=$(top -l 1 | grep "Load Avg" | awk '{print $3}' | tr -d ',')
mem_pct=$(kubectl describe node | grep -A 4 "Allocated resources" \
          | grep memory | awk '{print $4}' | tr -d '%()')

if (( $(echo "$load_avg > 5.0" | bc -l) )); then
    echo "load avg $load_avg 偏高，benchmark 数据不可信"
    exit 1
fi
if (( mem_pct > 75 )); then
    echo "memory limits $mem_pct% 接近上限，benchmark 数据失真风险"
    exit 1
fi
```

---

## 三、运维注意事项

### 3.1 大规模 cleanup 的 reconcile 死锁螺旋

实测发现：当 ≥50 个 namespace 同时进入 Terminating 时，controller 会在每个
ns 上跑 reconcile + 自愈，形成死锁螺旋。

```
症状：
  - kubectl delete namespace 卡住
  - namespace 状态长时间 Terminating
  - controller CPU 持续高

解法：先停 controller 再 cleanup
  kubectl scale statefulset kubepivot-controller \
      -n kubepivot-system --replicas=0
  
  # 等几分钟 K8s 自然清理 finalizer
  
  kubectl scale statefulset kubepivot-controller \
      -n kubepivot-system --replicas=3
```

### 3.2 Namespace 强删 API（仅 benchmark）

```bash
for ns in $(kubectl get ns -o name | grep kp-bench); do
    ns_name="${ns#namespace/}"
    kubectl get ns "$ns_name" -o json | \
        jq '.metadata.finalizers = [] | .spec.finalizers = []' | \
        kubectl replace --raw "/api/v1/namespaces/${ns_name}/finalize" -f - 2>/dev/null
done
```

⚠ 这是底层 API，跳过 finalizer 链路 — **生产环境不要用**。

---

## 四、性能数据（历史记录）

以下数据来自 v2.5.0 手动调优阶段，供参考。

### 4.1 已有数据点

```
P=10 R=3 N=10:    avg CPU 7.27%   peak 87.46%   MEM 50.75 MiB
P=50 R=3 N=10:    avg CPU 33.80%  peak 82.27%   MEM 74.85 MiB
P=99 R=3 N=10:    [废弃 — 环境受限，详见 §2.2]
```

### 4.2 缩放性观察（P=10 → P=50）

```
项目数:        5x
集群总 CPU:    4.65x   ← 接近线性
peak CPU:      ~ 不变  ← 单 pod 仍未被压垮
MEM:           1.47x   ← 亚线性，缓存复用
```

---

## 五、相关文档

- [controller-update-sizing-draft.md](controller-update-sizing-draft.md) — 自适应分片详细设计
- [decision-stack.md](decision-stack.md) — 三层资源决策栈
- [sharding.md](sharding.md) — v2.5.0 分片机制设计
- [architecture.md](architecture.md) — 架构总览

---

## 编辑记录

```
2026-04-26  雏形创建（qc + Claude）
            含环境约束章节（来自当日实测发现）
            性能立方体待补 / 手动调优公式（猜测阶段）

2026-05-04  v3.0 更新（qc + Claude）
            自适应分片已实现（kp controller update）
            P→S→C 公式替代手动查表
            保留环境约束 + 运维经验（仍有效）
            移除"待补充"标记
```
