# 项目交接文档

> 写给下一个 Claude
> 日期：2026-03-29
> 版本：v1.0.0 封神版（最终）

---

## 写在前面

你好。dtk 完成了 v1.0.0，从 v0.3 开始，两天时间，qc 和 Claude 一起把它从零干到了多服务独立 release 全链路跑通，并经过了两个项目的 e2e 完整验证。

这是一个真正有工程价值的工具，不是玩具。

**qc 的工作风格（认真读）**：
- 设计优先，代码其次。不要上来就写代码，先对齐设计再动手
- 每完成一个里程碑：commit → tag → SNAPSHOT → 更新 TODO
- 喜欢被推 back，不喜欢被一味认同。他通常是对的
- `slog` 不用 `log`，kubectl CLI 不用 client-go，严格分包
- 豆包是他女友，会偶尔提供产品/工程建议，认真对待

---

## 一、项目概览

### dev-toolkit（dtk）v1.0.0

**仓库**：github.com/Ixecd/dev-toolkit
**定位**：Go 云原生项目脚手架 + 多服务部署工具

**命令全览**：

```
dtk init          --name <n> --module <m> [--with-frontend]
dtk deploy        [--namespace] [--context] [--dry-run]
dtk resume        从中断点恢复
dtk rollback      手动整组 helm rollback（拓扑逆序）
dtk release       --version v1.0.0 [--deploy] [--push=false]
dtk down          彻底下线，删除所有资源
dtk status        [--history] 查看部署状态
dtk history       [-n 20] 查看状态转换历史
dtk diff          [--from N] [--to M] 对比版本差异
dtk doctor        检查环境依赖
dtk ai-plan       [--suggest-only] [--desc] AI 扫描仓库
dtk controller start  （controller pod 内部运行）
```

**目录结构**：

```
dev-toolkit/
├── cmd/dtk/
│   ├── deploy.go        # executeDeploy：单/多服务分支，goto 留着（有情怀）
│   ├── multi_deploy.go  # deployLayers/deployService/级联 rollback
│   ├── progress.go      # P.Start/Done/Fail/Info 统一进度输出
│   ├── ai_plan.go       # dtk ai-plan
│   ├── runner.go / release.go / down.go
│   ├── status.go / history.go / diff.go / doctor.go
│   ├── ssa.go / preflight.go
│   └── main.go
├── internal/
│   ├── ai/              # Grok/Claude/OpenAI/豆包 + 仓库扫描 + prompt
│   ├── planner/         # DAG + Kahn 拓扑排序 + Downstream（32个单测）
│   ├── scaffold/        # 多 chart 生成（34个单测）
│   ├── state/           # 状态机 FSM（57个单测）
│   └── controller/      # A2 Reconciliation Controller（20个单测）
```

### web3-blitz v0.1.11

**仓库**：github.com/Ixecd/web3-blitz
**定位**：BTC/ETH 充提币系统，dtk 活体验证

**当前 K8s 状态（全部独立 chart）**：

```
namespace: web3-blitz
  web3-blitz-web3-blitz-postgres  （StatefulSet）✅
  web3-blitz-web3-blitz-etcd      （Deployment）✅
  web3-blitz-wallet-service       （Deployment × 2）✅
```

**components.yaml**：

```yaml
components:
  - name: web3-blitz-postgres
    type: statefulset
    port: 5432
    image: ""
  - name: web3-blitz-etcd
    type: deployment
    port: 2379
    image: ""
  - name: wallet-service
    type: deployment
    port: 2113
    image: wallet-service
    replicas: 2
    cpu: 200m
    memory: 256Mi
    depends_on:
      - web3-blitz-postgres
      - web3-blitz-etcd
  - name: chain-miner
    type: deployment
    port: 0
    image: ""
```

### e2e（验证项目）

**仓库**：github.com/Ixecd/e2e
**定位**：dtk 新项目 e2e 验证，3层拓扑

**当前 K8s 状态**：

```
namespace: e2e
  e2e-e2e-postgres  （StatefulSet）✅
  e2e-e2e-etcd      （Deployment）✅
  e2e-e2e           （Deployment）✅
```

---

## 二、多服务部署架构

### components.yaml 格式

```yaml
components:
  - name: postgres
    type: statefulset      # deployment（默认）/ statefulset
    port: 5432
    image: ""              # 空 = 跳过 build/push，使用预置镜像

  - name: wallet-service
    type: deployment
    port: 2113
    image: wallet-service
    depends_on:
      - postgres
      - etcd
```

### 部署流程

```
BuildLayers → 拓扑排序 → []Layer

for each 层级（同层 goroutine 并行，层间串行）：
    deployService：
        ① chart 不存在 → 快速失败
        ② image 为空且无 chart → 跳过（CLI 工具）
        ③ build/push（只做一次）
        ④ helm upgrade --install（最多重试 3 次）
        ⑤ kubectl rollout status
        ↓ 失败
        helmReleaseExists 检查
        级联 rollback（Downstream 逆序）
        整组 rollback
        dtk down
```

### helm release 命名

```
{project}-{service}
web3-blitz-wallet-service
e2e-e2e-postgres
```

### chart 目录结构（dtk init 生成）

```
deployments/{name}/
├── {name}-postgres/     # StatefulSet
├── {name}-etcd/         # Deployment
├── {name}/              # 业务服务（含 initContainers）
└── {name}-controller/   # 默认 disabled，统一镜像 dev-toolkit-controller
```

---

## 三、AI 组件

```bash
export DTK_LLM_PROVIDER=grok
export DTK_LLM_API_KEY=xai-xxx
dtk ai-plan --suggest-only
dtk ai-plan --desc "BTC/ETH 充提币系统，基础设施不要列进来"
```

---

## 四、状态机

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                          ↓              ↓
                    ROLLING_BACK ←───────┘
首次失败 → CLEANING → IDLE | 下线 → TERMINATED
```

---

## 五、controller

所有项目 controller pod 统一命名 `dev-toolkit-controller`，部署在各自 namespace。镜像共用，只需构建一次：

```bash
docker build --no-cache \
  -f build/docker/controller/Dockerfile \
  -t your-registry/dev-toolkit-controller:latest .
```

启用：`deployments/<n>/<n>-controller/values.yaml` → `enabled: true`，填好 image，`dtk deploy`。

---

## 六、接下来（v1.1.0）

1. 构建 `dev-toolkit-controller` 镜像，验证 controller 自愈 e2e
2. `dtk status` 展示每个 release 独立状态
3. controller SSA 冲突处理

**不要急着加功能，先把现有的用好。**

---

## 七、常用命令

```bash
# e2e 项目
cd ~/e2e && dtk deploy

# web3-blitz
cd ~/web3-blitz && dtk deploy

# 测试
cd ~/dev-toolkit && go test ./... -race

# AI 规划
export DTK_LLM_PROVIDER=grok && export DTK_LLM_API_KEY=xai-xxx
dtk ai-plan --suggest-only
```

---

## 八、致下一个 Claude

从 v0.3 到 v1.0.0，两天，143 个单元测试，两个项目 e2e 全链路验证，多服务独立 release，AI 扫描，统一进度输出，文档全量更新。

这是 qc 的工具，他有非常清晰的工程判断力。你的工作是帮他把想法变成代码，而不是替他做决定。

遇到设计问题，先问清楚再动手。
遇到 Bug，先找到根因再给方案。
遇到他说"不对"，认真听。

v1.0.0 已经封神。v1.1.0 继续冲 🚀
