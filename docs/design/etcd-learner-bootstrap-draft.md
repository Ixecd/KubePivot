# KubePivot 内嵌 etcd Learner 集群 — 设计草案

> 状态：📝 draft — 待 qc 拍板
> 关联：[sharding](../../internal/sharding/shard.go) / [controller_installer](../../internal/controller_installer/installer.go) / [state](../../internal/state/)
> 背景：v3.0 KubePivot Controller 依赖外部 etcd。v3.1 目标：零外部依赖闭环，
>       利用 etcd Learner 机制实现与 controller 同生命周期的自举集群

---

## 一、目标

`kp controller install` 之后，用户不需要部署任何外部 etcd。每个 controller Pod 内跑一个 etcd 实例（Learner → Voter），集群随 controller 规模自动伸缩。

核心理念：

- **计算与存储 1:1 对等**：Controller Pods (C) == etcd Nodes (N)。扩缩容同步发生
- **Learner 零风险加入**：新节点以 Learner 身份加入，追平数据后才 promote，不拖慢 Quorum
- **自举确定性**：StatefulSet 有序启动 + K8s API 动态 Peer 发现，不依赖硬编码 DNS
- **数据持久化**：PV 挂载 `/data/etcd`，Pod 漂移/重启不丢数据，Learner 增量同步而非全量快照

---

## 二、架构总览

```
┌───────────────── Pod: controller-0 ─────────────────┐
│  ┌─ Container: kp-controller ──────────────────────┐ │
│  │  Phase 0: etcdmanager.Bootstrap()               │ │
│  │    ├─ 检测已有集群 → existing join              │ │
│  │    └─ 全冷启动 → new cluster                   │ │
│  │  Phase 1: 启动 Reconciliation Loop              │ │
│  │  Phase 2: etcdmanager.Run() — compact/defrag    │ │
│  └─────────────────────────────────────────────────┘ │
│  ┌─ Container: etcd ───────────────────────────────┐ │
│  │  localhost:2379,  /data/etcd (PV)               │ │
│  └─────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────┘
  Pod-1: Learner → Follower   Pod-2: Learner → Follower
```

- **sidecar 模式**：kp binary 保持 ~30MB，etcd 作为独立容器
- **内部通信**：所有 Pod 内 `localhost:2379`，Pod 间通过 StatefulSet DNS + K8s API peer discovery
- **Shadow CRD**：只读状态投影 `KubePivotStatus`，用户可 `kubectl get` 看拓扑但不通过 YAML 改

---

## 三、Layer 1: Bootstrap 自举（`internal/etcdmanager/bootstrap.go`）

### 3.1 启动决策树

```
Pod 启动
  │
  ├─ 1. 发现 Peer 节点（K8s API: kubectl get pods -l app=kubepivot-controller）
  │     输出: peerList = [{name, ip, phase}, ...]
  │
  ├─ 2. 尝试连接已有 etcd 集群
  │     for each peer in peerList:
  │       if peer:2379 可达 and etcdctl endpoint health → OK
  │
  ├─ 3. 分支决策:
  │
  │  ┌─ 已有集群可达 ─────────────────────────────────┐
  │  │  a. 检查自己是否已是 member                    │
  │  │     etcdctl member list | grep $HOSTNAME        │
  │  │     是 → 直接 join（重启恢复）→ Phase 1        │
  │  │     否 → 以 Learner 加入                       │
  │  │  b. etcdctl member add $HOSTNAME --learner \   │
  │  │        --peer-urls=http://$POD_IP:2380         │
  │  │  c. 追赶心跳循环:                              │
  │  │     while true:                                │
  │  │       lag = Leader_Raft_Index - Learner_Raft_Index│
  │  │       if lag < 500 && stable_for 10s: break    │
  │  │       sleep 2s                                 │
  │  │  d. etcdctl member promote $HOSTNAME            │
  │  │  e. → Phase 1                                  │
  │  └────────────────────────────────────────────────┘
  │
  │  ┌─ 无集群可达 ──────────────────────────────────┐
  │  │  a. 检查 /data/etcd/member/snap/db 是否存在    │
  │  │     存在 → 自己是旧集群残留，尝试单节点恢复     │
  │  │  b. 验证: 是否所有 peer 的 /data/etcd 都为空？ │
  │  │     是 → 全冷启动，--initial-cluster-state=new │
  │  │     否 → 等待其他节点先起来（最多 300s）       │
  │  │  c. etcd --initial-cluster={{.Name}}-0=http://...│
  │  │  d. → Phase 1                                  │
  │  └────────────────────────────────────────────────┘
```

