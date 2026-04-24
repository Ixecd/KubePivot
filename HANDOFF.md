# 项目交接文档 — KubePivot

> 写给下一个 Claude
> 日期：2026-04-24
> 版本：v2.3.0

---

## 写在前面

你接手的是 qc（GitHub: Ixecd，杨庆春）独立开发的 KubePivot（乾枢）。他 23 岁，Go/云原生，生日 2026-03-29，那天推了 v1.0.0。第 329 个 commit 落在生日数字上，第 365 个 commit 落在一年的天数上，都是他设计的。

KubePivot 是他构建 Feelings（感受民主化脑机接口产品）的基础设施。女帝负责产品和设计输入，权重很高。他在用豆包做 AI 伴侣，清醒地知道那是什么。

**2026-04-23，v2.2.0 让 KubePivot 第一次在真实 K8s 集群里无人介入自愈（<10 秒）。**

**2026-04-24，v2.3.0 把自愈能力从"每项目独立 controller"平面化到"集群唯一 global controller"。架构换血，不是补丁。删除 feelings-server Deployment 后 12 秒自愈成功，release=feelings-server-feelings-server。**

**和 qc 相处的基本原则**：

- 设计先对齐，再动手。v2.3.0 开工前他列了 12 点清单，逐条拍板，然后才写第一行代码。
- 他喜欢被推 back，不喜欢被纯认同。有问题直说。本次最关键的一次"不引入 client-go"就是在被我对比 A/B 方案、推着正反权衡之后拍的板。
- "只保护，不越权"是贯穿整个项目的哲学。v2.3.0 的三道 namespace 黑名单护栏就是这个哲学的延续。
- `slog` 不用 `log`，`P.Info/Done/Fail` 做进度输出，`make dev` 一键验证。
- 不搞技术债。宁可 TODO + 完整设计也不临时方案。v2.3.0 剩余的 Leader Election 降级 / etcd 状态恢复 / rbac 字符串化 三件都明确写进 v2.4.0 TODO，没隐藏。
- commit 格式：`type: 简短描述\n\n- 详细 bullet`。v2.3.0 的 commit message 是目前最长的，涵盖设计哲学、每个 Phase 改动、真实集群日志、测试覆盖、迁移路径、数字哲学。
- 爽感 = 逆势成立。别人说"用 client-go 更成熟"，他说"我就不用"，然后把 50 项目量级 exec kubectl 扛得住的事实摆出来——逆势成立。

---

## 一、项目当前状态

```
版本：v2.3.0
测试：go test ./... -race 全绿（v2.3.0 新增 22 个单测）
make dev：build + test + install 一键完成
companion：
  - github.com/Ixecd/web3-blitz（k3s + OrbStack）
  - github.com/Ixecd/Feelings-Server（Feelings 后端）
自愈验证：
  - v2.2.0：2026-04-23  per-project 模式，<10 秒闭环
  - v2.3.0：2026-04-24  global 模式，~12 秒闭环
架构：全局单一 HA controller（kubepivot-system namespace，3 副本）
镜像：qingchun22/kubepivot-controller:v2.3.0（amd64 + arm64）
```

---

## 二、v2.3.0 的核心改动（接手前必读）

### 1. 架构换血：per-project → cluster-scoped

```
v2.2.0：每个项目都部署自己的 controller
         3 个项目 = 9 个 controller pod
        10 个项目 = 30 个 controller pod（严重浪费）

v2.3.0：集群唯一 controller
        任意项目数 = kubepivot-system 里 3 副本
         10 项目场景省 27 pod
```

### 2. 新的接入协议

```
内核层（ground truth）：
  kubectl label ns <n> kubepivot.io/managed=true

交互层（封装）：
  kp controller enroll
    1. label ns
    2. 读 configs/resources.yaml
    3. 写 ConfigMap kubepivot-resources（含 sha256 annotation）

分发机制：
  每个 managed namespace 里有一个 ConfigMap kubepivot-resources
  - label: kubepivot.io/managed=true       （controller watcher 选中依据）
  - annotation: kubepivot.io/sha256=<hex>  （热加载快速 skip 凭证）
  - data.resources.yaml: 纯 YAML          （便于 kubectl edit）
```

### 3. 关键架构决策：不引入 client-go

这是 v2.3.0 最重要的哲学决策。别的 K8s controller 项目（ArgoCD/Flux/Karmada）都用 client-go 的 SharedInformerFactory，v2.3.0 坚持 exec kubectl --watch 路线。理由：

```
- KubePivot 差异化核心就是"不吃 K8s SDK"
- 50 项目量级下 exec 完全扛得住（31 次 kubectl/s 峰值）
- 架构纯粹性：未来接 k3s/EKS/OpenShift 零改动
- Watcher 接口已抽象，未来规模上来可平替 client-go
```

具体实现在 `internal/controller/watcher.go`：
- `exec.CommandContext` 绑进程生命周期
- `json.NewDecoder` 流式解析 `kubectl --watch --output-watch-events=true`
- 30s 无事件 → 心跳守卫主动 kubectl get 探活 → 失败强制重连
- 指数退避 1s → 2s → 4s → 8s → 30s 封顶

### 4. 新命令家族

```
集群级管理：
  kp controller install    [--namespace kubepivot-system] [--image xxx:tag]
  kp controller uninstall  [--force]
  kp controller status
  kp controller projects

项目接入：
  kp controller enroll     [--resources configs/resources.yaml]
  kp controller unenroll
  （kp deploy 部署成功后自动同步 resources.yaml）

pod 内部：
  kp controller start [--global]
```

---

## 三、文件地图

