# SNAPSHOT — KubePivot v2.3.0

> 日期：2026-04-24
> 状态：✅ 真实集群 global 架构自愈闭环通过
> 代号：🌐 架构换血

---

## 一句话总结

**从"每项目独立 controller（浪费）"到"集群唯一 controller（秩序）"的架构级跃迁。自愈能力从 per-project 平面化到 cluster-scoped，10 项目场景省 27 pod。**

---

## 核心成就

### 1. 架构换血

```
v2.2.0（per-project）           v2.3.0（global）
──────────────────             ──────────────────
每项目 ns 3 副本 controller     kubepivot-system 3 副本 controller
资源模型：N × 3                  资源模型：3（不随项目数增长）
```

### 2. 双层接入协议

- **内核层**：`kubectl label ns <n> kubepivot.io/managed=true`（ground truth）
- **交互层**：`kp controller enroll`（封装 label + ConfigMap sync）
- **分发**：ConfigMap `kubepivot-resources`，含 `sha256` annotation 做热加载去重

### 3. 关键哲学决策：不引入 client-go

坚持 KubePivot "只用 CLI 不吃 K8s SDK" 的路线。自己实现 Watcher 层：
- `exec.CommandContext` 绑进程生命周期
- `json.NewDecoder` 流式解析 `kubectl --watch`
- 30s 心跳守卫 + 指数退避重连
- `Watcher` 接口抽象，未来可平替 client-go

### 4. 真实集群端到端验证

```
2026-04-24 09:14:06  资源缺失，启动自愈
2026-04-24 09:14:18  ✅ 自愈成功（recreate）
                     release=feelings-server-feelings-server

自愈延迟：~12 秒（kubectl watch 感知 + 任务入队 + worker 消费 +
helm rollback + Deployment rollout 就绪）
```

---

## 代码改动

### 新增

```
internal/controller/
├── global.go                  StartGlobal + runAsLeader 主循环
├── global_state.go            多项目状态缓存 + sha256 指纹
├── worker_pool.go             固定大小 goroutine 池
├── watcher.go                 kubectl --watch + 心跳守卫 + 指数退避
└── namespace_blacklist.go     5 个系统 ns 黑名单 + env 扩展

internal/controller_installer/（新包）
├── installer.go               Install/Uninstall/Status
└── templates/
    ├── namespace.yaml
    ├── rbac.yaml
    └── deployment.yaml

cmd/kp/
├── controller.go              kp controller 子命令分发
└── controller_enroll.go       enroll/unenroll/projects 实现

测试：
├── namespace_blacklist_test.go     2 个
├── worker_pool_test.go             5 个
├── watcher_test.go                 4 个
└── global_state_test.go           11 个
共 22 个新测试，全部绿色。
```

### 修改

```
internal/controller/
├── controller.go              Start() 加 --global flag 分支
├── reconciler.go              Reconciler 加 project 字段
├── heal.go                    所有 getenv("PROJECT_NAME") → r.project
└── drift_sync.go              forceSync 加 --history-max=10

internal/executor/executor.go  新增 KubectlPath() / HelmPath()

internal/scaffold/
├── helm.go                    删除 writeControllerChart
├── skeleton.go                components.yaml 删除 controller 段
└── scaffold.go                kp init 输出文案升级

cmd/kp/
├── main.go                    controller.Start(args...) 传 flag
├── multi_deploy.go            buildHelmArgs 加 --history-max + 删除 controller 分支
└── deploy.go                  executeDeploy 末尾 autoSyncResourcesIfEnrolled

build/docker/controller/Dockerfile
  CMD 改为 ["controller", "start", "--global"]
```

### 删除

```
- writeControllerChart 函数（~170 行）
- components.yaml 模板里的 kubepivot-controller 段
- multi_deploy.go buildHelmArgs 里 kubepivot-controller 两处特殊分支
```

---

## 架构图

```
kubepivot-system ns
  └── kubepivot-controller（3 副本 HA）
      ├── Leader Election（/kubepivot/global/leader）
      │   └── 无 etcd 时降级单机模式（v2.4.0 要处理）
      └── 成为 Leader 后：
          ├── Namespace Watcher     label=kubepivot.io/managed=true
          │   └── ADDED → loadResourcesConfigMap 补偿
          ├── ConfigMap Watcher     all-ns, name=kubepivot-resources
          │   └── sha256 变 → UpsertProject → 立即入队全量 reconcile
          ├── Reconcile Loop        8s 周期扫所有 managed 项目入队
          └── Worker Pool           20 goroutine（KUBEPIVOT_WORKER_POOL_SIZE 可调）
              └── handleTask
                  ├── Protected ns 二次护栏
                  ├── DetectResourceExists
                  └── 缺失 → 复用 v2.2.0 Reconciler.checkAndHeal
                             （自愈逻辑零重写）

每项目 ns：
  label:                      kubepivot.io/managed=true
  ConfigMap kubepivot-resources：
    labels:       kubepivot.io/managed: "true"
    annotations:  kubepivot.io/sha256: <hex>
    data.resources.yaml:      用户声明的监控资源清单
```

---

## Phase 划分（实施顺序）

```
Phase 1    helm --history-max 小债清理                ✅
Phase 2.1  kp controller install/uninstall/status    ✅
Phase 2.2  Controller global 模式代码                 ✅
  ├── 2.2.1  namespace_blacklist                     ✅
  ├── 2.2.2  worker_pool                             ✅
  ├── 2.2.3  watcher                                 ✅
  ├── 2.2.4  global_state                            ✅
  └── 2.2.5  global（组装）                          ✅
Phase 2.5  kp controller enroll/unenroll/projects    ✅
Phase 2.6  kp deploy 顺带同步 resources.yaml          ✅
Phase 2.7  kp init 剥离 controller                    ✅
Phase 2.8  真实集群端到端验证                         ✅
```

