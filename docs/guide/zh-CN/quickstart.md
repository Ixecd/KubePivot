# Quickstart — KubePivot

> 从零到服务跑在 K8s 上，预计 15 分钟。

---

## 前置条件

| 工具     | 最低版本 | 检查命令                   |
| -------- | -------- | -------------------------- |
| Go       | 1.21+    | `go version`               |
| Docker   | 任意     | `docker version`           |
| kubectl  | 任意     | `kubectl version --client` |
| helm     | 3.x      | `helm version`             |
| K8s 集群 | 任意     | `kubectl cluster-info`     |

K8s 集群可以是本地的（OrbStack、Docker Desktop、minikube）或远程集群。

```bash
kp doctor   # 一键检查所有前置条件
```

可选增强（有则自动启用，无则静默跳过）：

| 工具 | 用途 |
|------|------|
| trivy | CVE 扫描（`kp scan` / `kp deploy` 前） |
| helm-diff | drift 检测（`kp diff --drift`） |
| oasdiff | API 兼容性检查（`kp compat check`） |
| opa | OPA 策略检查（`kp deploy` 前自动运行） |

---

## 第一步：安装 kp

```bash
go install github.com/Ixecd/kubepivot/cmd/kp@latest

# 验证
kp version
```

升级到最新版本：

```bash
kp update
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
├── cmd/myapp/          # 业务服务入口
├── internal/           # 业务逻辑（api/db/service）
├── migrations/         # SQL 迁移文件
├── configs/
│   ├── project.env     # 部署配置
│   ├── components.yaml # 服务列表 + 依赖关系
│   └── resources.yaml  # Controller 监控资源
├── deployments/
│   ├── myapp/          # 业务服务 Helm chart
│   ├── myapp-postgres/ # PostgreSQL chart
│   ├── myapp-etcd/     # etcd chart
│   └── myapp-controller/ # A2 Controller chart
└── scripts/
    └── create-secret.sh
```

---

## 第三步：配置部署参数

编辑 `configs/project.env`：

```ini
PROJECT_NAME=myapp
REGISTRY_PREFIX=your-dockerhub-username   # ← 必填
KUBE_CONTEXT=                             # ← 留空=当前 context
KUBE_NAMESPACE=myapp
ARCH=arm64                                # arm64 或 amd64
VERSION=v0.1.0
ETCD_ENDPOINTS=                           # ← 留空=本地文件存储状态
```

**只有 `REGISTRY_PREFIX` 是必须填的。**

多集群支持：

```bash
kp context add --name prod --context my-k8s --namespace production
kp deploy --env prod   # 部署到 prod 环境
```

---

## 第四步：创建 K8s Secret

```bash
./scripts/create-secret.sh   # 幂等，可多次运行
```

敏感变量（DB 密码、JWT 密钥）不进 git，通过 Secret 注入。

---

## 第五步：（可选）添加 OPA 策略

```bash
kp policy add --name no-latest-tag \
  --file examples/policies/no-latest-tag.rego

kp policy list    # 查看已配置策略
```

策略检查在 `kp deploy` 前自动运行，未安装 `opa` 则静默跳过。

---

## 第六步：部署

```bash
kp deploy
```

完整输出示例：

```
[07:14:02] ✅ OPA 策略检查通过
[07:14:02] 🗂  多服务模式：2 层，独立 helm release
[07:14:02] 🔍 开始扫描 1 个镜像（阻断级别: CRITICAL,HIGH）
[07:14:03] ✓  myapp:v0.1.0 无漏洞
[07:14:03] 📦 部署第 1 层（2 个服务）
[07:14:03] ✓  helm upgrade myapp-myapp-postgres 完成
[07:14:03] ✓  第 1 层全部就绪
[07:14:03] 📦 部署第 2 层（1 个服务）
[07:14:05] ✓  构建 myapp 完成（8.3s）
[07:14:08] ✓  推送 myapp 完成（3.1s）
[07:14:12] ✓  helm upgrade myapp-myapp 完成（9.2s）
[07:14:14] ✓  myapp 就绪（1.8s）
[07:14:14] ✅ 部署完成，状态: RUNNING (version=v0.1.0)
```

验证：

