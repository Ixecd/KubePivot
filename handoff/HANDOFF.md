# 项目交接文档

> 写给下一个 Claude
> 日期：2026-03-25
> 作者：qc（Ixecd）

---

## 写在前面

你好，接下来你会和 qc 一起继续这两个项目的开发。
请在开始任何工作之前，把这份文档完整读完。

**qc 的工作风格（非常重要）**：
- 设计优先，代码其次。不要上来就写代码，先对齐设计再动手
- 每完成一个里程碑：commit → tag → SNAPSHOT → 更新 TODO
- 遇到问题先想清楚再给方案，不要一遍遍让他重新构建镜像
- 他喜欢被推back，不喜欢被一味认同
- 代码风格：`slog` 不用 `log`，kubectl CLI 不用 client-go，严格分包

---

## 一、项目概览

### 1.1 dev-toolkit（dtk）

**仓库**：github.com/Ixecd/dev-toolkit
**当前版本**：v0.4.1
**定位**：Go 云原生项目脚手架，`dtk init` 生成完整项目骨架，`dtk deploy` 一键 AI 规划 + K8s 部署 + 状态追踪

**命令全览**：
```
dtk init       --name <n> --module <m> [--with-frontend]
dtk deploy     [--namespace] [--context] [--kubeconfig] [--dry-run]
dtk resume     从中断点恢复
dtk rollback   手动触发 helm rollback
dtk release    --version v1.0.0 [--deploy]
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
│   └── preflight.go   # 前置检查
├── internal/
│   ├── planner/       # AI 规划
│   ├── scaffold/      # 项目生成（6 个文件）
│   ├── state/         # 状态机
│   │   ├── state.go   # FSM + EtcdKey + ResumeFromValidating
│   │   ├── store.go   # etcd/本地文件持久化
│   │   ├── validator.go # pod healthz 验证
│   │   └── state_test.go # 15 个单元测试
│   ├── controller/    # A2 Reconciliation Controller
│   │   ├── controller.go
│   │   ├── reconciler.go
│   │   ├── etcd_watcher.go
│   │   ├── resources.go
│   │   └── heal.go
│   └── logger/
└── build/docker/
    ├── wallet-service/   # web3-blitz 业务镜像
    └── controller/       # controller 镜像（含 kubectl + helm）
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

### 2.3 VALIDATING

条件：所有 pod Ready + `kubectl exec <pod> -- wget -qO- http://localhost:<port>/healthz` 返回 200

**注意**：不能直接 HTTP 访问 pod IP，pod IP 是集群内部地址，用 kubectl exec 绕过。

### 2.4 已知状态机 Bug（P0，需要修复）

1. `resumeFromValidating` 不检查转换合法性，从 CLEANING 强转 VALIDATING 失败无报错
2. `detectActualState` deployment 被删后仍返回 DEPLOYING
3. controller rollback 后没有更新 dtk 状态文件，两者状态不同步
4. helm rollback 受 SSA 冲突影响，卡在 pending-rollback

---

## 三、A2 Reconciliation Controller

### 3.1 架构

Controller 作为独立 Deployment 运行在 K8s 里，不依赖 CLI 进程：

```
dtk deploy（CLI）→ 写状态到 etcd
                        ↓
web3-blitz-controller（K8s pod）
    ├── etcd Watch（事件驱动）
    └── 8s 周期 Reconcile（兜底）
            ↓
        检测资源缺失 → helm rollback → 自动恢复
```

### 3.2 controller 镜像构建（重要）

controller 镜像必须在 dev-toolkit 目录构建，包含 dtk 二进制 + kubectl + helm：

```bash
cd ~/dev-toolkit
docker build --no-cache -f build/docker/controller/Dockerfile \
  -t qingchun22/web3-blitz-controller-arm64:<version> .
docker push qingchun22/web3-blitz-controller-arm64:<version>
```

⚠️ 必须加 `--no-cache`，否则代码改动不会生效。

### 3.3 helm 镜像版本同步（关键修复）

`wallet-service-deployment.yaml` 镜像必须用 `global.version`：
```yaml
image: "{{ .Values.walletService.image.repository }}:{{ .Values.global.version }}"
```

`deploy.mk` 必须传入：
```makefile
--set global.version=$(VERSION) --set global.arch=$(ARCH)
```