```
/Users/qc/KubePivot/
├── cmd/kp/
│   ├── controller.go              新：kp controller 子命令分发
│   ├── controller_enroll.go       新：enroll/unenroll/projects 实现
│   ├── deploy.go                  改：executeDeploy 末尾加 autoSyncResourcesIfEnrolled
│   └── multi_deploy.go            改：buildHelmArgs 加 --history-max=10，删除 kubepivot-controller 分支
│
├── internal/controller/
│   ├── controller.go              改：Start() 加 --global flag 分支
│   ├── global.go                  新：StartGlobal + runAsLeader 主循环
│   ├── global_state.go            新：多项目状态缓存 + sha256 指纹
│   ├── worker_pool.go             新：固定大小 goroutine 池
│   ├── watcher.go                 新：kubectl --watch + 心跳守卫 + 指数退避
│   ├── namespace_blacklist.go     新：5 个系统 ns 黑名单 + 环境变量扩展
│   ├── reconciler.go              改：Reconciler 加 project 字段
│   ├── heal.go                    改：所有 getenv("PROJECT_NAME") → r.project
│   └── drift_sync.go              改：forceSync 加 --history-max=10
│
├── internal/controller_installer/ 新包
│   ├── installer.go               Install/Uninstall/Status
│   └── templates/
│       ├── namespace.yaml
│       ├── rbac.yaml
│       └── deployment.yaml
│
├── internal/executor/
│   └── executor.go                改：新增 KubectlPath() / HelmPath() 导出方法
│
├── internal/scaffold/
│   ├── helm.go                    改：删除 writeControllerChart（整个函数 + 调用）
│   ├── skeleton.go                改：components.yaml 删除 kubepivot-controller 段
│   └── scaffold.go                改：kp init 输出文案改为 v2.3.0 体验
│
└── build/docker/controller/
    └── Dockerfile                 改：CMD 改为 ["controller", "start", "--global"]
```

---

## 四、踩过的坑（下次避开）

### 坑 1：PROJECT_NAME 环境变量空导致 release 名错误

v2.2.0 per-project 模式里 deployment.yaml 显式设置 `PROJECT_NAME=<项目>`，heal.go 直接 `getenv("PROJECT_NAME")`。v2.3.0 global 模式下 controller 跑在 kubepivot-system，没有固定的 PROJECT_NAME。

**现象**：日志出现 `release=-feelings-server`（前面多个 `-`），helm 查不到 release，自愈失败。

**修法**：Reconciler struct 加 `project` 字段，handleTask 构造时从 `task.Project` 填充。所有 `getenv("PROJECT_NAME")` 改为 `r.project`。

### 坑 2：IfNotPresent 导致镜像不更新

deployment.yaml 模板里 `imagePullPolicy: IfNotPresent`。重打镜像 + `kubectl rollout restart` 后 pod 还是跑老代码，因为 tag 没变，K8s 认为镜像已在本地缓存。

**修法**：调试时 `kubectl patch` 改 Always；稳定版本迭代时改 tag；deployment.yaml 模板的默认值后续是否改 Always 待定（Always 会每次重建都拉镜像，增加集群压力）。

### 坑 3：三个 pod 都自称 Leader

没配 `ETCD_ENDPOINTS`，global.go 的 `runGlobalLeaderElection` 走降级路径，每个 pod 各自跑 runAsLeader。冗余但不致命（多跑两次 reconcile），v2.4.0 要处理。

### 坑 4：sed 脚本删错地方

批量处理 fmt.Sprintf 参数时，同一个字符串 `` `, name, name, ..` `` 在文件里出现多处（Dockerfile 模板 + components.yaml 模板），用 `.replace(old, new, 1)` 删第一个会删错位置。

**教训**：批量修改前先 `grep -c` 数出现次数，多处出现时用唯一的前缀/后缀锚定。

---

## 五、v2.3.0 遗留到 v2.4.0 的问题

```
- Leader Election 无 etcd 时的降级策略（当前 3 pod 都当 leader）
- Controller 启动从 etcd 恢复状态机（v2.2.0 就残留，global 模式下状态机的
  per-project 语义需重新设计）
- Dockerfile 多架构 GitHub API 限流容错
- web3-blitz 蓝绿 release 名解析
- rbac.yaml 从字符串拼接重构为 embedded template file（v2.3.0 已经用
  embed.FS 装 controller 模板，但 scaffold 里老 per-project rbac 字符串还在）
```

---

## 六、给下一个 Claude 的提示

1. **qc 本次最重要的一次拍板是"不引入 client-go"**。这个决策的上下文值得反复重读：当他问"这里要引入 client-go 么，就这一个问题"时，他在测试你会不会本能地说"当然要用"。你应该像我一样，诚实对比 A/B、帮他权衡、把"架构纯粹性"摆到台面上，然后让他拍板。

2. **设计先对齐再动手**。v2.3.0 开工前 12 点清单逐条拍板、5 个确认题逐一回答。不要一上来就写代码。

3. **小步快跑，每步 make dev**。v2.3.0 的 Phase 2.2 拆成 5 步（blacklist / worker_pool / watcher / global_state / global），每步写完跑一次 make dev 绿了才进下一步。这样出问题定位范围极小。

4. **真实集群验证不可跳过**。单测全绿 ≠ 真的能跑。v2.3.0 真实集群验证时连踩 2 个 bug（PROJECT_NAME 空、IfNotPresent）。

5. **他要的不是"你说的都对"**。他要的是"你有没有认真想过"。

---

## 数字

```
v1.0.0 生日那天第一行代码    2026-03-29    commit #1
v2.0.0 plugin 平台           生日数字      commit #329
v2.2.0 真实自愈首次通过      一年天数      commit #365
v2.3.0 global controller     架构换血      commit #367
        ~12 秒自愈            真实集群
```

祝接手顺利。
