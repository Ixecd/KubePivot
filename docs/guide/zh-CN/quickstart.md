# Quickstart — KubePivot

> 从零到服务跑在 K8s 上，预计 10 分钟。

---

## 前置条件

| 工具     | 最低版本 | 检查命令                   |
| -------- | -------- | -------------------------- |
| Go       | 1.25+    | `go version`               |
| Docker   | 任意     | `docker version`           |
| kubectl  | 任意     | `kubectl version --client` |
| helm     | 3.x      | `helm version`             |
| K8s 集群 | 任意     | `kubectl cluster-info`     |

```bash
kp doctor   # 一键检查所有前置条件
```

---

## 第一步：安装 kp

```bash
go install github.com/Ixecd/kubepivot/cmd/kp@latest
kp version
kp update      # 升级到最新版
```

---

## 第二步：生成项目

```bash
kp init --name myapp --module github.com/me/myapp
cd myapp
```

生成的项目结构：

```
myapp/
├── cmd/myapp/              # 业务服务入口
├── internal/               # 业务逻辑（api/db/service）
├── migrations/             # SQL 迁移文件
├── build/docker/myapp/     # Dockerfile + build.sh
├── configs/
│   ├── project.env         # 部署配置
│   ├── components.yaml     # 服务列表 + 依赖关系
│   ├── resources.yaml      # Controller 监控资源
│   └── system.yaml         # KubePivot 控制器参数（etcd/调度/缓存）
├── deployments/
│   ├── myapp/              # 业务服务 Helm chart
│   ├── myapp-postgres/     # PostgreSQL chart
│   ├── myapp-etcd/         # etcd chart
│   └── myapp-controller/   # Controller chart
└── scripts/
    └── create-secret.sh
```

## 第三步：配置部署参数

编辑 `configs/project.env`：

```ini
PROJECT_NAME=myapp
REGISTRY_PREFIX=your-dockerhub-username   # ← 必填
KUBE_NAMESPACE=myapp
ARCH=arm64
VERSION=v0.1.0
```

**只有 `REGISTRY_PREFIX` 是必须填的。** 无 Docker 账户时：

```bash
# kind
kind load docker-image qingchun22/kubepivot-controller:latest

# GitHub Container Registry (免费)
docker tag myapp:v0.1.0 ghcr.io/<user>/myapp:v0.1.0
docker push ghcr.io/<user>/myapp:v0.1.0
# 然后改 REGISTRY_PREFIX=ghcr.io/<user>
```

---

## 第四步：部署

```bash
kp deploy
```

验证：

```bash
kubectl get pods -n myapp
kp status
kp scheduler status
```

## 第五步：访问服务

```bash
kubectl port-forward -n myapp deployment/myapp 8080:8080
curl http://localhost:8080/healthz
```

---

## 常用命令速查

### 部署

```bash
kp deploy                    # 全量部署
kp deploy --changed-only     # 增量
kp deploy --env prod         # 指定环境
kp rollback                  # 回滚
```

### 状态

```bash
kp status                    # 当前状态
kp status --all-envs         # 跨集群
kp diff                      # 变更对比
kp diff --drift              # 漂移检测
```

### 蓝绿

```bash
kp deploy --preview          # 部署到 inactive slot
kp promote --service myapp   # 切换流量
```

### 调度 & 性能

```bash
kp scheduler status          # 集群利用率 + 碎片率
kp scheduler reschedule      # 手动触发重调度
kp bench all                 # 全量性能基准（KVCache/调度/内存）
kp bench kvcache             # KVCache 基准
kp bench scale               # 规模化基准 (1k/10k/100k Pods)
```

### 数据库

```bash
kp migrate status / plan / run
```

### 安全部署（Sandbox）

```bash
kp sandbox start             # LOCKED→SNAPSHOTTING→SIMULATING→COMMITTING→RUNNING
kp sandbox status
```

### 多集群

```bash
kp context add --name prod --context my-k8s --namespace production
kp deploy --env prod
```

### 工具链

```bash
kp doctor                    # 环境检查
kp sync                      # 同步框架文件（Makefile/scripts/configs）
kp version                   # 查看版本
kp update                    # 自动更新
kp release --version v0.2.0  # 发布新版本（自动 tag + push）
```

---

## 第 N 步（可选）：接入 Controller 获得自动自愈

```bash
kp controller install        # 集群级一次性安装
kp controller enroll         # 当前项目接入
kp controller status         # 验证
```

Controller 监听 `configs/resources.yaml` 声明的资源，缺失自动 `helm rollback` 恢复。
修改 `resources.yaml` 后 `kp deploy` 自动同步。

完整使用见 [controller.md](controller.md)。

---

## 遇到问题？

- **`kp deploy` 报 image not found**：检查 `REGISTRY_PREFIX`，或用 kind/ghcr 替代方案
- **`make deploy` 报 `copyright.mk: No such file`**：运行 `kp sync` 同步框架文件
- **部署卡在 VALIDATING 超时**：确认 `/healthz` 路由返回 200
- **helm upgrade 报 pending-rollback**：参考 [常见问题](gotchas.md)
