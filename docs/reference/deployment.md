# kp deploy

> KubePivot 部署引擎：从 `components.yaml` 到集群 Running，状态机驱动 + 拓扑分层 + 外挂 sizing / supply-chain。
> 最后更新：2026-05-08（v3.2）

---

## 概览

`kp deploy` 是 KubePivot 的一键部署命令，封装了完整的部署管线：

```
components.yaml → BuildPlan/BuildLayers → 拓扑排序
  → 前置检查（state / supply-chain / OPA / migration）
  → 分层构建 + 推送 + helm upgrade + rollout
  → sizing 优化（可选 auto）
  → resources.yaml 自动同步到 controller
```

一条命令替代 `docker build → push → helm install → rollout status` 的手动 4 步流程。

---

## kp deploy

```
kp deploy [flags]
```

### Flags

| Flag | 默认值 | 说明 |
|---|---|---|
| `--components` | `configs/components.yaml` | 组件配置文件路径 |
| `--namespace` | `$KUBE_NAMESPACE` 或项目名 | K8s namespace |
| `--context` | `$KUBE_CONTEXT` | K8s context |
| `--kubeconfig` | `$KUBE_CONFIG` 或 `~/.kube/config` | kubeconfig 路径 |
| `--dry-run` | false | 仅打印计划，不执行 |
| `--sign` | false | 部署后 cosign keyless 签名镜像 |
| `--force-migrate` | false | 忽略破坏性迁移警告，强制部署 |
| `--preview` | false | 部署后生成 Header-based Preview 路由模板 |
| `--changed-only` | false | 只部署有 git 变更的服务（基于 git diff） |
| `--parallelism` | 0（不限） | 同层最大并发数（建议大规模集群 4-8） |
| `--skip-supply-chain` | false | 跳过供应链策略验证（紧急场景） |
| `--env` | — | 指定部署环境（kp context add 配置的环境名） |

**Sizing 相关**：

| Flag | 默认值 | 说明 |
|---|---|---|
| `--sizing-mode` | `manual` | 资源优化：`auto` / `manual` |
| `--sizing-profile` | `default` | 业务模板：`web` / `batch` / `db` / `default` |
| `--sizing-force` | false | 自动应用建议（忽略置信度阈值） |
| `--prometheus-url` | — | Prometheus API 地址 |
| `--prometheus-window` | 7d | Prometheus 查询窗口 |
| `--prometheus-step` | 15m | Prometheus 采样步长 |
| `--sizing-threshold` | 0.7 | 自动应用置信度阈值 |

**RBAC**: `PermDeploy`

### 环境变量（`configs/project.env`）

部署配置的三级解析优先级：**YAML > flag > project.env**。

| 变量 | 说明 |
|---|---|
| `PROJECT_NAME` | 项目名（Helm release 前缀） |
| `VERSION` | 镜像版本 |
| `REGISTRY_PREFIX` | 镜像仓库前缀 |
| `ARCH` | 目标架构（amd64/arm64） |
| `KUBE_NAMESPACE` | K8s namespace |
| `KUBE_CONTEXT` | K8s context |
| `KUBE_CONFIG` | kubeconfig 路径 |
| `ETCD_ENDPOINTS` | etcd 地址（逗号分隔） |
| `SUPPLY_CHAIN_ENFORCE` | 启用供应链策略（true/false） |
| `REGISTRY_ALLOW` / `REGISTRY_DENY` | 注册表白/黑名单 |
| `SUPPLY_CHAIN_SIGN_ENFORCE` | 强制签名验证 |
| `SUPPLY_CHAIN_COSIGN_KEY` | Cosign 公钥路径 |

---

## 部署管线

### 完整流程

