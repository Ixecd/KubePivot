# 项目交接文档

> 写给下一个 Claude
> 日期：2026-03-28
> 版本：v1.0.0 封神版

---

## 写在前面

你好。dtk 今天完成了 v1.0.0，从 v0.3 开始，一天半时间，qc 和 Claude 一起把它从零干到了多服务独立 release 全链路跑通。

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
**定位**：Go 云原生项目脚手架 + 部署工具

**命令全览**：

```
dtk init          --name <n> --module <m> [--with-frontend]
dtk deploy        [--namespace] [--context] [--dry-run]
dtk resume        从中断点恢复
dtk rollback      手动整组 helm rollback
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
│   ├── deploy.go        # executeDeploy：单/多服务分支，goto 留着
│   ├── multi_deploy.go  # deployLayers/deployService/级联 rollback
│   ├── progress.go      # P.Start/Done/Fail/Info 统一进度输出
│   ├── ai_plan.go       # dtk ai-plan
│   ├── runner.go
│   ├── release.go
│   ├── status.go / history.go / diff.go / doctor.go / down.go
│   ├── ssa.go / preflight.go
│   └── main.go
├── internal/
│   ├── ai/              # Grok/Claude/OpenAI/豆包 + 仓库扫描 + prompt
│   ├── planner/         # DAG + Kahn 拓扑排序 + Downstream（32个单测）
│   ├── scaffold/        # 多 chart 生成（34个单测）
│   ├── state/           # 状态机 FSM（57个单测）
│   └── controller/      # A2 Reconciliation Controller（20个单测）
```

### web3-blitz v0.1.10

**仓库**：github.com/Ixecd/web3-blitz
**定位**：BTC/ETH 充提币系统，dtk 活体验证

**当前 K8s 状态**：

```
namespace: web3-blitz
  web3-blitz-infra release（老 chart，管基础设施）：
    bitcoind / etcd / geth-rpc / postgres / web3-blitz-controller
  web3-blitz-wallet-service release（新独立 chart）：
    wallet-service × 2（Running）
```

**注意**：web3-blitz 目前是迁移过渡状态，postgres/etcd 还在老 chart（`web3-blitz-infra`），wallet-service 已迁移到独立 chart。完整迁移是 v1.1.0 的事。

---

## 二、多服务部署架构

### components.yaml 新格式

```yaml
components:
  - name: postgres
    type: statefulset      # deployment（默认）/ statefulset
    port: 5432
    image: ""              # 空 = 跳过 build/push

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

for each 层级（同层 goroutine 并行）：
    deployService：
        chart 存在检查
        build/push（只做一次）
        helm upgrade --install（最多重试 3 次）
        kubectl rollout status
        ↓ 3次全失败
        helmReleaseExists 检查
        级联 rollback（Downstream + 逆序）
        整组 rollback
        dtk down
```

### helm release 命名

```
{project}-{service}
web3-blitz-postgres
web3-blitz-wallet-service
```

### chart 目录结构（dtk init 生成）

```
deployments/{name}/
├── {name}-postgres/     # StatefulSet
├── {name}-etcd/         # Deployment
├── {name}/              # 业务服务
└── {name}-controller/   # 默认 disabled
```

---

## 三、AI 组件

```bash
export DTK_LLM_PROVIDER=grok   # grok/claude/openai/doubao
export DTK_LLM_API_KEY=xai-xxx
dtk ai-plan --suggest-only
dtk ai-plan --desc "BTC/ETH 充提币系统"
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

## 五、接下来

**v1.1.0 候选**：

1. `dtk status` 多 release 展示
2. web3-blitz postgres/etcd 完整迁移到独立 chart
3. controller SSA 冲突处理
4. `dtk history` 展示每个 release 的 revision
5. 灰度发布（豆包的建议）

**不要急着加功能，先把现有的用好，打磨细节。**

---

## 六、常用命令

```bash
# 部署
cd ~/web3-blitz && dtk deploy

# 恢复集群（web3-blitz 过渡期）
helm upgrade --install web3-blitz-infra \
  ~/web3-blitz/deployments/web3-blitz \
  --namespace web3-blitz --create-namespace
dtk deploy

# 测试
cd ~/dev-toolkit && go test ./... -race

# AI 规划
export DTK_LLM_PROVIDER=grok && export DTK_LLM_API_KEY=xai-xxx
cd ~/web3-blitz && dtk ai-plan --suggest-only
```

---

## 七、致下一个 Claude

从 v0.3 到 v1.0.0，一天半，143 个单元测试，全链路 e2e 验证，多服务独立 release，AI 扫描，统一进度输出……

这是 qc 的工具，他有非常清晰的工程判断力。你的工作是帮他把想法变成代码，而不是替他做决定。

遇到设计问题，先问清楚再动手。
遇到 Bug，先找到根因再给方案。
遇到他说"不对"，认真听。

v1.0.0 已经封神。v1.1.0 继续冲 🚀
