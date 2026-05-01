# kp controller update — 自适应分片规模推荐 设计草案

> 状态：📝 draft — 待 qc 拍板
> 关联：[controller.md](../cmd/controller/controller.md) / [sharding](../../internal/sharding/shard.go)
> 背景：v3.0 全系统审计中发现 controller 副本数和分片数手工配置缺乏依据，
>       应根据"项目数 → 分片数 → controller 数"三维关系自动推导最优值

---

## 一、目标

`kp controller update` 读取集群当前状态（managed namespaces 数量、controller deployment replicas、KUBEPIVOT_SHARDS env），基于三维关系推导最优 (shards, replicas) 组合，展示 diff，用户确认后 apply。

三条铁律：

1. **只建议，不擅自改**。默认 dry-run 模式，显示当前值 → 推荐值 → 理由。`--apply` 才写。
2. **不降配**。当前 replicas 比推荐值高时，推荐值取其 max（避免"建议缩容"引发的运维恐慌）。
3. **安全下限**。HA 最低 C≥2、S≥3。

---

## 二、三维关系模型

```
P = 被管理的项目数（managed namespace count）
S = 分片数（KUBEPIVOT_SHARDS）
C = controller 副本数（deployment.spec.replicas）

约束：
  每分片承担项目数 = P/S，甜区 [2, 8]
  每 controller 持分片数 = ceil(S/C)，甜区 [2, 5]
  HA 最低: C≥2, S≥3
  实际上限: C≤10, S≤50（lease 开销）

推导公式：
  S = clamp(ceil(P / 4), min=3, max=50)
  C = clamp(ceil(S / 3), min=2, max=10)
```

### 2.1 举例

| 项目数 P | 分片数 S | 副本数 C | 每分片项目 | 每 pod 分片 |
|---|---|---|---|---|
| 3 | 3 | 2 | 1.0 | 1.5 |
| 10 | 3 | 2 | 3.3 | 1.5 |
| 20 | 5 | 2 | 4.0 | 2.5 |
| 50 | 13 | 5 | 3.8 | 2.6 |
| 100 | 25 | 9 | 4.0 | 2.8 |
| 200 | 50 | 10 | 4.0 | 5.0 |
| 500 | 50 | 10 | 10.0 ⚠️ | 5.0 |
| 1000 | 50 | 10 | 20.0 🔴 | 5.0 |

> P≥200 时触及 S=50/C=10 硬上限。P≥500 时严重超载（每分片 >10 项目），程序应警告用户考虑多集群或分层 controller。

---

## 三、CLI 设计

```
kp controller update [flags]
```

### Flag

| Flag | 默认值 | 说明 |
|---|---|---|
| `--dry-run` | `true` | 预览建议，不实际修改 |
| `--apply` | `false` | 实际应用（更新 ConfigMap + scale deployment） |
| `--shards` | (自动计算) | 手动指定分片数（跳过自动推导） |
| `--replicas` | (自动计算) | 手动指定副本数（跳过自动推导） |
| `--force-downscale` | `false` | 允许推荐值低于当前值（默认不降配） |
| `--namespace` | `kubepivot-system` | controller 部署 namespace |
| `--kubeconfig` | | kubeconfig 路径 |

`--dry-run` 和 `--apply` 互斥。

**不降配策略**：默认情况下，如果当前 replicas 已大于推荐值，保留当前值不变。这防止"用户因压测手动扩到 10 副本，kp 自动缩回 3"引发的运维事故。只有显式传 `--force-downscale` 时才允许推荐值低于当前值。

**`--force-downscale` 仅影响 replicas**：分片数（shards）不受不降配策略约束——分片减少降低 etcd Lease 开销，是纯粹的优化，不会引起服务中断。

### 输出格式（dry-run）

```
$ kp controller update

📊 KubePivot Controller 规模分析

  当前状态:
    Managed Projects:  18
    Shards:            10
    Controller Pods:   3
    Projects/Shard:    1.8
    Shards/Pod (quota): 4

  推荐配置:
    Shards:            10 → 5
    Controller Pods:   3  → 2
    Projects/Shard:    1.8 → 3.6
    Shards/Pod (quota): 4 → 3

  理由: P=18, 推荐 S=ceil(18/4)=5, C=ceil(5/3)=2
  当前 10 分片对 18 个项目过于分散（每分片仅 1.8 项目），
  缩减到 5 分片可减少 lease 开销且不影响分布均匀性。

💡 运行 kp controller update --apply 应用此推荐
```

### 实际变更（--apply）

```
$ kp controller update --apply

📊 应用推荐配置...
  ✓ ConfigMap kubepivot-controller-config 已更新 (shards: 10 → 5)
  ✓ Deployment kubepivot-controller 已 scale (replicas: 3 → 2)
  ✓ 等待新 Pod 就绪...
  ✅ Controller 规模调整完成
```

