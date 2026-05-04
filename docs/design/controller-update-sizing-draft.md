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

### 2.2 安全熔断（P≥500 超载保护）

当 P≥500 时，即使触及 S=50/C=10 上限，单分片压力（P/S > 10）仍会超过 etcd 建议响应延迟。除警告外，算法引入硬熔断：

```
S = clamp(ceil(P / 4), 3, 50)
C = clamp(ceil(S / 3), 2, 10)

当 P/S > 10 时：
  - 输出 🔴 标记，明确告知用户当前集群已超载
  - 建议：多集群分片（每个集群承载 ≤200 项目）或分层 controller
  - 不自动突破 S=50 上限（Lease 数量有 etcd 硬开销）
```

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

  压力变化趋势:
    维度              当前值      推荐值      变化
    ─────────────────────────────────────────
    单分片承载 (P/S)     1.8         3.6        ↑
    单 Pod 承载 (S/C)    3.3         2.5        ↓
    Lease 总数           10          5          ↓ 50%
    Controller Pod 开销   3           2          ↓ 33%

  理由: P=18, 推荐 S=ceil(18/4)=5, C=ceil(5/3)=2
  当前 10 分片对 18 个项目过于分散（每分片仅 1.8 项目），
  缩减到 5 分片可减少 lease 开销且不影响分布均匀性。

💡 运行 kp controller update --apply 应用此推荐
```

**压力变化趋势表** 提供直观的维度对比，帮助用户在 dry-run 阶段做出判断。当 S 变化幅度 ≥50% 时额外输出重平衡预警（见 4.2）。

### 实际变更（--apply）

```
$ kp controller update --apply

📊 应用推荐配置...
  ✓ ConfigMap kubepivot-controller-config 已更新 (shards: 10 → 5)
  ✓ 已清理孤儿 Lease: shard-5 ~ shard-9 (5 个)
  ✓ Deployment kubepivot-controller 已 scale (replicas: 3 → 2)
  ✓ 等待新 Pod 就绪...
  ✅ Controller 规模调整完成
```

**变更内容**：
1. 权限预检（AccessReview）— 确认当前 kubeconfig 有 patch+scale 权限
2. 快照当前状态（oldShards, oldReplicas）— 用于失败回滚
3. 更新 ConfigMap `kubepivot-controller-config` 的 `data.shards` 字段
4. `kubectl scale deployment/kubepivot-controller --replicas=N`
5. 等待就绪（自适应超时）— 失败则回滚 ConfigMap+Replicas 到旧值
6. 清理孤儿 Lease（若 S 减少）— 在 WaitReady 之后执行，避免回滚时 Lease 已删无法恢复

**不重启已有 Pod**——scale 操作由 K8s 渐进收/扩。分片变更在下一次 lease 扫描周期（~15s）生效。

**父 Context 超时**：`--apply` 模式使用 600s 超时（WaitReady 自适应最长 10min），`--dry-run` 使用 60s。避免父 ctx 提前取消导致 WaitReady 被截断。

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

    // 3. 统计 managed namespace 数量
    //    不排除任何 K8s phase（Terminating 因 finalizer 可能持续数小时，S 不应在此期间缩得过快）
    //    仅排除 kubepivot.io/phase = Error|Paused 的项目
    out, _ = exec.Kubectl(ctx, kubeconfig,
        "get", "namespace",
        "-l", "kubepivot.io/managed=true",
        "-o", "json")
    var nsList struct {
        Items []struct {
            Metadata struct {
                Name   string            `json:"name"`
                Labels map[string]string `json:"labels"`
            } `json:"metadata"`
            Status struct {
                Phase string `json:"phase"`
            } `json:"status"`
        } `json:"items"`
    }
    json.Unmarshal(out, &nsList)

    for _, ns := range nsList.Items {
        // 排除 Error/Paused 项目（若 kubepivot.io/phase label 存在）
        // 注意：不排除 Terminating namespace — finalizer 挂起可能持续数小时，
        // Controller 仍在同步它们，S 不应在此期间缩得过快
        phase := ns.Metadata.Labels["kubepivot.io/phase"]
        if phase == "Error" || phase == "Paused" {
            continue
        }
        info.ProjectCount++
        info.Projects = append(info.Projects, ns.Metadata.Name)
    }

    return info, nil
}

type SizingInfo struct {
    ProjectCount    int      // P（经过过滤的有效项目数）
    Projects        []string // 项目名列表（调试用）
    CurrentShards   int      // S
    CurrentReplicas int      // C
}
```

