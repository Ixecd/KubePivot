# SNAPSHOT — KubePivot v1.6.0

> 日期：2026-04-03
> 作者：qc（Ixecd）
> 版本：v1.5.2 + v1.6.0

---

## 一、版本范围

本快照覆盖从 v1.5.2 到 v1.6.0 的完整开发历程。

---

## 二、v1.5.2 — Secret 轮转

### 新增文件
- `cmd/kp/secret.go`：kp secret rotate/cleanup/audit
- `cmd/kp/secret_test.go`：10 个单测（scanSecretRefs、extractServiceFromPath、parseCertExpiry）
- `cmd/kp/doctor_secret.go`：TLS 证书过期检测（30d warn，7d error）
- `scripts/gencerts.sh`：自签 CA + 证书生成

### 核心设计
```
kp secret rotate --strategy=graceful：
  1. 更新 Secret（新密码写入，旧密码保留为 *_OLD 字段）
  2. 滚动重启所有引用服务（Deployment 并行，StatefulSet 按 ordinal）
  3. /healthz 验证通过
  4. 提示用户在 DB 端禁用旧密码
  5. kp secret cleanup 删除 *_OLD 字段

关键约束：kp 不操作 DB 用户权限，DB 端由用户确认
```

---

## 三、v1.6.0 — Controller HA + 大规模场景 + 可观测性

### 3.1 Controller HA

**Leader Election**（`internal/controller/leader.go`）：
- etcd 分布式锁，无 client-go，TTL=15s，续约 5s，重试 3s
- `RunWithLeaderElection(ctx, etcdEndpoints, project, namespace, fn)`
- 抢锁：原子 txn CAS，key=`/kubepivot/<project>/<ns>/leader`
- Follower：Watch key 释放后重新竞选
- 无 etcd 时降级单机模式，fn 直接运行
- 单测：leaderKey 格式、identity 格式、no-etcd fallback（5 cases）

**WorkQueue 三集合**（`internal/controller/workqueue.go`）：
- `queue`/`dirty`/`processing` 三集合语义（client-go style）
- `Add(key)`：在 dirty → 去重跳过；在 processing → 只加 dirty
- `done(key)`：从 processing 移除；dirty 有事件 → 重新入队
- etcd watcher：`queue.Add("reconcile")` 替换直接调 `r.reconcile()`
- 8s tick：`queue.Add("tick")`
- 单测：dedup、processing-dedup、done-requeue、run-processes-all（4 cases）

### 3.2 kp doctor 增强

- `--perf` flag：10 次 `kubectl get nodes` 采样，P50/P99 延迟，推荐并发度
  - P99 > 500ms → warn + 建议降 parallelism
  - P99 > 2000ms → error，阻断大规模部署
- `--context/--kubeconfig` 透传到 `checkApiserverLatency`
- helm-diff 插件检测（未安装则 warn + 安装命令）
- TLS Secret 过期时间检测
- 跨 namespace 依赖嗅探（只读，缺失标红）

### 3.3 大规模部署性能

**`--parallelism` flag**（`cmd/kp/multi_deploy.go`）：
- semaphore via buffered channel：`semCh := make(chan struct{}, sem)`
- `parallelism=0` 不限制（默认，向后兼容）

**`--changed-only` 增量部署**（`cmd/kp/changed.go`）：
- `detectChangedServices(root, plans)` → git diff HEAD~1 HEAD
- `classifyChangedFiles(files, plans)` → 纯函数，可测
- 规则：configs/go.mod/scripts/build → 全量；internal/ → 所有 image 服务；cmd/<svc>/ → 该服务；deployments/<proj>/<svc>/ → 该服务
- 单测：7 cases 覆盖所有路径模式

**部署耗时统计**（`cmd/kp/deploy_timing.go`）：
- `deployTiming{build, push, helm, rollout}`
- `deployService` 返回 `(deployTiming, error)`，各阶段独立计时
- `deployLayers` 结束后输出 `服务/build/push/helm/rollout/总计` 表格

