# 已知坑和注意事项

> 遇到问题先查这里，大概率能找到答案。

---

## 一、部署相关

### kp deploy 前必须有 Secret（v1.2.0+）

`kp deploy` 会自动扫描 `deployments/` 下所有 yaml 里的 `secretKeyRef`，如果 K8s 里没有对应的 Secret，会警告并可能导致 pod 启动失败。

先创建 Secret：

```bash
./scripts/create-secret.sh   # kp init 自动生成，幂等
```

**`kp down` 会删除整个 namespace，包括 Secret**，重新部署前必须重建：

```bash
kp down
./scripts/create-secret.sh   # ← 必须！
kp deploy
```

### DATABASE_URL 在 K8s 里要用 Service 名

`.env` 里的 `DATABASE_URL` 通常是 `localhost:5432`（本地开发），但 K8s 里要用 Service 名：

```bash
# ❌ .env 里的（本地开发用）
DATABASE_URL=postgres://blitz:blitz@localhost:5432/blitz

# ✅ K8s Secret 里的（create-secret.sh 已自动处理）
DATABASE_URL=postgres://blitz:blitz@web3-blitz-postgres:5432/blitz
```

### resources.yaml 里的资源名必须和实际 K8s 资源名完全一致

`resources.yaml` 里声明的 `name` 用于 controller 自愈检测和 `kp resume` 的 IDLE 判断。如果名字对不上，controller 会认为资源不存在，触发误报或误愈。

```yaml
# ❌ 错误：写了缩写
resources:
  - kind: StatefulSet
    name: postgres         # 实际名字是 web3-blitz-postgres

# ✅ 正确：和实际 K8s 资源名完全一致
resources:
  - kind: StatefulSet
    name: web3-blitz-postgres
```

`kp init` 生成的模板已自动使用 `{project}-postgres` 格式，存量项目需手动检查。

---

## 二、状态机

### 当前状态为 DEPLOYING，不能发起新部署

```
当前部署状态为 DEPLOYING，不能发起新部署
如需继续，请运行: kp resume
```

原因：上次部署中断（网络超时、手动 Ctrl+C 等），状态机停在 DEPLOYING。

```bash
kp resume   # 从中断点恢复
```

### 状态机卡住需要手动重置

```bash
python3 -c "
import json, os
p = os.path.expanduser('~/.kp/state/<project>/<ns>.json')
d = json.load(open(p))
d['state'] = 'IDLE'
json.dump(d, open(p, 'w'), indent=2)
"
```

---

## 三、蓝绿发布

### Chart 模板必须用 Release.Name（不能硬编码服务名）

蓝绿发布时会创建两个 helm release（`web3-blitz-wallet-service` 和 `web3-blitz-wallet-service-green`），如果 chart 模板里 `metadata.name` 硬编码了服务名，第二个 release 创建同名资源时会报 ownership 冲突：

```
Error: unable to continue with install: Service "wallet-service" in namespace "web3-blitz"
exists and cannot be imported into the current release: invalid ownership metadata
```

**修法**：所有 `metadata.name` 改用 `{{ .Release.Name }}`：

- `service.yaml`
- `serviceaccount.yaml`
- `deployment.yaml`（`metadata.name`）

### Service 冲突：蓝绿 slot 不应创建独立 Service

入口 Service（`wallet-service`）由原始 rolling release 持有，`kp promote` patch 它的 selector 来切换流量。蓝绿 slot（green/blue）不需要自己的 Service。

在 `service.yaml` 加条件跳过：

```yaml
{{- if not .Values.bluegreen.skipService }}
apiVersion: v1
kind: Service
...
{{- end }}
```

`values.yaml` 加默认值：

```yaml
bluegreen:
  slot: ""
  skipService: false
```

kp 在蓝绿部署时自动传 `--set bluegreen.skipService=true`。

### helm release 被锁（pending-install/pending-upgrade）

