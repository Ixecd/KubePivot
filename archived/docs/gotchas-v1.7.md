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

---

## 二、状态机

### 当前状态为 DEPLOYING，不能发起新部署

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

### Chart 模板必须用 Release.Name

蓝绿部署时创建 `-blue` / `-green` 两个 release，模板里 `metadata.name` 必须用 `{{ .Release.Name }}` 而不是硬编码服务名，否则 ownership 冲突。

### Service 冲突：蓝绿 slot 不应创建独立 Service

```yaml
{{- if not .Values.bluegreen.skipService }}
apiVersion: v1
kind: Service
...
{{- end }}
```

kp 在蓝绿部署时自动传 `--set bluegreen.skipService=true`。

### helm release 被锁（pending-install/pending-upgrade）

```bash
kubectl delete secret -n <ns> \
  $(kubectl get secret -n <ns> -l owner=helm,name=<release> \
    -o jsonpath='{.items[?(@.metadata.labels.status=="pending-install")].metadata.name}')
```

---

## 四、数据库迁移

### kp migrate run 失败不会自动回滚 helm

这是故意的设计，失败后选择：

1. 修复 SQL → 重新 `kp migrate run`
2. `kp rollback` 回滚整个服务

### dirty migration 导致迁移被锁

```bash
kp migrate status
psql -c "UPDATE schema_migrations SET dirty=false WHERE version=<version>"
```

### kp deploy 迁移兼容性检查阻断了部署

```bash
kp deploy --force-migrate   # 不推荐，除非确认风险可控
```

---

## 五、Helm 相关

### helm upgrade 报 pending-rollback

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

---

## 六、镜像相关

### ACR 镜像名不需要 -arch 后缀

```ini
REGISTRY_PREFIX=registry.cn-hangzhou.aliyuncs.com/yournamespace
```

---

## 七、Secret 轮转相关（v1.5.2+）

### graceful 轮转后必须手动在 DB 端禁用旧密码

`kp secret rotate --strategy graceful` 完成后，旧密码保留为 `*_OLD` 字段直到你手动清理：

1. DB 端禁用旧密码
2. 确认所有 pod 已使用新密码
3. `kp secret cleanup --secret <n>`

### Volume 挂载的 Secret 更新后 kp 强制 rollout restart

kp 检测到 Volume 挂载时会强制触发 `rollout restart` 并打出警告，应用需确认支持热加载或无状态重启。

---

## 八、Drift 治理相关（v1.7.0+）

### kp diff --drift 用 helm diff --three-way-merge 对比 live 状态

普通 `helm diff` 只对比 chart vs helm release 记录，不看 live K8s 状态。`--three-way-merge` 才能检测到 `kubectl scale` 等外部工具造成的漂移。

### kubectl scale 后 kp deploy 可能报 SSA conflict

```
conflict with "kubectl" with subresource "scale" using apps/v1: .spec.replicas
```

这是因为 `kubectl scale` 抢占了 `.spec.replicas` 的 field ownership。kp 加了 `--force-conflicts` 来强制接管，正常 `kp deploy` 就能解决：

```bash
kp deploy   # --force-conflicts 自动处理冲突
```

### --field-manager=kubepivot 暂不支持（技术债）

helm v4 不支持 `--field-manager` flag，当前用 `--force-conflicts` 替代。等 helm v4 文档稳定后研究正确姿势。

### no-sync-fields 豁免字段不会触发 force-sync

```yaml
resources:
  - name: wallet-service
    force-sync: true
    no-sync-fields:
      - replicas   # HPA 管理的字段，kp 不强制同步
```

`kp diff --drift` 对豁免字段显示 `ℹ️ 已豁免`，Controller 30s 扫描也会跳过。

---

## 九、controller 相关

### controller 自愈时间约 13 秒

etcd Watch + 8s 周期 Reconcile + WorkQueue 去重。

### OOMKilled 自动调整 memory limit

controller 检测到 OOMKilled 后自动 `kubectl patch` 将 memory limit 上调 25%（256Mi → 320Mi）。如果你手动设置了 limit，下次 `kp deploy` 会以 chart values 为准重置。

### CrashLoopBackOff 运行时崩溃 restarts≥5 自动 rollback

controller 分析 `--previous` 日志，识别为运行时错误（panic/nil pointer 等）且 restarts≥5 时自动触发 `healRollback`。如果不希望自动 rollback，改 `on-missing: alert`。

---

## 十、安全相关（v1.2.0+）

### kp doctor 安全检查

```bash
kp doctor
kp doctor --perf
kp doctor --perf --context prod
ETCD_ENDPOINTS=<host>:2379 kp doctor
```

### kp doctor --perf 建议降低并发度

```bash
kp deploy --parallelism 4   # P99 > 500ms 时推荐
```

---

## 十一、跨 namespace 依赖（v1.6.0+）

### kp network gen 不自动 apply

```bash
kp network gen
cat deployments/myapp/network/networkpolicy-wallet-service-cross-ns.yaml
kubectl apply -f deployments/myapp/network/   # 审查后手动执行
```

NetworkPolicy 写错会立即生效拒绝所有入站，必须人工确认。
