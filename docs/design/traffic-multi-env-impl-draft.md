# KubePivot v2.6.1 多环境流量传播 — 实施草案

> 编写日期：2026-04-28
> 状态：📐 实施草案（16 个设计 Q 已拍板，待 Step 1 实施）
> 适用版本：v2.6.1 起（v2.6.0 traffic-layer 的延续工作）
> 工作量预估：~3 天（Step 1-5 完整实施 + 测试 + 文档）
> 前置依赖：v2.6.0 traffic-layer / v2.5.0 sharding / v2.4 state.Machine 持久化

---

## 一、摘要

### 1.1 核心论断

**"verified-traffic ConfigMap 是 K8s 原生的状态广播"**。

KubePivot v2.6.0 把流量切换纳入 Sandbox 状态机。v2.6.1 把"已验证的流量
配置"纳入 K8s 原生的状态广播机制 —— **不引入新基础设施**，复用 ConfigMap +
label 约定，让任意 controller 之间通过 K8s API 完成状态传递。

`kp deploy --env prod --from-env staging` 不是简单的"配置拷贝"。它是
**三方协作的事件传播链**：

```
Staging Controller (生产者)
  ↓ watch state → RUNNING + 5min
  ↓ write ConfigMap kubepivot-verified-traffic
  
ConfigMap kubepivot-verified-traffic
  (label kubepivot.io/verified-traffic="true")
  
kp 进程 (搬运工)
  ↓ --from-env staging → loadEnv("staging") → kubectl get cm
  ↓ inject prod deployConfig
  ↓ trigger prod sandbox commit
  
Prod Controller (消费者)
  ↓ COMMITTING → 蓝绿切换 → RUNNING
  ↓ 5min 后自己也写 verified-traffic（传播链可继续级联）
```

### 1.2 级联语义

传播链**可任意级联**：

```
staging → prod → DR-cluster → ...
```

任何已经 RUNNING + 5min 稳态的 env 都能作为传播源。这是"verified-traffic
ConfigMap 是 K8s 原生状态广播"论断的直接推论 —— ConfigMap 跟"是谁产出
的"无关，只跟"产出后是谁验证过"有关。

prod 接到 staging 的配置后，自己也走完整状态机（LOCKED → ... → RUNNING），
RUNNING + 5min 后自己也产出 verified-traffic，下游 env 可以以 prod 为
传播源。

### 1.3 与 v2.6.0 traffic-layer 的关系

```
v2.6.0  traffic-layer    Provider 抽象 + Ingress/Gateway API + Sandbox 协同
                          单集群单 namespace 内的蓝绿切换
                          
v2.6.1  traffic-multi-env 多 env 间的"已验证配置"传播
                          复用 v2.6.0 的 Provider / Sandbox / 命名约定
                          仅新增 verifiedTrafficWriter (controller 侧) +
                                --from-env flag (CLI 侧) +
                                state.RunningSince helper
```

**v2.6.1 不替代 v2.6.0**，是延续工作。v2.6.0 的 14 个 Q 拍板继续生效。

---

## 二、设计原则（贯穿 v2.6.1）

```
1. 不引入新基础设施
   ConfigMap + label 是 K8s 原生约定
   不引入 CRD / 不引入新存储 / 不引入新通信通道
   
2. 复用 KPEnv 机制
   `~/.kp/envs/<name>.yaml` 已经是完整的 env 抽象
   v2.6.1 不发明新的 kubeconfig 加载层
   "持两个 KPEnv" = "用 staging KPEnv 读 + 用 prod KPEnv 写"
   
3. 三方协作 / 单一职责
   Controller 生产 verified-traffic（基于状态机视角）
   kp 搬运 verified-traffic（基于用户意图）
   Controller 消费 verified-traffic（基于 sandbox commit）
   各自只管自己的角色
   
4. fail-fast 默认
   staging 没 verified-traffic → fail (Q7=A)
   staging cluster 不可达 → fail (Q8=A)
   staging controller 版本不到 v2.6.1 → fail + actionable hint (Q11=C)
   不做猜测式 fallback
   
5. 不动 git
   `--from-env` 是"主动拷贝"，不是"git sync"
   resources.yaml 只在内存里被覆盖，git 历史不留痕
   audit log（slog.Info）记录"manual override traffic via --from-env"
   传播链 ≠ GitOps 同步（这是与 ArgoCD / Flux 的关键差异）
   
6. 与 v2.5.0 / v2.6.0 哲学一致
   双保险（ConfigMap 缺失 → fail-fast，不 fallback git）
   只保护不越权（label kubepivot.io/verified-traffic 标记 KubePivot 管这个 cm）
   简单 > 完美（不引入版本/签名/加密，v2.8+ 范围）
```

---

## 三、三方协作架构

### 3.1 整体架构图

