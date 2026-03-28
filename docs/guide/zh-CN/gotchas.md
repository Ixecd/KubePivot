# 已知坑和注意事项

> 这里记录使用 dtk 和开发过程中踩到的所有坑，遇到问题先查这里。
> 最后更新：2026-03-28 / v0.8.0

---

## 一、并发与竞态

### 部署中不能再发起新部署

dtk 状态机在 `DEPLOYING` / `VALIDATING` / `ROLLING_BACK` 状态时会拒绝新部署：

```
当前部署状态为 DEPLOYING，不能发起新部署
如需继续，请运行: dtk resume
```

这是故意的，并发部署会导致 helm 状态混乱。如果上次部署卡住了：

```bash
dtk resume    # 尝试从中断点恢复
dtk rollback  # 或者放弃当前版本，回滚到上一个
```

不要手动重置状态机，除非上面两个都失败了。

---

### controller 和 dtk deploy 并发触发 pending-rollback 死锁

controller 检测到资源缺失并触发 `helm rollback` 的同时，用户手动跑 `dtk deploy`，两个 helm 操作冲突，release 卡在 `pending-rollback`。

**症状**：

```
helm upgrade failed: UPGRADE FAILED: release: not in a deployable state
```

**dtk deploy 会自动检测并提示处理**（v0.5.1+），按提示操作即可。

手动处理步骤：

```bash
kubectl scale deployment/myapp-controller -n myapp --replicas=0
kubectl delete secret -n myapp \
  $(kubectl get secret -n myapp -l owner=helm,name=myapp \
    -o jsonpath='{.items[?(@.metadata.labels.status=="pending-rollback")].metadata.name}')
dtk deploy
```

---

### etcd 断线后 controller 依赖定时对账

etcd Watch 断线后，controller 会指数退避重连（1s → 2s → 4s ... 最大 30s）。重连期间只依赖 8s 周期 Reconcile，响应延迟略长。重连成功后恢复实时监听。

---

## 二、状态机

### 状态卡住时手动重置

只有在 `dtk resume` 和 `dtk rollback` 都无法解决时，才手动重置：

```bash
python3 -c "
import json, os
p=os.path.expanduser('~/.dtk/state/myapp/myapp.json')
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

---

### revision=1 时无法 rollback

第一次部署（helm revision=1）没有上一个版本，`dtk rollback` 会报：

```
helm rollback 失败: 当前是第一个版本（revision=1），无法回滚
```

这是正确行为，不是 bug。要回到"没有部署"的状态，用 `dtk down`。

---

### rollback 失败后状态变成 CLEANING（已修复）

**v0.5.1 之前**：`dtk rollback` 失败时错误地将状态转为 CLEANING，导致后续无法操作。

**v0.5.1 修复**：rollback 失败时状态机保持 RUNNING，允许用户重试。

---

### RUNNING → ROLLING_BACK 非法转换（已修复）

**v0.5.1 之前**：`dtk rollback` 报"非法状态转换 RUNNING → ROLLING_BACK"。

**v0.5.1 修复**：将 `ROLLING_BACK` 加入 RUNNING 的合法转换目标。

---

### helm rollback 报 release has no 0 version（已修复）

**v0.5.1 之前**：`helmRollback` 传 revision=0，helm 不认，报错。

**v0.5.1 修复**：查询 helm history 取 latest-1，明确传目标 revision。

---

## 三、环境

### /healthz 路由缺失导致 VALIDATING 卡死

dtk 在 VALIDATING 阶段会检查服务的 `/healthz` 路由是否返回 200。如果路由不存在，超时后自动回滚。

**必须在业务服务里实现**：

```go
mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
})
```

验证方式：

```bash
kubectl exec -n myapp deployment/myapp -- wget -qO- http://localhost:8080/healthz
```

---

### Docker 镜像架构不匹配

`configs/project.env` 里的 `ARCH` 必须和本机架构一致，`dtk init` 会自动检测，但升级 dtk 前生成的老项目需要手动确认：

```bash
go env GOARCH   # 查看本机架构
```

架构不匹配会导致镜像 build 成功但 pod 启动失败（`exec format error`）。

---

### kubectl context 切错集群

`dtk deploy` 会操作当前 kubectl context 指向的集群。建议在 `configs/project.env` 里明确写 `KUBE_CONTEXT`，不依赖默认 context：

```ini
KUBE_CONTEXT=orbstack
```

部署前确认：

```bash
kubectl config current-context
```

---

### macOS IPv6 localhost 解析问题（ghost postgres）

**症状**：`psql -h localhost` 连接失败，但 `psql -h 127.0.0.1` 正常。

**原因**：macOS 将 `localhost` 解析为 `::1`（IPv6），而 postgres 只监听 IPv4。

**解法**：连接字符串改用 `127.0.0.1` 而不是 `localhost`，或者用 Docker 容器的直接 IP。

---

### goreman 双进程导致唯一约束冲突

**症状**：本地开发时出现主键/唯一约束冲突，但代码逻辑没问题。

**原因**：goreman 重启时上一个进程没有完全退出，两个进程同时写数据库。

**解法**：`pkill -f 'cmd/wallet-service'` 彻底杀掉旧进程再重启。

---

## 四、Helm

### SSA managedFields 冲突

多次 `helm upgrade` 后，K8s Server-Side Apply 可能产生 managedFields 冲突：

```
Error: UPGRADE FAILED: field is immutable / another manager owns field
```

`dtk deploy` 会自动检测 SSA 冲突，清除 namespace 下所有资源的 managedFields 后重试一次（v0.6.0+）。

手动处理：

```bash
kubectl patch deployment myapp -n myapp \
  --type=merge \
  --patch '{"metadata":{"managedFields":null}}'