**过滤逻辑完整规则**：

| 条件 | 计入 P | 说明 |
|---|---|---|
| `status.phase == "Active"` + 无特殊 label | ✅ | 正常运行 |
| `status.phase == "Terminating"` + 无特殊 label | ✅ | 计入 P。finalizer 挂起可能持续数小时，Controller 仍在同步这些项目，S 不应在此期间缩得过快 |
| `kubepivot.io/phase == "Error"` | ❌ | 处于错误状态，不应分配分片资源 |
| `kubepivot.io/phase == "Paused"` | ❌ | 管理已暂停，不应计入负载 |
| `kubepivot.io/phase` label 不存在 | ✅ | 向后兼容：旧项目未打 label 仍计入 |

> **Terminating 计入的理由**：K8s namespace 在 Terminating 状态下可能因 finalizer 挂起持续数小时。Controller 在此期间仍会尝试同步这些 namespace 的资源。如果排除 Terminating 项目，大量删除操作时 S 会缩得过快，删除完成后又要扩回来——引起不必要的分片抖动。
>
> 关于 `kubepivot.io/phase` label：这是本设计方案引入的新约定。Controller 应在本迭代中增加写入此 label 的逻辑（在项目 enrollment 时设为 `Active`，在检测到异常时更新为 `Error`，在暂停管理时设为 `Paused`）。GetSizingInfo 读不到此 label 时默认视为 Active（向后兼容）。

### 4.2 推荐算法

```go
func Recommend(projectCount int, currentShards int, currentReplicas int, forceDownscale bool) (shards, replicas int, reason string, warnings []string) {
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

    // 超载警告
    if projectCount >= 500 {
        warnings = append(warnings,
            "🔴 P≥500 严重超载，每分片 >10 项目，建议考虑多集群或分层 controller 架构")
    } else if projectCount >= 200 {
        warnings = append(warnings,
            "💡 P≥200 触及上限，每分片项目数可能超过甜区")
    }

    // 重平衡风暴预警：S 变化幅度 ≥50% 且 P≥50 时警告
    if currentShards > 0 {
        deltaRatio := float64(abs(shards-currentShards)) / float64(currentShards)
        if deltaRatio >= 0.5 && projectCount >= 50 {
            warnings = append(warnings, fmt.Sprintf(
                "⚡ 分片数变化 %.0f%% (%d→%d)，将触发全量项目重平衡。"+
                    "%d 个项目将在首个 Lease 扫描周期（~15s）内同时迁移分片，"+
                    "可能引起短暂的调取风暴（Reconciliation Storm）。"+
                    "建议在低峰期执行 --apply。",
                deltaRatio*100, currentShards, shards, projectCount,
            ))
        }
    }

    // 单分片超载预警
    if projectCount/shards > 10 {
        warnings = append(warnings, fmt.Sprintf(
            "🔴 单分片承载 %d 个项目 (P/S=%.1f)，超过 etcd 建议响应延迟。"+
                "当前集群已超载，S=%d 已是硬上限。请考虑多集群部署。",
            projectCount/shards, float64(projectCount)/float64(shards), shards,
        ))
    }

    return shards, replicas, reason, warnings
}
```

### 4.3 Apply 逻辑（加强版）