```
┌─────────────────────────────────────────────────────────────────┐
│  Staging cluster（或同集群 staging namespace）                   │
│                                                                   │
│  ┌──────────────────────────────────────┐                       │
│  │ kubepivot-controller (v2.6.1+)       │                       │
│  │                                       │                       │
│  │  ┌─────────────────────────────────┐ │                       │
│  │  │ verifiedTrafficWriter (新增)    │ │                       │
│  │  │   每 1min 扫所有 RUNNING state  │ │                       │
│  │  │   state.RunningSince() ≥ 5min   │ │                       │
│  │  │   ↓                              │ │                       │
│  │  │   读 K8s 实际 traffic           │ │                       │
│  │  │   (kubectl get ingress/...)     │ │                       │
│  │  │   ↓                              │ │                       │
│  │  │   diff vs 当前 ConfigMap         │ │                       │
│  │  │   不同才写                       │ │                       │
│  │  └─────────────────────────────────┘ │                       │
│  └──────────────────────────────────────┘                       │
│         │ K8s API                                                 │
│         ▼                                                         │
│  ┌──────────────────────────────────────────────┐                │
│  │ namespace: <project-namespace>               │                │
│  │ ConfigMap: kubepivot-verified-traffic        │                │
│  │   labels:                                     │                │
│  │     kubepivot.io/verified-traffic: "true"    │                │
│  │     app.kubernetes.io/managed-by: kp         │                │
│  │   annotations:                                │                │
│  │     kubepivot.io/last-verified: <RFC3339>    │                │
│  │     kubepivot.io/source-state: RUNNING       │                │
│  │   data:                                       │                │
│  │     traffic.yaml: <traffic block>            │                │
│  │     source.yaml:  <provenance metadata>      │                │
│  └──────────────────────────────────────────────┘                │
└─────────────────────────────────────────────────────────────────┘
                  ▲
                  │ kubectl get cm
                  │ (用 staging KPEnv：kubeconfig + context + namespace)
                  │
         ┌────────┴────────────────────────┐
         │ kp deploy --env prod \           │
         │           --from-env staging     │
         │                                  │
         │  loadEnv("staging") → KPEnv      │
         │  loadEnv("prod")    → KPEnv      │
         │                                  │
         │  read  staging ConfigMap         │
         │  parse traffic.yaml               │
         │  inject prod deployConfig         │
         │                                  │
         │  audit: slog.Info "override..."  │
         └────────┬────────────────────────┘
                  │ trigger prod sandbox commit
                  │ (用 prod KPEnv)
                  ▼
┌─────────────────────────────────────────────────────────────────┐
│  Prod cluster（或同集群 prod namespace）                          │
│                                                                   │
│  ┌──────────────────────────────────────┐                       │
│  │ kubepivot-controller (v2.6.1+)       │                       │
│  │                                       │                       │
│  │  receive sandbox commit with new      │                       │
│  │  traffic config (in resources.yaml    │                       │
│  │  内存形式，git 不动)                   │                       │
│  │                                       │                       │
│  │  state machine:                       │                       │
│  │    LOCKED → SNAPSHOTTING → SIMULATING │                       │
│  │    → COMMITTING                       │                       │
│  │       Step 1: helm upgrade            │                       │
│  │       Step 2: 蓝绿切换 (v2.6.0)       │                       │
│  │    → RUNNING                          │                       │
│  │                                       │                       │
│  │  RUNNING + 5min:                      │                       │
│  │    verifiedTrafficWriter 也写 prod    │                       │
│  │    自己的 verified-traffic ConfigMap  │                       │
│  │    (传播链可级联)                      │                       │
│  └──────────────────────────────────────┘                       │
└─────────────────────────────────────────────────────────────────┘
```

### 3.2 三方角色定义

| 角色 | 进程 | 职责 | 不做 |
|------|------|------|------|
| 生产者 | controller (staging) | 基于状态机视角输出 verified-traffic | 不做 traffic 切换决策 |
| 搬运工 | kp 进程 (用户机器) | 用户意图驱动的"主动拷贝" | 不持久化中间状态 |
| 消费者 | controller (prod) | 接收 sandbox commit + 走完整状态机 | 不区分配置来源 |

**单一职责的工程价值**：

- 生产者不知道"谁会消费"。它只对自己的 K8s 实际状态负责
- 搬运工不知道"谁产出的"。它只读 ConfigMap，不查谁写的
- 消费者不知道"配置从哪来"。它只走 sandbox commit 流程

这种解耦让传播链天然支持任意级联（§1.2）。

### 3.3 K8s API 是唯一通信媒介

```
不引入：
  ✗ kpcontroller 之间的直连 gRPC
  ✗ 中心化的 KubePivot 元数据服务
  ✗ git 作为 staging→prod 的中转
  ✗ 文件系统共享（NFS / S3 / 等）

只用：
  ✓ K8s API（ConfigMap CRUD + watch）
  ✓ kubeconfig（从 KPEnv 加载）

理由：
  - K8s API 已经是 KubePivot 的"事实信道"
  - ConfigMap 是 K8s 原生最简单的"键值广播"
  - 任何 K8s RBAC / 网络策略 / 审计日志都自然适用
  - 不引入新可靠性问题
```

---

## 四、数据契约（ConfigMap schema）

### 4.1 命名约定

```
namespace:  <project-namespace>          (Q1.1=A: ns 是 KubePivot 硬边界)
name:       kubepivot-verified-traffic   (固定名)
```

每个 namespace 一个 ConfigMap。如果同 namespace 有多个 KubePivot project，
当前 v2.6.1 范围内**约定一个 namespace 一个 project**（与 v2.5.0 sharding
"per-namespace ownership" 保持一致）。

### 4.2 完整 schema

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: kubepivot-verified-traffic
  namespace: staging-app
  
  # 业务 label：标记这是 KubePivot 管理的 verified-traffic ConfigMap
  labels:
    kubepivot.io/verified-traffic: "true"
    app.kubernetes.io/managed-by: kp
    app.kubernetes.io/component: traffic-layer
  
  # 时间 / 来源 metadata：审计 + 监控用
  annotations:
    kubepivot.io/last-verified: "2026-04-28T08:00:00Z"
    kubepivot.io/source-state: "RUNNING"
    kubepivot.io/running-since: "2026-04-28T07:55:00Z"
    kubepivot.io/source-version: "v1.5.0"

# 业务数据：双文件设计
data:
  # traffic.yaml: 跟 resources.yaml 的 traffic block 完全同 schema
  # 读取侧直接复用既有 yaml parser
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
  
  # source.yaml: 出处溯源
  # 不做"运行时校验"，只做审计记录
  source.yaml: |
    project: wallet
    namespace: staging-app
    cluster: staging  # KPEnv name (写入侧从 controller 配置读)
    version: v1.5.0
    runningSince: "2026-04-28T07:55:00Z"
    verifiedAt: "2026-04-28T08:00:00Z"
    kpVersion: v2.6.1
