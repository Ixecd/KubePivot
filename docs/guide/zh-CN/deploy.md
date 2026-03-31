# 部署指南

`kp deploy` 是 KubePivot 的核心命令，封装了从构建镜像到 K8s 滚动更新的完整流程。

---

## 完整流程

```
kp deploy
  │
  ├── 1. 读取 configs/components.yaml     解析组件列表
  ├── 2. 读取 configs/project.env         读取 VERSION / ARCH / REGISTRY_PREFIX
  ├── 3. AI 规划资源                       replicas / cpu / memory / storage
  ├── 4. 过滤 image="" 的组件             CLI 工具不部署，只部署有 image 的服务
  ├── 5. 前置检查                          Secret 存在性 / CVE 扫描 / 迁移兼容性
  │
  └── 按拓扑层级部署（同层并行，层间串行）
        ├── build                         docker build（VERSION 不变则跳过）
        ├── push                          docker push
        ├── helm upgrade --install --wait
        └── rollout status
```

---

## 部署前自动检查

`kp deploy` 在实际部署前会自动执行以下检查，有问题时阻断部署：

| 检查项        | 阻断条件                        | 跳过方式                            |
| ------------- | ------------------------------- | ----------------------------------- |
| Secret 存在性 | 缺少 secretKeyRef 引用的 Secret | 先运行 `./scripts/create-secret.sh` |
| CVE 扫描      | 发现 CRITICAL/HIGH 漏洞         | `--severity LOW` 降低阻断级别       |
| 迁移兼容性    | 待执行迁移含破坏性变更          | `--force-migrate`（不推荐）         |

---

## VERSION 机制

**VERSION 是触发重新部署的开关。**

```
VERSION 不变 → 本地 docker image inspect 发现已有该 tag
             → 跳过 build 和 push
             → helm 用已有镜像，不更新

VERSION 变了 → 重新 build → push → helm 更新 image tag → 滚动更新
```

每次发布新版本：

```bash
# 推荐方式：用 kp release 自动管理版本
kp release --version v0.2.0

# 或手动编辑 configs/project.env
VERSION=v0.2.0
kp deploy
```

---

## components.yaml 详解

```yaml
components:
  - name: myapp           # 必须与 cmd/ 下 binary 名一致，也是 deployment 名
    port: 8080            # 容器端口
    image: myapp          # 非空 = 参与 build/push/deploy
                          # 留空或 "" = 跳过，不构建不部署
    strategy: rolling     # rolling（默认）/ blue-green / canary
    api_version: v1       # 对外 API 版本，用于 kp compat 依赖检查
    type: deployment      # deployment（默认）/ statefulset
    depends_on: []        # 依赖的服务名，kp 按依赖顺序部署
```

**`image` 为空的使用场景：**

CLI 工具（如 `kp` 本身）不应部署到 K8s。CLI 启动后打印 usage 立刻退出，
K8s 会认为进程崩溃，导致 CrashLoopBackOff 无限重启。

```yaml
components:
  - name: kp
    port: 0
    image: ""    # CLI 工具，跳过部署
```

---

## project.env 详解

```env
PROJECT_NAME=myapp          # 项目名，helm release name 和 namespace 默认值

REGISTRY_PREFIX=qingchun22  # 镜像前缀，拼出来是 qingchun22/myapp-arm64:v0.1.0
                             # 可以是 Docker Hub username 或 ACR 地址
                             # ACR 格式：registry.cn-hangzhou.aliyuncs.com/ns（自动跳过 -arch 后缀）

KUBE_CONTEXT=               # kubectl context 名
                             # 留空 = 使用当前 context
                             # ⚠️ 禁止写 ""，空字符串会导致 --kube-context "" 报错

KUBE_NAMESPACE=myapp        # K8s namespace，不存在时自动创建

ARCH=arm64                  # 镜像架构，影响 tag 后缀（arm64 / amd64）
                             # ACR 仓库不需要此后缀，会自动跳过

VERSION=v0.1.0              # 镜像 tag，改这个触发重新 build+push
```

---

## 蓝绿发布

在 `configs/components.yaml` 里声明：

```yaml
components:
  - name: wallet-service
    strategy: blue-green   # rolling（默认）/ blue-green / canary
    image: wallet-service
    port: 2113
```

支持的策略：

- `rolling`（默认）：K8s 原生滚动更新
- `blue-green`：内置蓝绿，`kp deploy` + `kp promote` 两步完成
- `canary`：打印提示并调用 `scripts/canary-hook.sh`（用户自实现）

**蓝绿完整流程**：

```bash
kp deploy                     # 部署新版本到非活跃 slot（不影响线上流量）
# 验证新版本
kubectl port-forward deployment/wallet-service-green ...
kp promote                    # 切换流量
kp rollback                   # 如有问题，切回旧版本
```

---

## 部署到国内 / 生产环境

**镜像仓库换阿里云 ACR：**

```env
REGISTRY_PREFIX=registry.cn-hangzhou.aliyuncs.com/yournamespace
```

ACR 格式自动跳过 `-arch` 后缀，镜像名格式为 `registry.cn-xxx/ns/image:tag`。

**多架构镜像：**

```bash
make image.multiarch PLATFORMS="linux_amd64 linux_arm64"
make push.multiarch
```

---

## 常见问题

**部署卡住不动**

`--wait` 在等 pod ready，另开终端查看：

```bash
kubectl get pods -n myapp
kubectl describe pod -n myapp <pod-name>
kubectl logs -n myapp <pod-name>
```

**SSA field manager 冲突**

```
conflict with "kubectl-set" using apps/v1
```

清除 field manager 记录：

```bash
kubectl patch deployment myapp -n myapp \
  --type=merge \
  -p '{"metadata":{"managedFields":null}}'
```

**pod 一直 0/1 Running，不变 Ready**

检查 readiness probe。生成的 `values.yaml` 中探测路径是 `/healthz`，
确认服务确实在该路径返回 200。

```bash
kubectl exec -n myapp <pod-name> -- wget -qO- http://localhost:8080/healthz
```

**迁移兼容性检查阻断了部署**

```
❌ 发现破坏性 DB 变更，升级终止
```

先运行 `kp migrate plan` 查看具体风险，确认安全后：

```bash
kp deploy --force-migrate   # 不推荐，除非你确认风险可控
```

或者先修复迁移文件，再重新部署。