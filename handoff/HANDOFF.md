# 项目交接文档 — KubePivot

> 写给下一个 Claude
> 日期：2026-04-04
> 版本：v1.9.0

---

## 写在前面

KubePivot（乾枢）是企业级 K8s 研发脚手架，qc（GitHub: Ixecd，杨庆春）独立开发。23岁（2026-03-29生日），Go/云原生，独居，女帝负责产品和设计输入。KubePivot 是他构建 Feelings（感受民主化脑机接口产品）的基础设施。

**工作风格**：设计优先、喜欢被推 back、"只保护，不越权"、不搞技术债、`make dev` 一键验证。

---

## 一、当前状态

**测试**：`go test ./... -race` 全绿
**版本**：v1.9.0（未 tag，文档完成后打）

---

## 二、关键文件

```
cmd/kp/
├── env.go           # kp context + KPEnv，多集群管理
├── audit.go         # kp audit，三来源聚合，jsonl/csv/table
├── policy.go        # kp policy，OPA 策略引擎
├── sandbox.go       # kp sandbox start/status/unlock
├── preview.go       # kp deploy --preview, kp warmup
├── migrate_snapshot.go  # PVC 快照联动，双层回滚，fix-dirty
├── drift.go         # kp diff --drift 三级分层
├── hpa.go           # applyHPA
└── multi_deploy.go  # --force-conflicts, --parallelism, OPA check

internal/controller/
├── sandbox_gc.go    # StartSandboxGCLoop
├── heal.go          # on-missing 全策略 + OOMKilled + CrashLoop
├── drift_sync.go    # StartDriftSyncLoop
├── leader.go        # etcd Leader Election
└── workqueue.go     # 三集合 WorkQueue

internal/state/
└── state.go         # 完整状态机（含 Sandbox 状态）

examples/policies/
├── no-latest-tag.rego
├── require-resource-limits.rego
└── README.md

docs/design/
├── sandbox.md
└── preview-warmup.md
```

---

## 三、技术债

| 优先级 | 描述 | 计划 |
|--------|------|------|
| P1 | SSA `--field-manager`：helm v4 不支持 | helm v4 稳定后 |
| P2 | OPA stdin pipe（input JSON 传递）| v2.0.0 |
| P2 | drift etcd 审计 stub | v2.0.0 |
| P2 | kp secret sync curl → Vault SDK | v2.0.0 |
| P2 | SIMULATING Job / CSI / Istio / Prometheus 待验证 | 有环境时 |

---

## 四、下一步（v2.0.0）

企业级插件平台 + 全文档统一大版本更新（README/docs/gotchas 完整过一遍）

---

## 五、常用命令

```bash
cd ~/KubePivot && make dev

cd ~/web3-blitz
kp deploy
kp deploy --env staging --dry-run
kp status --all-envs
kp diff --to-env staging
kp audit --format table
kp policy check
kp sandbox start --dry-run
kp diff --drift
kp doctor
LOG_FORMAT=json kp deploy 2>log
```
