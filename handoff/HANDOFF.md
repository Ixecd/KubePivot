# 项目交接文档 — KubePivot

> 写给下一个 Claude
> 日期：2026-04-04
> 版本：v1.8.0

---

## 写在前面

KubePivot（乾枢）是企业级 K8s 研发脚手架，qc（GitHub: Ixecd，杨庆春）独立开发。23岁（2026-03-29生日），Go/云原生，独居，女帝负责产品和设计输入。KubePivot 是他构建 Feelings（感受民主化脑机接口产品）的基础设施。

**工作风格**：设计优先、喜欢被推 back、"只保护，不越权"、不搞技术债、`make dev` 一键验证。

---

## 一、当前状态

**测试**：`go test ./... -race` 全绿
**版本**：v1.8.0（未 tag，文档完成后打）

---

## 二、关键文件

```
cmd/kp/
├── sandbox.go           # kp sandbox start/status/unlock
├── preview.go           # runPreviewGen, runWarmup, patchTrafficWeight, sampleErrorRate
├── migrate_snapshot.go  # PVC 快照联动，双层回滚，fix-dirty
├── drift.go             # kp diff --drift 三级分层
├── hpa.go               # applyHPA
├── doctor.go            # 集成 drift 告警
└── multi_deploy.go      # --force-conflicts, --parallelism

internal/controller/
├── sandbox_gc.go        # StartSandboxGCLoop（5m 扫描超期 Session）
├── heal.go              # on-missing 全策略 + OOMKilled + CrashLoop
├── drift_sync.go        # StartDriftSyncLoop（30s 扫描）
├── leader.go            # etcd Leader Election
└── workqueue.go         # 三集合 WorkQueue

internal/state/
└── state.go             # 完整状态机（含 v1.8.0 Sandbox 状态）

docs/design/
├── sandbox.md           # Operation Sandbox 设计
└── preview-warmup.md    # Header Preview + Warmup 设计
```

---

## 三、技术债

| 优先级 | 描述 | 计划 |
|--------|------|------|
| P1 | SSA `--field-manager`：helm v4 不支持，用 `--force-conflicts` | helm v4 稳定后 |
| P2 | SIMULATING Job / PVC 快照 / Istio weight / Prometheus | 有对应环境时验证 |
| P2 | Controller GC 端到端验证 | v1.8.1 |
| P2 | 蓝绿 timing 统计为 `-` | v1.9.0 顺手 |
| P3 | `kp upgrade --service` 待 e2e | v1.9.0 |

---

## 四、下一步（v1.9.0）

多集群：`kp context add`，`kp deploy --env prod`，`kp status --all-envs`
企业合规：`kp audit`，OPA 策略，`kp secret sync --from vault`

---

## 五、常用命令

```bash
cd ~/KubePivot && make dev

cd ~/web3-blitz
kp deploy
kp deploy --preview
kp sandbox start --dry-run
kp diff --drift
kp doctor
kp doctor --perf
kp warmup --service wallet-service --steps 10,50,100 --interval 2m,5m --dry-run
kp migrate fix-dirty
LOG_FORMAT=json kp deploy 2>log
```
