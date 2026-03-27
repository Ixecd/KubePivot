# 已知坑和注意事项

> 这里记录使用 dtk 过程中会踩到的坑，遇到问题先查这里。

---

## 一、并发与竞态

### 部署中不能再发起新部署

dtk 状态机在 `DEPLOYING` / `VALIDATING` / `ROLLING_BACK` 状态时会拒绝新部署：

```
当前部署状态为 DEPLOYING，不能发起新部署
如需继续，请运行: dtk resume
```

**这是故意的**，并发部署会导致 helm 状态混乱，状态机无法正确追踪。

如果上次部署卡住了，按以下顺序处理：

```bash
dtk resume    # 尝试从中断点恢复
# 如果 resume 判断服务已正常运行，会同步状态为 RUNNING
# 如果服务不存在，会从头重新部署

dtk rollback  # 或者直接放弃当前版本，回滚到上一个
```

不要手动重置状态机，除非 resume 和 rollback 都失败了。

---

### controller 和 dtk deploy 并发触发 pending-rollback 死锁

当 controller 检测到资源缺失并触发 `helm rollback` 的同时，用户手动跑 `dtk deploy`，两个 helm 操作会互相冲突，导致 release 卡在 `pending-rollback` 状态。

**症状**：

```
helm upgrade failed: UPGRADE FAILED: release: not in a deployable state
```

**处理步骤**：

```bash
# 1. 停掉 controller，防止继续干扰
kubectl scale deployment/myapp-controller -n myapp --replicas=0

# 2. 清理 pending-rollback secret
kubectl delete secret -n myapp \
  $(kubectl get secret -n myapp -l owner=helm,name=myapp \
    -o jsonpath='{.items[?(@.metadata.labels.status=="pending-rollback")].metadata.name}')

# 3. 重置状态机
python3 -c "
import json, os
p=os.path.expanduser('~/.dtk/state/myapp/myapp.json')
d=json.load(open(p))
d['state']='RUNNING'
d['reason']='手动重置'
json.dump(d,open(p,'w'),indent=2)
"

# 4. 重新部署
dtk deploy
```

**根本预防**：不要在 controller 运行时手动 `dtk deploy`，或者部署前先停掉 controller。

---

### etcd 断线后 controller 只依赖定时对账

`startEtcdWatcher` 断线后不会自动重连，controller 退化为只依赖 8 秒周期的定时 Reconcile。

**影响**：etcd 状态变更后，controller 最多延迟 8 秒才能感知，不是实时的。

**现状**：已知限制，P1 修复。正常情况下影响不大。

---

## 二、状态机

### 状态卡住时手动重置

只有在 `dtk resume` 和 `dtk rollback` 都无法解决时，才手动重置：

```bash
python3 -c "
import json, os
p=os.path.expanduser('~/.dtk/state/{project}/{namespace}.json')
d=json.load(open(p))
d['state']='IDLE'   # 或 RUNNING，按实际情况
d['reason']='手动重置'
json.dump(d,open(p,'w'),indent=2)
"
```

重置前先确认 K8s 里服务的实际状态：

```bash
kubectl get pods -n myapp
helm status myapp -n myapp
```

状态机和 K8s 实际状态要对齐，不要设成 RUNNING 但 pod 其实不存在。

---

### revision=1 时无法 rollback

第一次部署（helm revision=1）没有上一个版本，`dtk rollback` 会报：

```
helm rollback 失败: 当前是第一个版本（revision=1），无法回滚
```

这是正确行为，不是 bug。要回到"没有部署"的状态，用 `dtk down`。

---

### CLEANING 状态下无法 rollback

状态机处于 `CLEANING` 时，不允许转换到 `ROLLING_BACK`。

**原因**：CLEANING 是首次部署失败后的清理阶段，这时候 helm release 可能不完整，rollback 没有意义。

**处理**：等 CLEANING 完成回到 IDLE，再重新 `dtk deploy`。如果卡在 CLEANING，手动重置到 IDLE。