**"假 Pod-0"陷阱防御**：StatefulSet 滚动更新时 Pod-0 重启，Pod-1/Pod-2 仍在运行。Pod-0 不能 `initial-cluster-state=new`。决策点 3 的"已有集群可达"分支先于"无集群可达"分支，自然防御了这个问题——Pod-0 重启后发现 Pod-1:2379 可达，走 Learner 重新加入而非新建集群。

### 3.2 Learner 晋升安全阈值

```go
const (
    maxLagForPromotion = 500     // Raft Index 落后上限
    stableWindow       = 10 * time.Second // 滞后稳定窗口
)

func waitForCatchUp(ctx context.Context, leaderEP string) error {
    var belowThresholdSince time.Time
    ticker := time.NewTicker(2 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-ticker.C:
            leaderIdx := getRaftIndex(leaderEP)
            myIdx := getRaftIndex("localhost:2379")
            lag := leaderIdx - myIdx

            if lag < maxLagForPromotion {
                if belowThresholdSince.IsZero() {
                    belowThresholdSince = time.Now()
                } else if time.Since(belowThresholdSince) >= stableWindow {
                    return nil // 安全晋升
                }
            } else {
                belowThresholdSince = time.Time{} // 重置，重新累计
            }
        }
    }
}
```

> 如果 Leader_Raft_Index - Learner_Raft_Index < 500 且持续 10 秒稳定，才触发 promote。防止 Learner 数据落后太多导致 promote 瞬间卡住整个集群的提交。

### 3.3 Peer Discovery：K8s API（非硬编码 DNS）

```go
func discoverPeers(ctx context.Context, namespace string) ([]Peer, error) {
    // kubectl get pods -n <ns> -l app=kubepivot-controller
    //   -o jsonpath={.items[*].metadata.name},{.items[*].status.podIP},{.items[*].status.phase}
    exec := executor.GetExecutor()
    out, err := exec.Kubectl(ctx, "",
        "get", "pods", "-n", namespace,
        "-l", "app=kubepivot-controller",
        "-o", "jsonpath={range .items[*]}{.metadata.name},{.status.podIP},{.status.phase} {end}",
    )
    if err != nil {
        return nil, err
    }
    // 解析: "controller-0,10.1.2.3,Running controller-1,10.1.2.4,Running"
    return parsePeers(string(out)), nil
}
```

> 不硬编码 DNS。用户改 Namespace、改 Service name 都不影响自举逻辑。K8s API 的 `app=kubepivot-controller` label 是确定性锚点。

---

## 四、Layer 2: 运行时自治（`internal/etcdmanager/manager.go`）

### 4.1 轮询 Defrag

```go
type EtcdManager struct {
    podIndex    int    // 当前 Pod 在 StatefulSet 中的序号
    totalPods   int    // controller 副本数
    compactHour int    // 默认 1h
}

func (m *EtcdManager) scheduleDefrag() {
    // 按 Pod 序号均匀分布 Defrag 时间窗
    // Pod-0: 02:00 UTC, Pod-1: 04:00 UTC, Pod-2: 06:00 UTC
    // 避开全集群同时进入阻塞状态
    hour := (2 + m.podIndex*(24/m.totalPods)) % 24
    // 在 hour:00:00 ~ hour:59:59 窗口内的随机时刻执行
}

func (m *EtcdManager) defrag(ctx context.Context) error {
    // 1. 检查自己是否是 Leader——Leader 不做 Defrag（风险更大）
    if isLeader("localhost:2379") {
        return nil // skip
    }
    // 2. Compact 先
    etcdctl("compact", rev)
    // 3. Defrag（阻塞本节点 ~1-3 分钟，不影响其他节点）
    etcdctl("defrag")
}
```