```

### 4.3 schema 设计决策

**为什么 traffic.yaml + source.yaml 双文件（Q4 校准点 C）**：

```
A. 单文件 traffic.yaml         缺出处溯源
B. 单文件 verified.yaml        混合业务 + metadata，schema 难以演化
C. 双文件 traffic.yaml + source.yaml  ← 选择
   - traffic.yaml 直接复用 resources.yaml 的 yaml parser
   - source.yaml 提供 audit / 调试支持
   - 二者独立演化（traffic.yaml 跟 v2.6 traffic schema 走，
     source.yaml 跟 v2.6.1+ 元信息扩展走）
```

**为什么 label 不用 selector 形式**（如 `traffic-source: staging`）：

```
不要让 label 表达业务语义
label 只标记"这是 KubePivot 管的 verified-traffic ConfigMap"
具体业务 metadata（who/when/version）放 annotation 或 data
理由：label 用于 list filtering，简单的 boolean 语义最稳定
```

### 4.4 ConfigMap 生命周期

```
创建：verifiedTrafficWriter 第一次扫到 RUNNING + 5min 时
更新：当前 K8s 实际 traffic 状态变化时（Q2 校准点 B）
删除：
  - 项目 cleanup → KubePivot controller 顺带清理（v2.6.1 范围内不做，留 v2.6.2）
  - namespace 删除 → K8s 自动清理（不需要特殊处理）
  - 用户手动删除 → 下次 verifiedTrafficWriter 扫到时重新创建
```

---

## 五、写入侧（controller 端 verifiedTrafficWriter）

### 5.1 触发条件

```
每 1min 扫描所有 KubePivot 管理的 project
对每个 project 检查：
  1. state.Machine.State() == RUNNING ?  (continue if not)
  2. state.Machine.RunningSince() >= 5min ?  (continue if not)
  3. K8s 实际 traffic 状态 vs ConfigMap data 不一致 ?  (continue if same)
  4. → 写 ConfigMap

扫描机制：
  跟 internal/controller/sweeper.go (orphan sweeper) 同模式
  ticker := time.NewTicker(1 * time.Minute)
  controller goroutine 内启动
  跨 controller 重启稳定（state 持久化保证）
```

### 5.2 实现（草图）

```go
// internal/controller/verified_traffic_writer.go (新增 ~250 行)
package controller

import (
    "context"
    "log/slog"
    "time"
    
    "github.com/Ixecd/kubepivot/internal/route"
    "github.com/Ixecd/kubepivot/internal/state"
)

const (
    verifiedTrafficStableThreshold = 5 * time.Minute
    verifiedTrafficScanInterval    = 1 * time.Minute
    verifiedTrafficConfigMapName   = "kubepivot-verified-traffic"
)

// VerifiedTrafficWriter 周期扫描 RUNNING + 5min 的 project，
// 写 verified-traffic ConfigMap 到 K8s
type VerifiedTrafficWriter struct {
    store      state.Store
    shardSet   eventstream.ShardSet  // v2.5 集成：仅扫本 pod 持有的 shard
    kubeconfig string
    
    // 测试 mock 注入（与 v2.7 同模式）
    readActualTraffic func(ctx context.Context, ns string) (*route.Traffic, error)
}

func NewVerifiedTrafficWriter(store state.Store, shardSet eventstream.ShardSet, kubeconfig string) *VerifiedTrafficWriter {
    return &VerifiedTrafficWriter{
        store:             store,
        shardSet:          shardSet,
        kubeconfig:        kubeconfig,
        readActualTraffic: defaultReadActualTraffic,
    }
}

func (w *VerifiedTrafficWriter) Run(ctx context.Context) {
    ticker := time.NewTicker(verifiedTrafficScanInterval)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            w.scanOnce(ctx)
        }
    }
}

func (w *VerifiedTrafficWriter) scanOnce(ctx context.Context) {
    // 列举所有 KubePivot 管理的 project（通过 store 或 K8s namespace label）
    namespaces, err := listManagedNamespaces(ctx, w.kubeconfig)
    if err != nil {
        slog.Warn("verifiedTrafficWriter: list namespaces failed", "err", err)
        return
    }
    
    for _, ns := range namespaces {
        // shard 过滤：仅处理本 pod 持有的 ns（v2.5 集成）
        if !w.shardSet.Owns(ns) {
            continue
        }
        
        // 读 state record
        record, err := w.store.Load(projectFromNamespace(ns), ns)
        if err != nil {
            continue  // 静默：可能项目还没初始化
        }
        
        // 检查触发条件
        if record.State != state.StateRunning {
            continue
        }
        runningSince := state.RunningSinceFromHistory(record)
        if runningSince == nil || time.Since(*runningSince) < verifiedTrafficStableThreshold {
            continue
        }
        
        // 读当前 K8s 实际 traffic 状态
        actual, err := w.readActualTraffic(ctx, ns)
        if err != nil {
            slog.Warn("verifiedTrafficWriter: read actual traffic failed",
                "ns", ns, "err", err)
            continue
        }
        if actual == nil {
            continue  // 项目没声明 traffic，不需要写
        }
        
        // 对比 ConfigMap，不同才写（Q2 校准点 B）
        if !w.needsUpdate(ctx, ns, actual) {
            continue
        }
        
        // 写 ConfigMap
        if err := w.writeConfigMap(ctx, ns, record, actual); err != nil {
            slog.Warn("verifiedTrafficWriter: write configmap failed",
                "ns", ns, "err", err)
            continue
        }
        
        slog.Info("verifiedTrafficWriter: wrote configmap",
            "ns", ns, "state", record.State, "since", runningSince.Format(time.RFC3339))
    }
}