---

## 三、环境

### /healthz 路由缺失导致 VALIDATING 卡死

dtk 在 VALIDATING 阶段会通过 `kubectl exec` 检查服务的 `/healthz` 路由是否返回 200。如果路由不存在，会超时后自动回滚。

**必须在业务服务里实现**：

```go
mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
})
```

dtk init 生成的骨架里已经包含这个路由，不要删掉。

**验证方式**：

```bash
kubectl exec -n myapp deployment/myapp -- wget -qO- http://localhost:8080/healthz
# 返回 200 OK 才正常
```

---

### Docker 镜像架构不匹配

`configs/project.env` 里的 `ARCH` 必须和本机架构一致：

```ini
ARCH=arm64   # Apple Silicon / AWS Graviton
ARCH=amd64   # Intel / AMD
```

架构不匹配会导致镜像 build 成功但 pod 启动失败（`exec format error`）。

查看本机架构：

```bash
go env GOARCH
```

---

### kubectl context 切错集群

`dtk deploy` 会操作当前 kubectl context 指向的集群。如果 context 切错，可能误操作生产环境。

**建议**：在 `configs/project.env` 里明确写 `KUBE_CONTEXT`，不要依赖当前默认 context：

```ini
KUBE_CONTEXT=orbstack     # 明确指定，防止误操作
```

部署前确认：

```bash
kubectl config current-context
```

---

## 四、Helm

### SSA 冲突（managedFields）

多次 `helm upgrade` 后，K8s 的 Server-Side Apply 可能产生 managedFields 冲突，导致 upgrade 失败：

```
Error: UPGRADE FAILED: failed to create resource: ... field is immutable
```

临时处理：

```bash
helm upgrade myapp ./deployments/myapp \
  --namespace myapp \
  --force-conflicts \
  --wait
```

dtk 的 `deploy.mk` 已经默认带 `--force-conflicts`，正常情况下不会触发。如果依然报错，说明有字段真的不可变（比如 StatefulSet 的 `volumeClaimTemplates`），需要手动删除 StatefulSet 重建。

---

### values.yaml 改动后必须 dtk deploy 才能生效

修改 `deployments/myapp/values.yaml` 或 `configs/resources.yaml` 后，必须重新跑 `dtk deploy` 才会同步到集群。

特别注意：`configs/resources.yaml` 是通过 `--set-file` 注入到 helm 的，改了文件后 controller ConfigMap 不会自动更新，必须重新 deploy。

---

## 五、部署配置

### VERSION 不改不会重新 build/push

dtk 会检查镜像是否已存在于 Docker Hub，如果 `VERSION` 没变，直接跳过 build/push：

```
===========> Image already exists, skipping build
===========> Image already pushed, skipping push
```

这是正常的优化行为。如果代码改了但忘记改 VERSION，新代码不会生效。

**发版标准姿势**：

```bash
dtk release --version v0.2.0 --deploy
# 自动改 VERSION、commit、tag、build、push、deploy
```

---

### controller 镜像未配置导致 deploy 卡住（已有保护）

`values.yaml` 里 `controller.enabled` 默认是 `false`，首次 `dtk deploy` 不会部署 controller。

如果手动改成 `true` 但没有配置正确的镜像，pod 会一直 `ImagePullBackOff`，`--wait` 会卡住直到超时。

**启用 controller 前必须**：
1. 构建包含 `dtk` 二进制 + kubectl + helm 的镜像
2. 填写 `values.yaml` 里的 `controller.image.repository` 和 `tag`
3. 再将 `controller.enabled` 改为 `true`

---

### REGISTRY_PREFIX 未填

`configs/project.env` 里 `REGISTRY_PREFIX` 留空会导致 push 失败：

```
The push refers to repository [docker.io//myapp-arm64]
```

运行 `dtk doctor` 可以检查是否已填写。
