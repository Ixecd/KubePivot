# kp controller

> 全局 KubePivot Controller 生命周期管理 + 自愈引擎。
> 最后更新：2026-05-08（v3.2）

---

## 概览

Controller 是 KubePivot 的控制面常驻进程，运行在 `kubepivot-system` namespace 的 StatefulSet 中。核心职责：

1. **资源自愈** — 监控 `resources.yaml` 声明的资源，缺失时自动修复
2. **异常检测** — OOMKilled 自动调内存 / CrashLoopBackOff 自动回滚
3. **配置漂移对齐** — `force-sync` 资源自动检测并修复手动变更
4. **分片管理** — 多 Pod + Lease 抢占，水平扩展
5. **事件驱动** — eventstream Informer(KVCache) 加速检测

---

## CLI 子命令

### install

安装全局 Controller 到集群 `kubepivot-system` namespace。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--namespace` | `kubepivot-system` | controller 部署 namespace |
| `--image` | `qingchun22/kubepivot-controller:<kpVersion>` | controller 镜像 |
| `--kubeconfig` | | kubeconfig 路径 |
| `--context` | | kube context |
| `--wait` | `true` | 等待 Deployment ready |
| `--wait-timeout` | `120s` | 等待超时 |

**部署内容**：Namespace + ServiceAccount + ClusterRole + ClusterRoleBinding + ConfigMap + StatefulSet。

### uninstall

卸载 Controller。**危险操作**，会删除整个 `kubepivot-system` namespace 及所有 ClusterRole/Binding。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--namespace` | `kubepivot-system` | controller 部署 namespace |
| `--kubeconfig` | | kubeconfig 路径 |
| `--force` | `false` | 跳过确认 |

**不会动被管理项目的 namespace label**（用户需手工 `unenroll`）。

### status

查看 controller 状态。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--namespace` | `kubepivot-system` | controller 部署 namespace |
| `--kubeconfig` | | kubeconfig 路径 |

### enroll / unenroll

将项目 namespace 打上 `kubepivot.io/managed=true` label 接入全局 controller，或取消接入。

### projects

列出所有被管理的项目。

---

## resources.yaml 参考

`resources.yaml` 是每个项目自愈配置的入口文件，存放在项目根目录 `configs/resources.yaml`，部署时会被写入 `kubepivot-resources` ConfigMap。

Controller 通过 `watchConfigMaps()` 监听 CM 变更，秒级热加载，无需重启。

### 顶层结构

```yaml
# ResourcesConfig — 顶层结构
resources:       # 必填：资源列表
  - ...
  - ...
traffic:         # 可选：流量层配置（蓝绿部署）
  ...
```

### Resource 字段 — 资源声明

每个被管理资源都是一个 `Resource`：

| 字段 | 类型 | 必填 | 默认值 | 说明 |
|---|---|---|---|---|
| `kind` | string | **是** | — | K8s 资源类型。内置：Deployment / StatefulSet / DaemonSet / ReplicaSet / Job / CronJob / Pod / Service / ConfigMap / Secret。**CRD 也支持**，走 kubectl apply 路径 |
| `name` | string | **是** | — | 资源名称 |
| `namespace` | string | 否 | `$KUBE_NAMESPACE` 或 `default` | 资源所在 namespace，未填则取环境变量 |
| `on-missing` | string | 否 | — | 资源缺失时的自愈策略（见下方策略表） |
| `max-retry` | int | 否 | 0 | 自定义策略的最大重试次数（防递归炸弹） |
| `fallback` | string | 否 | — | `custom` 策略的降级策略（递归基） |
| `force-sync` | bool | 否 | false | 是否开启配置漂移对齐（30s 一次 helm diff） |
| `no-sync-fields` | []string | 否 | — | drift-sync 豁免字段列表（子串匹配） |
| `helm-release` | string | 否 | `$PROJECT-$name` | 显式覆盖 Helm release 名。蓝绿 / 金丝雀场景下同一资源挂多 release 时必需 |
| `supply-chain` | object | 否 | — | 供应链安全策略（注册表 / 签名 / SBOM / CVE） |

### on-missing 自愈策略

Controller 在 reconcile 循环中检测资源是否存在，不存在时按 `on-missing` 执行对应策略：

| 策略值 | 行为 | 适用场景 |
|---|---|---|
| `auto-heal` 或 `recreate` | `helm rollback` 到上一版本 → 重新应用。CRD 资源走 `kubectl apply` | 默认策略，适合大多数资源 |
| `rollback` | 查询 Helm release 历史，回滚到 `latestRevision - 1`。仅一个 revision 时降级为 recreate | 代码回滚场景 |
| `scale-down` | `kubectl scale --replicas=0`，保留资源定义但停止服务 | 临时止血，需人工介入 |
| `alert` | 仅 `slog.Error` 告警，不做任何自动操作 | 需要人工决策的关键资源 |
| `custom` | 递归调用 `checkAndHeal`，用 `fallback` 作为 `on-missing`，`max-retry` - 1 | 组合策略（先尝试 A，失败再 B） |
| （空 / 其他） | 跳过，不处理 | 仅声明存在性（用于 `DetectActualState`） |

**custom 策略递归示例**：

```yaml
- kind: Deployment
  name: critical-app
  on-missing: custom
  max-retry: 2
  fallback: rollback