func (w *VerifiedTrafficWriter) needsUpdate(ctx context.Context, ns string, actual *route.Traffic) bool {
    // 读 ConfigMap 的当前 data
    // 反序列化 traffic.yaml
    // deep equal vs actual
    // 不同 → 返回 true
    // 一致 → 返回 false（避免每分钟重复 update）
    // 错误（ConfigMap 不存在）→ 返回 true（首次创建）
    ...
}

func (w *VerifiedTrafficWriter) writeConfigMap(
    ctx context.Context, ns string,
    record *state.DeployRecord, actual *route.Traffic,
) error {
    trafficYAML := marshalTraffic(actual)
    sourceYAML := marshalSource(record, ns)
    
    cmYAML := buildConfigMapYAML(ns, trafficYAML, sourceYAML, record)
    return kubectlApply(ctx, w.kubeconfig, ns, cmYAML)
}
```

### 5.3 接入位置（controller 主循环）

```go
// internal/controller/global.go
// 在 orphanSweeper 之后启动 verifiedTrafficWriter
// 跟 v2.7 informerPool 同位置
//
// 6. v2.5 orphanSweeper
// 7. v2.6.1 verifiedTrafficWriter (新增)
// 8. v2.7 informerPool
```

### 5.4 错误处理

**全部 warn-only**，不阻断 controller 主循环：

```
list namespaces 失败       → log warn + 跳过本轮
state.Load 失败            → 静默（可能项目未初始化）
readActualTraffic 失败     → log warn + 跳过该 ns
writeConfigMap 失败        → log warn + 下轮重试
shardSet 不持有 ns         → 静默跳过（不是错误）
```

**理由**：verifiedTrafficWriter 是"事后异步广播"，不在用户操作的关键路径上。
失败重试比错误传播更合适。

### 5.5 与 v2.5 sharding 的集成

```
shardSet 过滤：仅扫本 pod 持有的 namespace
跟 v2.7 informerPool / v2.5 orphanSweeper 同模式
NewShardSetAdapter(shardMgr.Shards(), totalShards) 复用
不需要新增 sharding 接口
```

### 5.6 测试覆盖（~150 行）

```
TestRunningSinceCalculation     state 历史多场景（含回滚后重入）
TestScanOnce_NotRunning         非 RUNNING 跳过
TestScanOnce_LessThan5Min       不到 5min 跳过
TestScanOnce_NotOwnedShard      非本 pod shard 跳过
TestScanOnce_FirstWrite         首次创建 ConfigMap
TestScanOnce_NoUpdate           ConfigMap 已有同内容跳过 update
TestScanOnce_UpdateNeeded       K8s 实际 traffic 变化触发 update
TestScanOnce_ReadActualFailure  读 K8s ingress 失败 warn-only
TestScanOnce_WriteFailure       kubectl apply 失败 warn-only
TestNeedsUpdate_DeepEqual       traffic 字段级 diff
```

---

## 六、state.RunningSince() helper

### 6.1 接口

```go
// internal/state/state.go (+1 函数, ~30 行)

// RunningSinceFromHistory 倒序遍历 record.History
// 找最近一次 To == StateRunning 的 Timestamp
// 没有任何 →RUNNING 历史则返回 nil
//
// 语义：v2.6.1 范围内取"最近一次进 RUNNING 的时间"
// 不区分 deploy / rollback / resume → RUNNING（Q1 校准点 A）
//
// 用途：
//   - verifiedTrafficWriter 判断 5min 稳态
//   - 未来 v2.7+ kp status / metrics 暴露
func RunningSinceFromHistory(record *DeployRecord) *time.Time {
    if record == nil {
        return nil
    }
    if record.State != StateRunning {
        return nil  // 当前不在 RUNNING，没有"since"语义
    }
    for i := len(record.History) - 1; i >= 0; i-- {
        if record.History[i].To == StateRunning {
            ts := record.History[i].Timestamp
            return &ts
        }
    }
    return nil  // 当前 state == RUNNING 但 History 没记录（理论不应该，防御性）
}
```

### 6.2 语义边界（Q1 校准点 A）

```
场景 1: 首次部署
  IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING (t=10:00)
  RunningSince() = t=10:00 ✓

场景 2: 重新部署
  RUNNING → INITIALIZING → DEPLOYING → ... → RUNNING (t=10:30)
  RunningSince() = t=10:30 ✓ (不是 t=10:00)

场景 3: 回滚后
  RUNNING → ROLLINGBACK → RUNNING (t=10:45)
  RunningSince() = t=10:45 ✓

场景 4: 当前不在 RUNNING
  RUNNING → ROLLINGBACK (当前)
  RunningSince() = nil ✓

场景 5: 历史不完整（异常）
  当前 State=RUNNING 但 History 为空
  RunningSince() = nil ✓ (防御性，verifiedTrafficWriter 跳过)
```

**为什么不排除"短暂 RUNNING"**（不选 B/C 复杂语义）：

verifiedTrafficWriter 写的是**当前 K8s 实际 traffic 状态**（kubectl get
ingress），不是历史声明。回滚后即使 5min 触发写入，写出去的就是回滚后的
traffic 状态，逻辑自洽。

如果担心"短暂 RUNNING 触发误写"，那也是 verifiedTrafficWriter 的
needsUpdate 逻辑要处理（K8s 实际状态 vs ConfigMap diff），不是 RunningSince
的语义。

### 6.3 测试覆盖（~50 行）

```
TestRunningSince_FirstDeploy         首次部署
TestRunningSince_Redeploy            重部署后
TestRunningSince_AfterRollback       回滚后
TestRunningSince_NotInRunning        当前非 RUNNING
TestRunningSince_EmptyHistory        防御性
TestRunningSince_NilRecord           防御性
TestRunningSince_MultipleRunning     多次 RUNNING 取最近
```

---

## 七、读取侧（CLI 端 --from-env）

### 7.1 flag 接入位置

```go
// cmd/kp/deploy.go
// 在既有 envName flag 旁边加 fromEnvName flag

