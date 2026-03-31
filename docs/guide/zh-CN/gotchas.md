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

`scripts/create-secret.sh` 已经硬编码了 K8s Service 名，不从 `.env` 的 `DATABASE_URL` 读。

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

### kp resume 检测到 IDLE 后未触发重新部署（已修复 v1.2.0）

**v1.1.0 及之前**：`kp resume` 检测到 K8s 实际状态为 IDLE 后，打印"从头重新部署"但实际没有执行。

根因：`executeDeploy` 开头做状态转换，resume 时状态机已是 DEPLOYING，转换失败被忽略。

**v1.2.0 修复**：resume 检测到 IDLE 时先 `ForceState(IDLE)` 重置状态机再重新部署。

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

## 三、数据库迁移

### kp migrate run 失败不会自动回滚 helm

这是故意的设计：DB 迁移失败 ≠ 部署失败，自动回滚太激进。

失败后用户有两个选择：
1. 修复迁移 SQL，重新 `kp migrate run`
2. `kp rollback` 回滚整个服务（DB 数据已变更，需要 down migration）

### dirty migration 导致迁移被锁

golang-migrate 在迁移失败后会把版本标记为 `dirty=true`，后续所有迁移都会被阻断。

```bash
# 查看 dirty 状态
kp migrate status

# 手动清除 dirty 标记（先修复 SQL 再操作）
psql -c "UPDATE schema_migrations SET dirty=false WHERE version=<version>"
```

---

## 四、Helm 相关

### helm upgrade 报 pending-rollback

controller 和 kp deploy 并发操作可能导致 release 进入 `pending-rollback` 状态。

```bash
# 1. 停止 controller（临时）
kubectl scale deployment/kubepivot-controller -n <ns> --replicas=0

# 2. 删除 pending-rollback 的 secret
kubectl delete secret -n <ns> \
  $(kubectl get secret -n <ns> -l owner=helm,name=<release> \
    -o jsonpath='{.items[?(@.metadata.labels.status=="pending-rollback")].metadata.name}')

# 3. 重新部署
kp deploy

# 4. 恢复 controller
kubectl scale deployment/kubepivot-controller -n <ns> --replicas=1
```

### kp rollback 不支持 --service

`kp rollback` 是整组 helm rollback（拓扑逆序），这是故意的设计决策——部分回滚容易造成服务间版本不一致。

如需只回滚某个服务：

```bash
helm rollback <project>-<service> -n <namespace>
```

---

## 五、镜像相关

### VERSION 不变时跳过 build/push

kp 用**本地** `docker image inspect` 判断镜像是否存在（v1.2.0+ 修复，之前用 `docker manifest inspect` 远端查询，网络抖动时会误触发 push）：

```
本地有镜像 → 刚 build 的，需要 push
本地没镜像 → build 被跳过（远端已有），跳过 push
```

### ACR 镜像名不需要 -arch 后缀

普通 Docker Hub：`qingchun22/wallet-service-arm64:v0.1.12`

阿里云 ACR：`registry.cn-hangzhou.aliyuncs.com/ns/wallet-service:v0.1.12`（自动跳过 `-arch`）

在 `project.env` 里配置 ACR 前缀后，kp 会自动检测并跳过 arch 后缀。

---

## 六、kp release

### push 失败后 tag 已打，重试报"tag 已存在"

```bash
# tag 已经在本地打了，只需要手动 push
git push && git push --tags
```

---

## 七、controller 相关

### controller 自愈时间约 13 秒

controller 使用 etcd Watch（事件驱动）+ 8 秒周期 Reconcile（兜底）。
从资源被删除到 controller 检测并触发 helm rollback，整个恢复约 13 秒。

### VALIDATING 阶段依赖 /healthz

kp 在 VALIDATING 阶段检查服务的 `/healthz` 路由是否返回 200。如果路由不存在，超时后自动回滚。

脚手架生成的 `cmd/<n>/main.go` 已包含此路由，不要删除。

---

## 八、安全相关（v1.2.0+）

### kp doctor 安全检查

```bash
kp doctor   # 检查环境依赖 + 安全基线
```

检查项：
- 明文密码（values.yaml 里的 password/secret/token 字段）
- Pod SecurityContext（runAsNonRoot/readOnlyRootFilesystem）
- RBAC 通配符权限
- NetworkPolicy 是否存在

### Pod 用 readOnlyRootFilesystem 时写文件报错

kp init 生成的模板默认 `readOnlyRootFilesystem: false`，如果改为 `true` 需要确保服务不写本地文件（日志、临时文件等需要挂载 volume）。