```go
func (i *Installer) ApplySizing(ctx context.Context, info *SizingInfo, shards, replicas int) error {
    // 0. 权限预检 — 别等 apply 到一半才发现没权限
    if err := checkAccessReview(ctx, i.kubeconfig, i.namespace); err != nil {
        return fmt.Errorf("权限不足: %w\n\n请联系集群管理员授予以下权限：\n"+
            "  - patch configmaps in namespace %s\n"+
            "  - update deployments/scale in namespace %s\n"+
            "  - delete leases in namespace %s\n"+
            "  或使用具有 cluster-admin 权限的 kubeconfig。", err, i.namespace, i.namespace, i.namespace)
    }

    // 0.5. etcd 健康度预检（分片数大幅变更可能冲击 etcd Lease 数量）
    if err := checkEtcdHealth(ctx); err != nil {
        return fmt.Errorf("etcd health check failed, aborting: %w", err)
    }

    // 0.6. 快照旧状态 — 失败时回滚用
    oldShards := info.CurrentShards
    oldReplicas := info.CurrentReplicas

    // 1. 更新 ConfigMap
    patch := fmt.Sprintf(`{"data":{"shards":"%d"}}`, shards)
    _, err := exec.Kubectl(ctx, i.kubeconfig,
        "patch", "configmap", "kubepivot-controller-config",
        "-n", i.namespace, "--type=merge", "-p", patch)
    if err != nil {
        return fmt.Errorf("update ConfigMap: %w", err)
    }

    // 2. 清理孤儿 Lease（若 S 减少）
    if shards < oldShards {
        if err := cleanupOrphanedLeases(ctx, i.kubeconfig, i.namespace, shards, oldShards); err != nil {
            // 清理失败不阻断主流程，但记录警告
            slog.Warn("孤儿 Lease 清理失败（不影响主流程，Lease 将在 TTL 后自动过期）", "error", err)
        }
    }

    // 3. Scale Deployment
    _, err = exec.Kubectl(ctx, i.kubeconfig,
        "scale", "deployment", "kubepivot-controller",
        "-n", i.namespace, fmt.Sprintf("--replicas=%d", replicas))
    if err != nil {
        // Scale 失败 → 进入回滚流程
        slog.Error("scale deployment 失败，开始回滚", "error", err)
        return rollbackSizing(ctx, i, oldShards, oldReplicas, err)
    }

    // 4. 等待 rollout，超时与项目数正相关
    //    基础 120s，每个项目加 2s（大规模集群同步更慢）
    adaptiveTimeout := time.Duration(120+info.ProjectCount*2) * time.Second
    if adaptiveTimeout > 600*time.Second {
        adaptiveTimeout = 600 * time.Second // cap at 10min
    }
    if err := i.WaitReady(ctx, adaptiveTimeout); err != nil {
        // WaitReady 超时 → 提示回滚（此时 ConfigMap 和 Scale 已生效，但 Pod 未就绪）
        slog.Error("WaitReady 超时，Pod 可能未完全就绪", "error", err)
        return fmt.Errorf(
            "WaitReady 超时 (%v): %w\n\n"+
                "ConfigMap 和 Scale 已应用，但 Pod 未在预期时间内就绪。\n"+
                "你可以：\n"+
                "  1. 运行 'kp controller update --apply' 再次尝试（恢复旧值）\n"+
                "  2. 手动排查 Pod 状态: kubectl -n %s describe pods\n"+
                "  3. 运行回滚: kp controller update --shards %d --replicas %d --apply",
            adaptiveTimeout, err, i.namespace, oldShards, oldReplicas)
    }

    return nil
}

// rollbackSizing 将 ConfigMap 和 replicas 恢复为 apply 前的值
func rollbackSizing(ctx context.Context, i *Installer, oldShards, oldReplicas int, originalErr error) error {
    slog.Warn("正在回滚 sizing 变更...", "shards", oldShards, "replicas", oldReplicas)

    // 回滚 ConfigMap
    patch := fmt.Sprintf(`{"data":{"shards":"%d"}}`, oldShards)
    _, err1 := exec.Kubectl(ctx, i.kubeconfig,
        "patch", "configmap", "kubepivot-controller-config",
        "-n", i.namespace, "--type=merge", "-p", patch)

    // 回滚 replicas
    _, err2 := exec.Kubectl(ctx, i.kubeconfig,
        "scale", "deployment", "kubepivot-controller",
        "-n", i.namespace, fmt.Sprintf("--replicas=%d", oldReplicas))

    if err1 != nil || err2 != nil {
        return fmt.Errorf(
            "回滚失败！请手动恢复:\n"+
                "  kubectl patch configmap kubepivot-controller-config -n %s --type=merge -p '{\"data\":{\"shards\":\"%d\"}}'\n"+
                "  kubectl scale deployment kubepivot-controller -n %s --replicas=%d\n"+
                "原始错误: %v\n回滚错误: ConfigMap=%v, Scale=%v",
            i.namespace, oldShards, i.namespace, oldReplicas, originalErr, err1, err2)
    }

    return fmt.Errorf("变更已回滚（ConfigMap=%d, Replicas=%d）。原始错误: %w", oldShards, oldReplicas, originalErr)
}
```