envName := flags.String("env", "", "指定部署环境（kp context add 配置）")
fromEnvName := flags.String("from-env", "",
    "从指定环境的 verified-traffic 拷贝流量配置（v2.6.1+）")
```

### 7.2 读取流程（在 LOCKED/INITIALIZING 之前 — Q9=A）

```go
// cmd/kp/deploy.go runDeploy（line ~68 之后）

// --env 覆盖（既有逻辑）
if *envName != "" {
    kpEnv, err := loadEnv(*envName)
    if err != nil { ... }
    applyEnvToConfig(cfg, kpEnv)
    applyEnvToMap(env, kpEnv)
}

// --from-env 读 verified-traffic（v2.6.1 新增）
if *fromEnvName != "" {
    if err := injectVerifiedTraffic(cfg, *fromEnvName); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)  // fail-fast (Q7/Q8/Q11)
    }
}

// ... resolveDeployConfig, BuildPlan, executeDeploy ...
```

### 7.3 injectVerifiedTraffic 实现

```go
// cmd/kp/deploy.go (~+80 行)

// injectVerifiedTraffic 从 fromEnv 读 verified-traffic ConfigMap
// 解析 traffic.yaml
// 注入到当前 deployConfig（覆盖 cfg.Traffic 字段）
//
// fail-fast（不做猜测式 fallback）:
//   - fromEnv KPEnv 不存在 → Q7
//   - fromEnv cluster 不可达 → Q8
//   - ConfigMap 不存在 → Q7 (含 Q11 hint)
//   - traffic.yaml 解析失败 → 直接报错
func injectVerifiedTraffic(cfg *deployConfig, fromEnvName string) error {
    // 1. 加载 fromEnv KPEnv
    fromEnv, err := loadEnv(fromEnvName)
    if err != nil {
        return fmt.Errorf("--from-env %q 配置不存在: %w\n"+
            "运行 `kp context add --name %s ...` 添加",
            fromEnvName, err, fromEnvName)
    }
    
    // 2. 用 fromEnv 的 kubeconfig + context + namespace 调 kubectl
    args := []string{
        "--kubeconfig", expandHome(fromEnv.Kubeconfig),
        "--context", fromEnv.Context,
        "--namespace", fromEnv.Namespace,
        "get", "configmap", "kubepivot-verified-traffic",
        "-o", "yaml",
    }
    out, err := runOutput(append([]string{"kubectl"}, args...)...)
    if err != nil {
        // 区分 Q7 / Q8 / Q11
        return diagnoseReadError(fromEnvName, fromEnv, err)
    }
    
    // 3. 解析 ConfigMap.data["traffic.yaml"]
    traffic, source, err := parseVerifiedTrafficConfigMap(out)
    if err != nil {
        return fmt.Errorf("解析 verified-traffic ConfigMap 失败: %w", err)
    }
    
    // 4. 注入 deployConfig（v2.6.1 新增 cfg.Traffic 字段，与 resources.yaml 同 schema）
    cfg.Traffic = traffic
    
    // 5. audit log（Q6.1=C 不动 git，但留痕）
    slog.Info("manual override traffic via --from-env",
        "from-env", fromEnvName,
        "from-namespace", fromEnv.Namespace,
        "from-cluster", fromEnv.Context,
        "verified-at", source.VerifiedAt,
        "source-version", source.Version,
    )
    
    P.Info("🔄", fmt.Sprintf("已从 %q 拷贝 verified-traffic（验证于 %s, v%s）",
        fromEnvName, source.VerifiedAt, source.Version))
    return nil
}
```

### 7.4 注入语义（Q5=C 直接覆盖 + Q6.1=C 不动 git）

```
prod 的 resources.yaml 已有 traffic 字段:
  → 内存覆盖（cfg.Traffic = staging 的 traffic）
  → 不写回 git（resources.yaml 文件不变）
  → audit log 记录覆盖动作

prod 的 K8s 实际 traffic vs 注入的 traffic 不一致:
  → 进入 sandbox commit（Q6=A 走正常蓝绿切换）
  → COMMITTING Step 2 (v2.6.0 traffic-layer) 执行流量切换
  → RUNNING

下次 git push 时:
  → resources.yaml 仍是原状（git 不动）
  → 用户需要手动同步（如希望 git 反映新状态）
  → 或下次 deploy 时再次 --from-env
```

---

## 八、错误模式（Q7 / Q8 / Q11 联合）

### 8.1 Q7：staging 没 verified-traffic ConfigMap

**场景**：staging cluster 可达，KPEnv 配置正确，但 ConfigMap 不存在。

可能原因：
1. staging 项目从未部署过
2. staging 上次部署 RESTORING 失败（没到 RUNNING）
3. staging RUNNING 后还不到 5min（ConfigMap 还没产出）
4. staging controller 版本 < v2.6.1（Q11 场景）
5. ConfigMap 被手动删除

**错误信息（Q11=C：错误信息加 hint，不探测 image tag）**：

```
❌ 未发现 verified-traffic ConfigMap

  环境: staging
  Cluster context: orbstack
  Namespace: staging-app
  期望 ConfigMap: kubepivot-verified-traffic

可能原因：
  1. staging controller 版本 < v2.6.1（不支持 verified-traffic 输出）
  2. staging 项目未达到 RUNNING + 5min 稳态
  3. staging 部署后 ConfigMap 被手动删除

检查命令：
  kubectl --kubeconfig=~/.kube/config --context=orbstack \
    -n kubepivot-system get pod -l app=kubepivot-controller \
    -o jsonpath='{.items[0].spec.containers[0].image}'

  kubectl --kubeconfig=~/.kube/config --context=orbstack \
    -n staging-app get cm kubepivot-verified-traffic

