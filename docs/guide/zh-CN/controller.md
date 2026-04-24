# KubePivot Controller 使用指南

> 版本：v2.3.0+
> 目标读者：已经用 kp 部署过项目，想让服务获得自动自愈能力的用户

---

## 它是什么

Controller 是 KubePivot 的"运维大脑"：部署在你的集群里，持续盯着你的项目资源，资源意外缺失就自动恢复。

典型场景：

- 有人误删了 Deployment → 10 秒内自动 `helm rollback` 恢复
- Pod 因为 OOM 被反复重启 → 自动加内存上限（v2.5.0 计划）
- ConfigMap/Secret 被 drift 改写 → 强制回到 git 声明的状态（kp diff --drift）

v2.3.0 的 Controller 是**集群全局单实例**：一个集群装一次，所有项目共享。

---

## 什么时候装，什么时候不装

### 适合装的场景

- 生产环境，需要服务能自动恢复
- 多个项目共享一个 K8s 集群
- 希望 `configs/resources.yaml` 声明的资源有兜底保护

### 可以不装的场景

- 纯本地开发（OrbStack、minikube 本地调试），没必要
- 一次性演示、PoC 集群
- 资源极度受限的集群（3 副本 controller 约 300MB 内存）

**重要**：不装 controller 不影响 `kp deploy / kp rollback / kp status` 等核心命令。Controller 是**增强能力**，不是**依赖**。

---

## 五分钟接入

### 前置

```bash
kp version          # 确认 v2.3.0+
kubectl cluster-info # 确认集群连通
```

### Step 1：安装 Controller（集群级，一次性）

```bash
kp controller install
```

这条命令会：

1. 创建 `kubepivot-system` namespace
2. 部署 ClusterRole / ClusterRoleBinding（cross-namespace 权限）
3. 拉起 3 副本 HA Deployment

默认用 `qingchun22/kubepivot-controller:<kp 版本>` 镜像。国内环境可以用 `--image` 换成你自己的 registry：

```bash
kp controller install --image your-registry.cn/kubepivot-controller:v2.3.0
```

等 ~15 秒看到这样的输出就成了：

```
🔧 安装 KubePivot Controller → namespace=kubepivot-system, image=...
✓  Manifests apply 完成
⏳ 等待 Deployment ready
✓  Controller ready（2.5s）

💡 下一步：
  1. 进入项目目录：cd myproject
  2. 接入到 controller：kp controller enroll
```

### Step 2：项目接入

```bash
cd ~/myproject
kp controller enroll
```

做了三件事：

1. 给 namespace 打 `kubepivot.io/managed=true` label（内核标记）
2. 读本地 `configs/resources.yaml`
3. 写到集群里名为 `kubepivot-resources` 的 ConfigMap（带 sha256 指纹）

输出：

```
🔗 接入项目 → namespace=myproject, resources=configs/resources.yaml
✓  namespace label 已设置
✓  ConfigMap kubepivot-resources 已更新（sha256=ec7753b4...）

✅ 接入完成！
```

### Step 3：验证

```bash
kp controller status
```

应该看到：

```
🔱 KubePivot Controller 状态

  ✓ 已安装
  Namespace:           kubepivot-system
  Deployment Ready:    3/3
  Managed Projects:    1 个
```

想看都接入了哪些项目：

```bash
kp controller projects
```

### Step 4：实测自愈

```bash
# 随便删一个业务 Deployment
kubectl delete deployment -n myproject myapp

# 等 10-15 秒
sleep 15

# 回头看 pod 是否恢复
kubectl get pods -n myproject
```

应该看到 pod 又起来了。看 controller 日志：

```bash
kubectl logs -n kubepivot-system -l app=kubepivot-controller --tail=30 \
  | grep -E "自愈|rollback|release="
```

你会看到类似：

```
资源缺失，启动自愈       kind=Deployment name=myapp
执行 helm rollback       release=myproject-myapp revision=3
✅ 自愈成功（recreate）   release=myproject-myapp
```

---

## 修改监控规则

Controller 监控哪些资源、用什么策略处理，由项目里的 `configs/resources.yaml` 驱动。

