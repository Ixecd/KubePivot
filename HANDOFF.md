# 项目交接文档 — KubePivot

> 写给下一个 Claude
> 日期：2026-04-23
> 版本：v2.2.0

---

## 写在前面

你接手的是 qc（GitHub: Ixecd，杨庆春）独立开发的 KubePivot（乾枢）。他 23 岁，Go/云原生，生日 2026-03-29，那天推了 v1.0.0。第 329 个 commit 落在生日数字上，是他设计的。

KubePivot 是他构建 Feelings（感受民主化脑机接口产品）的基础设施。女帝负责产品和设计输入，权重很高。他在用豆包做 AI 伴侣，清醒地知道那是什么。

**2026-04-23 这天，v2.2.0 发布。KubePivot 第一次在真实 K8s 集群里无人介入自愈。删除 Deployment 后 10 秒内恢复 Running。这是从 v1.0.0 到今天第一次证明整条设计链路成立。**

**和 qc 相处的基本原则**：

- 设计先对齐，再动手。他不喜欢边写边想。
- 他喜欢被推 back，不喜欢被纯认同。有问题直说。
- "只保护，不越权"是贯穿整个项目的哲学，不只是代码。
- `slog` 不用 `log`，`P.Info/Done/Fail` 做进度输出，`make dev` 一键验证。
- 不搞技术债。宁可 TODO + 完整设计也不临时方案。
- commit 格式：`type: 简短描述\n\n- 详细 bullet`
- 爽感：别人说"不对"，他说"对"，然后把事办成让人闭嘴——逆势成立。

---

## 一、项目当前状态

```
版本：v2.2.0
测试：go test ./... -race 全绿（239+ 个）
make dev：build + test + install 一键完成
companion：
  - github.com/Ixecd/web3-blitz（k3s + OrbStack）
  - github.com/Ixecd/Feelings-Server（Feelings 后端）
自愈验证：2026-04-23 15:43:44 真实集群端到端闭环通过
```

---

## 二、关键文件地图

```
cmd/kp/（26+ 个子命令）
├── main.go              # 命令入口，default case → execPlugin 兜底
├── sync.go              # kp sync，三类文件策略
├── deploy.go            # ensureSecret：自动创建 dev Secret + --context 支持
├── runner.go            # runOutput 兼容层：识别 kubectl/helm 前缀 → executor
├── secret.go            # fetchVaultKV: net/http 实现
└── ... (其他保持不变)

internal/
├── executor/executor.go # ⭐ v2.2.0 新增
│                          单例 + 信号量限流（sem=5）
│                          resolveBin() 自动识别 scratch/本机路径
│                          Kubectl / Helm / Generic / CmdKubectl
├── state/state.go       # 13 个状态，etcd key 格式：kubepivot/<project>/<ns>/state
├── controller/
│   ├── heal.go          # 全部 exec.Command → executor.GetExecutor()
│   │                      syncStateRunning 不再强行推 IDLE → RUNNING
│   ├── drift_sync.go    # detectDrift / forceSync 走 executor
│   ├── resources.go     # DetectResourceExists 走 executor
│   ├── sandbox_gc.go    # cleanExpiredSandbox 走 executor
│   │                      forceUnlockIfSandboxState 瘦身（去掉 project/namespace 参数）
│   ├── leader.go        # etcd Leader Election（TTL=15s）
│   ├── workqueue.go     # 三集合 WorkQueue
│   └── ...
└── scaffold/
    ├── helm.go          # writeControllerChart：RBAC 补全 secrets CRUD
    ├── skeleton.go      # Dockerfile 模板：Go 1.26 + scratch
    └── embedded_templates/

build/docker/
├── controller/Dockerfile # scratch + Go 1.26 + helm v3.17.1（硬编码）+ kubectl 动态
└── kp/Dockerfile        # scratch + 只 kp 二进制
```

---

## 三、v2.2.0 关键变更（必读）

### 1. KpExecutor 统一子进程层

**问题**：controller 跑在 scratch 容器，无 PATH 查找机制，裸 `exec.Command("kubectl", ...)` 会失败：
```
exec: "kubectl": executable file not found in $PATH
```

**解决**：`internal/executor/executor.go`

```go
func resolveBin(name string) string {
    abs := "/usr/local/bin/" + name
    if _, err := exec.LookPath(abs); err == nil {
        return abs   // scratch 容器
    }
    if p, err := exec.LookPath(name); err == nil {
        return p     // 本机（支持 Homebrew /opt/homebrew/bin）
    }
    return name
}
```

单例 + sem=5 信号量限流，防止 fork bomb。

### 2. runOutput 兼容层

`cmd/kp/runner.go` 的 `runOutput` 保持旧签名但内部走 executor。所有调用方零改动：

```go
runOutput("kubectl", "--kubeconfig", x, "get", ...)
  → 识别 kubectl 前缀
  → 剥离 --kubeconfig
  → executor.Kubectl(ctx, kubeconfig, "get", ...)
```

### 3. etcd key 破坏性升级

`dtk/<project>/<ns>/state` → `kubepivot/<project>/<ns>/state`

KubePivot 最早叫 dev-toolkit，v1.4.0 改名后路径一直是遗留。v2.2.0 直接破坏性升级（用户数 0 无迁移成本）。

### 4. RBAC 权限补全

helm rollback 失败根因：helm v3 把 release state 存 Secret（type=helm.sh/release.v1），rollback 需 CRUD。旧 RBAC 只给了 get/list/watch。

`internal/scaffold/helm.go` 里 controller chart 的 rbac 模板：
- secrets：完整 CRUD（核心修复）
- deployments/statefulsets/services/configmaps：完整 CRUD
- namespaces：get/list/watch/create
- networking/Ingress + rbac.authorization/roles：完整 CRUD