如果 staging controller 版本不到 v2.6.1，请升级 staging 端 controller
后再尝试。
```

### 8.2 Q8：staging cluster 不可达

**场景**：kubectl 无法连接 staging cluster。

原因：网络问题、kubeconfig 错误、context 不存在、cluster 已下线等。

**错误信息**：

```
❌ 无法连接 staging 环境

  Cluster context: orbstack
  Kubeconfig: ~/.kube/config

错误：dial tcp 192.168.x.x:6443: connect: connection refused

检查命令：
  kubectl --kubeconfig=~/.kube/config --context=orbstack get ns
  kp context show staging
```

### 8.3 Q11：staging controller 版本不到 v2.6.1

**v2.6.1 范围内不主动探测 controller 版本**（避免脆弱依赖 image tag）。

**实际行为**：跟 Q7 同处理，错误信息含 actionable hint（§8.1 已含）。

未来 v2.7+ 候选：在 ConfigMap.annotations 加 `kubepivot.io/kp-version`，
读取侧检查版本兼容性。但 v2.6.1 不做。

### 8.4 错误信息文案约定

```
- 中文为主（与 KubePivot 既有 CLI 错误信息风格一致）
- 必须含「检查命令」段（actionable）
- 错误码可选（v2.6.1 不引入 Err110100+ 子区段）
- slog.Error 写日志时英文 key + 中文 message
```

---

## 九、测试矩阵

### 9.1 单测（~280 行）

```
internal/state/state_test.go (+~50 行)
  TestRunningSinceFromHistory_*  7 cases (§6.3)

internal/controller/verified_traffic_writer_test.go (+~150 行)
  TestVerifiedTrafficWriter_*    10 cases (§5.6)
  fakeTrafficReader / fakeKubectl 注入 mock

cmd/kp/deploy_from_env_test.go (+~80 行)
  TestInjectVerifiedTraffic_*    8 cases
    Success / NoEnv / NoConfigMap / Unreachable / 
    InvalidYAML / EmptyTraffic / OverridesExisting / AuditLogged
```

### 9.2 集成测试（demo 工程）

```
docs/example-blue-green-multi-env/
├── Makefile
├── chart/                              共用 chart
│   ├── templates/
│   │   ├── deployment-blue.yaml
│   │   ├── deployment-green.yaml
│   │   ├── service-blue.yaml
│   │   ├── service-green.yaml
│   │   └── ingress.yaml
│   └── values.yaml
├── envs/
│   ├── staging.yaml                    ~/.kp/envs/staging.yaml 模板
│   └── prod.yaml                       ~/.kp/envs/prod.yaml 模板
├── resources-staging.yaml              staging 的 traffic 配置
├── resources-prod.yaml                 prod 初始（无 traffic 字段）
├── README.md                            完整 demo 教程
└── scripts/
    ├── setup.sh                         创建两个 namespace + 写 KPEnv
    ├── deploy-staging.sh                kp deploy --env staging
    ├── wait-stable.sh                   sleep 5min + verify ConfigMap
    ├── deploy-prod.sh                   kp deploy --env prod --from-env staging
    └── cleanup.sh

完整流程：
  make setup            # 创建 staging-app + prod-app namespace
  make deploy-staging   # 部署 staging（含 traffic 字段）
  make wait-stable      # 等 5min + 验证 verified-traffic ConfigMap 产出
  make deploy-prod      # 部署 prod，用 --from-env staging
  make verify           # 检查 prod ingress backend 跟 staging 一致
  make cleanup
```

### 9.3 v2.6.1 测试矩阵边界（Q3 校准点 3）

**已验证（v2.6.1 release 范围）**：

```
✓ 单集群多 namespace（staging-app / prod-app 同一 K8s cluster）
  example-blue-green-multi-env demo 工程
  
✓ verifiedTrafficWriter 5min 稳态判定
  单测 TestRunningSinceFromHistory_*

✓ --from-env 读取 + 注入 + audit log
  单测 TestInjectVerifiedTraffic_*

✓ Q7/Q8/Q11 三种 fail-fast
  单测 + demo 工程的 negative 场景
```

**未在 v2.6.1 验证（诚实标注）**：

```
✗ 真实多集群环境的端到端验证
  staging 和 prod 不同 K8s cluster + 不同 kubeconfig + 不同 RBAC
  KPEnv 机制天然支持多集群（kubeconfig 字段）
  代码路径上无差异
  但未在 v2.6.1 demo / 测试中跑过
  → v2.7+ 候选：真实多集群 e2e（HANDOFF 3.6 节"真实集群验证不可跳"）

✗ 跨 cloud / 跨 region 验证
  需要真实云厂商 K8s 集群
  v2.6.1 范围内不覆盖

✗ 大规模验证（>100 namespace 同时跑 verifiedTrafficWriter）
  跟 v2.5.1 性能立方体一样受限于测试环境
  受阻于 8 GiB orbstack 限制（v2.5.1 同前置依赖）
```

---

## 十、v2.6.1 vs 未来版本边界

```
v2.6.1（本设计范围）：
  ✓ verifiedTrafficWriter（controller 端）
  ✓ state.RunningSince helper
  ✓ --from-env flag（CLI 端）
  ✓ fail-fast Q7/Q8/Q11
  ✓ 单集群多 ns demo + 测试
  ✓ 多集群代码路径预留（KPEnv 已天然支持）

v2.6.2 / v2.7.x（持续改进）：
  [ ] 真实多集群 e2e 验证（在云上跑）
  [ ] verifiedTrafficWriter Prometheus 指标暴露（接入 v2.7 metrics 框架）
      kubepivot_verified_traffic_writes_total{ns,result}
      kubepivot_verified_traffic_age_seconds{ns}
  [ ] ConfigMap 删除时的清理（项目 cleanup 时顺带清 verified-traffic）