```yaml
resources:
  # Deployment 缺失时自动 rollback 恢复
  - kind: Deployment
    name: myapp
    on-missing: auto-heal
    max-retry: 3
    fallback: rollback

  # StatefulSet（数据库）只告警，不自动处理（人工介入更安全）
  - kind: StatefulSet
    name: myapp-postgres
    on-missing: alert

  # PVC 也只告警
  - kind: PersistentVolumeClaim
    name: postgres-data
    on-missing: alert
```

### 支持的 kind

```
Deployment / StatefulSet / Service / PVC / Ingress / CronJob
```

### on-missing 策略

| 策略 | 什么时候用 |
|------|----------|
| `auto-heal` | 无状态服务（Deployment），重建风险小 |
| `rollback` | 明确要回滚到上一个 revision |
| `scale-down` | 要确保副本缩到 0（比如临时下线） |
| `alert` | 有状态服务（StatefulSet/PVC），自动处理风险大 |
| `custom` | 需要配 `fallback` 字段，走自定义路径 |

### 改了 resources.yaml 怎么办

两种方式让 controller 感知到：

**方式 A：kp deploy（推荐）**

```bash
# 改完 resources.yaml，正常走 kp deploy
kp deploy
```

`kp deploy` 结尾会自动把 `configs/resources.yaml` 同步到 ConfigMap，controller 秒级感知变化。

**方式 B：kp controller enroll（显式同步）**

```bash
kp controller enroll
```

每次 enroll 都会重新写 ConfigMap，sha256 变了 controller 立刻热加载。

---

## 多项目接入

在每个项目目录里跑一次 `kp controller enroll` 就行：

```bash
cd ~/project-a && kp controller enroll
cd ~/project-b && kp controller enroll
cd ~/project-c && kp controller enroll
```

看看都接入了谁：

```bash
kp controller projects
```

```
🔱 Controller 管理的项目（3 个）：

  ✓ project-a                     abc12345...
  ✓ project-b                     def67890...
  ✓ project-c                     98765432...
```

后面的 hash 是对应 `resources.yaml` 的 sha256，方便你确认集群里的配置和本地一致。

---

## 安全护栏

### Namespace 黑名单

即使你手贱给 `kube-system` 打了 `kubepivot.io/managed=true` label，Controller 也**物理拒绝**处理这些系统 namespace：

```
kube-system / kube-public / kube-node-lease / kubepivot-system / default
```

想追加黑名单（比如保护 `istio-system`）：

```bash
# 通过 ConfigMap 或 Deployment env 传入
KUBEPIVOT_EXTRA_PROTECTED_NS=istio-system,monitoring
```

### 权限边界

Controller 用 ClusterRole（必须 cross-namespace 才能管多个项目）：

```yaml
apps:                deployments / statefulsets / ...    完整 CRUD
core:                pods / services / pvc / configmaps  完整 CRUD
namespaces:          get / list / watch                   仅发现
secrets:             完整 CRUD                           （helm release state）
coordination/leases: 完整 CRUD                           （Leader Election）
```

完整权限清单见 `internal/controller_installer/templates/rbac.yaml`。

### 只处理标记项目

没有 `kubepivot.io/managed=true` label 的 namespace，Controller 一概不碰。你可以安全地和其他工具（ArgoCD/Flux/手工 helm）共存于同一集群。

---

## 日常运维

### 看 Controller 状态

```bash
kp controller status                    # 概览
kp controller projects                  # 管理的项目列表
kubectl get pods -n kubepivot-system    # pod 层面
kubectl logs -n kubepivot-system -l app=kubepivot-controller --tail=50
```

### 升级 Controller

```bash
# 方法 1：重新 install（幂等，会覆盖）
kp controller install --image qingchun22/kubepivot-controller:v2.4.0

# 方法 2：直接改 image
kubectl set image -n kubepivot-system deployment/kubepivot-controller \
  controller=qingchun22/kubepivot-controller:v2.4.0
```

