# KubePivot 多环境流量传播

> 编写日期：2026-05-04（从 traffic-multi-env-impl-draft.md 演化）
> 状态：✅ 已实现（v3.0）
> 前身：traffic-multi-env-impl-draft.md（2026-04-28 设计草案）
> 关联：traffic-layer.md / state-machine.md / sharding.md

---

## 一、摘要

### 1.1 核心论断

**"verified-traffic ConfigMap 是 K8s 原生的状态广播"**。

KubePivot v2.6.0 把流量切换纳入 Sandbox 状态机。v2.6.1 起把"已验证的流量
配置"纳入 K8s 原生的状态广播机制 —— **不引入新基础设施**，复用 ConfigMap +
label 约定，让任意 controller 之间通过 K8s API 完成状态传递。

`kp sandbox start --from-env staging` 不是简单的"配置拷贝"。它是
**三方协作的事件传播链**：

```
Staging Controller (生产者)
  ↓ watch state → RUNNING + 5min
  ↓ write ConfigMap kubepivot-verified-traffic

ConfigMap kubepivot-verified-traffic
  (label kubepivot.io/verified-traffic="true")

kp 进程 (搬运工)
  ↓ --from-env staging → loadEnv("staging") → kubectl get cm
  ↓ inject prod sandbox config
  ↓ trigger prod sandbox commit

Prod Controller (消费者)
  ↓ COMMITTING → 蓝绿切换 → RUNNING
  ↓ 5min 后自己也写 verified-traffic（传播链可级联）
```

### 1.2 级联语义

传播链**可任意级联**：

```
staging → prod → DR-cluster → ...
```

任何已经 RUNNING + 5min 稳态的 env 都能作为传播源。这是"verified-traffic
ConfigMap 是 K8s 原生状态广播"论断的直接推论。

### 1.3 与 v2.6.0 traffic-layer 的关系

```
v2.6.0  traffic-layer    Provider 抽象 + Ingress/Gateway API + Sandbox 协同
                          单集群单 namespace 内的蓝绿切换

v2.6.1+ traffic-multi-env 多 env 间的"已验证配置"传播
                          复用 v2.6.0 的 Provider / Sandbox / 命名约定
                          新增 verifiedTrafficWriter (controller 侧) +
                                --from-env flag (CLI 侧) +
                                state.RunningSinceFromHistory helper
```

---

## 二、设计原则

```
1. 不引入新基础设施
   ConfigMap + label 是 K8s 原生约定
   不引入 CRD / 不引入新存储 / 不引入新通信通道

2. 复用 KPEnv 机制
   ~/.kp/envs/<name>.yaml 已经是完整的 env 抽象
   "持两个 KPEnv" = "用 staging KPEnv 读 + 用 prod KPEnv 写"

3. 三方协作 / 单一职责
   Controller 生产 verified-traffic（基于状态机视角）
   kp 搬运 verified-traffic（基于用户意图）
   Controller 消费 verified-traffic（基于 sandbox commit）

4. fail-fast 默认
   staging 没 verified-traffic → fail
   staging cluster 不可达 → fail
   不做猜测式 fallback

5. 不动 git
   --from-env 是"主动拷贝"，不是"git sync"
   传播链 ≠ GitOps 同步
```

---

## 三、三方协作架构

### 3.1 三方角色

| 角色 | 进程 | 职责 | 不做 |
|------|------|------|------|
| 生产者 | controller (staging) | 基于状态机视角输出 verified-traffic | 不做 traffic 切换决策 |
| 搬运工 | kp 进程 (用户机器) | 用户意图驱动的"主动拷贝" | 不持久化中间状态 |
| 消费者 | controller (prod) | 接收 sandbox commit + 走完整状态机 | 不区分配置来源 |

### 3.2 K8s API 是唯一通信媒介

```
只用：
  ✓ K8s API（ConfigMap CRUD + watch）
  ✓ kubeconfig（从 KPEnv 加载）

不引入：
  ✗ controller 之间的直连 gRPC
  ✗ 中心化元数据服务
  ✗ git 作为中转
  ✗ 文件系统共享
```

---

## 四、数据契约（ConfigMap schema）

### 4.1 命名约定

```
namespace:  <project-namespace>
name:       kubepivot-verified-traffic
```

### 4.2 完整 schema

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: kubepivot-verified-traffic
  namespace: staging-app
  labels:
    kubepivot.io/verified-traffic: "true"
    app.kubernetes.io/managed-by: kp
    app.kubernetes.io/component: traffic-layer
  annotations:
    kubepivot.io/last-verified: "2026-04-28T08:00:00Z"
    kubepivot.io/source-state: "RUNNING"
    kubepivot.io/running-since: "2026-04-28T07:55:00Z"
    kubepivot.io/source-version: "v1.5.0"
data:
  traffic.yaml: |
    kind: Ingress
    refs:
      name: wallet-ingress
    routes:
      - service: wallet-service-blue
        weight: 100
      - service: wallet-service-green
        weight: 0
    validation:
      podReadyTimeoutSec: 60
  source.yaml: |
    project: wallet
    namespace: staging-app
    cluster: staging
    version: v1.5.0
    runningSince: "2026-04-28T07:55:00Z"
    verifiedAt: "2026-04-28T08:00:00Z"
    kpVersion: v2.6.1
