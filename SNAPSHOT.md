# KubePivot 当前快照

> 版本：v1.8.0
> 日期：2026-04-04
> 状态：✅ 全绿，待打 tag

---

## 快速状态

```
go test ./... -race  → 全绿
make dev             → build + test + install 一键完成
当前版本             → v1.8.0（未 tag）
companion            → github.com/Ixecd/web3-blitz
```

---

## v1.8.0 新增能力

### kp sandbox
```bash
kp sandbox start --dry-run        # 查看执行计划
kp sandbox start                  # 启动沙盒（LOCKED→SNAPSHOTTING→SIMULATING→COMMITTING→RUNNING）
kp sandbox status                 # 查看当前状态
kp sandbox unlock --force --reason "..."  # 强制解锁（COMMITTING 禁止）
```

### kp deploy --preview
```bash
kp deploy --preview               # 部署后生成 Header 路由模板（Istio/Nginx/降级）
# 输出到 deployments/<project>/preview/
# ⚠️ 不自动 apply，审查后手动执行
```

### kp warmup
```bash
kp warmup --service wallet-service --steps 10,50,100 --interval 2m,5m --dry-run
kp warmup --service wallet-service --steps 10,50,100 --interval 2m,5m --err-threshold 0.01
```

### kp migrate fix-dirty
```bash
kp migrate fix-dirty              # 交互式 dirty 迁移状态修复指引
```

### 迁移保护
```
kp migrate run  → 自动触发 PVC 快照（有 CSI）→ 迁移失败双层回滚
kp upgrade      → 快照保护 + 失败双层回滚
```

---

## 新状态机

```
IDLE/RUNNING → LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
                                                  ↓          ↓
                                              RESTORING   RESTORING
                                                  ↓
                                                IDLE
COMMITTING 阶段禁止 force-unlock
```

---

## 设计文档

- `docs/design/sandbox.md` — Operation Sandbox 完整设计
- `docs/design/preview-warmup.md` — Header Preview + Warmup 设计

---

## 技术债
- SIMULATING Job / PVC 快照 / Istio weight patch / Prometheus：待对应环境验证
- `--field-manager`：helm v4 不支持，用 `--force-conflicts` 替代

---

## 下一步

v1.9.0 — 多集群联邦 + 企业合规