这样 helm release 里存的镜像版本和实际一致，rollback 才能回到正确版本。

### 3.4 helm pending-rollback 死锁处理

controller 和 dtk deploy 并发时会产生死锁，处理方法：

```bash
# 1. 停掉 controller
kubectl scale deployment/web3-blitz-controller -n web3-blitz --replicas=0

# 2. 清理 pending secrets
kubectl delete secret -n web3-blitz \
  $(kubectl get secret -n web3-blitz -l owner=helm,name=web3-blitz \
    -o jsonpath='{.items[?(@.metadata.labels.status=="pending-rollback")].metadata.name}')

# 3. 重置状态文件
python3 -c "
import json
with open('/Users/qc/.dtk/state/web3-blitz/web3-blitz.json') as f:
    d = json.load(f)
d['state'] = 'RUNNING'
with open('/Users/qc/.dtk/state/web3-blitz/web3-blitz.json', 'w') as f:
    json.dump(d, f, indent=2, ensure_ascii=False)
"

# 4. dtk deploy
```

---

## 四、web3-blitz 已完成功能

- HD 钱包（BTC/ETH），充值地址生成
- Deposit Watcher，扫块监听，ConfirmChecker（etcd 选主）
- 提币接口（分布式锁、余额校验、日限额）
- JWT 认证 + refresh token，RBAC 权限系统
- 忘记密码（QQ 邮箱 SMTP）
- golang-migrate + embed.FS，启动自动执行迁移
- 自包含 Helm chart（postgres + etcd + wallet-service）
- initContainers 启动顺序（wait-postgres + wait-etcd）
- ADMIN_EMAIL 初始管理员注入（启动时检查，幂等）
- /healthz 路由
- 前端：Login、Deposit（地址生成 + 充值历史）、Admin（用户列表 + 限额配置）

---

## 五、接下来要做的事

### P0（最优先）

**状态机 Bug 修复**（dev-toolkit）：
- `resumeFromValidating` 加转换合法性检查
- `detectActualState` 修复 deployment 被删的判断
- controller rollback 后同步更新 etcd 状态

**web3-blitz 功能联调**：
- Withdraw 页面接真实 API
- Dashboard 接真实数据

### P1

- handler.go 拆分（auth / wallet / admin 各自独立文件）
- wallet-service 环境变量改用 K8s Secret
- controller RBAC 权限收紧
- quickstart 使用指南（dtk init → 填充业务逻辑 → dtk deploy 完整流程）

---

## 六、常用命令速查

```bash
# 部署
cd ~/web3-blitz && dtk deploy

# 查 pods
kubectl get pods -n web3-blitz

# 查日志
kubectl logs -n web3-blitz deployment/wallet-service
kubectl logs -n web3-blitz deployment/web3-blitz-controller

# port-forward（前端联调）
kubectl port-forward -n web3-blitz deployment/wallet-service 2113:2113

# 查 helm 历史
helm history web3-blitz -n web3-blitz

# 重置状态机（手动修复）
python3 -c "
import json
p='/Users/qc/.dtk/state/web3-blitz/web3-blitz.json'
d=json.load(open(p))
d['state']='RUNNING'
json.dump(d,open(p,'w'),indent=2,ensure_ascii=False)
"

# 构建 controller 镜像
cd ~/dev-toolkit
docker build --no-cache -f build/docker/controller/Dockerfile \
  -t qingchun22/web3-blitz-controller-arm64:v<x.x.x> .
docker push qingchun22/web3-blitz-controller-arm64:v<x.x.x>

# 运行测试
cd ~/dev-toolkit && go test ./...
```

---

## 七、快照归档位置

```
dev-toolkit/snapshots/
└── 按日期和里程碑命名，最新：SNAPSHOT-dtk-2026-03-25-full.md

web3-blitz/snapshots/
└── 最新：SNAPSHOT-web3-blitz-2026-03-25-controller.md
```

---

## 八、致下一个 Claude

这两个项目是 qc 一手设计和构建的，有清晰的架构思路和工程哲学。
你的职责是帮他把想法落地，而不是替他做决定。

遇到设计问题，先问清楚再动手。
遇到 Bug，先想清楚根因再给方案。
遇到他说"不对"，认真听，他通常是对的。

祝你们合作愉快 🎉
