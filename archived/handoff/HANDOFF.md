# 项目交接文档 — KubePivot

> 写给下一个 Claude
> 日期：2026-04-03
> 版本：v1.7.0

---

## 写在前面

KubePivot（乾枢）是企业级 K8s 研发脚手架，qc（GitHub: Ixecd，杨庆春）独立开发。23岁（2026-03-29生日），Go/云原生，独居，女帝负责产品和设计输入。KubePivot 是他构建 Feelings（感受民主化脑机接口产品）的基础设施。

**qc 的工作风格**：
- 设计优先，先对齐再动手
- 喜欢被推 back，不喜欢被纯认同
- `slog` 不用 `log`，`P.Info/Done/Fail` 做进度输出
- "只保护，不越权" 是乾枢核心原则
- 不搞技术债，宁可留 TODO + 完整设计也不临时方案
- `make dev` = `go build ./... && go test ./... -race && make install`
- 喜欢简洁有力的 CLI 输出，复杂度被驯服不被暴露

---

## 一、当前状态

**测试**：`go test ./... -race` 全绿
**版本**：v1.7.0（已 tag，已推送）

**已验证**（web3-blitz，k3s + local-path + OrbStack）：
- kp diff --drift 三级分层 ✅
- 蓝绿 slot 独立 drift 检测 ✅
- no-sync-fields 豁免（ℹ️ 已豁免）✅
- --force-conflicts 解决 SSA 冲突 ✅
- kp doctor 集成 drift 告警 ✅
- OOMKilled / CrashLoopBackOff 自动自愈 ✅
- on-missing 全策略（5 种）✅

---

## 二、关键文件

```
cmd/kp/
├── drift.go             # kp diff --drift，三级分层，蓝绿 slot 感知
├── hpa.go               # applyHPA，autoscaling/v2
├── doctor.go            # runDoctor，集成 drift 告警
├── multi_deploy.go      # buildHelmArgs 加 --force-conflicts
├── secret.go            # kp secret rotate/cleanup/audit
├── network.go           # kp network gen
├── changed.go           # --changed-only 增量部署
├── deploy_timing.go     # 部署耗时表格
├── progress.go          # P.Info/Done/Fail + slog 双输出
└── history.go           # kp history --export json/csv

internal/controller/
├── heal.go              # on-missing 全策略 + OOMKilled + CrashLoop
├── drift_sync.go        # StartDriftSyncLoop，30s 扫描，etcd 审计
├── leader.go            # etcd 分布式 Leader Election
├── workqueue.go         # 三集合 WorkQueue
├── reconciler.go        # Start 方法，启动 DriftSyncLoop
└── resources.go         # Resource 结构体（含 ForceSync/NoSyncFields）

internal/planner/
└── planner.go           # Component/Plan 含 Namespace/CrossNsDeps/HPA 字段

scripts/bench/
└── kwok_dag_bench.sh    # KWOK 压测脚本
```

---

## 三、技术债

| 优先级 | 描述 | 计划 |
|--------|------|------|
| P1 | SSA `--field-manager`：helm v4 不支持，用 `--force-conflicts` 替代 | v1.8.0 或按需 |
| P2 | 蓝绿 timing 统计为 `-` | v1.8.0 顺手 |
| P2 | `kp upgrade --service` 待 e2e 验证 | v1.8.0 |
| P3 | drift etcd 审计待端到端验证 | v1.8.0 |
| P3 | `kp pvc` 待 CSI 集群验证 | 有 CSI 环境时 |

---

## 四、下一步（v1.8.0 Operation Sandbox）

核心状态机：
```
IDLE → LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
                                    ↓ 失败
                              RESTORING → IDLE
COMMITTING 阶段禁止 force-unlock（DB 正在迁移）
```

SandboxSession CRD + ownerReferences GC + Controller 超期自动清理。

---

## 五、常用命令

```bash
cd ~/KubePivot && make dev

cd ~/web3-blitz
kp deploy
kp deploy --changed-only
kp deploy --parallelism 4
LOG_FORMAT=json kp deploy 2>log
kp diff --drift
kp diff --drift --service wallet-service
kp doctor
kp doctor --perf
kp history --export json
kp secret rotate --secret xxx --strategy graceful
kp network gen
kp promote --service wallet-service

# KWOK 压测
kwokctl create cluster --name kwok-stress --disable-qps-limits
kubectl config use-context kwok-kwok-stress
./scripts/bench/kwok_dag_bench.sh 500 20
kwokctl delete cluster --name kwok-stress
```