**变更内容**：
1. 更新 ConfigMap `kubepivot-controller-config` 的 `data.shards` 字段
2. `kubectl scale deployment/kubepivot-controller --replicas=N`

**不重启已有 Pod**——scale 操作由 K8s 渐进收/扩。分片变更在下一次 lease 扫描周期（~15s）生效。

---

## 四、实现方案

### 4.1 数据采集

```go
func (i *Installer) GetSizingInfo(ctx context.Context) (*SizingInfo, error) {
    info := &SizingInfo{}

    // 1. 读取当前 replicas
    out, _ := exec.Kubectl(ctx, kubeconfig,
        "get", "deployment", "kubepivot-controller",
        "-n", ns, "-o", "jsonpath={.spec.replicas}")
    info.CurrentReplicas, _ = strconv.Atoi(strings.TrimSpace(string(out)))

    // 2. 读取当前 shards：ConfigMap 优先，其次查 Deployment env 覆盖
    out, _ = exec.Kubectl(ctx, kubeconfig,
        "get", "configmap", "kubepivot-controller-config",
        "-n", ns, "-o", "jsonpath={.data.shards}", "--ignore-not-found")
    cmShards := strings.TrimSpace(string(out))

    // 检查 Deployment 是否通过 env 直接覆盖了 shards
    out, _ = exec.Kubectl(ctx, kubeconfig,
        "get", "deployment", "kubepivot-controller",
        "-n", ns, "-o",
        `jsonpath={.spec.template.spec.containers[?(@.name=="controller")].env[?(@.name=="KUBEPIVOT_SHARDS")].value}`)
    envShards := strings.TrimSpace(string(out))

    if envShards != "" && cmShards != "" && envShards != cmShards {
        // ConfigMap 和 Deployment env 冲突 → 报错，让用户先统一
        return nil, fmt.Errorf(
            "KUBEPIVOT_SHARDS conflict: ConfigMap says %s, Deployment env says %s. "+
                "Remove the env override or update ConfigMap to match.", cmShards, envShards)
    }
    if envShards != "" {
        info.CurrentShards, _ = strconv.Atoi(envShards)
    } else if cmShards != "" {
        info.CurrentShards, _ = strconv.Atoi(cmShards)
    } else {
        info.CurrentShards = 10 // 默认值（与 MultiLeaseConfig 一致）
    }

    // 3. 统计 managed namespace 数量（仅 Active，排除 Terminating）
    out, _ = exec.Kubectl(ctx, kubeconfig,
        "get", "namespace",
        "-l", "kubepivot.io/managed=true",
        "-o", `jsonpath={.items[?(@.status.phase=="Active")].metadata.name}`)
    names := strings.Fields(strings.TrimSpace(string(out)))
    info.ProjectCount = len(names)

    return info, nil
}

type SizingInfo struct {
    ProjectCount    int // P
    CurrentShards   int // S
    CurrentReplicas int // C
}
```

### 4.2 推荐算法

```go
func Recommend(projectCount int, currentReplicas int, forceDownscale bool) (shards, replicas int, reason string) {
    // S = clamp(ceil(P/4), 3, 50)
    shards = (projectCount + 3) / 4
    if shards < 3 { shards = 3 }
    if shards > 50 { shards = 50 }

    // C = clamp(ceil(S/3), 2, 10)
    replicas = (shards + 2) / 3
    if replicas < 2 { replicas = 2 }
    if replicas > 10 { replicas = 10 }

    // 不降配策略：仅 replicas 受保护，shards 不受限制（降低分片是纯优化）
    if !forceDownscale && replicas < currentReplicas {
        reason = fmt.Sprintf(
            "replicas 推荐值 %d 低于当前值 %d，保持当前值（使用 --force-downscale 允许缩减）",
            replicas, currentReplicas)
        replicas = currentReplicas
    }

    if projectCount >= 500 {
        reason += "\n⚠️  P>=500 严重超载，建议考虑多集群或分层 controller 架构"
    } else if projectCount >= 200 {
        reason += "\n💡 P>=200 触及上限，每分片项目数可能超过甜区"
    }

    return shards, replicas, reason
}
```

### 4.3 Apply 逻辑

