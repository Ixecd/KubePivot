# 项目交接文档 — KubePivot

> 写给下一个 Claude
> 日期：2026-04-03
> 版本：v1.6.0

---

## 写在前面

KubePivot（乾枢）是企业级 K8s 研发脚手架，qc（GitHub: Ixecd，杨庆春）独立开发。23岁，Go/云原生方向，住独居，女帝负责产品和设计输入。

**qc 的工作风格**：
- 设计优先，先对齐再动手
- 喜欢被推 back，不喜欢被纯认同
- `slog` 不用 `log`，`P.Info/Done/Fail` 做进度输出
- "只保护，不越权" 是乾枢核心原则
- 不搞技术债，宁可留 TODO + 完整设计也不临时方案
- `make dev` = `go build ./... && go test ./... -race && make install`
- 喜欢 KWOK 压测，已验证 500 节点 DAG P99=40ms

---

## 一、当前状态

**测试**：`go test ./... -race` 全绿
**版本**：v1.6.0（未打 tag，待文档更新后打）

**已验证**（web3-blitz，k3s + local-path + OrbStack）：
- 蓝绿发布完整 e2e ✅
- Controller HA（Leader Election 单测 5 cases）✅
- WorkQueue 三集合去重（单测 4 cases）✅
- kp secret rotate e2e ✅
- kp doctor --perf（orbstack P50=28ms P99=30ms）✅
- JSON 结构化日志（LOG_FORMAT=json）✅
- kp history --export json/csv ✅
- KWOK 500 节点压测（DAG P99=40ms）✅
- 跨 namespace 依赖嗅探 ✅

---

## 二、关键文件

```
cmd/kp/
├── secret.go            # kp secret rotate/cleanup/audit
├── secret_test.go       # 10 tests
├── network.go           # kp network gen（跨 ns NetworkPolicy 模板）
├── doctor.go            # 主检查入口（--perf flag）
├── doctor_perf.go       # Apiserver P99 延迟检测
├── doctor_secret.go     # TLS 证书过期检测
├── doctor_crossns.go    # 跨 namespace 依赖嗅探
├── changed.go           # --changed-only 增量部署
├── changed_test.go      # 7 tests
├── deploy_timing.go     # 部署耗时表格输出
├── multi_deploy.go      # deployLayers（semaphore + timing）
├── progress.go          # P.Info/Done/Fail + slog 双输出
└── history.go           # kp history --export json/csv

internal/controller/
├── leader.go            # etcd 分布式 Leader Election
├── leader_test.go       # 5 tests
├── workqueue.go         # 三集合 WorkQueue
├── workqueue_test.go    # 4 tests
├── reconciler.go        # 使用 WorkQueue，事件入队
├── etcd_watcher.go      # queue.Add("reconcile")
└── controller.go        # RunWithLeaderElection 入口

internal/planner/
└── planner.go           # Component/Plan 加 Namespace/CrossNsDeps
                         # LoadComponents 解析 other-ns/svc 格式

scripts/bench/
└── kwok_dag_bench.sh    # KWOK 压测脚本（节点数/服务数可配）
```

---

## 三、已知 Bug / 技术债

### P2（已知但可接受）
- `kp upgrade --service` 过滤已实现，待 e2e 验证
- 蓝绿 timing 统计为 `-`（走 deployBlueGreen 分支，未接入 timing）
- `kp pvc backup/restore/list` 完整实现，待 CSI 集群验证

### 设计决策记录
- **不引入 client-go**：Leader Election 用 etcd 实现，保持"kubectl CLI 不用 client-go"原则
- **NetworkPolicy 不自动 apply**：只生成模板，符合"只保护，不越权"
- **跨 namespace 依赖不进 DAG**：只做只读嗅探，不自愈跨 ns 资源
- **Secret 轮转不操作 DB 用户权限**：只管 K8s Secret 生命周期，DB 端由用户确认

---

## 四、下一步（v1.7.0）

**Drift 治理核心设计**：
- SSA `--field-manager=kubepivot` 声明字段所有权
- `kp diff --drift` 三级分层：Hard/Managed/External
- Controller 30s 扫描 + `helm upgrade --force` 强制对齐
- 只对 kp 拥有所有权的字段执行 force-sync，避免和 Istio/HPA 套娃

**Operation Sandbox（v1.8.0）核心状态机**：
```
IDLE → LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
                                    ↓ 失败
                              RESTORING → IDLE
COMMITTING 阶段禁止 force-unlock（DB 正在迁移）
```

---

## 五、常用命令

```bash
cd ~/KubePivot && make dev           # 一键 build + test + install

cd ~/web3-blitz
kp doctor                            # 含 etcd/TLS/跨域嗅探
kp doctor --perf                     # Apiserver 延迟
ETCD_ENDPOINTS=localhost:2379 kp doctor
kp status                            # 含 StatefulSet 详情
kp deploy                            # 蓝绿：部署到 inactive slot
kp deploy --changed-only             # 增量部署
kp deploy --parallelism 4            # 限制并发
LOG_FORMAT=json kp deploy 2>log      # JSON 结构化日志
kp promote --service wallet-service  # 切换蓝绿流量
kp history --export json             # 导出历史
kp secret rotate --secret xxx --strategy graceful
kp network gen                       # 生成跨 ns NetworkPolicy 模板

# KWOK 压测
kwokctl create cluster --name kwok-stress --disable-qps-limits
kubectl config use-context kwok-kwok-stress
./scripts/bench/kwok_dag_bench.sh 500 20
kwokctl delete cluster --name kwok-stress
```