### 4.4 孤儿 Lease 清理

当分片数减少（例如 S: 10→5）时，旧分片范围（shard-5 ~ shard-9）对应的 K8s Lease 对象在 etcd 中依然存在。虽然它们会在 TTL（15s）后自动过期，但在过期前的窗口内，这些 Lease 处于僵尸状态，可能干扰监控告警或运维脚本的 Lease 计数。

**清理策略**：

```go
// cleanupOrphanedLeases 删除 newShards ≤ idx < oldShards 范围的 Lease 对象
//
// Lease 命名规则: {prefix}{idx}，0-indexed
// 例：S 从 10 减到 5 时，删除 kubepivot-controller-shard-5 ~ kubepivot-controller-shard-9
func cleanupOrphanedLeases(ctx context.Context, kubeconfig, namespace string, newShards, oldShards int) error {
    leasePrefix := "kubepivot-controller-shard-"
    var deleted int
    for idx := newShards; idx < oldShards; idx++ {
        leaseName := fmt.Sprintf("%s%d", leasePrefix, idx)
        _, err := exec.Kubectl(ctx, kubeconfig,
            "delete", "lease", leaseName,
            "-n", namespace,
            "--ignore-not-found",
            "--wait=false") // 异步删除，不阻塞主流程
        if err != nil {
            slog.Warn("删除孤儿 Lease 失败", "lease", leaseName, "error", err)
            continue
        }
        deleted++
    }
    if deleted > 0 {
        fmt.Fprintf(os.Stderr, "  ✓ 已清理孤儿 Lease: shard-%d ~ shard-%d (%d 个)\n",
            newShards, oldShards-1, deleted)
    }
    return nil
}
```

**设计考量**：
- 清理失败不阻断主流程（Lease 会在 TTL 后自动过期，清理是加速手段）
- `--ignore-not-found`：如果 Lease 已被其他进程清理，不报错
- `--wait=false`：异步删除，不在 etcd 响应上阻塞 apply 链路
- 只在 `shards < oldShards` 时触发（扩容不需要清理）

### 4.5 权限预检（AccessReview）

在 `--apply` 实际写入前，先检查当前 kubeconfig 是否具备所需权限。不等到 Patch 失败才报 "Permission Denied"。

```go
// checkAccessReview 验证当前 kubeconfig 是否具备 apply 所需权限
func checkAccessReview(ctx context.Context, kubeconfig, namespace string) error {
    checks := []struct {
        verb     string
        resource string
    }{
        {"patch", "configmaps"},
        {"update", "deployments/scale"},
        {"delete", "leases"},
    }

    for _, c := range checks {
        args := []string{"auth", "can-i", c.verb, c.resource, "-n", namespace}
        if kubeconfig != "" {
            args = append(args, "--kubeconfig", kubeconfig)
        }
        out, err := exec.Kubectl(ctx, kubeconfig, args...)
        if err != nil {
            return fmt.Errorf("无法检查权限: %w", err)
        }
        if strings.TrimSpace(string(out)) != "yes" {
            return fmt.Errorf("缺少权限: %s %s in namespace %s", c.verb, c.resource, namespace)
        }
    }
    return nil
}
```

**交互流程**：

```
$ kp controller update --apply

🔐 权限预检...
  ✓ patch configmaps         — 通过
  ✓ update deployments/scale — 通过
  ✓ delete leases            — 通过

📊 应用推荐配置...
  ...
```

