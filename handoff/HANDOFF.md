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
**当前版本**：v0.6.0
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
│   ├── history.go     # runHistory
│   ├── doctor.go      # runDoctor
│   ├── ssa.go         # SSA 冲突检测与修复
│   └── preflight.go   # 前置检查（含 checkPendingRollback）
├── internal/
│   ├── planner/       # AI 规划
│   ├── scaffold/      # 项目生成
│   │   ├── scaffold.go
│   │   ├── helm.go
│   │   ├── handoff.go
│   │   ├── ai_coding_guide.go
│   │   └── ...
│   ├── state/         # 状态机
│   │   ├── state.go   # FSM + ForceState + ResumeFromValidating
│   │   ├── store.go   # etcd/本地文件 + 自动迁移
│   │   ├── validator.go
│   │   └── state_test.go  # 47 个单元测试
│   ├── controller/    # A2 Reconciliation Controller
│   │   ├── controller.go
│   │   ├── reconciler.go
│   │   ├── etcd_watcher.go  # 指数退避重连
│   │   ├── resources.go     # DetectResourceExists / DetectActualState
│   │   └── heal.go
│   └── logger/
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
- 配置了 etcd 但本地有状态 → 自动迁移到 etcd（state.New() 内检测）

### 2.3 特殊方法

- `ResumeFromValidating`：只允许从 VALIDATING 调用
- `ForceState`：跳过转换表，专用于异常恢复（如 pending-rollback 清理）

---

## 三、A2 Reconciliation Controller

### 3.1 架构

```
dtk deploy（CLI）→ 写状态到 etcd
                        ↓
web3-blitz-controller（K8s pod）
    ├── etcd Watch（事件驱动，断线自动指数退避重连）
    └── 8s 周期 Reconcile（兜底）
            ↓
        检测资源缺失 → helm rollback → 自动恢复（10s 内）
```

### 3.2 职责边界

- `internal/state`：纯 FSM，零 K8s 依赖
- `internal/controller`：K8s 检测 + 自愈
- `cmd/dtk`：CLI 入口

---

## 四、接下来要做的事

### P1（下一步）

- controller 单元测试
- 多服务支持
- `dtk diff` 版本对比
- 边界 case 加固（pending-install/failed、镜像不存在）
- web3-blitz deploy.mk 同步 dtk 最新版本

### P2

- `ARCH` 自动检测
- 统一进度输出格式
- `dtk init --dry-run`

---

## 五、常用命令速查

```bash
# 部署
cd ~/web3-blitz && dtk deploy

# 查看状态
dtk status
dtk status --history
dtk history
dtk history -n 5

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

## 六、快照归档位置

```
dev-toolkit/snapshots/
└── 最新：SNAPSHOT-dtk-2026-03-27-v0.6.0.md

web3-blitz/snapshots/
└── 最新：SNAPSHOT-web3-blitz-2026-03-25-controller.md
```

---

## 七、致下一个 Claude

这两个项目是 qc 一手设计和构建的，架构思路清晰，工程哲学严格。

遇到设计问题，先问清楚再动手。
遇到 Bug，先想清楚根因再给方案。
遇到他说"不对"，认真听，他通常是对的。

路线图：v0.6.0（当前）→ v0.8.0（稳到无坑）→ v0.9.0（体验拉满）→ v1.0.0（封神）

祝你们合作愉快 🎉