```bash
kubectl get pods -n myapp
kp status
```

---

## 第七步：访问服务

```bash
kubectl port-forward -n myapp deployment/myapp 8080:8080
curl http://localhost:8080/healthz
```

---

## 常用命令速查

### 日常部署

```bash
kp deploy                    # 全量部署
kp deploy --changed-only     # 增量（只部署有 git 变更的服务）
kp deploy --parallelism 4    # 控制并发（大规模集群）
kp deploy --dry-run          # 只看规划，不执行
kp deploy --env prod         # 部署到指定环境
kp resume                    # 中断后恢复
kp rollback                  # 手动回滚
```

### 查看状态

```bash
kp status                    # 当前状态
kp status --all-envs         # 跨集群统一视图
kp status --env prod         # 指定环境
kp diff                      # helm values 变更对比
kp diff --drift              # 配置漂移检测
kp diff --to-env prod        # 环境间配置对比
```

### 蓝绿发布

```bash
kp deploy --preview          # 部署到 inactive slot + 生成 Header 路由模板
kp promote --service myapp   # 切换流量
kp warmup --service myapp \  # 线性预热
  --steps 10,50,100 --interval 2m,5m
```

### 数据库迁移

```bash
kp migrate status
kp migrate plan
kp migrate run --dry-run
kp migrate run               # 自动 PVC 快照保护
kp migrate fix-dirty         # 修复 dirty 状态
```

### 原子性迁移（Sandbox）

```bash
kp sandbox start --dry-run   # 查看执行计划
kp sandbox start             # LOCKED→SNAPSHOTTING→SIMULATING→COMMITTING→RUNNING
kp sandbox status
```

### 多集群

```bash
kp context add --name staging --context orbstack --namespace myapp-staging
kp context list
kp deploy --env staging
kp status --all-envs
kp diff --from-env local --to-env staging
```

### 合规 & 安全

```bash
kp audit --format table      # 审计日志
kp policy add --name check --file examples/policies/no-latest-tag.rego
kp secret rotate --secret myapp-secret --strategy graceful
kp secret sync --from vault --secret myapp-secret --vault-path secret/data/myapp
kp scan                      # CVE 扫描
```

### 混沌工程

```bash
kp chaos inject --service myapp --kind pod-kill --duration 30s --dry-run
kp chaos inject --service myapp --kind network-delay --latency 200ms
kp chaos list
kp chaos stop --uid <uid>
```

### 工具链

```bash
kp doctor                    # 环境检查（含 drift 告警）
kp doctor --perf             # 性能基准
kp history --export json     # 部署历史导出
kp version                   # 查看版本
kp update                    # 自动更新
kp plugin install <name>     # 安装插件
kp release --version v0.2.0  # 发布新版本（自动 tag + push）
LOG_FORMAT=json kp deploy    # 结构化日志（接入 ELK/Loki）
```

---

## 遇到问题？

**`kp deploy` 报 image not found**：检查 `REGISTRY_PREFIX` 是否填写，Docker Hub 是否已登录。

**部署卡在 VALIDATING 超时**：确认 `/healthz` 路由返回 200。

**当前状态为 DEPLOYING，不能发起新部署**：运行 `kp resume`。

**当前处于 Sandbox 会话**：运行 `kp sandbox status` 查看，或 `kp sandbox unlock --force --reason "xxx"`（COMMITTING 阶段禁止）。

**helm upgrade 报 pending-rollback**：参考 [常见问题](gotchas.md)。

更多问题参考 [常见问题](gotchas.md)。


---

## 第 N 步（可选）：接入全局 Controller 获得自动自愈

> v2.3.0 新功能。部署完业务之后，可选择接入集群全局 Controller，
> 让服务在资源意外缺失时（比如有人误删 Deployment）自动恢复。

```bash
# 1. 集群级一次性安装（一辈子只跑一次）
kp controller install

# 2. 当前项目接入
kp controller enroll

# 3. 验证
kp controller status
```

Controller 会监听本项目 `configs/resources.yaml` 声明的资源，缺失了自动 `helm rollback` 恢复。

以后修改 `resources.yaml` 推送 `kp deploy` 时会自动同步，无需手工做额外操作。

完整使用见 [controller.md](controller.md)。