```

执行路径：`custom` → `rollback`（第 1 次） → `recreate`（第 2 次）

### Deployment 异常状态检测（自动触发）

仅对 `kind: Deployment` 生效，在资源**存在**时被动检查：

#### OOMKilled

- **触发条件**：Pod 的 `LastState.Terminated.Reason == "OOMKilled"`
- **自动操作**：`bumpMemory(current, +25%)` → `kubectl patch deployment` 上调 memory limit
- **支持单位**：Mi / Gi（其他单位跳过）

```
检测到 OOMKilled
  512Mi → 640Mi (上调 25%)
  1Gi   → 1Gi   (不满足 +1 最小步进，调整为 2Gi)
```

#### CrashLoopBackOff

- **触发条件**：Pod 的 `State.Waiting.Reason == "CrashLoopBackOff"`
- **分析逻辑**：读取 Pod `--previous` 日志（最后 50 行），关键词分类：

| 崩溃类型 | 关键词 | 自动操作 |
|---|---|---|
| `crashStartup` | `connection refused` / `dial tcp` / `no such host` / `timeout` / `database` / `etcd` / `config` / `secret` / `permission denied` | 仅告警，等待人工介入 |
| `crashRuntime` | `panic:` / `runtime error` / `segmentation fault` / `nil pointer` / `index out of range` | RestartCount >= 5 → 自动 rollback |
| `crashUnknown` | 不匹配上述任何关键词 | 仅告警 |

### force-sync 配置漂移对齐

**30s 周期**的独立扫描循环，与 8s 自愈周期解耦。

**工作流**：

```
force-sync: true 的资源
  → helm diff upgrade (30s 间隔)
  → 过滤 Kubepivot 所有权的字段: image / replicas / limits / requests / cpu / memory
  → 排除 no-sync-fields 豁免字段
  → 有差异 → helm upgrade --reuse-values --wait --timeout 120s
  → 写漂移审计日志到 etcd
```

**no-sync-fields 示例**：

```yaml
- kind: Deployment
  name: api-server
  force-sync: true
  no-sync-fields:
    - replicas   # HPA 控制的副本数不强制对齐
    - image      # CI/CD 更新的镜像不强制对齐
```

漂移审计日志 key：`/kubepivot/$PROJECT/$namespace/drift/$timestamp_nano`

### supply-chain 供应链策略

```yaml
supply-chain:
  registries:
    allow:                          # 注册表白名单（空=全部允许）
      - ghcr.io
      - docker.io/library
    deny:                           # 黑名单（优先级高于 allow）
      - docker.io/malicious
  signing:
    enforce: true                   # 强制验证镜像签名
    cosign-key: ./keys/cosign.pub   # Cosign 公钥路径
    keyless:                        # 可选：keyless 签名
      identity: ci@kubepivot.io
      issuer: https://token.actions.githubusercontent.com
      regexp: false
  sbom:
    require: true                   # 要求镜像附带 SBOM
    format: cyclonedx-json          # 期望格式：cyclonedx-json / spdx-json
  cve:
    max-severity: high              # 阻断阈值：critical / high / medium / low / none
    exceptions:                     # 豁免的 CVE ID
      - CVE-2024-0001
      - CVE-2024-0002