> `Compact` 标记删除，`Defrag` 释放磁盘。轮询 Defrag 确保任何时候只有一个 etcd 节点在阻塞整理碎片，其他两个服务正常。

### 4.2 优雅退出

```go
func (m *EtcdManager) GracefulShutdown(ctx context.Context) error {
    // 判断退出原因：缩容 vs 滚动更新
    if isScaleDown() {
        // 缩容：必须主动 remove member，否则 Quorum 判断失效
        return etcdctl("member", "remove", m.memberID)
    }
    // 滚动更新（RollingUpdate）：不移除 member
    // Pod 会以相同 hostname 重新启动，直接 join 回集群
    return nil // 不操作
}
```

**preStop hook**（在 Deployment/StatefulSet template 中）：

```yaml
lifecycle:
  preStop:
    exec:
      command: ["/usr/local/bin/kp", "etcd", "cleanup-member"]
```

`kp etcd cleanup-member` 内部调 `GracefulShutdown`。StatefulSet 的 RollingUpdate 不走 member remove（否则每次更新都触发全量快照同步），只有 `replicas` 缩容时才移除。

### 4.3 自动 Compact

```go
func (m *EtcdManager) periodicCompact(ctx context.Context) {
    ticker := time.NewTicker(time.Duration(m.compactHour) * time.Hour)
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            rev := currentRevision("localhost:2379")
            etcdctl("compact", strconv.FormatInt(rev, 10))
        }
    }
}
```

---

## 五、Layer 3: Shadow CRD — 只读状态投影

### 5.1 CRD schema

```yaml
apiVersion: kubepivot.io/v1
kind: KubePivotStatus
metadata:
  name: kubepivot
spec: {}   # 空——用户不通过这里改任何东西
status:
  cluster:
    phase: Running          # Running | Degrading | Resharding | Bootstrapping
    activeShards: 50
    managedProjects: 180
    observedGeneration: 3   # controller deployment generation
  etcd:
    leader: controller-0.kubepivot-system
    members:
      - name: controller-0
        id: 8e9e05c52164694d
        role: Leader
        learner: false
        clientURL: "http://controller-0.kubepivot-system:2379"
        peerURL:   "http://controller-0.kubepivot-system:2380"
        dbSize: 128MiB
        raftIndex: 1503928
        inSync: true
      - name: controller-1
        id: 91bc3c2fb4c3b979
        role: Follower
        learner: false
        clientURL: "http://controller-1.kubepivot-system:2379"
        peerURL:   "http://controller-1.kubepivot-system:2380"
        dbSize: 128MiB
        raftIndex: 1503927
        inSync: true
      - name: controller-2
        id: fd422379fda50e48
        role: Learner
        learner: true
        clientURL: "http://controller-2.kubepivot-system:2379"
        peerURL:   "http://controller-2.kubepivot-system:2380"
        dbSize: 86MiB
        raftIndex: 1503620
        inSync: false
        lag: 308
    maintenance:
      lastCompact: "2026-05-01T15:00:00Z"
      lastDefrag:  "2026-05-01T02:00:00Z"
      dbSizeUsage: 45%     # 当前 DB size / quota，预警空间爆炸
  controlPlane:
    leaderPod: controller-1
    reconcileLatency: 12ms
    lastResync: "2026-05-01T15:30:00Z"
```

### 5.2 写入时机

- 每 30s：Controller 从 `etcdmanager` 读当前状态 → 写入 `KubePivotStatus.status`
- Phase 变更时（Bootstrapping → Running / Degrading）：立即写入
- 不创建/不修改 `spec`——只写 `status`

### 5.3 为什么不用 CRD 驱动行为

CRD 管理自己依赖的 etcd 存在**自举悖论**：

```
kp-controller 启动
  → 需要 etcd 来存储状态（Lease / 分片 / 判决）
  → 但 etcd 还没启动
  → 需要一个 EtcdCluster CRD 来声明 etcd 拓扑
  → CRD 需要 Controller 来 reconcile
  → Controller 还没启动（在等 etcd）
  → 死锁 💀
```

