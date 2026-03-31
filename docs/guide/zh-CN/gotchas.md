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

### [P1 Bug] 蓝绿发布失败后状态机停在 DEPLOYING

**现象**：蓝绿发布的 rollout 失败，级联 rollback 完成后状态机应回到 RUNNING，但实际停在 DEPLOYING，导致下次 `kp deploy` 报"不能发起新部署"。

**临时解法**：

```bash
kp resume   # resume 会检测实际状态，重新部署
```

**根因**：`cascadeOK=true` 时调用 `sm.Transition(StateRunning)` 的路径在蓝绿场景下判断不正确，待 v1.5.2 修复。

### [P1 Bug] kp resume 检测到 IDLE 就重新部署（蓝绿场景误判）

**现象**：蓝绿发布失败 → rollback → 状态机 DEPLOYING → `kp resume` 检测 K8s 状态时只看 StatefulSet，rolling release 的 Deployment 还在 Running，但被判断为 IDLE，导致从头重新部署。

**临时解法**：手动跑 `kp rollback` 让状态机回到 RUNNING，再重新 `kp deploy`。

**根因**：resume 的 IDLE 判断逻辑只检查 StatefulSet，未检查 Deployment，待修复。

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

---

## 四、数据库迁移

### kp migrate run 失败不会自动回滚 helm

这是故意的设计：DB 迁移失败 ≠ 部署失败，自动回滚太激进。

失败后用户有两个选择：

1. 修复迁移 SQL，重新 `kp migrate run`
2. `kp rollback` 回滚整个服务

### dirty migration 导致迁移被锁

golang-migrate 在迁移失败后会把版本标记为 `dirty=true`，后续所有迁移都会被阻断。

```bash
kp migrate status   # 查看 dirty 状态
# 手动清除 dirty 标记（先修复 SQL 再操作）
psql -c "UPDATE schema_migrations SET dirty=false WHERE version=<version>"
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

---

## 六、镜像相关

### ACR 镜像名不需要 -arch 后缀

普通 Docker Hub：`qingchun22/wallet-service-arm64:v0.1.12`

阿里云 ACR：`registry.cn-hangzhou.aliyuncs.com/ns/wallet-service:v0.1.12`（自动跳过 `-arch`）

### kp release push 失败后 tag 已打，重试报"已存在"

```bash
git push && git push --tags
```

---

## 七、controller 相关

### controller 自愈时间约 13 秒

etcd Watch（事件驱动）+ 8 秒周期 Reconcile。从资源被删除到恢复约 13 秒。

### VALIDATING 阶段依赖 /healthz

脚手架生成的 `cmd/<n>/main.go` 已包含此路由，不要删除。

---

## 八、安全相关（v1.2.0+）

### kp doctor 安全检查

```bash
kp doctor   # 检查环境依赖 + 安全基线 + etcd 健康
ETCD_ENDPOINTS=<host>:2379 kp doctor   # 含 etcd 连通性检查
```

### etcd raft index 差值告警

```
⚠ etcd raft index   raftIndex=48161, raftAppliedIndex=47000, diff=1161
```

diff > 1000 说明集群有压力，关注磁盘 IO 和网络延迟。diff > 10000 立即排查（可能脑裂）。