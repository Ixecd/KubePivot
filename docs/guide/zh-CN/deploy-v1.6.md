# 部署指南

`kp deploy` 是 KubePivot 的核心命令，封装了从构建镜像到 K8s 滚动更新的完整流程。

---

## 完整流程

```
kp deploy
  │
  ├── 1. 读取 configs/components.yaml     解析组件列表 + 跨 namespace 依赖
  ├── 2. 读取 configs/project.env         读取 VERSION / ARCH / REGISTRY_PREFIX
  ├── 3. AI 规划资源                       replicas / cpu / memory / storage
  ├── 4. 过滤 image="" 的组件             CLI 工具不部署，只部署有 image 的服务
  ├── 5. 前置检查
  │     ├── 迁移兼容性（破坏性变更阻断）
  │     ├── Secret 存在性（缺失则警告）
  │     └── CVE 扫描（Trivy，有则运行）
  │
  ├── 6. --changed-only（可选）           git diff 增量部署，只部署有变更的服务
  │
  └── 7. 按拓扑层级部署（Kahn 算法）
        │   同层并行（受 --parallelism 控制），层间串行
        ├── build                         docker build
        ├── push                          docker push
        ├── helm upgrade --install --wait
        └── rollout status
              │
              失败 → 级联 rollback → 整组 rollback → kp down
              │
  └── 8. 耗时统计（各服务 build/push/helm/rollout 耗时表格）
```

---

## 部署前自动检查

`kp deploy` 在实际部署前会自动执行以下检查：

| 检查项       | 阻断条件               | 跳过方式                   |
| ------------ | ---------------------- | -------------------------- |
| 迁移兼容性   | 待执行迁移含破坏性变更 | `--force-migrate`（不推荐）|
| Secret 存在性| 缺少 secretKeyRef 的 Secret | 先运行 `create-secret.sh` |
| CVE 扫描     | 发现 CRITICAL/HIGH 漏洞 | `--severity LOW`（降低阈值）|

---

## 所有 flag

```bash
kp deploy [flags]

  --components string      components.yaml 路径（默认 configs/components.yaml）
  --namespace  string      kubernetes namespace
  --context    string      kubernetes context
  --kubeconfig string      kubeconfig 路径
  --dry-run                只打印规划，不实际执行
  --sign                   部署后对镜像进行 cosign keyless 签名
  --force-migrate          忽略破坏性迁移警告强制部署（不推荐）
  --changed-only           只部署有 git 变更的服务（基于 git diff HEAD~1 HEAD）
  --parallelism  int       同层最大并发部署数（0=不限制，默认；大规模集群建议 4-8）
```

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
  - name: myapp             # 必须与 cmd/ 下 binary 名一致，也是 deployment 名
    port: 8080              # 容器端口
    image: myapp            # 非空 = 参与 build/push/deploy
                            # 留空或 "" = 跳过，不构建不部署
    strategy: rolling       # rolling（默认）/ blue-green / canary
    api_version: v1         # 对外 API 版本，用于 kp compat 依赖检查
    type: deployment        # deployment（默认）/ statefulset
    namespace: myapp        # 所在 namespace（可选，默认用 project.env 的 KUBE_NAMESPACE）
    depends_on:
      - myapp-postgres      # 同 namespace 依赖，参与 DAG 拓扑排序
      - kube-system/dns     # 跨 namespace 依赖（格式：ns/svc），只做只读嗅探
```

**`image` 为空的使用场景：**

CLI 工具（如 `kp` 本身）、基础设施服务（postgres/etcd）不应走 build/push 流程：

```yaml
components:
  - name: myapp-postgres
    type: statefulset
    port: 5432
    image: ""    # 基础设施，不构建，只部署 helm chart
```

---

## project.env 详解

```ini
PROJECT_NAME=myapp          # 项目名，helm release name 的前缀，namespace 默认值

REGISTRY_PREFIX=qingchun22  # 镜像前缀，拼出来是 qingchun22/myapp-arm64:v0.1.0
                             # Docker Hub username，或 ACR 地址
                             # ACR 格式：registry.cn-hangzhou.aliyuncs.com/ns
                             # ACR 会自动跳过 -arch 后缀

KUBE_CONTEXT=               # kubectl context 名
                             # 留空 = 使用当前 context
                             # ⚠️ 禁止写 ""，空字符串会导致 --kube-context "" 报错

KUBE_NAMESPACE=myapp        # K8s namespace，不存在时自动创建

ARCH=arm64                  # 镜像架构，影响 tag 后缀（arm64 / amd64）
                             # ACR 仓库不需要此后缀，会自动跳过

VERSION=v0.1.0              # 镜像 tag，改这个触发重新 build + push + deploy

ETCD_ENDPOINTS=             # etcd 地址（如 localhost:2379）
                             # 留空 = 状态存本地文件 ~/.kp/state/
