# Quickstart — KubePivot

> 从零到服务跑在 K8s 上，预计 15 分钟。

---

## 前置条件

在开始之前，确认本机已安装：

| 工具     | 最低版本 | 检查命令                   |
| -------- | -------- | -------------------------- |
| Go       | 1.21+    | `go version`               |
| Docker   | 任意     | `docker version`           |
| kubectl  | 任意     | `kubectl version --client` |
| helm     | 3.x      | `helm version`             |
| K8s 集群 | 任意     | `kubectl cluster-info`     |

K8s 集群可以是本地的（OrbStack、Docker Desktop、minikube）或远程集群，只要 `kubectl` 能连上即可。

---

## 第一步：安装 kp

```bash
go install github.com/Ixecd/kubepivot/cmd/kp@latest
```

验证安装：

```bash
kp --help
```

---

## 第二步：生成项目

```bash
kp init --name myapp --module github.com/me/myapp
```

你会看到：

```
✅ 项目已成功生成！

  路径    ~/myapp
  模块    github.com/me/myapp
  入口    cmd/myapp

下一步：
  cd ~/myapp
  make tools   # 安装所有工具
  make build   # 编译
  make test    # 测试

⚠️  上线前请检查：
  deployments/myapp/templates/controller-rbac.yaml
  → 当前为全量权限，请按实际需要收紧 ClusterRole rules
  deployments/myapp/templates/controller-deployment.yaml
  → 替换 controller.image.repository 为你构建的镜像
```

进入项目目录：

```bash
cd myapp
```

---

## 第三步：配置部署参数

编辑 `configs/project.env`：

```ini
PROJECT_NAME=myapp
REGISTRY_PREFIX=your-dockerhub-username   # ← 必填，改成你的 Docker Hub 用户名
KUBE_CONTEXT=                             # ← 留空=当前 context，或填指定 context 名
KUBE_CONFIG=                             # ← 留空=~/.kube/config
KUBE_NAMESPACE=myapp
ARCH=arm64                               # ← 按你的机器改：arm64 或 amd64
VERSION=v0.1.0
ETCD_ENDPOINTS=                          # ← 留空，状态存本地文件
```

**只有 `REGISTRY_PREFIX` 是必须填的**，其他按需修改。

---

## 第四步：确认业务服务有 /healthz 路由

打开 `cmd/myapp/main.go`，确认有 `/healthz` 路由（脚手架已生成，正常不需要改）：

```go
mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
})
```

这是 K8s liveness/readiness probe 和 kp VALIDATING 阶段的依赖，缺了部署会卡住。

---

## 第五步：部署

```bash
kp deploy
```

完整输出示例：

```
AI 规划结果:
- myapp: replicas=1 cpu=100m memory=128Mi storage=1Gi

[07:52:10] 🏗  构建镜像 myapp（v0.1.0）
[07:52:18] ✓  构建完成（8.3s）
[07:52:18] 📤 推送镜像 myapp（v0.1.0）
[07:52:21] ✓  推送完成（3.1s）
[07:52:21] ⛵ helm upgrade myapp
[07:52:30] ✓  helm upgrade 完成（9.2s）
[07:52:30] 🔍 等待 rollout 就绪
[07:52:32] ✓  服务就绪（1.8s）
[07:52:32] ✅ 部署完成，状态: RUNNING (version=v0.1.0)
```

验证：

```bash
kubectl get pods -n myapp
```

看到三个 pod 都是 `Running` 就成功了：

```
NAME                          READY   STATUS    RESTARTS   AGE
myapp-xxx                     1/1     Running   0          30s
myapp-etcd-xxx                1/1     Running   0          30s
myapp-postgres-0              1/1     Running   0          30s
```

---

## 第六步：访问服务

```bash
kubectl port-forward -n myapp deployment/myapp 8080:8080
```

另开一个终端：

```bash
curl http://localhost:8080/healthz
# 返回 200 OK
```

---

## 下一步：填充业务逻辑

骨架生成好之后，参考 `handoff/AI-CODING-GUIDE.md` 了解在哪里加代码、不能动哪些文件。

主要的业务入口：

```
internal/api/handler.go    # 加 HTTP handler
internal/api/server.go     # 注册路由
internal/db/              # 加数据库操作
migrations/               # 加 SQL 迁移文件
```

业务逻辑写完后，发布新版本：

```bash
kp release --version v0.2.0 --deploy
```

这会自动更新 VERSION、打 git tag、重新 build + push + deploy。

---

## 常用命令速查

```bash
# 查看部署状态
kubectl get pods -n myapp
helm history myapp -n myapp

# 部署中断后恢复
kp resume

# 手动回滚
kp rollback

# 彻底下线（删除所有资源）
kp down

# 只看规划，不执行
kp deploy --dry-run

# 数据库迁移
kp migrate status
kp migrate plan
kp migrate run --dry-run

# 跨版本升级
kp upgrade --dry-run
```

---

## 遇到问题？

**`kp deploy` 报 image not found**

检查 `REGISTRY_PREFIX` 是否填写，Docker Hub 是否已登录（`docker login`）。

**部署卡在 VALIDATING 超时**

确认 `/healthz` 路由返回 200，用以下命令手动验证：

```bash
kubectl exec -n myapp deployment/myapp -- wget -qO- http://localhost:8080/healthz
```

**当前状态为 DEPLOYING，不能发起新部署**

```bash
kp resume
```

**helm upgrade 报 pending-rollback**

```bash
kubectl scale deployment/myapp-controller -n myapp --replicas=0
kubectl delete secret -n myapp \
  $(kubectl get secret -n myapp -l owner=helm,name=myapp \
    -o jsonpath='{.items[?(@.metadata.labels.status=="pending-rollback")].metadata.name}')
kp deploy
```

**状态机卡住，需要手动重置**

```bash
python3 -c "
import json, os
p=os.path.expanduser('~/.kp/state/myapp/myapp.json')
d=json.load(open(p))
d['state']='IDLE'
json.dump(d,open(p,'w'),indent=2)
"
```