# KubePivot 当前快照

> 版本：v1.7.0
> 日期：2026-04-03
> 状态：✅ 全绿，已发布

---

## 快速状态

```
go test ./... -race  → 全绿
make dev             → build + test + install 一键完成
当前 tag             → v1.7.0
companion            → github.com/Ixecd/web3-blitz
```

---

## v1.7.0 新增能力

### kp diff --drift
```bash
kp diff --drift
kp diff --drift --service wallet-service

# 输出三级：
# ❌ 硬冲突（kp 拥有所有权，强制同步）
# ⚠️  受控偏离（透明展示，不强制同步）
# ℹ️  已豁免（no-sync-fields，完全跳过）
```

### resources.yaml 新字段
```yaml
- kind: Deployment
  name: wallet-service
  on-missing: recreate | rollback | scale-down | alert | custom
  force-sync: true
  no-sync-fields:
    - replicas
```

### 自愈策略全覆盖
```
OOMKilled        → memory limit +25%（kubectl patch）
CrashLoopBackOff → 启动错误告警；运行时 restarts≥5 自动 rollback
```

### HPA 集成
```yaml
- name: wallet-service
  min_replicas: 1
  max_replicas: 5
  target_cpu: 70
```

### kp doctor drift 告警
```bash
kp doctor   # 有 helm-diff 时自动运行 drift 检查
```

---

## 技术债
- `--field-manager`：helm v4 不支持，用 `--force-conflicts` 替代
- 蓝绿 timing：deployBlueGreen 分支未接入 timing 表格

---

## 下一步

v1.8.0 — Operation Sandbox