```
── 1. checkDeps ───────────────────────────────────────
    docker + kubectl + helm 存在性检查
    全缺时一次性列出，避免逐个安装

── 2. RBAC check ──────────────────────────────────────
    mustCheck(PermDeploy)

── 3. --env 覆盖 ──────────────────────────────────────
    加载 kp env 配置，覆盖 namespace/context 等

── 4. resolveDeployConfig ─────────────────────────────
    env → deployConfig 字段补齐

── 5. ensureNamespaceExists ───────────────────────────
    kubectl apply -f <namespace YAML>（幂等创建）

── 6. BuildPlan / BuildLayers ─────────────────────────
    planner 解析 components.yaml → 拓扑分层

── 7. state machine init ──────────────────────────────
    state.NewAutoStore(ETCD_ENDPOINTS) → etcd 或 fallback 本地文件
    sm.MarkFirstDeploy(!namespaceExists)

── 8. 前置检查 ────────────────────────────────────────
    ├─ checkHelmReleaseState: pending-rollback / pending-install / failed
    ├─ sandbox state guard: LOCKED/SNAPSHOTTING/... → 拒绝
    ├─ duplicate deploy guard: 非 IDLE/RUNNING/TERMINATED → 拒绝

── 9. runSizingHook ───────────────────────────────────
    --sizing-mode=auto → 10 并发 compute + components.yaml 回写

── 10. 供应链策略验证 ─────────────────────────────────
     registry 白/黑名单 → 签名验证 → SBOM → CVE

── 11. executeDeploy ──────────────────────────────────
    IDLE → INITIALIZING → DEPLOYING
    ┌─ 单服务: make build → push → helm upgrade → rollout
    └─ 多服务: 拓扑分层并行
         同层并行（semaphore 限流）
         层间串行
         失败 → 级联 rollback → 整组 rollback → down
    DEPLOYING → VALIDATING → RUNNING

── 12. autoSyncResourcesIfEnrolled ────────────────────
    namespace label kubepivot.io/managed=true
      → 同步 configs/resources.yaml 到 ConfigMap
```

### 状态机驱动的错误恢复

```
首次部署失败:
  RUNNING → CLEANING → IDLE (delete namespace)

更新失败:
  RUNNING → ROLLING_BACK → RUNNING (helm rollback)
  回滚失败 → CLEANING (降级到手动干预)
```

---

## components.yaml 参考

### 顶层结构

```yaml
components:
  - name: api-server
    type: deployment           # deployment（默认）/ statefulset
    image: api-server          # 镜像名（不含 registry prefix + tag）
    port: 8080
    replicas: 3
    cpu: "500m"
    memory: "512Mi"
    storage: "10Gi"
    strategy: rolling          # rolling（默认）/ blue-green / canary
    min_replicas: 2
    max_replicas: 10
    target_cpu: 80             # HPA 目标 CPU（0=不启用）
    depends_on:
      - postgres               # 同 namespace 依赖
      - other-ns/cache-service # 跨 namespace 依赖
    api_version: v1
    namespace: default
    sizing:                    # 可选：资源优化策略
      mode: auto
      profile: web
      threshold: 0.7
      auto_profile: true
      weight_learning: true
```

### Component 字段全表（17 个）

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `name` | string | **必填** | 组件唯一标识 |
| `type` | string | `deployment` | 工作负载类型：`deployment` / `statefulset` |
| `image` | string | — | 容器镜像名（空 = CLI 工具/无构建） |
| `port` | int | — | 服务端口 |
| `replicas` | int | 1 | 副本数 |
| `cpu` | string | `100m` | CPU request |
| `memory` | string | `128Mi` | Memory request |
| `storage` | string | `1Gi` | 持久存储大小 |
| `strategy` | string | `rolling` | 部署策略：`rolling` / `blue-green` / `canary` |
| `min_replicas` | int | — | HPA 最小副本数 |
| `max_replicas` | int | — | HPA 最大副本数 |
| `target_cpu` | int | 0 | HPA 目标 CPU 利用率%（0 = 不启用） |
| `depends_on` | []string | — | 依赖服务列表。含 `/` 为跨 namespace |
| `api_version` | string | — | 组件 API 版本，供 `kp compat` 依赖检查 |
| `namespace` | string | — | 目标 namespace |
| `sizing` | object | — | 资源优化配置（见 sizing 文档） |

**depends_on 拆分逻辑**：`postgres` → `DependsOn`（同 ns），`other-ns/cache` → `CrossNsDeps`（跨 ns）。

### SizingConfig 字段（11 个）

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `mode` | string | `manual` | `auto` / `manual` |
| `profile` | string | `default` | `web` / `batch` / `db` / `default` |
| `force` | bool | false | 忽略置信度阈值自动应用 |
| `samples` | int | 5 | 采样次数 (2-10) |
| `interval` | string | `2s` | 采样间隔 |
| `window` | string | `7d` | Prometheus 查询窗口 |
| `step` | string | `15m` | Prometheus 采样步长 |
| `threshold` | float64 | 0.7 | 置信度阈值 |
| `auto_profile` | bool | false | 自动推荐业务模板 |
| `weight_learning` | bool | false | 自适应权重学习 |

---

## 拓扑分层调度

### BuildLayers — Kahn 算法

`planner.BuildLayers(path)` 解析 components 依赖关系，用 Kahn 拓扑排序输出分层计划：