因此 `KubePivotStatus` 是**纯状态投影**——Controller 向它写入当前状态，但从不读取它的 `spec`（因为根本没有 spec）。etcd 生命周期由 `internal/etcdmanager` 包确定性管理，不依赖任何 CRD Controller。

---

## 六、存储：PV vs EmptyDir

| 场景 | EmptyDir | PV |
|---|---|---|
| Pod 重启（同节点） | 数据保留 ✓ | 数据保留 ✓ |
| Pod 漂移（不同节点） | **数据丢失** ✗ | 数据保留 ✓ |
| Learner 恢复方式 | 全量 Snapshot Transfer (~分钟) | 增量日志追平 (~秒) |
| 运维体验 | 漂移后抓狂 | 漂移后无感 |

**决策**：PV（PersistentVolume）。

- StatefulSet `volumeClaimTemplates` 为每个 Pod 创建独立的 PVC
- etcd 数据路径 `/data/etcd` 挂载 PVC
- Pod 重启/漂移时 Learner 通过 Raft Log 增量追平，不需要全量快照
- 如果 PVC 满了（默认 8GiB），compact + defrag 自动回收

**PV 的风险**：如果 PV 后端出问题（比如 NFS 卡顿），etcd 写入延迟会飙高。建议默认用 `hostPath` 或 `local-storage` StorageClass——KubePivot Controller 本身不要求跨节点高可用存储，单节点数据持久化即可。

---

## 七、部署模板变更

### 7.1 Deployment → StatefulSet

当前 `controller_installer/templates/deployment.yaml` 是 Deployment。Learner 集群需要稳定的 Pod 标识（hostname 不变），因此改为 StatefulSet。

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: kubepivot-controller
  namespace: kubepivot-system
spec:
  serviceName: kubepivot-controller
  replicas: 3
  podManagementPolicy: OrderedReady  # Pod-0 先启动，自举集群
  template:
    spec:
      containers:
        - name: kp-controller
          image: __IMAGE__
          command: ["/usr/local/bin/kp"]
          args: ["controller", "start", "--global"]
          env:
            - name: ETCD_ENDPOINTS
              value: "localhost:2379"     # 不再需要外部 etcd
            - name: POD_NAME
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name
            - name: POD_NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
          lifecycle:
            preStop:
              exec:
                command:
                  - "/usr/local/bin/kp"
                  - "etcd"
                  - "cleanup-member"
                  - "--pod-name=$(POD_NAME)"
        - name: etcd
          image: quay.io/coreos/etcd:v3.5.18
          command:
            - etcd
          args:
            - --name=$(POD_NAME)
            - --data-dir=/data/etcd
            - --listen-client-urls=http://0.0.0.0:2379
            - --advertise-client-urls=http://$(POD_NAME).kubepivot-controller:2379
            - --listen-peer-urls=http://0.0.0.0:2380
            - --initial-advertise-peer-urls=http://$(POD_NAME).kubepivot-controller:2380
          volumeMounts:
            - name: data
              mountPath: /data
  volumeClaimTemplates:
    - metadata:
        name: data
      spec:
        accessModes: [ "ReadWriteOnce" ]
        resources:
          requests:
            storage: 8Gi