```

### 4.3 ConfigMap 生命周期

```
创建：verifiedTrafficWriter 第一次扫到 RUNNING + 5min 时
更新：当前 K8s 实际 traffic 状态变化时
删除：namespace 删除 → K8s 自动清理；手动删除 → 下次扫描自动重建
```

---

## 五、代码归档

### 5.1 写入侧（Controller 端）

```
internal/controller/verified_traffic_writer.go   405 行
  runVerifiedTrafficWriter()          — 主循环（1min ticker）
  scanOnceForVerifiedTraffic()        — 扫描所有 managed namespace
  processNamespaceForVerifiedTraffic()  — 单 ns 处理：读 state / 读 traffic / diff / 写 CM
  defaultWriteVerifiedTrafficCM()     — kubectl apply ConfigMap
  buildVerifiedTrafficCMYAML()        — 构造 traffic.yaml + source.yaml

internal/controller/global.go         接入点
  verifiedTrafficWriter 在 orphanSweeper 之后、informerPool 之前启动
```

### 5.2 读取侧（CLI 端）

```
cmd/kp/sandbox_from_env.go           151 行
  loadVerifiedTrafficFromEnv()        — 读 staging ConfigMap → 解析 traffic
  --from-env flag                     — kp sandbox start --from-env staging
```

### 5.3 基础组件

```
internal/state/state.go
  RunningSinceFromHistory()           — 倒序遍历 History，找最近 →RUNNING 时间戳
```

### 5.4 测试覆盖

```
internal/controller/verified_traffic_writer_test.go   ~150 行 / 10 cases
  覆盖：非 RUNNING 跳过 / <5min 跳过 / 首次创建 / 无变更跳过 / 变更触发更新
        读 traffic 失败 / 写 CM 失败 / 非本 pod shard 跳过

internal/state/state_test.go                          ~50 行 / 7 cases
  覆盖：首次部署 / 重部署 / 回滚后 / 非 RUNNING / 空 History / Nil Record / 多次 RUNNING

cmd/kp/sandbox_from_env_test.go                       ~80 行 / 8 cases
  覆盖：成功 / 无 env / 无 ConfigMap / 不可达 / 无效 YAML / 空 traffic / 覆盖已有 / audit 记录
```

---

## 六、错误模式

### 6.1 staging 没 verified-traffic ConfigMap

```
❌ 未发现 verified-traffic ConfigMap

  环境: staging
  Cluster context: orbstack
  Namespace: staging-app

可能原因：
  1. staging controller 版本 < v2.6.1
  2. staging 项目未达到 RUNNING + 5min 稳态
  3. staging 部署后 ConfigMap 被手动删除

检查命令：
  kubectl --context=orbstack -n staging-app get cm kubepivot-verified-traffic
```

### 6.2 staging cluster 不可达

```
❌ 无法连接 staging 环境
  Cluster context: orbstack
  错误：connection refused
```

---

## 七、风险与约束

1. **传播链级联不一致** — staging 更新后 prod 仍跑旧配置。v2.6.1 不保证全局一致（GitOps 范围外）。source.yaml 记录 verifiedAt 时间戳供审计。

2. **ConfigMap 被手动删除** — 下次 verifiedTrafficWriter 扫描（5min 后）自动重建。期间 --from-env 触发 fail-fast。

3. **staging controller 版本错配** — 错误信息含 actionable hint，用户自查 image tag。

4. **多集群未验证** — KPEnv 机制天然支持多集群，但未在真实多集群环境跑过 e2e。

---

## 八、16 个设计 Q 拍板表（摘要）

| Q | 决定 |
|---|------|
| Q1 | ConfigMap + label `kubepivot.io/verified-traffic="true"` |
| Q2 | RUNNING + 5min 稳态后写 |
| Q3 | 直连 staging cluster，复用 KPEnv 机制 |
| Q4 | schema: traffic.yaml + source.yaml 双文件 |
| Q5 | prod 已有 traffic 直接覆盖 |
| Q6 | 不动 git + audit log |
| Q7 | staging 没 verified-traffic → fail-fast |
| Q8 | staging cluster 不可达 → fail-fast |
| Q9 | 进 LOCKED 之前读 staging |
| Q10 | 写入由 controller 周期扫描（1min） |
| Q11 | staging controller 版本错配 → fail + hint |

---

## 九、相关文档

- [traffic-layer.md](traffic-layer.md) — v2.6.0 流量层基础
- [state-machine.md](state-machine.md) — 状态机
- [sharding.md](sharding.md) — v2.5.0 分片（verifiedTrafficWriter 复用 ShardSet）
- [traffic-multi-env-impl-draft.md](traffic-multi-env-impl-draft.md) — 原设计草案（保留存档）

---

## 编辑记录

```
2026-04-28  设计草案创建（qc + Claude）
            16 个设计 Q 全部拍板

2026-05-04  演化为正式实施文档（qc + Claude）
            状态 📐→✅：verifiedTrafficWriter + --from-env + RunningSince 全部落地
            代码归档：~550 行实现 + ~280 行测试
            去除实施脚手架章节，保留设计决策 + 架构
            原草案 traffic-multi-env-impl-draft.md 保留存档
```