（Phase 2.3 和 2.4 在实施过程中合入了 2.2，内容为 ConfigMap Watcher + sha256 热加载。）

---

## 踩过的坑

### 坑 1：PROJECT_NAME 环境变量空 → release 名错误

**现象**：controller 日志持续报 `release=-feelings-server`（多了个 `-`），
helm 查不到 release，自愈失败。

**根因**：v2.2.0 per-project 模式下 deployment.yaml 显式设置 `PROJECT_NAME=<项目>`，
heal.go 里 `getenv("PROJECT_NAME", "") + "-" + res.Name` 能组出 `<project>-<res>`。
v2.3.0 global 模式下 controller 跑在 kubepivot-system，没有固定 PROJECT_NAME。

**修法**：`Reconciler` struct 加 `project` 字段，`handleTask` 构造时显式传
`task.Project`。所有 heal 方法里 `getenv("PROJECT_NAME")` 改为 `r.project`。

test helper `newTestReconciler` 也要跟着加 `project: "myapp"` 字段，
匹配测试里的 resource name。

### 坑 2：IfNotPresent 导致镜像不更新

**现象**：重打 v2.3.0 镜像 + `kubectl rollout restart` 后 pod 还跑老代码。

**根因**：`imagePullPolicy: IfNotPresent`，tag 没变，K8s 认为本地缓存可用。

**修法**：`kubectl patch` 改 Always → `rollout restart`。

**经验**：大版本迭代调试时改 Always 临时验证，稳定后改 tag（v2.3.0→v2.3.1）而不是
反复用同一个 tag 推镜像。

### 坑 3：三个 pod 都当 Leader

**现象**：三个 controller pod 启动日志都打 `👑 已成为全局 Leader`。

**根因**：没配 `ETCD_ENDPOINTS`，`runGlobalLeaderElection` 走降级路径，
每个 pod 各自调用 `runAsLeader`。

**影响**：冗余但不致命（多跑两次 reconcile，资源浪费但无 race）。

**修法**：v2.4.0 P0 处理——无 etcd 时改走 K8s Lease API 或副本缩为 1。

### 坑 4：sed 脚本删错地方

**现象**：给 `components.yaml` 模板削 fmt.Sprintf 参数，不小心把 Dockerfile 模板的
参数列表也削了，编译报错 `fmt.Sprintf format %s reads arg #7, but call has 6 args`。

**根因**：同一个子字符串 `` `, name, name, ... ` `` 在文件里出现多处，
`.replace(old, new, 1)` 只替换第一次，但第一次不一定是你要改的那处。

**教训**：批量修改前 `grep -c` 数出现次数，多处出现时用唯一前缀/后缀锚定。

---

## 设计哲学（动手前对齐的 12 点清单）

所有 12 点 qc 逐条拍板后才动笔：

1. 项目接入机制：ns label + kp enroll 组合（内核 + 交互双层）
2. ConfigMap 分发：data 纯 YAML + annotation sha256 指纹
3. 部署 namespace：`kubepivot-system`
4. CLI 命令家族：`kp controller`（独立子命令家族）
5. 老项目清理：强制清理（用户数为 0 的特权）
6. Leader Election：`/kubepivot/global/leader`，Leader 只分发，Worker Pool 异步执行
7. Namespace 黑名单：代码层物理拒绝（即使 RBAC 授权）
8. controller chart 存放：embed.FS YAML 模板
9. kp enroll：合并到 `kp controller enroll`
10. resources.yaml 同步：enroll 自动做 + deploy 顺带做（消灭忘记同步）
11. 老项目清理 via kp sync：只 notify 让用户决定
12. ConfigMap 热加载：Watcher 监听 → sha256 比对 → 立即刷新

**最关键的一次拍板**：不引入 client-go。保持 KubePivot 架构纯粹性。

---

## 测试数据

```
新增单元测试：22 个
  namespace_blacklist_test.go   2
  worker_pool_test.go           5
  watcher_test.go               4
  global_state_test.go         11（含并发 race 测试）

make dev（build + test + install）全绿
go test ./... -race 全绿
```

---

## 真实集群运行数据

```
controller 镜像：   qingchun22/kubepivot-controller:v2.3.0
                   qingchun22/kubepivot-controller:latest
架构：              linux/arm64 + linux/amd64
集群：              orbstack（macOS ARM）
部署 namespace：    kubepivot-system
副本数：            3/3 Available
启动耗时：          ~2.5 秒
管理项目：          feelings-server（首个接入）
自愈延迟：          ~12 秒（资源缺失 → pod Running）
```

---

## v2.2.0 → v2.3.0 迁移

```
手工步骤（用户数为 0 的特权）：
  1. helm uninstall <project>-kubepivot-controller
  2. kp controller install
  3. cd <project> && kp controller enroll
  4. 后续 kp deploy 自动同步 resources.yaml

向后兼容：
  controller.Start() 仍保留 per-project 路径（默认行为），
  新项目 kp init 生成的骨架不再包含 per-project controller。
```

---

## 数字

```
v1.0.0 生日那天第一行代码    2026-03-29    commit #1
v2.0.0 plugin 平台           生日数字      commit #329
v2.2.0 真实自愈首次通过      一年天数      commit #365
v2.3.0 global controller     架构换血      commit #367
        ~12 秒自愈            真实集群
```

从"每项目都养一个 controller 的资源浪费"到"集群唯一的运维大脑"。
从 per-project 碎片到 cluster-scoped 整体。从多副本各自跑的混乱
到单 Leader + Worker Pool 的秩序。

不引入 client-go，因为 KubePivot 的 DNA 就是"只用 CLI 不吃 SDK"。
爽感 = 逆势成立。