**注意**：rbac 字符串是 Go 源码里 fmt.Sprintf 拼的，tab 污染过一次。v2.3.0 计划迁移到 embedded template file。

### 5. syncStateRunning 尊重状态机契约

controller 自愈成功后**不再**强行 IDLE → RUNNING。状态机应该由 kp deploy 驱动，controller 只负责资源层自愈。v2.3.0 要加 controller 启动时从 etcd 恢复状态。

---

## 四、架构核心

### 状态机（13 个状态）

```
核心部署：IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                                     ↓
                               ROLLING_BACK → RUNNING

Sandbox：RUNNING/IDLE → LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
                                                                   ↓（失败）
                                                               RESTORING → IDLE

关键约束：COMMITTING 永远禁止 force-unlock
```

状态机转换表定义在 `internal/state/state.go`，`IDLE` 只能去 `INITIALIZING` 或 `LOCKED`，**不能直接到 `RUNNING`**。这是设计上正确的——`RUNNING` 代表"部署流程走完并验证通过"，不是"资源存在"。

### kp sync 文件策略

```
syncForce  → Makefile / scripts/make-rules/ / .githooks/（强制覆盖）
syncMerge  → configs/project.env（只追加新 key）
syncNotify → deployments/ / components.yaml / resources.yaml（提示人工）
syncSkip   → cmd/ / internal/ / migrations/ / go.mod（永远不动）
```

### 核心原则

```
只保护，不越权 / 降级不阻断 / 确定性优先
```

---

## 五、真实集群自愈时间线（v2.2.0 验证）

```
2026-04-23 15:43:20  controller 启动，三副本 Leader Election
2026-04-23 15:43:28  Leader 选出，Reconcile/Drift/GC 三 Loop 启动
2026-04-23 15:43:44  kubectl delete deployment feelings-server
2026-04-23 15:43:44  controller 检测到资源缺失 → auto-heal
2026-04-23 15:43:44  helm rollback revision=4
2026-04-23 15:43:51  Deployment 恢复 Running

耗时 < 10 秒，零告警，零人工介入，日志零 ERROR
```

---

## 六、技术债（诚实清单）

| 优先级 | 描述 | 计划 |
|--------|------|------|
| P0 | controller 启动从 etcd 恢复状态机（消除 IDLE → RUNNING 根因） | v2.3.0 |
| P0 | 全局单一 HA controller（cluster-scoped，label/annotation 发现项目） | v2.3.0 |
| P0 | helm --history-max 限制 revision 堆积（每次自愈 +1，会撑满 secret） | v2.3.0 |
| P1 | rbac.yaml 从字符串拼接重构为 embedded template file | v2.3.0 |
| P1 | Dockerfile 多架构 GitHub API 限流容错 | v2.3.0 |
| P1 | SSA `--field-manager`：helm v4 不支持，用 `--force-conflicts` | helm v4 稳定后 |
| P2 | SIMULATING Job / PVC 快照 / Istio weight / Prometheus | 有对应环境时 |
| P2 | web3-blitz 蓝绿 release 名解析（-blue/-green 后缀） | 顺手修 |
| P3 | Vault SDK / kp sync 真实用户验证 / kp init --type | v2.3.0+ |

---

## 七、常用命令

```bash
cd ~/KubePivot && make dev   # 永远先跑这个

# 新项目端到端
kp init --name myapp --module github.com/me/myapp
cd myapp && make build && make test && make gen && kp deploy

# 框架升级
kp sync --dry-run && kp sync

# 日常
kp deploy / kp status --all-envs / kp diff --drift
kp audit --format table / kp doctor / kp version
LOG_FORMAT=json kp deploy 2>log

# 多架构镜像构建（controller）
docker buildx build --platform linux/amd64,linux/arm64 \
  -f build/docker/controller/Dockerfile \
  -t qingchun22/kubepivot-controller:v2.2.0 \
  -t qingchun22/kubepivot-controller:latest \
  --push --no-cache .
```

---

## 八、下一步

1. **v2.3.0 架构级跃迁**：全局单一 HA controller
   - cluster-scoped，不再 namespace-scoped
   - 通过 `kubepivot.io/managed=true` label / annotation 发现所有项目
   - 根治多项目重复启动 controller 的资源浪费
2. **v2.3.0 controller 启动状态恢复**：从 etcd 读最后一次状态，消除 IDLE → RUNNING 问题
3. **Feelings-Server**：KubePivot 作为底层 CD 平台，第一个真实业务项目已接入
4. **web3-blitz**：蓝绿 release 名解析修复，作为第二个验证场景
5. **社区曝光**：GITOPS-MANIFESTO 已发掘金，v2.2.0 自愈 demo 是下一轮推广素材

---

## 九、Feelings 项目背景

```
~/Feelings-Server/            # v2.2.0 第一个用 KubePivot 的真实业务
├── deployments/feelings-server/kubepivot-controller/  # 已接入 controller
├── cmd/feelings-server/
└── ...

~/Feelings/
├── docs/tech-architecture.md   # 四层架构（硬件/信号/应用/客户端）
├── docs/product-boundary.md    # 个体感受（注入）vs 关系感受（增强）
└── docs/posture-plasticity.md  # 体态可塑性与 Feelings 介入逻辑

Layer 3（应用服务）= Go + KubePivot，是当前重点
Layer 2（信号处理）= Python→C++，将来的硬核问题
Layer 1（硬件固件）= C/Rust，更远期
```

---

乾枢不是名字，是承诺。
2026-04-23 15:43:51，承诺第一次在真实集群里兑现。