```

---

### helm pending-rollback 死锁手动处理

```bash
kubectl scale deployment/myapp-controller -n myapp --replicas=0
kubectl delete secret -n myapp \
  $(kubectl get secret -n myapp -l owner=helm,name=myapp \
    -o jsonpath='{.items[?(@.metadata.labels.status=="pending-rollback")].metadata.name}')
dtk deploy
```

---

### helm pending-install 处理

上次首次安装被中断，release 卡在 pending-install：

```bash
helm delete myapp -n myapp
dtk deploy
```

`dtk deploy` 会自动检测并提示（v0.7.0+）。

---

### values.yaml 改动后必须 dtk deploy 才能生效

修改 `deployments/myapp/values.yaml` 或 `configs/resources.yaml` 后，必须重新跑 `dtk deploy` 才会同步到集群。`configs/resources.yaml` 通过 `--set-file` 注入到 helm，改了文件不 deploy，controller ConfigMap 不会更新。

---

### fixChartYAMLs 使用了 Go 不支持的正则（已修复）

**v0.8.0 之前**：`fixChartYAMLs` 用了 lookahead 正则 `(?=`，Go RE2 不支持，遇到有 `maintainers:` 字段的 Chart.yaml 直接 panic。

**v0.8.0 修复**：改为逐行解析，单元测试覆盖。

---

## 五、代码生成（scaffold）

### replaceInDir 不跳过 .git 目录（已修复）

**v0.8.0 之前**：`replaceInDir` 遍历时没有跳过 `.git` 目录，会把 git 内部文件也替换，潜在破坏仓库。

**v0.8.0 修复**：所有遍历操作统一用 `shouldSkip` 检查，`.git` / `.cursor` / `node_modules` 等全部跳过。

---

### replaceInDir 多 key 替换顺序不确定（已修复）

**v0.8.0 之前**：Go map 遍历顺序不确定，短 key 可能先于长 key 执行，导致 `github.com/Ixecd/dev-toolkit` 被替换成 `github.com/Ixecd/myapp` 而不是 `github.com/me/myapp`。

**v0.8.0 修复**：按 key 长度降序排序，长的先替换。

---

### 反引号在 Go raw string 里导致编译错误

`--with-frontend` 生成的 TypeScript 文件里不能有反引号（Go raw string 的边界符）。

**已修复**：所有前端模板字符串改为字符串拼接，不使用模板字面量。

---

### pgx v5 找不到表

**症状**：migrate 成功，但查询报 `relation "users" does not exist`。

**原因**：pgx v5 连接时会重置 `search_path` 为空。

**解法**：DSN 加 `&search_path=public`：

```
postgres://user:pass@localhost:5432/myapp?sslmode=disable&search_path=public
```

---

## 六、部署配置

### VERSION 不改不会重新 build/push

`deploy.mk` 里检查镜像是否已存在于 Docker Hub，如果 VERSION 没变直接跳过 build/push。代码改了但没改 VERSION，新代码不会生效。

**标准姿势**：

```bash
dtk release --version v0.2.0 --deploy
```

---

### controller 镜像未配置导致 deploy 卡住

`values.yaml` 里 `controller.enabled` 默认是 `false`。如果手动改成 `true` 但没有配置正确的镜像，pod 会一直 `ImagePullBackOff`，`--wait` 会超时。

启用 controller 前必须：

1. 构建包含 dtk + kubectl + helm 的镜像
2. 填写 `values.yaml` 里的 `controller.image.repository` 和 `tag`

---

### REGISTRY_PREFIX 未填

留空会导致 push 失败，运行 `dtk doctor` 可以提前检查。

---

### controller 镜像构建必须加 --no-cache

```bash
docker build --no-cache -f build/docker/controller/Dockerfile ...
```

不加 `--no-cache` 时代码改动可能不会进镜像，导致 controller 运行的是旧版本。

---

## 七、CI

### fmt.Println 不能带 \n 结尾

golangci-lint 的 `fmt` 检查会报：

```
fmt.Println arg list ends with redundant newline
```

改成两行：

```go
fmt.Println("检查环境依赖...")
fmt.Println()
```

---

### zsh 里感叹号的特殊含义

zsh 把 `!` 当历史扩展符号，commit message 含 `!` 时用单引号：

```bash
git commit -m 'feat!: breaking change'
# 不能用双引号：git commit -m "feat!: Crazy"  ← zsh 报 illegal modifier
```

---

### CI 配置了但不阻止合并

只写了 CI yaml 文件，没有在 GitHub 配 branch protection rules，CI 挂了代码还是能合并。

solo 开发时这是正常配置（CI 只用于提醒），如果需要强制阻止：

**Settings → Branches → Add branch ruleset → Require status checks to pass**

然后把 CI job 名称加入 status checks 列表。