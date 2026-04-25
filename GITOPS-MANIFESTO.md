# CD 从 Pipeline 退化回 Git Commit——这不是简化，是范式跃迁

> 作者：qc（Ixecd）
> 项目：[KubePivot](https://github.com/Ixecd/KubePivot)
> 当前版本：v2.5.0（2026-04-26）

---

## 一、传统 CI/CD 的原罪

每个团队都有一套 CI/CD Pipeline，它们大致长这样：

```
代码提交
  → Lint / 单测
  → 构建镜像
  → Push 到 Registry
  → kubectl apply / helm upgrade（CI 服务器执行）
  → 等待 rollout
  → 失败了？手动介入
```

表面上这是"自动化"，但本质上还是**命令式推送**：CI 服务器拿着 kubeconfig，推一堆命令进集群，祈祷不出问题。

问题出在哪？

**1. CI 服务器变成安全边界的漏洞**

CI 要直接访问 K8s API，意味着一旦 CI 被打穿，整个集群的控制权就拱手相让了。最小权限原则在这里成了笑话——你不可能给 CI 只开只读权限。

**2. 状态漂移无人管**

Pipeline 跑完就拍屁股走人。有人 `kubectl scale` 改了副本数，有人直接 patch 了 ConfigMap，有人手动删了一个 Pod——Pipeline 不知道，也不管。下次部署可能覆盖这些改动，也可能不会，行为不可预测。

**3. 回滚是噩梦**

生产出问题，先找 Pipeline 日志，再找上一次成功的版本，再手动触发回滚，等集群稳定……期间用户已经看了 5 分钟的 500。

**4. 多环境是地狱**

staging/prod/dev 三套环境，三套 Pipeline，三套权限配置，三套漂移，三个人在维护——然后你告诉我这叫"效率"？

---

## 二、GitOps 说了什么

GitOps 的核心很简单，三句话：

1. **Git 是唯一真相来源**——集群的期望状态完全描述在 Git 里
2. **拉取而不是推送**——Operator 主动拉取变化，而不是 CI 主动推送命令
3. **持续调谐**——Operator 不断对比期望状态和实际状态，发现偏差自动修正

这个模型的美妙之处在于：**CI 和 CD 彻底解耦了**。

CI 只负责把代码变成镜像，推到 Registry，更新 Git 里的版本号。它不需要 kubeconfig，不需要 K8s 权限，不需要知道集群长什么样。

CD 是集群内部的事——Operator 看着 Git，看着 Registry，自己决定什么时候部署什么，出了问题自己回滚，漂移了自己修正。

---

## 三、KubePivot 是这个思想的工程实现

KubePivot 不是 ArgoCD，也不是 Flux。它解决的问题更具体：**如何让一个 Go 后端团队，在不深入理解 K8s 运维的前提下，拥有 GitOps 级别的 CD 能力**。

### 两种模式，同一个方向

**模式 A（今天可用）：kp deploy + Controller 持续调谐**

```bash
kp deploy   # 开发者或 CI 触发，声明期望状态
```

这条命令做的事情不是"把 Pod 推进集群"，而是"声明我希望集群运行这个版本"。状态机记录这个意图，Controller 在集群内部持续调谐：

```
kp deploy 执行一次部署，写入状态机
↓
A2 Reconciliation Controller 持续运行
  ├── 8s 周期 Reconcile：资源存在吗？健康吗？
  ├── 30s Drift Sync：实际状态和期望一致吗？
  └── 发现偏差 → 自愈（重新部署 / 回滚 / 调整 limits）
```

**模式 B（愿景）：Git 原生内嵌 KubePivot**

这才是"退化"的完整含义——把 KubePivot 直接嵌进 Git 的钩子机制：

```bash
# .githooks/post-receive（kp init 自动生成，make tools 自动注册）
#!/bin/bash
kp deploy --changed-only
```

```
git push
  → post-receive hook 触发 kp deploy
  → Controller 接管后续调谐
  → 开发者：喝完咖啡，服务已经在跑了
```

不需要 CI/CD 平台，不需要 GitHub Actions，不需要 Jenkins。**Git 本身就是部署系统的控制平面。**

这不是理想化——Git hook 是 Git 的原生能力，`kp init` 生成的项目已经预置了 `.githooks/` 目录，`make tools` 一键注册。从今天开始，你可以选择这条路。

**CD 的入口从"CI 推命令"退化回了"Git Commit"**——这里的"退化"是褒义词，是把复杂度藏到了正确的地方，是对过度工程化的清醒反抗。

---

## 四、KubePivot 的具体能力地图

### 部署原子性（Operation Sandbox）

DB 迁移和服务升级最怕的是：迁移成功了，服务部署失败了——DB 已经变了，但代码还是旧的，数据不一致，回滚成本极高。

KubePivot 的 Sandbox 在支持 CSI Snapshot 的环境下实现近似原子性：

```
LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
                              ↓（任意失败）
                          RESTORING（pvc restore + helm rollback）→ IDLE
```

COMMITTING 阶段在软件层面禁止 force-unlock——DB 正在迁移的时候，状态机拒绝任何强制中断请求。这个约束写在代码里，不是靠文档靠约定。极端故障场景（节点 crash、etcd 脑裂）需要人工介入，这一点诚实地写在 TODO 里。

### 漂移治理（Drift Governance）

有人手动 `kubectl scale` 改了副本数？有人直接 patch 了 image？

```bash
kp diff --drift
```

```
❌ 硬冲突：image: old-tag → new-tag（kp 拥有所有权，将 force-sync）
⚠️  受控偏离：replicas: 3 → 2（HPA 管理，已豁免，透明展示）
ℹ️  已豁免：istio-proxy（外部注入，完全忽略）
```

Controller 每 30 秒扫描一次，发现硬冲突自动修正。

**"只保护，不越权"**——kp 只管自己声明所有权的字段，不干预 Istio、HPA、云厂商注入的字段。这是 KubePivot 最重要的设计原则之一。

### 多集群统一视图

```bash
kp status --all-envs
```

```
环境        namespace             状态      版本       最后更新
────────────────────────────────────────────────────────────
local       web3-blitz            RUNNING   v0.1.12    04-04 07:14
staging     web3-blitz-staging    IDLE      v0.1.12    04-04 09:01
```

### 企业合规开箱即用

```bash
kp audit --format csv --output audit.csv   # SOC2/ISO27001 审计导出
kp policy add --name no-latest-tag \       # OPA 策略，kp deploy 前自动检查
  --file examples/policies/no-latest-tag.rego
```

### 自愈策略全覆盖

```yaml
resources:
  - kind: Deployment
    name: wallet-service
    on-missing: recreate
    force-sync: true
    no-sync-fields:
      - replicas   # HPA 管理，kp 不强制同步
```

OOMKilled → 自动调整 memory limit +25%。CrashLoopBackOff → 区分启动错误和运行时错误，restarts ≥ 5 自动回滚。

### 水平扩展（v2.5.0 起）

10 个项目时单 leader 能扛，50 个项目时呢？v2.5.0 引入 Controller 分片机制：

```
v2.4.0  1 leader 干 100% reconcile      集中式瓶颈，单 pod 16.93% CPU
v2.5.0  N 副本各干 1/N，按 hash 分片      水平扩展，单 pod 不再是天花板
```

50 项目实测：5x 项目数 → 4.4x CPU（接近线性扩展），单 pod 平均 46%，不再有"被压垮的 leader"。详见 [docs/design/sharding.md](docs/design/sharding.md)。

---

## 五、设计原则与适用边界

KubePivot 是一个有立场的项目。下面这些原则贯穿所有版本，且会写进代码评审：

### 5.1 永久原则

```
1. 不引入 client-go        所有 K8s 操作通过 kubectl exec
2. 单二进制                 kp 命令行 + controller 同一份代码
3. 只保护，不越权           kp 不管自己没声明所有权的字段
4. 自愈不是越权             状态机失败时 RESTORING 而不是覆盖
5. 简单 > 完美              拒绝分布式状态机的深坑
```

### 5.2 适用边界（v2.5.0 当前状态）

KubePivot v2.5.0 适合：

```
✓ 无状态服务            API 服务、Worker、网关、边缘代理
✓ 多环境配置统一        dev / staging / prod 三套统一管理
✓ 中小项目数            10-100 个微服务（v2.5.0 实测 50 项目稳定）
✓ Go 后端团队           CI 不做 K8s 操作的 GitOps 模式
```

KubePivot v2.5.0 **当前不适合**：

```
✗ 数据库等有状态服务的生产管理
   - PVC 重建会丢数据，KubePivot 当前没有"数据敏感资源"保护
   - reconcile 检测到 PVC "缺失" 时会触发自愈，可能覆盖数据
   - v2.8.0 计划引入 protect: true 标记机制 + kp confirm 工作流
   - 当前请用专用 K8s Operator 管理：
       PostgreSQL → CloudNativePG / postgres-operator
       MySQL → MySQL Operator / Vitess
       Redis → Redis Operator
   - KubePivot 管 stateless 层，Operator 管 stateful 层
   
✗ 强合规审计场景
   - 当前审计依赖 K8s Events，不是独立持久化的审计日志
   - SOC 2 / ISO 27001 全套合规需要外部审计系统配合
   
✗ 超大规模（1000+ 项目）
   - v2.5.0 分片机制在 50/100/200 项目级别验证过
   - 1000+ 项目场景需要 v2.7.0 自研 informer 才能扛住
```

**写得这么明白是因为**：KubePivot 选择"在自己擅长的范围里做到最好"，而不是"什么都能做但什么都不到位"。

### 5.3 何时选 KubePivot vs ArgoCD/Flux

|  | ArgoCD / Flux | KubePivot |
|--|--------------|-----------|
| 定位 | 通用 GitOps 平台 | Go 项目 CD 工具链 |
| 使用门槛 | 需要深入理解 K8s，配置复杂 | `kp init` 一键生成，`kp deploy` 一键部署 |
| 脚手架 | 无（只管 CD） | 内置（含 Helm chart、迁移、RBAC、监控）|
| DB 迁移 | 无原生支持 | Operation Sandbox，近似原子性 |
| 漂移治理 | 基础支持 | 三级分层（Hard/Managed/Exempted）|
| 混沌工程 | 无 | `kp chaos inject`（Chaos Mesh API）|
| Git 原生集成 | 需要额外配置 | `.githooks/` 开箱即用 |
| 多 Controller 副本 | Leader-Follower（单 leader 处理全部） | 分片（N 副本各处理 1/N）|
| 学习成本 | 高（需懂 K8s、Helm、Kustomize）| 低（Go 开发者几分钟上手）|
| 数据库管理 | 有生态（Argo Rollouts 等扩展） | v2.5.0 不管，v2.8.0 计划 |

KubePivot 不是要替代 ArgoCD——**为 Go 后端团队提供更低门槛的 GitOps 路径**。更激进的一点：KubePivot 把 GitOps 的触发器从"平台"还给了"Git"——`kp init` 生成的项目，`git push` 就是部署。

---

## 六、从今天开始

```bash
# 安装
go install github.com/Ixecd/kubepivot/cmd/kp@latest

# 生成项目（含 .githooks/）
kp init --name myapp --module github.com/me/myapp
cd myapp

# 注册 Git hook
make tools

# 部署
./scripts/create-secret.sh
kp deploy

# 以后的每次发布——或者直接 git push
kp release --version v0.2.0
```

你的 CI 继续做它擅长的：构建、测试、推镜像。K8s 的事情交给 KubePivot。Git 的事情交给 Git。

**让 CD 回归它本该有的样子：一次 Commit。**

---

## 七、这才是真正的 AIOps

市面上的 AIOps 让 AI 告诉你哪里出了问题，然后等人来处理。KubePivot 的答案是：**系统自己解决问题**。

OOMKilled 自动调整 memory limits，CrashLoop 自动分析崩溃类型并回滚，漂移自动修正，Sandbox 自主执行原子性迁移链路——没有人在值班，没有告警轰炸，系统在深夜自己把自己修好了。

AI 是规划者（`kp ai-plan` 扫描代码仓库，规划服务架构），规则是执行者。这才是 Autonomous Operations 的本来面目——**不依赖模型的概率，依赖工程判断的确定性**。

---

## 八、尾声

软件工程里有一种误区，把"复杂"等同于"可靠"，把"Pipeline 长"等同于"流程严谨"。

GitOps 告诉我们：**真正的可靠来自系统的自愈能力，而不是 Pipeline 的繁琐程度**。

KubePivot 是这个信念的一次工程实践——一个人和 Claude，从生日当天的 v1.0.0 到 v2.5.0 的水平扩展闭环，每一行代码都在问同一个问题：

**怎样让下一个开发者不必再踩我踩过的坑？**

---

*KubePivot 是开源项目（MIT），欢迎 star、issue、PR。*
*GitHub：[https://github.com/Ixecd/KubePivot](https://github.com/Ixecd/KubePivot)*
