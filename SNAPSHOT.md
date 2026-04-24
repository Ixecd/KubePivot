# KubePivot 当前快照

> 版本：v2.3.0
> 日期：2026-04-24
> 状态：✅ 全绿，真实集群自愈闭环（global 架构）通过

---

## 快速状态

```
go test ./... -race  → 全绿（v2.3.0 新增 22 个测试）
make dev             → build + test + install 一键完成
当前版本             → v2.3.0
companion            → github.com/Ixecd/web3-blitz
                       github.com/Ixecd/Feelings-Server
自愈验证             → 2026-04-24 真实集群 global 架构 ~12 秒闭环
controller 镜像      → qingchun22/kubepivot-controller:v2.3.0（arm64 + amd64）
```

---

## 完整版本线

```
v1.0.0  生日当天 🏆  DAG + A2 Controller + 安全合规基线
v1.4.0              跨版本迁移（KubePivot 改名，乾枢）
v1.6.0              Controller HA（Leader Election + WorkQueue）
v1.7.0              状态漂移治理（终态强权）
v1.8.0              Operation Sandbox + Header Preview + Warmup
v1.9.0              多集群联邦 + 企业合规（audit + OPA + Vault）
v2.0.0  #329 🏆     插件平台 + Chaos + GitOps Manifesto
v2.1.0              脚手架适配性 + 扩展性 + GitOps 愿景落地
v2.2.0  #365 🔱     真实集群自愈闭环 + scratch 容器化全量改造
v2.3.0  #367 🌐     全局单一 HA Controller 架构级跃迁
```

---

## v2.3.0 核心成就

### 1. 架构换血：per-project → cluster-scoped

```
v2.2.0：每项目 3 副本 controller        10 项目 = 30 pod
v2.3.0：集群唯一 3 副本 controller       10 项目 =  3 pod
                                         省 27 pod
```

Controller 部署到 `kubepivot-system` namespace，一次性安装（`kp controller install`），
所有项目通过 `kp controller enroll` 接入。

### 2. 双层接入协议

```
内核层：kubectl label ns <n> kubepivot.io/managed=true
交互层：kp controller enroll
分发：   ConfigMap kubepivot-resources（含 sha256 annotation）
热加载：sha256 变化触发 reconcile，内容未变静默跳过
```

### 3. 关键哲学决策：不引入 client-go

保持 KubePivot "只用 CLI 不吃 K8s SDK" 的架构纯粹性。自己实现的 Watcher 层：

- `exec.CommandContext` 绑进程生命周期
- `json.NewDecoder` 流式解析 `kubectl --watch --output-watch-events=true`
- 30s 无事件心跳守卫 + 指数退避重连（1s → 30s 封顶）
- `Watcher` 接口抽象，未来规模上来可平替 client-go

### 4. 真实集群端到端验证

```
场景：删除 feelings-server Deployment，观察自愈

09:14:06  资源缺失，启动自愈    kind=Deployment name=feelings-server
09:14:06  执行 helm rollback   release=feelings-server-feelings-server revision=8
09:14:18  ✅ 自愈成功（recreate）
pod 恢复   feelings-server-64df876dc7-8m59c  Running  22s

自愈延迟 ~12 秒（kubectl watch 感知 + 任务入队 + worker 消费 +
helm rollback + Deployment rollout 就绪）
```

### 5. 三道 namespace 黑名单护栏

即使用户误给 `kube-system` 打上 `managed=true` label，controller 在三处代码层面物理拒绝：

```
1. Worker Pool Enqueue 入队前       worker_pool.go
2. handleTask 执行前（二次）         global.go
3. GlobalState UpsertProject 前     global_state.go
```

---

## 架构图

```
kubepivot-system ns
  └── kubepivot-controller（3 副本 HA）
      ├── Leader Election（/kubepivot/global/leader）
      └── 成为 Leader 后：
          ├── Namespace Watcher     （label=kubepivot.io/managed=true）
          ├── ConfigMap Watcher     （all-ns, name=kubepivot-resources）
          ├── Reconcile Loop        （8s 周期，扫所有 managed 项目）
          └── Worker Pool           （20 goroutine，固定大小）

每项目 ns：
  label:                      kubepivot.io/managed=true
  ConfigMap kubepivot-resources：
    labels:       kubepivot.io/managed: "true"
    annotations:  kubepivot.io/sha256: <hex>
    data.resources.yaml:      用户声明的监控资源清单
```

---

## 新命令家族

```
kp controller install       集群级一次性安装
kp controller uninstall     卸载
kp controller status        查看状态 + 管理项目数
kp controller projects      列出所有被管理的项目
kp controller enroll        当前项目接入
kp controller unenroll      解除接入
kp controller start [--global]   pod 内部使用
```

---

## 单元测试覆盖

v2.3.0 新增 22 个测试：

```
namespace_blacklist_test.go     2  （黑名单 + 环境变量扩展）
worker_pool_test.go             5  （分发 / 黑名单拒绝 / panic 恢复 / 环境变量 / 降级）
watcher_test.go                 4  （WatchEvent.Meta + buildArgs 三种 namespace 场景）
global_state_test.go           11  （upsert / sha256 dedup / remove / protected ns / 坏 YAML /
                                     list / copy-on-read / 并发 race / fingerprint 2 个）
```

全部绿色通过，`make dev` 一键完成。

---

## 从 v2.2.0 迁移

```
1. helm uninstall <project>-kubepivot-controller     # 卸老 controller
2. kp controller install                              # 装全局 controller
3. cd <project> && kp controller enroll               # 接入项目
4. 后续 kp deploy 自动同步 resources.yaml
```

`kp init` 生成的新项目骨架默认不再包含 per-project controller chart。

---

## 下一步

v2.3.0 剩余问题写入 v2.4.0 TODO：

- Leader Election 无 etcd 时的降级策略（当前 3 pod 都当 leader，冗余但不致命）
- Controller 启动从 etcd 恢复状态机（global 模式语义待重设计）
- Dockerfile 多架构 GitHub API 限流容错
- web3-blitz 蓝绿 release 名解析
- rbac.yaml 从字符串拼接重构为 embedded template file