权限不足时：

```
$ kp controller update --apply

🔐 权限预检...
  ✓ patch configmaps         — 通过
  ✗ update deployments/scale — 拒绝

❌ 权限不足: 缺少权限: update deployments/scale in namespace kubepivot-system

请联系集群管理员授予以下权限：
  - patch configmaps in namespace kubepivot-system
  - update deployments/scale in namespace kubepivot-system
  - delete leases in namespace kubepivot-system
  或使用具有 cluster-admin 权限的 kubeconfig。
```

### 4.6 代码位置

`kp controller update` 子命令放在 `cmd/kp/controller.go` 的 `runController` switch 中（与 `install/uninstall/status/enroll/projects` 平级）。推荐算法放 `internal/controller_installer/` 包。

```
cmd/kp/controller.go                         +1 case + runControllerUpdate()
internal/controller_installer/installer.go   +Recommend() + SizingInfo + GetSizingInfo()
internal/controller_installer/apply.go       +ApplySizing() + rollbackSizing()
internal/controller_installer/cleanup.go     +cleanupOrphanedLeases()
internal/controller_installer/auth.go        +checkAccessReview()
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
| `TestRecommend_RebalanceWarning` | P=100, 当前 S=10, 推荐 S=25 (Δ=150%) → 重平衡预警 |
| `TestRecommend_RebalanceSmallChange` | P=20, 当前 S=3, 推荐 S=5 (Δ=67% 但 P<50) → 无预警 |
| `TestRecommend_ShardOverload` | P=600 → S=50, C=10 + P/S>10 单分片超载预警 |
| `TestGetSizingInfo` | 模拟 kubectl 输出，验证解析 |
| `TestGetSizingInfo_NoConfigMap` | ConfigMap 缺失 → 优雅退到默认值 S=10 |
| `TestGetSizingInfo_EnvConflict` | ConfigMap 和 Deployment env 不一致 → 报错 |
| `TestGetSizingInfo_TerminatingNS` | Terminating namespace 被计入 P（finalizer 挂起风险，S 不应缩得过快） |
| `TestGetSizingInfo_ErrorPhaseNS` | kubepivot.io/phase=Error 的 namespace 被排除 |
| `TestGetSizingInfo_PausedPhaseNS` | kubepivot.io/phase=Paused 的 namespace 被排除 |
| `TestGetSizingInfo_NoPhaseLabel` | 无 kubepivot.io/phase label → 计入 P（向后兼容） |
| `TestControllerUpdate_DryRun` | `kp controller update --dry-run` → 输出 diff + 压力趋势表 |
| `TestControllerUpdate_Apply` | `kp controller update --apply` → ConfigMap + replicas 更新 |
| `TestControllerUpdate_EtcdPreCheck` | etcd 不健康 → apply 被阻断 + 提示 |
| `TestControllerUpdate_AccessReview` | 无权限 → 报错 + 列出缺失权限 |
| `TestControllerUpdate_Rollback` | Scale 失败 → 自动回滚到旧值 |
| `TestControllerUpdate_OrphanCleanup` | S 10→5 → 验证 shard-5~9 Lease 被删除 |
| `TestControllerUpdate_OrphanNoopOnScaleUp` | S 5→10 → 确认不触发清理逻辑 |
| `TestCleanupOrphanedLeases` | 模拟 Lease 存在 + 已过期混合场景 |
| `TestCleanupOrphanedLeases_Idempotent` | 重复清理 → `--ignore-not-found` 不报错 |

---

## 六、风险与约束

1. **--apply 会 scale deployment**：直接改 replicas，K8s 渐进收/扩，不会瞬时中断。但用户应了解这是在线操作。

2. **shards 变更可能冲击 etcd**：`S` 大幅增加（如 10→50）会导致 Lease 对象激增。`--apply` 前自动检查 etcd 健康度，异常时阻断并提示。

3. **ConfigMap 与 Deployment env 冲突**：部分用户直接在 Deployment YAML 设 `KUBEPIVOT_SHARDS` env 而不走 ConfigMap。`GetSizingInfo` 检测到两者不一致时报错，要求用户先统一。

4. **不降配策略**：默认不降低 replicas。`--force-downscale` 显式允许。分片数不受此策略约束。

5. **手动指定 --shards/--replicas 时跳过算法**：直接 apply 用户指定的值，不做额外校验。用户对自己指定的值负责。

6. **Terminating namespace 计入 P**：K8s namespace 在 Terminating 状态下可能因 finalizer 挂起持续数小时。Controller 在此期间仍会同步这些项目，排除会导致 S 缩得过快，删除完成后又要扩回来。

7. **孤儿 Lease 残留**：S 减少时，旧分片 Lease 可能残留为僵尸对象。ApplySizing 在 WaitReady 成功后主动清理 `[newS, oldS)` 区间的 Lease（见 4.4）。清理在 Scale+WaitReady 之后执行，避免回滚时 Lease 已删无法恢复。

8. **重平衡瞬时压力**：当 |ΔS|/oldS ≥ 50% 且 P≥50 时，`Recommend()` 输出重平衡风暴预警。所有 Controller 会在首个 Lease 扫描周期（~15s）内同时释放旧分片 + 抢占新分片，引起短暂调取风暴。建议在低峰期执行。

9. **部分写入回滚**：ConfigMap Patch 成功后 Scale 失败时，`ApplySizing` 自动回滚 ConfigMap 和 replicas 到旧值。若回滚也失败，输出手动恢复命令（见 4.3 `rollbackSizing`）。

10. **Error/Paused 项目排除**：`GetSizingInfo` 检查 `kubepivot.io/phase` label，排除 `Error` 和 `Paused` 状态的项目。无此 label 的项目视为 Active（向后兼容，见 4.1）。

11. **权限预检**：`--apply` 前通过 `kubectl auth can-i` 验证 patch configmaps、update deployments/scale、delete leases 三项权限。权限不足时提前报错并列出缺失项（见 4.5）。

12. **单分片超载保护**：当 P/S > 10 时，输出 🔴 警告并建议多集群。S=50 是硬上限，不自动突破（见 2.2）。

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

2026-05-04  qc + Claude 设计审查 + 5 边缘情况补全 + 3 实施微调
    A. 父 Context 超时 — apply 模式 600s，防止 WaitReady 被截断
    B. 孤儿 Lease 清理时序 — 移至 WaitReady 之后，保护回滚路径
    C. Terminating 计入 P — finalizer 挂起时 S 不应缩得过快
    原始 5 边缘情况补全：
       1. 孤儿 Lease 清理 (4.4) — S 减少时主动删除旧分片 Lease
       2. 重平衡风暴预警 (4.2) — ΔS≥50% 且 P≥50 时输出 ⚡ 警告
       3. 原子性保证 + 回滚 (4.3) — Scale 失败自动回滚，WaitReady 超时提示恢复
       4. P 计数精准化 (4.1) — 排除 kubepivot.io/phase=Error|Paused 项目
       5. CLI 权限预检 (4.5) — kubectl auth can-i 提前验证三项权限
       6. 安全熔断 (2.2) — P/S>10 硬超载保护 + 多集群建议
       7. 压力变化趋势表 (3) — dry-run 输出维度对比表
    - 测试用例扩展 14→28
    1. 孤儿 Lease 清理 (4.4) — S 减少时主动删除旧分片 Lease
    2. 重平衡风暴预警 (4.2) — ΔS≥50% 且 P≥50 时输出 ⚡ 警告
    3. 原子性保证 + 回滚 (4.3) — Scale 失败自动回滚，WaitReady 超时提示恢复
    4. P 计数精准化 (4.1) — 排除 kubepivot.io/phase=Error|Paused 项目
    5. CLI 权限预检 (4.5) — kubectl auth can-i 提前验证三项权限
    6. 安全熔断 (2.2) — P/S>10 硬超载保护 + 多集群建议
    7. 压力变化趋势表 (3) — dry-run 输出维度对比表
    - 测试用例扩展 14→28
```
