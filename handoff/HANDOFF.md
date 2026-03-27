# 项目交接文档

> 写给下一个 Claude
> 日期：2026-03-27
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

---

## 一、项目概览

### 1.1 dev-toolkit（dtk）

**仓库**：github.com/Ixecd/dev-toolkit
**当前版本**：v0.5.1
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
dtk doctor        检查环境依赖
dtk controller start  （在 controller pod 内部运行）
```

**目录结构**：
```
dev-toolkit/
├── cmd/dtk/
│   ├── main.go        # CLI 入口
│   ├── deploy.go      # runDeploy/runResume/runRollback
│   ├── runner.go      # kubectl/helm 辅助
│   ├── release.go     # runRelease
│   ├── down.go        # runDown
│   ├── status.go      # runStatus
│   ├── doctor.go      # runDoctor
│   └── preflight.go   # 前置检查（含 checkPendingRollback）
├── internal/
│   ├── planner/       # AI 规划
│   ├── scaffold/      # 项目生成（scaffold.go + helm.go + handoff.go + ai_coding_guide.go 等）
│   ├── state/         # 状态机
│   │   ├── state.go   # FSM + EtcdKey + ResumeFromValidating + ForceState
│   │   ├── store.go   # etcd/本地文件持久化
│   │   ├── validator.go
│   │   └── state_test.go  # 47 个单元测试
│   ├── controller/    # A2 Reconciliation Controller
│   │   ├── controller.go
│   │   ├── reconciler.go
│   │   ├── etcd_watcher.go
│   │   ├── resources.go   # DetectResourceExists / DetectActualState / LoadResources
│   │   └── heal.go
│   └── logger/
└── build/docker/
    ├── wallet-service/
    └── controller/
```

### 1.2 web3-blitz

**仓库**：github.com/Ixecd/web3-blitz
**当前版本**：v0.1.10
**定位**：Go + K8s 充提币系统，BTC/ETH，作为 dtk 的活体验证 demo

**K8s 环境（orbstack 本地集群）**：
```
namespace: web3-blitz
pods：
  bitcoind              BTC 节点（regtest）
  geth-rpc              ETH 节点（dev mode）
  postgres-0            业务数据库（StatefulSet + PVC）
  etcd                  分布式协调
  wallet-service        核心业务服务
  web3-blitz-controller A2 Reconciliation Controller
```

---

## 二、状态机设计

### 2.1 状态流转

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                          ↓              ↓
                    ROLLING_BACK ←───────┘
首次失败 → CLEANING → IDLE
下线   → TERMINATED
```

### 2.2 持久化

- etcd 优先（key: `dtk/<project>/<ns>/state`）
- 无 etcd 降级到 `~/.dtk/state/<project>/<ns>.json`

### 2.3 特殊方法

- `ResumeFromValidating`：只允许从 VALIDATING 调用，转到 RUNNING
- `ForceState`：跳过转换表，强制设置状态，专用于异常恢复（如 pending-rollback 清理后重置）

### 2.4 已知遗留问题

| # | 问题 | 优先级 |
|---|------|--------|
| 1 | `startEtcdWatcher` 断线后不重连 | P1 |
| 2 | SSA 冲突自动清除未实现 | P1 |
| 3 | controller 自愈流程未端到端验证 | P1 |
| 4 | controller 单元测试缺失 | P1 |

---

## 三、A2 Reconciliation Controller

### 3.1 架构

```
dtk deploy（CLI）→ 写状态到 etcd
                        ↓
web3-blitz-controller（K8s pod）
    ├── etcd Watch（事件驱动）
    └── 8s 周期 Reconcile（兜底）
            ↓
        检测资源缺失 → helm rollback → 自动恢复
```

### 3.2 职责边界（重要）

- `internal/state`：纯 FSM，零 K8s 依赖
- `internal/controller`：K8s 检测 + 自愈，`DetectResourceExists` / `DetectActualState` 在这里
- `cmd/dtk`：CLI 入口，引用两个包

### 3.3 controller 镜像构建

```bash
cd ~/dev-toolkit
docker build --no-cache -f build/docker/controller/Dockerfile \
  -t qingchun22/web3-blitz-controller-arm64:<version> .
docker push qingchun22/web3-blitz-controller-arm64:<version>
```

### 3.4 helm pending-rollback 死锁

controller 和 dtk deploy 并发时会产生死锁。dtk deploy 现在会自动检测并提示处理，详见 `docs/guide/zh-CN/gotchas.md`。

---

## 四、scaffold 生成内容

`dtk init` 生成的项目包含：

```
configs/
├── project.env         # 部署配置
├── components.yaml     # AI 规划输入
└── resources.yaml      # controller 监控资源列表

deployments/{name}/
├── templates/
│   ├── deployment.yaml          # 业务服务（含 initContainers）
│   ├── {name}-postgres-*.yaml   # postgres，enabled 开关
│   ├── {name}-etcd-*.yaml       # etcd，enabled 开关
│   ├── controller-*.yaml        # controller 骨架，默认 disabled
│   └── NOTES.txt                # 部署后组件状态展示
└── values.yaml                  # 含 postgres/etcd/controller enabled 开关

handoff/
├── HANDOFF.md           # 项目上下文（本文件格式）
└── AI-CODING-GUIDE.md   # AI 编码约束指南
```

---

## 五、接下来要做的事

### P1（下一步）

**稳定性**：
- `startEtcdWatcher` 断线重连
- SSA 冲突自动清除
- controller 单元测试

**功能**：
- `dtk history` 查看版本历史
- 多服务支持

### P2

- `ARCH` 自动检测
- 统一进度输出格式
- `dtk init --dry-run`

---

## 六、常用命令速查

```bash
# 部署
cd ~/web3-blitz && dtk deploy

# 查看状态
dtk status
dtk status --history

# 环境检查
dtk doctor

# 查 pods
kubectl get pods -n web3-blitz

# 查日志
kubectl logs -n web3-blitz deployment/wallet-service
kubectl logs -n web3-blitz deployment/web3-blitz-controller

# port-forward
kubectl port-forward -n web3-blitz deployment/wallet-service 2113:2113

# 查 helm 历史
helm history web3-blitz -n web3-blitz

# 手动重置状态机
python3 -c "
import json, os
p=os.path.expanduser('~/.dtk/state/web3-blitz/web3-blitz.json')
d=json.load(open(p))
d['state']='RUNNING'
d['reason']='手动重置'
json.dump(d,open(p,'w'),indent=2)
"

# 构建 controller 镜像
cd ~/dev-toolkit
docker build --no-cache -f build/docker/controller/Dockerfile \
  -t qingchun22/web3-blitz-controller-arm64:v<x.x.x> .

# 运行测试
cd ~/dev-toolkit && go test ./...
```

---

## 七、快照归档位置

```
dev-toolkit/snapshots/
└── 最新：SNAPSHOT-dtk-2026-03-27-v0.5.1.md

web3-blitz/snapshots/
└── 最新：SNAPSHOT-web3-blitz-2026-03-25-controller.md
```

---

## 八、致下一个 Claude

这两个项目是 qc 一手设计和构建的，架构思路清晰，工程哲学严格。

遇到设计问题，先问清楚再动手。
遇到 Bug，先想清楚根因再给方案。
遇到他说"不对"，认真听，他通常是对的。

路线图：v0.5.1 → v0.8.0（稳到无坑）→ v0.9.0（体验拉满）→ v1.0.0（封神）

祝你们合作愉快 🎉