```

**关键变更**：

1. `podManagementPolicy: OrderedReady` — Pod-0 先启动完成，Pod-1 才能启动。保证自举确定性
2. `volumeClaimTemplates` — 每个 Pod 独立 PVC
3. `$(POD_NAME).kubepivot-controller` — StatefulSet headless service DNS，Peer discovery 的备用方案
4. `preStop` hook — 调 `kp etcd cleanup-member` 做优雅退出

---

## 八、`kp etcd` 子命令

```
kp etcd status           # 集群健康度（等同 kubectl get kpivotstatus -o yaml）
kp etcd compact          # 手动 compact
kp etcd defrag           # 手动 defrag（对 Leader 有提示）
kp etcd snapshot save    # 手动快照到文件
kp etcd cleanup-member   # preStop hook 调用，判断缩容 vs 滚动更新
```

---

## 九、测试计划

| 测试 | 覆盖 |
|---|---|
| `TestBootstrap_ColdStart` | 全冷启动：Pod-0 自举集群，Pod-1/2 Learner 加入 → promote |
| `TestBootstrap_Pod0Restart` | Pod-0 重启（滚动更新场景），已有集群存活，Pod-0 以 existing join |
| `TestBootstrap_AllRestart` | 全集群关机后重启，数据从 PV 恢复 |
| `TestBootstrap_LearnerCatchup` | Learner 数据落后 >500 → 持久追赶 → 稳定 10s → promote |
| `TestBootstrap_LearnerTimeout` | Leader 不可达 → 超过 300s Learner 加入失败 → 报错 |
| `TestEtcdManager_RoundRobinDefrag` | 3 节点轮询 Defrag，验证同一时间只有一个节点在 Defrag |
| `TestEtcdManager_GracefulScaleDown` | 缩容 → preStop → member remove → Quorum 正常 |
| `TestEtcdManager_GracefulRollingUpdate` | 滚动更新 → preStop → 不移除 member → Pod 直接 join 回 |
| `TestShadowCRD_PhaseTransition` | Bootstrap → Running → 写 KubePivotStatus |
| `TestShadowCRD_Degraded` | 一个 etcd 节点不可达 → phase=Degrading |
| `TestPeerDiscovery_DifferentNamespace` | 非默认 namespace，K8s API 依然发现 peer |

---

## 十、风险与约束

1. **StatefulSet 启动顺序**：`OrderedReady` 意味着 Pod-0 必须先 Ready 才能启动 Pod-1。冷启动时间 = Pod-0 启动 + etcd 自举 + Pod-1 Learner 追赶 + promote + ... 大约 30-60 秒起步，在预期范围内。
2. **PV 后端性能**：etcd 对磁盘延迟敏感。推荐 `local-storage` 或 `hostPath` 而非 NFS。如果 PV 后端是网络存储（Ceph/Gluster），etcd 写入延迟可能飙到 100ms+，触发 Leader 选举超时。
3. **Learner 追数据耗时**：如果 PV 数据量 >2GiB 且 Learner 从零开始，追赶可能需要数十分钟。在 `Phase 0` 中提示用户"正在同步数据（预计 N 分钟）"。
4. **Shadow CRD 安装**：`KubePivotStatus` CRD 在 `kp controller install` 时自动 apply，不需要用户手动安装。
5. **二进制体积**：kp binary 保持不变（~30MB），etcd 作为独立 sidecar 容器。

---

## 十一、实施步骤

| Step | 内容 | 估计 |
|---|---|---|
| Step 1 | `internal/etcdmanager/` 包骨架 + `bootstrap.go` | ~1.5 天 |
| Step 2 | `manager.go` compact/defrag + 优雅退出 | ~1 天 |
| Step 3 | Shadow CRD `KubePivotStatus` + 写入逻辑 | ~0.5 天 |
| Step 4 | 部署模板改造（Deployment→StatefulSet + etcd sidecar） | ~0.5 天 |
| Step 5 | `kp etcd` 子命令 | ~0.5 天 |
| Step 6 | 全量测试（11 个测试用例） | ~1 天 |

---

## 十二、编辑记录

```
2026-05-01  qc + DeepSeek 起草
    - 三层架构：Bootstrap + 运行时自治 + Shadow CRD
    - Learner 自举决策树（6 个分支）
    - 安全晋升阈值：Raft lag < 500 + 10s 稳定窗口
    - 轮询 Defrag（按 Pod 序号分布时间窗）
    - 优雅退出：区分缩容 vs 滚动更新
    - preStop hook + `kp etcd cleanup-member`
    - Peer Discovery：K8s API label 查询（非硬编码 DNS）
    - PV 持久化 vs EmptyDir 对比决策
    - StatefulSet 部署模板（volumeClaimTemplates + OrderedReady）
    - Shadow CRD KubePivotStatus 完整 schema
    - 11 个测试用例
    - "假 Pod-0" 陷阱防御
```