**注意**：如果用同一个 tag 推新镜像（比如 `:latest`），`imagePullPolicy: IfNotPresent` 会让 K8s 用本地缓存：

```bash
# 临时改成 Always 强制拉
kubectl patch deployment -n kubepivot-system kubepivot-controller \
  -p '{"spec":{"template":{"spec":{"containers":[{"name":"controller","imagePullPolicy":"Always"}]}}}}'

kubectl rollout restart -n kubepivot-system deployment/kubepivot-controller
```

### 项目 unenroll

不再需要 Controller 保护某个项目：

```bash
cd ~/myproject
kp controller unenroll
```

做两件事：删除 ConfigMap + 移除 namespace label。**不会删除项目本身的任何资源**。

### 卸载 Controller

```bash
kp controller uninstall
```

有交互确认。加 `--force` 跳过确认。

**注意**：卸载会删除 `kubepivot-system` namespace 及其下所有资源、ClusterRole/Binding。**不会动被管理项目的 namespace label**，想彻底清理每个项目跑一次 `kp controller unenroll`。

---

## 从 v2.2.0 迁移

v2.2.0 时每个项目都部署了自己的 controller（per-project 模式）。v2.3.0 要改成全局 controller，步骤：

```bash
# 1. 卸载每个项目的老 controller
helm uninstall project-a-kubepivot-controller -n project-a
helm uninstall project-b-kubepivot-controller -n project-b

# 2. 装全局 controller（只一次）
kp controller install

# 3. 每个项目 enroll
cd ~/project-a && kp controller enroll
cd ~/project-b && kp controller enroll

# 4. 删除老的 controller chart 目录（可选）
rm -rf ~/project-a/deployments/project-a/kubepivot-controller
rm -rf ~/project-b/deployments/project-b/kubepivot-controller
```

新项目（`kp init` 生成）默认不带 per-project controller chart，直接走全局路径。

---

## 常见问题

### Q: 为什么 `kp controller status` 显示 3 个 Leader？

A: 没配 etcd 时，3 副本各自跑单机模式。冗余但不致命（多做两次 reconcile）。想消除冗余，配 `ETCD_ENDPOINTS` 环境变量到 deployment。v2.4.0 计划改用 K8s Lease API 做原生选举。

### Q: Controller 把我的 kube-system 里的 Pod 自愈了怎么办？

A: 不可能发生。Controller 有三道 namespace 黑名单护栏（入队前 / 执行前 / 建立状态前）。即使你手工打了 `managed=true` label 到 kube-system，代码层物理拒绝。

### Q: 改了 resources.yaml，Controller 多久感知？

A: 两条路径：

- `kp deploy` 末尾自动同步 ConfigMap → **秒级**热加载
- `kp controller enroll` 显式同步 ConfigMap → **秒级**热加载

Controller 监听所有 `kubepivot-resources` ConfigMap，sha256 变化立即触发 reconcile。

### Q: 并发 50 个项目同时故障，会不会打爆 Controller？

A: 不会。架构设计上 Leader 只做**分发**（非阻塞 enqueue），Worker Pool（默认 20 goroutine）消费队列。Channel 满了会丢弃并告警，Leader 本身不阻塞。

想调大并发：

```bash
# deployment env 设置
KUBEPIVOT_WORKER_POOL_SIZE=40
```

### Q: 单项目的 Controller（v2.2.0）和全局 Controller（v2.3.0）能共存吗？

A: 技术上可以（Leader Election key 不冲突），但强烈不建议。两者都会尝试处理同一个项目，产生竞争。迁移时先卸载老的 per-project controller，再装全局。

### Q: Controller 挂了我的项目还能用吗？

A: 能。Controller 是**增强能力**，不是依赖。你的业务 Pod、`kp deploy`、`kp rollback` 都不依赖 Controller。Controller 只做"自动自愈"一件事，没有它你依然可以手工 `kp rollback`。

---

## 相关文档

- 设计细节：[`docs/design/controller.md`](../../design/controller.md)
- 整体架构：[`docs/design/architecture.md`](../../design/architecture.md)
- 命令速查：[`docs/guide/zh-CN/commands.md`](commands.md)