v2.8+（远期）：
  [ ] 跨集群 RBAC 治理（kpcontroller 跨集群信任链）
  [ ] verified-traffic 的版本签名（防止中间人篡改 ConfigMap）
  [ ] verified-traffic 的过期机制（如 24h 没更新就标记 stale）
```

---

## 十一、实施分解（Step 1-5）

依赖链：Step 2 → Step 1 → Step 3 → Step 4 → Step 5

### 11.1 Step 1：state.RunningSince helper（前置依赖）

```
内容: internal/state/state.go +1 函数 + 测试
工作量: ~半天
代码: +~30 行 / 测试 +~50 行 / 7 cases
依赖: 无
风险: 低（纯逻辑函数 + 已有完整 History 结构）
```

### 11.2 Step 2：verifiedTrafficWriter（controller 端）

```
内容: internal/controller/verified_traffic_writer.go (新增)
       internal/controller/global.go (+~10 行接入)
       函数变量注入 mock（与 v2.7 同模式）
工作量: ~1.5 天
代码: +~250 行 / 测试 +~150 行 / 10 cases
依赖: Step 1（RunningSince）/ v2.5 sharding（ShardSet）/ v2.6 route 包
风险: 中（涉及 controller 主循环 + K8s API 写）
```

### 11.3 Step 3：CLI --from-env flag

```
内容: cmd/kp/deploy.go (~+80 行 + flag + injectVerifiedTraffic)
       cmd/kp/deploy_from_env_test.go (新增)
工作量: ~半天
代码: +~120 行 / 测试 +~80 行 / 8 cases
依赖: Step 2（写入侧已 ready，否则 demo 跑不通）
风险: 低（接入位置清晰 + KPEnv 机制已有）
```

### 11.4 Step 4：example-blue-green-multi-env demo 工程

```
内容: docs/example-blue-green-multi-env/ (~13 文件)
       Makefile / 共用 chart / 双 namespace 配置 / 5 个 shell 脚本
       README.md 完整教程
工作量: ~半天
依赖: Step 3（端到端流程已 ready）
风险: 低（参考 v2.6.0 example-blue-green）
```

### 11.5 Step 5：文档收尾

```
内容: 
  docs/design/traffic-multi-env-impl-draft.md → traffic-multi-env.md
    (实施完成后去 -impl-draft 后缀，演化为实施记录)
  docs/design/traffic-multi-env-impl-notes.md (新增 Day 1-N 实施日志)
  docs/design/traffic-layer.md (链接补 v2.6.1 章节)
  README.md (v2.6.1 用法示例)
工作量: ~半天
依赖: Step 1-4 全部完成
```

### 11.6 总工作量

```
合计 ~3 天（含测试 + 文档）
跟 TODO.md 原估算对得上
```

---

## 十二、风险与对策

### 12.1 风险 R1：传播链级联不一致

```
场景:
  staging 在 t=0 部署 v1.0
  staging 在 t=10min 写 verified-traffic（v1.0 配置）
  prod 在 t=11min --from-env staging（拿到 v1.0 配置）
  staging 在 t=15min 部署 v2.0
  staging 在 t=25min 写 verified-traffic（v2.0 配置覆盖）
  此时 prod 已经在跑 v1.0 配置，跟 staging 当前不一致

对策:
  v2.6.1 不试图保证全局一致性（这是 GitOps 同步的事，不是 KubePivot 范围）
  source.yaml 记录 verifiedAt 时间戳
  用户责任：自己判断"传播窗口"是否合理
  
  v2.7+ 可加 staleness 检测（verifiedAt 距 now > N 分钟则警告）
```

### 12.2 风险 R2：ConfigMap 被手动删除

```
场景:
  staging cluster 的 verified-traffic ConfigMap 被运维误删

对策:
  下次 verifiedTrafficWriter 扫到时自动重新创建（5min 后）
  期间 prod --from-env staging 会触发 Q7 fail-fast（actionable hint）
  自动愈合，不需要人工干预
```

### 12.3 风险 R3：staging controller 版本错配（Q11）

```
场景:
  staging controller 还在 v2.5.0
  prod kp 升级到 v2.6.1
  用户 kp deploy --env prod --from-env staging
  → Q7 fail-fast，但用户不知道根本原因

对策:
  错误信息 actionable hint（§8.1）
  用户运行 kubectl get pod -l app=kubepivot-controller 检查 image
  
  v2.7+ 候选：ConfigMap.annotations 加 kp-version，读取侧版本兼容检查
```

### 12.4 风险 R4：5min 阈值争议

```
场景:
  用户想立即拷贝（不等 5min）
  用户希望延长稳态判定（更保守）

对策:
  v2.6.1 不暴露 5min 阈值给用户
  保持简单：单一阈值 + 透明行为
  
  如有强需求 v2.7+ 加可配置（但牺牲简单性）
```

### 12.5 风险 R5：单集群多 ns 测试不能完全覆盖多集群场景

```
场景:
  v2.6.1 demo 在单集群跑通
  用户在真实多集群环境部署遇到 RBAC / 网络隔离问题

对策:
  HANDOFF 3.6 节"真实集群验证不可跳"原则适用
  v2.6.1 文档明确标注"未在多集群环境验证"
  代码路径上 KPEnv 已天然支持多集群
  v2.7+ 候选：在云上跑真实多集群 e2e