```go
func (i *Installer) ApplySizing(ctx context.Context, shards, replicas int) error {
    // 0. etcd 健康度预检（分片数大幅变更可能冲击 etcd Lease 数量）
    if err := checkEtcdHealth(ctx); err != nil {
        return fmt.Errorf("etcd health check failed, aborting: %w", err)
    }

    // 1. 更新 ConfigMap
    patch := fmt.Sprintf(`{"data":{"shards":"%d"}}`, shards)
    _, err := exec.Kubectl(ctx, kubeconfig,
        "patch", "configmap", "kubepivot-controller-config",
        "-n", ns, "--type=merge", "-p", patch)
    if err != nil { return fmt.Errorf("update ConfigMap: %w", err) }

    // 2. Scale Deployment
    _, err = exec.Kubectl(ctx, kubeconfig,
        "scale", "deployment", "kubepivot-controller",
        "-n", ns, fmt.Sprintf("--replicas=%d", replicas))
    if err != nil { return fmt.Errorf("scale deployment: %w", err) }

    // 3. 等待 rollout，超时与项目数正相关
    //    基础 120s，每个项目加 2s（大规模集群同步更慢）
    adaptiveTimeout := time.Duration(120+projectCount*2) * time.Second
    if adaptiveTimeout > 600*time.Second {
        adaptiveTimeout = 600 * time.Second // cap at 10min
    }
    return i.WaitReady(ctx, adaptiveTimeout)
}

// checkEtcdHealth 检查 etcd 连接和 DB size。
// 如果 etcd 不可达或 DB 过大（>2GiB），返回 error。
func checkEtcdHealth(ctx context.Context) error {
    endpoints := os.Getenv("ETCD_ENDPOINTS")
    if endpoints == "" {
        return nil // 无 etcd 配置 → 用本地 store，跳过检查
    }
    // 用 etcdctl endpoint health 或直接 HTTP GET /metrics
    // 返回 nil if healthy, error otherwise
    ...
}
```

### 4.4 代码位置

`kp controller update` 子命令放在 `cmd/kp/controller.go` 的 `runController` switch 中（与 `install/uninstall/status/enroll/projects` 平级）。推荐算法放 `internal/controller_installer/` 包。

```
cmd/kp/controller.go                         +1 case + runControllerUpdate()
internal/controller_installer/installer.go   +Recommend() + SizingInfo + GetSizingInfo() + ApplySizing()
```

---

## 五、测试计划

| 测试 | 覆盖 |
|---|---|
| `TestRecommend_P3` | P=3 → S=3, C=2 |
| `TestRecommend_P20` | P=20 → S=5, C=2 |
| `TestRecommend_P100` | P=100 → S=25, C=9 |
| `TestRecommend_P200` | P=200 → S=50, C=10 (触及上限) |
| `TestRecommend_Empty` | P=0 → S=3, C=2 (安全下限) |
| `TestRecommend_Boundary` | P=1 → S=3, C=2 (不变) |
| `TestRecommend_NoDownscale` | P=10 当前 C=10 → C=10（不降配），S=3 |
| `TestRecommend_ForceDownscale` | P=10 当前 C=10 + `--force-downscale` → C=2, S=3 |
| `TestRecommend_MaxLimit` | P=1000 → S=50, C=10 + 多集群警告 |
| `TestGetSizingInfo` | 模拟 kubectl 输出，验证解析 |
| `TestGetSizingInfo_NoConfigMap` | ConfigMap 缺失 → 优雅退到默认值 S=10 |
| `TestGetSizingInfo_EnvConflict` | ConfigMap 和 Deployment env 不一致 → 报错 |
| `TestGetSizingInfo_TerminatingNS` | Terminating namespace 被排除在 P 计数外 |
| `TestControllerUpdate_DryRun` | `kp controller update --dry-run` → 输出 diff 不修改 |
| `TestControllerUpdate_Apply` | `kp controller update --apply` → ConfigMap + replicas 更新 |
| `TestControllerUpdate_EtcdPreCheck` | etcd 不健康 → apply 被阻断 + 提示 |

---

## 六、风险与约束

1. **--apply 会 scale deployment**：直接改 replicas，K8s 渐进收/扩，不会瞬时中断。但用户应了解这是在线操作。
2. **shards 变更可能冲击 etcd**：`S` 大幅增加（如 10→50）会导致 Lease 对象激增。`--apply` 前自动检查 etcd 健康度，异常时阻断并提示。
3. **ConfigMap 与 Deployment env 冲突**：部分用户直接在 Deployment YAML 设 `KUBEPIVOT_SHARDS` env 而不走 ConfigMap。`GetSizingInfo` 检测到两者不一致时报错，要求用户先统一。
4. **不降配策略**：默认不降低 replicas。`--force-downscale` 显式允许。分片数不受此策略约束。
5. **手动指定 --shards/--replicas 时跳过算法**：直接 apply 用户指定的值，不做额外校验。用户对自己指定的值负责。
6. **Terminating namespace 不计入 P**：避免因 namespace 删除中而高估项目数。

---

## 七、编辑记录

```
2026-05-01  qc + DeepSeek 起草
    - controller 三维关系模型（P→S→C）
    - Recommend() 算法
    - kp controller update CLI 设计
    - dry-run / --apply 双模式
    - 不降配策略 + --force-downscale 显式开关
    - etcd 健康度预检 + ConfigMap/env 冲突检测
    - Terminating namespace 过滤 + ConfigMap 缺失兜底
    - 自适应 WaitReady 超时 (120s + P×2s, max 600s)
    - 14 个测试用例
```
