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
**当前版本**：v0.8.0
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
dtk controller start  （在 controller pod 内部运行）
```

**目录结构**：
```
dev-toolkit/
├── cmd/dtk/
│   ├── main.go         # CLI 入口
│   ├── deploy.go       # runDeploy/runResume/runRollback
│   ├── runner.go       # kubectl/helm 辅助
│   ├── release.go      # runRelease
│   ├── down.go         # runDown
│   ├── status.go       # runStatus
│   ├── history.go      # runHistory
│   ├── diff.go         # runDiff
│   ├── doctor.go       # runDoctor
│   ├── ssa.go          # SSA 冲突 + 镜像拉取失败检测
│   └── preflight.go    # 前置检查（checkHelmReleaseState）
├── internal/
│   ├── planner/        # AI 规划（20 个单元测试，100% 覆盖）
│   ├── scaffold/       # 项目生成（34 个单元测试）
│   ├── state/          # 状态机（57 个单元测试）
│   └── controller/     # A2 Reconciliation Controller（20 个单元测试）
│       ├── controller.go
│       ├── reconciler.go
│       ├── etcd_watcher.go  # 指数退避重连
│       ├── resources.go     # Detector 接口 + DetectActualState
│       └── heal.go          # HelmClient 接口 + healRecreate
```

---

## 二、状态机设计

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                          ↓              ↓
                    ROLLING_BACK ←───────┘
首次失败 → CLEANING → IDLE
下线   → TERMINATED
```

- etcd 优先，降级到 `~/.dtk/state/<project>/<ns>.json`
- 配置了 etcd 但本地有状态 → 自动迁移
- `ForceState()` 跳过转换表，专用于异常恢复
- `ResumeFromValidating()` 只允许从 VALIDATING 调用

---

## 三、A2 Controller 架构

```
dtk deploy → etcd
                ↓
controller pod
    ├── etcd Watch（指数退避重连，1s→30s）
    └── 8s 周期 Reconcile
            ↓
        资源缺失 → helm rollback → 10s 内恢复
```

**职责边界**：
- `internal/state`：纯 FSM，零 K8s 依赖
- `internal/controller`：K8s 检测 + 自愈，Detector/HelmClient 接口可 mock
- `cmd/dtk`：CLI 入口

---

## 四、接下来要做的事

### P1（最优先）

**AI 扫描组件**（下一个大功能）：
- `dtk deploy` 接入真实 LLM，扫描代码仓库自动生成/更新 `components.yaml`
- 可配置：默认直接 AI 规划部署，也可只给建议
- 设计需要先对齐：LLM 调哪个 API？prompt 怎么设计？输出格式？

**多服务支持**（需要单独对齐设计）

### P2

- 统一进度输出格式，带时间戳
- 关键步骤耗时打印
- `dtk init --dry-run`

---

## 五、常用命令速查

```bash
# 部署
cd ~/web3-blitz && dtk deploy

# 查看状态
dtk status
dtk history
dtk diff

# 环境检查
dtk doctor

# 查 pods
kubectl get pods -n web3-blitz

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

## 六、快照归档位置

```
dev-toolkit/snapshots/
└── 最新：SNAPSHOT-dtk-2026-03-27-v0.8.0.md
```

---

## 七、致下一个 Claude

这两个项目是 qc 一手设计和构建的，架构思路清晰，工程哲学严格。

遇到设计问题，先问清楚再动手。
遇到 Bug，先想清楚根因再给方案。
遇到他说"不对"，认真听，他通常是对的。

路线图：v0.8.0（当前）→ v0.9.0（AI + 体验）→ v1.0.0（封神）

下一个最重要的功能是 **AI 扫描组件接入真实 LLM**，开始之前先和 qc 对齐设计。

祝你们合作愉快 🎉