```

**策略验证顺序**（按成本升序，快速失败）：

1. 注册表检查（无网络）
2. 缓存检查（同镜像 + 配置 5min TTL）
3. 签名验证（Cosign）
4. SBOM 存在性检查
5. CVE 阈值检查

### traffic 流量层配置（v2.6+ 蓝绿部署）

```yaml
traffic:
  kind: Ingress              # Ingress / Gateway / 不写=自动检测（Gateway API 优先）
  strategy: blue-green       # v2.6 仅 blue-green

  refs:
    name: myapp-ingress      # 流量层资源名
    namespace: myapp         # 可选，默认同项目 ns

  routes:                    # 恰好两个 entry，weight 一个 100 一个 0
    - service: myapp-blue
      weight: 100
    - service: myapp-green
      weight: 0

  validation:
    podReadyTimeoutSec: 60   # Pod ready 等待超时（秒）
```

---

## 项目受保护 namespace

Controller **永远不会管理**以下 namespace：

| 保护原因 | namespace |
|---|---|
| K8s 系统 | `kube-system` / `kube-public` / `kube-node-lease` |
| 自身 | `kubepivot-system` |
| 默认 | `default` |
| 自定义 | `$KUBEPIVOT_EXTRA_PROTECTED_NS`（逗号分隔） |

保护逻辑在 `namespace_blacklist.go:IsProtectedNamespace()`。

---

## 分片机制

默认 3 副本 × 10 分片。每个 pod 通过 K8s `coordination.k8s.io/Lease` API 抢占分片。`hash(namespace) % totalShards` 决定项目归属哪个 shard。

| 配置项 | system.yaml 路径 | 默认值 |
|---|---|---|
| 总分片数 | `controller.shards` | 10 |
| Worker 池大小 | `controller.workerPoolSize` | 20 |
| Reconcile 间隔 | `controller.reconcileInterval` | 8s |
| Orphan 清扫间隔 | `controller.orphanSweeperInterval` | 60s |

---

## Reconcile 循环

每个 Worker 执行 `handleTask()`：

```
1. IsProtectedNamespace 检查 → 保护空间跳过
2. DetectResourceExists(informer cache 优先 → kubectl 兜底)
3. 资源存在:
   - 加载 Labels（informer cache 优先）
   - Deployment → checkAbnormalState (OOM / CrashLoop)
   - 返回 nil
4. 资源缺失:
   - 加载 Labels（识别 Helm release）
   - 读取 StateMachine（避免并发冲突）
   - switch OnMissing → healRecreate / healRollback / healScaleDown / healCustom / alert
```

### 触发源汇总

| 触发源 | 间隔 | 机制 |
|---|---|---|
| reconcileLoop | 8s（可配） | 周期性全量扫描 |
| watchConfigMaps | 实时 | CM 变更 → enqueue 对应项目 |
| watchNamespaces | 实时 | ns label 变更 → 加载资源 + enqueue |
| etcd_watcher | 实时 | 状态机 key 变更 → enqueue |
| shard 接管 | 实时 | `OnShardChanged` → 同步项目并全量 reconcile |

---

## RBAC

Controller 操作受 RBAC 控制（`rbac_hook.go`），默认 fail-open：

| 操作点 | 权限 | 文件位置 |
|---|---|---|
| heal 自愈 | `PermHeal` | heal.go:107 |
| drift force-sync | `PermDriftSync` | drift_sync.go:138 |
| sandbox GC | `PermSandboxGC` | sandbox_gc.go:98 |
| sweeper lease | `PermSweeperLease` | sweeper.go:59 |

设置 `KUBEPIVOT_RBAC_ENFORCE=true` 切换为 fail-close（检查失败则拒绝操作）。

RBAC 配置从 `/etc/kubepivot/teams.yaml` 加载。

---

## 相关命令

- `kp controller start --global` — Pod 内部启动（`build/docker/controller/Dockerfile` 的 CMD）
- `kp bench` — 自愈 benchmark
- `kp drift` — 手动漂移检测 / 修复
- `kp sandbox` — 沙箱环境（复用 resources.yaml 配置）
- `kp deploy` — 部署时生成 resources.yaml