### 3.4 跨 namespace 依赖

**planner 改动**（`internal/planner/planner.go`）：
- `Component/Plan` 加 `Namespace string`、`CrossNsDeps []string`
- `LoadComponents` 解析 `other-ns/svc` 格式：
  - 含 `/` → `CrossNsDeps`（不进 DAG，只嗅探）
  - 不含 `/` → `DependsOn`（进 DAG，参与拓扑排序）

**kp doctor 跨域嗅探**（`cmd/kp/doctor_crossns.go`）：
- 只读 `kubectl get deployment/statefulset` 检测目标 namespace
- 缺失 → warn，不触发自愈，不申请跨 ns 写权限

**kp network gen**（`cmd/kp/network.go`）：
- 按服务分组生成 NetworkPolicy YAML
- 输出到 `deployments/<project>/network/`，不自动 apply
- 打印 `kubectl apply` 提示，用户审查后执行

### 3.5 可观测性

**结构化 JSON 日志**（`cmd/kp/progress.go`）：
- `P.Done/Fail/Info` 同时调用 `slog`
- `LOG_FORMAT=json` → stderr 输出 JSON（ELK/Loki ready）
- `deploy.done` 包含 `elapsed_ms`
- `LOG_LEVEL=debug` → `P.Start` 也记录

**kp history --export**（`cmd/kp/history.go`）：
- `--export json` → `kp-history-<project>-<ts>.json`
- `--export csv` → `kp-history-<project>-<ts>.csv`
- 导出全量历史（不受 `-n` 限制）

### 3.6 KWOK 压测

脚本：`scripts/bench/kwok_dag_bench.sh [节点数] [服务数]`

结果（500 节点，20 服务）：
```
Apiserver P50: 288ms  P99: 562ms → ⚠️ 触发降并发建议（正确）
DAG 规划  P50: 10ms   P99: 40ms  → ✅ 纯内存计算，不是瓶颈
```

结论：大规模场景瓶颈在 Apiserver，不在 kp 自身。

### 3.7 make dev

```makefile
.PHONY: dev
dev:
    @$(MAKE) go.dev

# golang.mk
.PHONY: go.dev
go.dev:
    @$(GO) build ./...
    @$(GO) test ./... -race
    @$(MAKE) install
```

---

## 四、修复项

- `scripts/make-rules/golang.mk`：`EXCLUDE_TESTS` dev-toolkit → kubepivot
- `build/docker/controller/Dockerfile`：dtk → kp，二进制和 CMD 更新
- `internal/scaffold/helm.go`：dev-toolkit-controller → kubepivot-controller，RBAC 全量权限 → 最小权限（含 leases）
- `cmd/kp/` 残留 dtk/dev-toolkit 清理（release.go/down.go/history.go/doctor.go）

---

## 五、单测覆盖

| 文件 | 测试数 | 覆盖内容 |
|------|--------|---------|
| secret_test.go | 10 | scanSecretRefs, extractServiceFromPath, parseCertExpiry |
| changed_test.go | 7 | classifyChangedFiles 所有路径模式, filterChangedPlans |
| leader_test.go | 5 | leaderKey, identity, no-etcd fallback |
| workqueue_test.go | 4 | dedup, processing-dedup, done-requeue, run-processes-all |
| pvc_test.go | 9 | buildSnapshotYAML, buildPVCFromSnapshotYAML, extractOrdinal |

---

## 六、已知技术债

| 优先级 | 描述 | 版本 |
|--------|------|------|
| P2 | 蓝绿 timing 统计为 `-`（未接入 deployTiming）| v1.7.0 顺手修 |
| P2 | `kp upgrade --service` 待 e2e 验证 | v1.7.0 |
| P3 | `kp pvc` 待 CSI 集群验证 | v1.5.1 CSI 环境 |