```

---

## 十三、16 个设计 Q 拍板表

### 13.1 原始 8 个 Q（独立拍板）

| Q | 决定 | 理由 |
|---|------|------|
| Q1 | ConfigMap + label `kubepivot.io/verified-traffic="true"` | 不引入新 CRD（运维负担） |
| Q2 | RUNNING + 5min 稳态后写 | 平衡延迟 vs 短暂 RUNNING 误写 |
| Q3 | 直连 staging cluster，复用 KPEnv 机制 | KubePivot 已有完整 env 抽象 |
| Q4 | v2.6.1 单集群多 ns + 多集群路径预留 | KPEnv 天然多集群，但测试矩阵限定 |
| Q5 | prod 已有 traffic 直接覆盖 | `--from-env` 即显式覆盖意图 |
| Q6 | prod 实际状态不一致走正常蓝绿切换 | 复用 v2.6.0 COMMITTING Step 2 |
| Q7 | staging 没 verified-traffic fail-fast | 不做猜测式 fallback |
| Q8 | staging cluster 不可达 fail-fast | 同上 |

### 13.2 cross-check 校准（3 项）

| Q | 决定 | 理由 |
|---|------|------|
| Q1.1 | ConfigMap 一 ns 一 cm | ns 是 KubePivot 硬边界 |
| Q2.1 | state schema 不改，从 History 推算 | 利用既有 Transition.Timestamp |
| Q6.1 | 不动 git + audit log | 传播 ≠ GitOps 同步 |

### 13.3 实施期补（3 项）

| Q | 决定 | 理由 |
|---|------|------|
| Q9 | 进 LOCKED 之前读 staging | fail-fast 落在状态机外 |
| Q10 | 写入由 controller 周期扫描做 | 长期稳态判定 + 跨重启稳定 |
| Q11 | staging controller 版本错配 fail-fast + actionable hint | 不探测 image tag（脆弱） |

### 13.4 最后一档校准（4 项）

| 校准点 | 决定 | 理由 |
|-------|------|------|
| 1: RunningSince 语义 | 最近一次 →RUNNING（不排除回滚）| writer 写实际状态，逻辑自洽 |
| 2: 是否更新判定 | 对比 K8s 实际 vs ConfigMap data | 避免每分钟无谓 update |
| 3: 测试矩阵边界 | 单集群多 ns + 多集群"未验证"诚实标注 | 不装作"已支持多集群" |
| 4: ConfigMap data schema | traffic.yaml + source.yaml 双文件 | 业务/元信息独立演化 |

---

## 十四、与既有原则的一致性核对

```
✓ 不引入 client-go
   verifiedTrafficWriter 用 executor.GetExecutor().Kubectl
   --from-env 直接 kubectl get cm
   
✓ 单二进制
   verified_traffic_writer.go 编译进 controller binary
   --from-env 编译进 kp 主二进制

✓ 只保护，不越权
   ConfigMap label kubepivot.io/verified-traffic="true" 标记 KubePivot 管这个 cm
   不越权管理用户自己加的 ConfigMap

✓ 自愈不是越权
   ConfigMap 被手动删除 → 下次扫描自动重建
   不主动告警用户"你删了我的 cm"

✓ 简单 > 完美
   单一 5min 阈值（不可配置）
   单一 ConfigMap（不分版本/不签名/不加密）
   不引入新基础设施

✓ 双保险
   ConfigMap 缺失 → fail-fast，不 fallback git
   保证"显式意图 = 显式行为"
   v2.6.1 不试图"自动补救"用户的配置缺失

✓ 数据驱动决策（v2.7 教训应用）
   verifiedTrafficWriter 的写入逻辑基于 K8s 实际状态
   不基于"声明的意图"（resources.yaml）
   K8s 是真相

✓ 工程纪律 > 数字仪式感（v2.7 教训应用）
   v2.6.1 不为追求"延续传播链级联"故事强行扩展范围
   v2.6.1 范围严格限定：单集群多 ns + 多集群路径预留
   真实多集群留 v2.7+
```

---

## 十五、未做事项（v2.6.1 范围外）

```
[ ] verifiedTrafficWriter Prometheus 指标暴露
    需要 v2.7 informer pool RegisterMetrics 接入完成（v2.7.1 候选）
    暂留：日志（slog）已足够 v2.6.1 范围

[ ] ConfigMap 删除时的项目 cleanup
    KubePivot 项目 cleanup 流程当前不清理 verified-traffic
    K8s namespace 删除时会自动清理（兜底机制）
    v2.6.2 候选：项目 cleanup 显式清理

[ ] verified-traffic 版本签名
    防止中间人篡改 ConfigMap
    v2.8 Enterprise Governance 范围（与 cosign 镜像签名一起）

[ ] verified-traffic 过期机制
    verifiedAt 距 now > N 分钟 → 标记 stale
    读取侧给 staleness warning
    v2.7+ 候选

[ ] 跨集群 RBAC 治理
    kpcontroller 跨集群信任链
    v2.8+ 范围

[ ] verifiedTrafficWriter 大规模性能验证
    >100 namespace 同时跑
    受阻于 v2.5.1 测试环境升级前置依赖

[ ] kp deploy --from-env 与 git 同步选项
    --from-env --sync-to-git 选项（写回 git 让 ArgoCD/Flux 接管）
    v2.7+ 候选（如真有需求）
```

---

## 相关文档

- [traffic-layer.md](traffic-layer.md) — v2.6.0 流量层基础（Provider / Sandbox 协同）
- [traffic-layer-draft.md](traffic-layer-draft.md) — v2.6.0 设计草案（含 §7 多环境传播方向稿）
- [state-machine.md](state-machine.md) — 状态机（v2.6.1 不引入新状态）
- [sharding.md](sharding.md) — v2.5.0 分片（verifiedTrafficWriter 复用 ShardSet）
- [eventstream-draft.md](eventstream-draft.md) — v2.7.0 informer 框架（v2.7+ metrics 接入参考）

---

## 编辑记录

```
2026-04-28  实施草案创建（qc + Claude）
            16 个设计 Q 全部拍板（8 原始 + 3 cross-check + 3 实施期补 + 4 最后校准）
            起手 Step 1: state.RunningSince helper
            预计 v2.6.1 实施完成: 2026-04-29 / 30
            实施完成后演化为 traffic-multi-env.md（去 -impl-draft 后缀）+
            traffic-multi-env-impl-notes.md（实施日志）
```