```
         ┌─────────┐
Layer 0  │ postgres │   入度=0 → 优先部署
         └────┬────┘
         ┌────▼────┐
Layer 1  │ redis   │   依赖 postgres
         └────┬────┘
         ┌────▼────┐
Layer 2  │ api     │   依赖 postgres + redis
         └─────────┘
```

- **同层并行**：Layer 内的所有服务可同时部署（受 `--parallelism` 限制）
- **层间串行**：上层全部 ready 后才开始下层
- **循环依赖**：检测到则返回 error，拒绝部署

### 循环依赖检测

遍历后 `visited < totalComponents` → 返回 `"circular dependency detected"` 错误。

### 单服务 vs 多服务

```
len(layers) > 1 或 len(layers[0]) > 1:
  → 多服务模式：独立 helm release ({project}-{service})
  → deployLayers()

否则:
  → 单服务模式：统一 helm release ({project})
  → make deploy.build → make deploy.push → helm upgrade → rollout
```

---

## 多服务失败恢复链

多服务部署的失败恢复采用三级递进：

```
层级 1: 单服务重试
  每个服务 helm upgrade + rollout 最多重试 3 次

层级 2: 级联回滚
  该层某服务第 3 次仍失败
    → collectAffected 收集该服务及所有下游（planner.Downstream）
    → 逆拓扑顺序 helm rollback
    → sm.StateRollingBack → sm.StateRunning

层级 3: 整组回滚
  级联回滚也失败
    → 所有已成功部署的 release 逆序 helm rollback
    → sm.StateRollingBack → sm.StateRunning

层级 4: kp down
  整组回滚也失败
    → kubectl delete namespace
    → sm.StateCleaning → sm.StateIdle
```

---

## 前置检查

### Helm Release 状态处理

`checkHelmReleaseState()` 在部署前检查 helm release，处理三种异常：

| 状态 | 操作 |
|---|---|
| `pending-rollback` | 交互确认 → 删除 pending secret → `ForceState(RUNNING)` |
| `pending-install` | 交互确认 → `helm delete` release |
| `failed` | 交互选择：r) 回滚 / d) 重新部署 / q) 取消 |

### OPA 策略检查

检测 `opa` 命令可用性，执行策略检查。阻断规则 → 部署终止，警告规则 → 仅日志。

### 数据库迁移风险检查

连接 DATABASE_URL，扫描待执行迁移 SQL：
- **破坏性变更**（DROP/TRUNCATE/ALTER COLUMN TYPE）：部署阻断
- **潜在风险**（大表 ALTER/索引变更）：警告
- `--force-migrate` 跳过检查

### 镜像安全扫描

检测 `trivy` 命令可用性，自动运行安全扫描（静默跳过不可用）。

---

## Sizing Hook 集成

`--sizing-mode=auto` 时，`runSizingHook` 在部署前自动执行：

```
1. 收集目标：遍历 plan，过滤有 image + cpu + memory 的组件
2. 10 并发执行 computeSizingForPod:
   ├─ 优先 Prometheus QueryRange (7d 历史)
   ├─ 降级 kubectl top 瞬时 5 采样
   ├─ 可选 AutoProfile 自动推荐
   └─ sizing.Compute(samples, profile)
3. 灰度保护：Confidence < threshold → 跳过
4. 批量原子回写 components.yaml（带 sizing 注释注入）
5. 生成 configs/vpa-suggestion.yaml (mode=Off)
6. 分级报告：成功/软失败/硬失败
```

软失败（无数据/超时/低置信度）降级不阻断，硬失败（认证/解析错误）阻断部署（`--sizing-force` 可绕过）。

---

## 供应链策略验证

部署前通过 `verifySupplyChainPolicy` 验证：

1. 提取 plan 中所有镜像（去重）
2. 从 env 加载全局策略（registries allow/deny / signing / SBOM）
3. 逐个镜像 `supplychain.ValidatePolicy()`：
   - 注册表检查（白/黑名单）
   - 签名验证（Cosign）
   - SBOM 存在性检查
   - CVE 阈值检查
4. 策略违反 → 部署阻断
5. `--skip-supply-chain` 紧急绕过

**env 配置**：

```bash
SUPPLY_CHAIN_ENFORCE=true
REGISTRY_ALLOW=ghcr.io,docker.io/library
REGISTRY_DENY=docker.io/malicious
SUPPLY_CHAIN_SIGN_ENFORCE=true
SUPPLY_CHAIN_COSIGN_KEY=./keys/cosign.pub
SUPPLY_CHAIN_SBOM_REQUIRE=true
SUPPLY_CHAIN_SBOM_FORMAT=cyclonedx-json
```