蓝绿部署失败后 helm release 可能停在 pending 状态，后续操作报：

```
Error: UPGRADE FAILED: another operation (install/upgrade/rollback) is in progress
```

```bash
kubectl delete secret -n <ns> \
  $(kubectl get secret -n <ns> -l owner=helm,name=<release> \
    -o jsonpath='{.items[?(@.metadata.labels.status=="pending-install")].metadata.name}')
```

### AppVersion 写死导致 green slot 拉错镜像

`Chart.yaml` 里的 `appVersion` 是 `image.tag` 的 fallback，如果 `values.yaml` 里 `image.tag` 为空，会拉 `appVersion` 的版本（通常是旧版本）。

确认 `values.yaml` 里：

```yaml
image:
  tag: ""   # 留空，kp 部署时通过 --set image.tag=<version> 传入
```

同时确认 `Chart.yaml` 的 `appVersion` 同步更新，否则手动 helm upgrade 时会出问题。

---

## 四、数据库迁移

### kp migrate run 失败不会自动回滚 helm

这是故意的设计：DB 迁移失败 ≠ 部署失败，自动回滚太激进。

失败后有两个选择：

1. 修复迁移 SQL，重新 `kp migrate run`
2. `kp rollback` 回滚整个服务

### dirty migration 导致迁移被锁

golang-migrate 在迁移失败后会把版本标记为 `dirty=true`，后续所有迁移都会被阻断。

```bash
kp migrate status   # 查看 dirty 状态
# 手动清除 dirty 标记（先修复 SQL 再操作）
psql -c "UPDATE schema_migrations SET dirty=false WHERE version=<version>"
```

### kp deploy 迁移兼容性检查阻断了部署

```
❌ 检测到破坏性数据库变更，部署已阻断
```

先运行 `kp migrate plan` 查看具体风险，确认安全后：

```bash
kp deploy --force-migrate   # 不推荐，除非确认风险可控
```

---

## 五、Helm 相关

### helm upgrade 报 pending-rollback

controller 和 kp deploy 并发操作可能导致 release 进入 `pending-rollback` 状态。

```bash
kubectl scale deployment/kubepivot-controller -n <ns> --replicas=0
kubectl delete secret -n <ns> \
  $(kubectl get secret -n <ns> -l owner=helm,name=<release> \
    -o jsonpath='{.items[?(@.metadata.labels.status=="pending-rollback")].metadata.name}')
kp deploy
kubectl scale deployment/kubepivot-controller -n <ns> --replicas=1
```

### helm-diff 未安装时 kp diff --drift 不可用

```bash
helm plugin install https://github.com/databus23/helm-diff
```

运行 `kp doctor` 可以检测是否已安装。

---

## 六、镜像相关

### ACR 镜像名不需要 -arch 后缀

普通 Docker Hub：`qingchun22/wallet-service-arm64:v0.1.12`

阿里云 ACR：`registry.cn-hangzhou.aliyuncs.com/ns/wallet-service:v0.1.12`（自动跳过 `-arch`）

在 `project.env` 里配置：

```ini
REGISTRY_PREFIX=registry.cn-hangzhou.aliyuncs.com/yournamespace
```

### kp release push 失败后 tag 已打，重试报"已存在"

```bash
git push && git push --tags
```

---

## 七、Secret 轮转相关（v1.5.2+）

### graceful 轮转后必须手动在 DB 端禁用旧密码

`kp secret rotate --strategy graceful` 执行完成后，旧密码会保留为 `*_OLD` 字段，用于重启期间的双密码过渡。**旧密码不会自动失效**，你需要：

1. 在数据库端手动禁用旧密码
2. 确认所有服务都已使用新密码（查看 pod 日志）
3. 运行 `kp secret cleanup --secret <n>` 删除 `*_OLD` 字段

### Volume 挂载的 Secret 更新后应用不一定热加载

