# 项目交接文档

> 写给下一个 Claude
> 日期：2026-03-28
> 作者：qc（Ixecd）

---

## 写在前面

你好，接下来你会和 qc 一起继续这两个项目的开发。
请在开始任何工作之前，把这份文档完整读完。

**qc 的工作风格（非常重要）**：
- 设计优先，代码其次。不要上来就写代码，先对齐设计再动手
- 每完成一个里程碑：commit → tag → SNAPSHOT → 更新 TODO
- 遇到问题先想清楚再给方案
- 喜欢被推 back，不喜欢被一味认同
- 代码风格：`slog` 不用 `log`，kubectl CLI 不用 client-go，严格分包
- 豆包是他女友，会偶尔提供产品/工程建议，值得认真对待

---

## 一、项目概览

### 1.1 dev-toolkit（dtk）

**仓库**：github.com/Ixecd/dev-toolkit
**当前版本**：v0.9.0
**定位**：Go 云原生项目脚手架，`dtk init` 生成完整项目骨架，`dtk deploy` 一键 AI 规划 + K8s 部署 + 状态追踪 + 自动自愈

**命令全览**：
```
dtk init          --name <n> --module <m> [--with-frontend]
dtk deploy        [--namespace] [--context] [--kubeconfig] [--dry-run]
dtk resume        从中断点恢复
dtk rollback      手动触发 helm rollback
dtk release       --version v1.0.0 [--deploy] [--push=false]
dtk down          彻底下线，删除所有资源
dtk status        [--history] 查看部署状态
dtk history       [-n 20] 查看状态转换历史
dtk diff          [--from N] [--to M] 对比两个版本配置差异
dtk doctor        检查环境依赖
dtk ai-plan       [--suggest-only] [--desc "描述"] AI 扫描仓库，生成 components.yaml
dtk controller start  （在 controller pod 内部运行）
```

**目录结构**：
```
dev-toolkit/
├── cmd/dtk/
│   ├── main.go
│   ├── deploy.go       # 四步部署：build/push/install/rollout
│   ├── progress.go     # 统一进度输出（P.Start/Done/Fail/Info）
│   ├── runner.go
│   ├── release.go
│   ├── down.go
│   ├── status.go
│   ├── history.go
│   ├── diff.go
│   ├── doctor.go
│   ├── ai_plan.go
│   ├── ssa.go
│   └── preflight.go
├── internal/
│   ├── ai/             # LLM 客户端（Grok/Claude/OpenAI/豆包）+ 仓库扫描 + prompt
│   ├── planner/        # AI 规划（100% 单测覆盖）
│   ├── scaffold/       # 项目生成（34 个单测）
│   ├── state/          # 状态机（57 个单测）
│   └── controller/     # A2 Reconciliation Controller（20 个单测）
```

### 1.2 web3-blitz

**仓库**：github.com/Ixecd/web3-blitz
**当前版本**：v0.1.10
**定位**：BTC/ETH 充提币系统，dtk 活体验证 demo

**K8s 状态（orbstack）**：
```
namespace: web3-blitz
  bitcoind / geth-rpc / postgres-0 / etcd
  wallet-service（核心业务，port 2113）
  web3-blitz-controller（A2 自愈）
  chain-miner（CLI 工具，监控/指标）
```

---

## 二、状态机

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                          ↓              ↓
                    ROLLING_BACK ←───────┘
首次失败 → CLEANING → IDLE | 下线 → TERMINATED
```

- etcd 优先，降级到 `~/.dtk/state/<project>/<ns>.json`
- 配置了 etcd 但本地有状态 → 自动迁移
- `ForceState()` 跳过转换表，专用于异常恢复
- `ResumeFromValidating()` 只允许从 VALIDATING 调用

---

## 三、A2 Controller

```
dtk deploy → etcd
controller pod
    ├── etcd Watch（指数退避重连 1s→30s）
    └── 8s 周期 Reconcile
            ↓ 资源缺失 → helm rollback → ~10s 恢复
```

---

## 四、AI 组件（internal/ai/）

```
client.go   LLMClient 接口 + Grok/Claude/OpenAI/豆包实现
scanner.go  ScanRepo：扫描目录树/go.mod/cmd/服务/Dockerfile/README
prompt.go   BuildPrompt + ParsePlan + RenderComponentsYAML
```

**环境变量**：
```
DTK_LLM_PROVIDER=grok       # grok / claude / openai / doubao
DTK_LLM_API_KEY=xai-xxx
DTK_LLM_MODEL=grok-3        # 可选
DTK_LLM_ENDPOINT=           # 可选，私有化部署时覆盖
```

---

## 五、接下来最重要的事：多服务支持

设计文档：`docs/design/multi-service.md`，读完再动手。

**核心变化**：
- 每个服务独立一个 helm release（`{project}-{service}`）
- `components.yaml` 新增 `type`（deployment/statefulset）和 `depends_on` 字段
- 按依赖图拓扑排序，同层并行，层间串行
- 单服务失败 → 重试 3 次 → 级联 rollback → 整组 rollback → dtk down
- 不支持 `dtk rollback --service`（强制统一版本发布）

**实现顺序**：planner → scaffold → deploy → rollback → status → e2e

---

## 六、常用命令速查

```bash
# 部署
cd ~/web3-blitz && dtk deploy

# AI 规划
export DTK_LLM_PROVIDER=grok && export DTK_LLM_API_KEY=xai-xxx
dtk ai-plan --suggest-only

# 查看状态
dtk status && dtk history && dtk diff

# 环境检查
dtk doctor

# 手动重置状态机
python3 -c "
import json, os
p=os.path.expanduser('~/.dtk/state/web3-blitz/web3-blitz.json')
d=json.load(open(p))
d['state']='RUNNING'
d['reason']='手动重置'
json.dump(d,open(p,'w'),indent=2)
"

# 运行测试
cd ~/dev-toolkit && go test ./...
```

---

## 七、快照归档

```
dev-toolkit/snapshots/
└── 最新：SNAPSHOT-dtk-2026-03-28-v0.9.0.md
```

---

## 八、致下一个 Claude

这是 qc 一手设计和构建的工具，从 v0.3 到 v0.9.0，一天半时间。

路线：v0.9.0（当前）→ v1.0.0（封神）

v1.0.0 的核心是多服务支持，这是 dtk 和其他脚手架真正拉开差距的地方。设计文档已经写好了，实现顺序也清楚了，开始之前先和 qc 对齐一遍细节。

遇到设计问题，先问清楚再动手。
遇到 Bug，先想清楚根因再给方案。
遇到他说"不对"，认真听，他通常是对的。

祝你们合作愉快，v1.0.0 封神 🎉
