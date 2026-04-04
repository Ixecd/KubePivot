# 已知坑和注意事项

> 遇到问题先查这里，大概率能找到答案。

---

## 一、部署相关

### kp deploy 前必须有 Secret

```bash
./scripts/create-secret.sh   # 幂等，可多次运行
```

`kp down` 会删除整个 namespace 包括 Secret，重新部署前必须重建。

### DATABASE_URL 在 K8s 里要用 Service 名

```bash
# ❌ 本地开发用
DATABASE_URL=postgres://blitz:blitz@localhost:5432/blitz

# ✅ K8s Secret 里
DATABASE_URL=postgres://blitz:blitz@web3-blitz-postgres:5432/blitz
```

### resources.yaml 资源名必须和 K8s 资源名完全一致

---

## 二、状态机

### 当前状态为 DEPLOYING，不能发起新部署

```bash
kp resume
```

### Sandbox 状态阻止 kp deploy

```
❌ 当前处于 Sandbox 会话（状态: LOCKED），禁止发起新部署
```

```bash
kp sandbox status
kp sandbox unlock --force --reason "xxx"  # COMMITTING 永远禁止
```

### 状态机卡住手动重置

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

不能硬编码服务名，否则蓝绿第二个 release 会 ownership 冲突。

### helm release 被锁（pending-install/pending-upgrade）

```bash
kubectl delete secret -n <ns> \
  $(kubectl get secret -n <ns> -l owner=helm,name=<release> \
    -o jsonpath='{.items[?(@.metadata.labels.status=="pending-install")].metadata.name}')
```

---

## 四、数据库迁移

### dirty migration 导致迁移被锁

```bash
kp migrate fix-dirty   # 打印修复指引
psql -c "UPDATE schema_migrations SET dirty=false WHERE version=<version>"
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

## 六、Secret 轮转（v1.5.2+）

### graceful 轮转后必须手动在 DB 端禁用旧密码

旧密码保留为 `*_OLD` 字段，不会自动失效。确认 DB 端禁用后：

```bash
kp secret cleanup --secret <n>
```

### kp secret sync --from vault 需要 VAULT_TOKEN

```bash
export VAULT_TOKEN=hvs.xxx
export VAULT_ADDR=https://vault.example.com  # 默认 http://127.0.0.1:8200

kp secret sync --from vault \
  --secret myapp-secret \
  --vault-path secret/data/myapp \
  --dry-run   # 先 dry-run 确认 key 列表
```

---

## 七、Drift 治理（v1.7.0+）

### kubectl scale 后 kp deploy 报 SSA conflict

```bash
kp deploy   # --force-conflicts 自动处理
```

### no-sync-fields 豁免字段不触发 force-sync

```yaml
resources:
  - name: wallet-service
    force-sync: true
    no-sync-fields:
      - replicas   # HPA 管理
```

---

## 八、Sandbox（v1.8.0+）

### COMMITTING 阶段永远禁止 force-unlock

DB 正在迁移，强制解锁会造成数据不一致，kp 直接拒绝。

### Controller GC 超期清理

Session TTL 默认 3600s，超期后 Controller 自动清理带 `kubepivot.io/sandbox-id` label 的资源并 ForceState → IDLE。

---

## 九、多集群（v1.9.0+）

### --from-env local 是保留字

`local` 代表当前 project.env，不需要在 `~/.kp/envs/` 里创建 `local.yaml`。

### kp status --all-envs 只显示已配置的 env

```bash
kp context add --name prod --context prod-k8s --namespace production
kp status --all-envs   # 才会显示 prod
```

### kp diff --to-env 对比的是 helm values

staging 未部署的服务会显示"无此 release"，不是报错，是正常提示。

---

## 十、OPA 策略（v1.9.0+）

### 无 opa 命令时策略检查静默跳过

```bash
brew install opa   # macOS
# 或 https://www.openpolicyagent.org/docs/latest/#1-download-opa
```

### 策略文件必须是 package kp

```rego
package kp

deny[msg] {
    input.version == "latest"
    msg := "禁止 latest tag"
}
```

### input 对象结构

```json
{
  "project":   "web3-blitz",
  "namespace": "web3-blitz",
  "version":   "v0.1.12",
  "services":  ["wallet-service"],
  "env":       {}
}
```

**已知 TODO**：input JSON 通过 stdin 传给 opa 的实现是 stub，策略里 `input.*` 字段暂时读不到值，v2.0.0 修复。

---

## 十一、安全相关

### kp doctor 一键检查

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