如果 Secret 是以 Volume 形式挂载的，Kubelet 会自动更新文件，但**应用进程不一定会感知到**。kp 在检测到 Volume 挂载时会强制触发 `rollout restart` 并打出警告：

```
⚠️ wallet-service 通过 Volume 挂载 Secret，应用需支持热加载，强制触发 rollout restart
```

如果你的应用支持 SIGHUP 热加载，可以在 `rollout restart` 前配置信号处理。

### TLS 证书过期检测

`kp doctor` 和 `kp secret audit` 会扫描所有 `kubernetes.io/tls` 类型的 Secret：

- 30 天内过期 → ⚠️ 警告
- 7 天内过期 → ❌ 错误

自签证书可用脚本生成：

```bash
./scripts/gencerts.sh generate-cert ./output/cert myapp
```

---

## 八、controller 相关

### controller 自愈时间约 13 秒

etcd Watch（事件驱动）+ 8 秒周期 Reconcile + WorkQueue 去重（1s dedupWindow）。从资源被删除到恢复约 13 秒。

### VALIDATING 阶段依赖 /healthz

脚手架生成的 `cmd/<n>/main.go` 已包含此路由，不要删除。

### controller 镜像必须包含 kp 二进制

controller 镜像（`qingchun22/kubepivot-controller`）内置了 kp CLI，CMD 是 `kp controller start`。如果你 fork 了项目并修改了 CLI，需要重新构建镜像：

```bash
docker build -f build/docker/controller/Dockerfile -t <your-registry>/kubepivot-controller:<version> .
docker push <your-registry>/kubepivot-controller:<version>
```

同步更新 `deployments/<project>/web3-blitz-controller/values.yaml` 里的 `image.repository` 和 `image.tag`。

---

## 九、安全相关（v1.2.0+）

### kp doctor 安全检查

```bash
kp doctor                              # 全量检查
kp doctor --perf                       # 含 Apiserver 延迟测试
kp doctor --perf --context prod        # 指定集群
ETCD_ENDPOINTS=<host>:2379 kp doctor   # 含 etcd 连通性检查
```

### etcd raft index 差值告警

```
⚠ etcd raft index   raftIndex=48161, raftAppliedIndex=47000, diff=1161
```

diff > 1000 说明集群有压力，关注磁盘 IO 和网络延迟。diff > 10000 立即排查（可能脑裂）。

### kp doctor --perf 建议降低并发度

```
⚠️ Apiserver 延迟  P50=288ms  P99=562ms  → 建议 --parallelism=4
```

大规模集群 Apiserver 延迟高时，用 `--parallelism` 控制 helm 并发数，防止把 Apiserver 打垮：

```bash
kp deploy --parallelism 4
```

---

## 十、跨 namespace 依赖（v1.6.0+）

### depends_on 里的跨 namespace 格式

```yaml
depends_on:
  - web3-blitz-postgres      # 同 namespace：参与 DAG 拓扑排序
  - kube-system/coredns      # 跨 namespace：只做只读嗅探，不参与 DAG
```

### kp doctor 跨域嗅探只做只读检测

kp 对跨 namespace 资源**只申请读权限，不会自愈**。发现跨 namespace 依赖缺失时，kp 只做告警：

```
⚠️ 跨域依赖 wallet-service→kube-system/coredns  namespace/kube-system 中 coredns 不存在
   请手动修复：检查 kube-system namespace 是否已部署，或确认 RBAC 只读权限
```

### kp network gen 不自动 apply

`kp network gen` 只生成 NetworkPolicy YAML 模板到 `deployments/<project>/network/`，**不会自动 apply**。生成后需要人工审查再执行：

```bash
kp network gen
# 审查生成的文件
cat deployments/myapp/network/networkpolicy-wallet-service-cross-ns.yaml
# 确认无误后手动执行
kubectl apply -f deployments/myapp/network/
```

这是故意的设计：NetworkPolicy 写错了默认拒绝入站，误杀流量是秒级生效的，必须人工确认。