```

---

## 增量部署（--changed-only）

基于 `git diff HEAD~1 HEAD` 自动识别有变更的服务，只 build/push/deploy 这些服务，其余跳过：

```bash
kp deploy --changed-only
```

变更识别规则：

| 变更路径 | 部署范围 |
|----------|---------|
| `configs/**` / `go.mod` / `go.sum` | 全量部署（全局变更） |
| `internal/**` | 所有有 image 的服务 |
| `cmd/<service>/**` | 该服务 |
| `deployments/<project>/<service>/**` | 该服务 |
| `scripts/**` / `build/**` | 全量部署 |

示例输出：

```
[07:52:10] 🎯 增量部署（1/3 个服务有变更）: wallet-service
  ⏭  跳过（无变更）: chain-miner
```

---

## 并发度控制（--parallelism）

同层多个服务默认无限并行。大规模集群 Apiserver 延迟高时，用并发度限制防止打垮 Apiserver：

```bash
kp deploy --parallelism 4   # 同层最多 4 个服务同时部署
```

结合 `kp doctor --perf` 的建议值使用：

```bash
kp doctor --perf
# ⚠️ Apiserver P99=562ms → 建议 --parallelism=4

kp deploy --parallelism 4
```

---

## 部署耗时统计

每次部署结束后自动输出各服务的 build/push/helm/rollout 耗时：

```
  部署耗时统计
  服务                       build     push      helm      rollout   总计
  ───────────────────────────────────────────────────────────────────────
  web3-blitz-postgres        -         -         0.2s      -         0.2s
  web3-blitz-etcd            -         -         0.2s      -         0.2s
  web3-blitz-controller      -         -         0.2s      -         0.2s
  wallet-service             12.6s     9.8s      0.4s      0.1s      22.9s
```

`-` 表示该阶段被跳过（image 为空则无 build/push，StatefulSet 无 rollout 等待）。

---

## 蓝绿发布

在 `configs/components.yaml` 里声明：

```yaml
components:
  - name: wallet-service
    strategy: blue-green
    image: wallet-service
    port: 2113
```

支持的策略：

| 策略 | 行为 |
|------|------|
| `rolling`（默认）| K8s 原生滚动更新 |
| `blue-green` | 内置蓝绿，`kp deploy` + `kp promote` 两步完成 |
| `canary` | 打印提示并调用 `scripts/canary-hook.sh`（用户自实现） |

**蓝绿完整流程**：

```bash
# 1. 部署新版本到非活跃 slot（不影响线上流量）
kp deploy
# [07:52:10] 🔵 wallet-service 蓝绿发布：部署到 green slot
# [07:52:10] ✅ wallet-service 已部署到 green slot，运行 kp promote 切换流量

# 2. 验证新版本（可选）
kubectl port-forward -n web3-blitz deployment/web3-blitz-wallet-service-green 2113:2113
curl http://localhost:2113/healthz

# 3. 切换流量
kp promote --service wallet-service
# Service selector: app=wallet-service → app=wallet-service, bluegreen-slot=green

# 4. 如有问题，切回旧版本
kp rollback
```

---

## 结构化日志（可观测性）

```bash
# JSON 格式输出到 stderr（适合接入 ELK/Loki/Grafana）
LOG_FORMAT=json kp deploy 2>deploy.log

# 调试模式（打印每个步骤的开始事件）
LOG_LEVEL=debug kp deploy

# 示例 JSON 日志
{"time":"2026-04-03T07:03:03.730082+08:00","level":"INFO","msg":"deploy.done","msg":"helm upgrade web3-blitz-web3-blitz-etcd 完成","elapsed_ms":217}
{"time":"2026-04-03T07:03:04.123456+08:00","level":"ERROR","msg":"deploy.fail","msg":"wallet-service rollout 超时","elapsed_ms":120003}
```

---

## 部署到国内 / 生产环境

**镜像仓库换阿里云 ACR：**

```ini
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

检查 readiness probe。生成的 `values.yaml` 中探测路径是 `/healthz`，确认服务确实在该路径返回 200：

```bash
kubectl exec -n myapp <pod-name> -- wget -qO- http://localhost:8080/healthz
```

**迁移兼容性检查阻断了部署**

```
❌ 检测到破坏性数据库变更，部署已阻断
```

先运行 `kp migrate plan` 查看具体风险，确认安全后：

```bash
kp deploy --force-migrate   # 不推荐，除非你确认风险可控
```

**--changed-only 检测不到变更**

git 历史不足（只有一个 commit）时，`--changed-only` 会自动降级为全量部署并打出警告：

```
⚠️ git 历史不足，跳过增量检测，全量部署
```

**大规模集群部署慢**

先检测 Apiserver 延迟：

```bash
kp doctor --perf
```

根据建议设置并发度：

```bash
kp deploy --parallelism 4   # P99 > 500ms 时推荐
kp deploy --parallelism 2   # P99 > 1s 时推荐
kp deploy --parallelism 1   # P99 > 2s 时推荐
```
