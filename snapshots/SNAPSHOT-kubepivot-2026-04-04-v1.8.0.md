# SNAPSHOT — KubePivot v1.8.0

> 日期：2026-04-04
> 作者：qc（Ixecd）
> 版本：v1.8.0

---

## 一、版本范围

本快照覆盖 v1.8.0 完整开发历程：Operation Sandbox、DB 迁移保护、Header-based Preview、Warmup。

---

## 二、新增文件

```
cmd/kp/
├── sandbox.go           # kp sandbox start/status/unlock
├── preview.go           # kp deploy --preview, kp warmup
├── migrate_snapshot.go  # PVC 快照联动，双层回滚，fix-dirty

internal/controller/
└── sandbox_gc.go        # 5m GC Loop，清理超期 Session

internal/state/
└── state.go             # 新增 5 个 Sandbox 状态

internal/scaffold/
└── helm.go              # 预留 virtualservice-preview.yaml 注释模板

docs/design/
├── sandbox.md           # Operation Sandbox 设计文档
└── preview-warmup.md    # Header Preview + Warmup 设计文档
```

---

## 三、核心设计

### Sandbox 状态机
```
IDLE → LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
COMMITTING 只允许 RUNNING/RESTORING，禁止直接到 IDLE（force-unlock 永远禁止）
```

### SIMULATING Job 降级策略
```
有 K8s + postgres → 创建 golang-migrate Job，等待完成
Job 创建失败      → 降级 kp migrate run --dry-run
无 DATABASE_URL   → 跳过
```

### 双层回滚
```
迁移失败 → Layer 1: kp pvc restore（有快照）
         → Layer 2: kp rollback（helm rollback）
```

### Preview 流量层检测
```
Istio → generateIstioVirtualService（kubectl patch virtualservice weight）
Nginx → generateNginxIngressCanary（canary-weight annotation）
none  → generatePreviewReadme（使用指引）
```

### sampleErrorRate
```promql
sum(rate(http_requests_total{service="<svc>",status=~"5.."}[2m]))
/ sum(rate(http_requests_total{service="<svc>"}[2m]))
```
Prometheus 不可达返回 -1，warmup 跳过检查继续。

---

## 四、单测覆盖（v1.8.0 新增）

| 测试 | 数量 |
|------|------|
| Sandbox 状态机转换（happy path/fail path/COMMITTING 禁止/force-unlock/RUNNING→LOCKED）| 5 |

---

## 五、技术债

| 项 | 原因 |
|----|------|
| SIMULATING Job 验证 | 需 K8s + postgres + golang-migrate 镜像 |
| PVC 快照联动验证 | 需 CSI VolumeSnapshot |
| patchIstioWeight/patchNginxWeight | 需 Istio/Nginx 集群 |
| sampleErrorRate | 需 Prometheus + http_requests_total |
| Controller GC 端到端 | 需长时间运行集群 |

---

## 六、常用命令

```bash
# Sandbox
kp sandbox start --dry-run
kp sandbox start
kp sandbox status
kp sandbox unlock --force --reason "xxx"

# Preview
kp deploy --preview
kp warmup --service wallet-service --steps 10,50,100 --interval 2m,5m --dry-run

# 迁移保护
kp migrate run      # 自动 PVC 快照（有 CSI）
kp migrate fix-dirty
kp upgrade          # 快照保护 + 双层回滚
```