---

## 部署后

### resources.yaml 自动同步

`autoSyncResourcesIfEnrolled()` 在部署成功后自动运行：

```
namespace label kubepivot.io/managed=true
  → 读取 configs/resources.yaml
  → sync 到 ConfigMap kubepivot-resources
  → sha256 指纹（controller watcher 自行比对）
```

降级不阻断：namespace 未 enroll / 文件不存在 / sync 失败都只 warn。

### 部署耗时统计

多服务模式输出 `部署耗时统计` 表格：

```
  部署耗时统计
  服务                      build     push      helm      rollout   总计
  ──────────────────────────────────────────────────────────────────────────
  postgres                  -         -         3.2s      15.1s      18.3s
  redis                     -         -         2.8s      10.3s      13.1s
  api-server                45.2s     12.1s     4.5s      22.0s      83.8s
```

---

## kp resume

部署中断后的状态恢复。

```
kp resume [flags]
```

| Flag | 说明 |
|---|---|
| `--namespace` | K8s namespace |
| `--context` | K8s context |
| `--kubeconfig` | kubeconfig 路径 |

**逻辑**：

```
1. 加载状态机 → 打印当前状态
2. DetectActualState() → 从 K8s 检测实际资源状态
3. K8s RUNNING → 同步状态机为 RUNNING
4. K8s IDLE → ForceState(IDLE) → 重新执行 deploy
5. 其他 → 提示手动处理
```

**RBAC**: `PermDeploy`（与 deploy 同权）

---

## kp rollback

手动回滚，支持单服务和多服务。

```
kp rollback [flags]
```

| Flag | 说明 |
|---|---|
| `--namespace` | K8s namespace |
| `--context` | K8s context |
| `--kubeconfig` | kubeconfig 路径 |

**逻辑**：

```
1. RUNNING → ROLLING_BACK
2. 加载 components.yaml → BuildLayers
3. 多服务：逆拓扑顺序逐层 helm rollback
   同层内服务并行 rollback
4. 单服务：helm rollback <project>
5. ROLLING_BACK → RUNNING
```

**RBAC**: `PermRollback`

---

## 状态机参考

### 部署状态（12 个）

```
                  ┌──────────────┐
                  │     IDLE     │
                  └──┬───────┬───┘
           INITIALIZING    LOCKED
                │             │
            DEPLOYING    SNAPSHOTTING
                │             │
           VALIDATING    SIMULATING
                │             │
            RUNNING      COMMITTING
           ┌────┼────┐       │
    ROLLING_BACK  │   CLEANING │
           │      │       │    │
        RUNNING   │   IDLE/   RUNNING
                  │  TERMINATED
              TERMINATED
```

### 沙盒状态（5 个）

```
LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
  ↓         ↓              ↓              ↓
IDLE      IDLE           IDLE         RESTORING → IDLE
```

### 存储

- **etcd** (`$ETCD_ENDPOINTS` 非空)：key = `kubepivot/{project}/{namespace}/state`
- **本地文件**（etcd 不可用）：`~/.kp/state/{project}/{namespace}.json`
- 自动迁移：etcd 就绪后自动从本地文件迁移到 etcd（保留本地备份）

### 关键约束

- `TERMINATED` 无转出（终态）
- `COMMITTING` 无逃生路径（"禁止 force-unlock"）
- `ForceState` 跳过所有转换表检查，仅供异常恢复（加 `[force]` 前缀）
- `ResumeFromValidating` 仅允许 `VALIDATING → RUNNING`（安全护栏）

---

## 依赖工具

| 工具 | 必需 | 用途 |
|---|---|---|
| docker | **是** | 镜像构建 (buildx) |
| kubectl | **是** | K8s 资源操作 |
| helm | **是** | 包管理 (upgrade/rollback/history) |
| trivy | 否 | 镜像安全扫描（不可用静默跳过） |
| opa | 否 | 策略检查（不可用静默跳过） |
| cosign | 否 | 镜像签名（--sign 时必需） |

---

## 相关命令

- `kp init` — 初始化项目 + 生成 components.yaml 模板
- `kp down` — 删除部署（helm uninstall + delete namespace）
- `kp sandbox` — 操作沙箱（零停机迁移）
- `kp sizing recommend` — 单 Pod 资源优化
- `kp controller enroll` — 接入全局 controller
