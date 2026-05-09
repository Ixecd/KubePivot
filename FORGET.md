# FORGET.md — 待修复项（P0 + P1）

> 扫描日期：2026-05-08
> 范围：`docs/design/` 下 34 个设计文档 + v3.2 收尾扫描
> 原则：只列 P0（生产命门）和 P1（功能受限），P2 Ops / P3 Polish / 长期演进不提

---

## P0 — 生产命门（上生产前必修）

### 可靠性

1. **Shard 切换 10s gap** — pod 重启/扩缩时 ~10s 无 Pod reconcile 某 shard。mid-deployment 任务会掉，下次 reconcile 捡起但不能接受生产 SLA。
   - 修复方向：lease handoff 预通知 + jump consistent hash 替代 FNV。

2. **Quota 分配不均** — `ceil(N/replicas)` 下 3 副本 10 shard → 4:4:2。fault domain 不均。
   - 🟡 hash 分布已修复（FNV→jump hash, v3.3），lease rebalance 待 v3.4。

3. ~~**Webhook TLS 证书缺失**~~ ✅ v3.3 — installer 自动生成自签证书写入 Secret，deployment volume mount 挂载 `/etc/kubepivot`。

4. ~~**WorkerPool 无背压机制**~~ ✅ v3.3 — token bucket (10 tokens/sec) 入队限流，config 可配 (workerPoolRateLimit=0 关闭)。

5. **自愈死循环保护** — 同一资源连续 rollback > N 次应暂停。无此保护时，helm rollback 失败 → 重建 → 再失败 → 再回滚的死循环可能无限进行。

### 可观测性

6. ~~**Prometheus 指标未注册**~~ ✅ v3.3 — HTTP /metrics server 启动，scheduler + informer 指标注册到 DefaultRegisterer。metricsPort=9090 可配，0 关闭。

7. ~~**etcdmanager compact/defrag 未接入 config**~~ ✅ v3.3 — EtcdManager 类型 int→time.Duration，NewEtcdManagerFromConfig() 接线，controller.Start() 自动启动维护循环。

---

## P1 — 功能受限（规模化/企业级前必做）

### Controller

8. **OOM 事件自动接线** — `ReportOOM()` 接口已存在，但未从 controller 的 Pod 状态监听器调用。Rescheduler 降级层级无法感知 OOM 事件。

9. **Jitter 阈值生产校准** — `jitterThreshold=0.95` / `jitterSpikeCount=3` 是经验值。需生产数据反馈校准，避免误触发/漏触发。

### Scheduler

10. **Fencer 接口 OOB 隔离确认** — `SetFencer(fn)` + `DefaultLeaseFencer()`（K8s Lease 隔离）已就位，但 `ConfirmIsolated` 显式确认闭环未完成。Stateful Cell 迁移缺网络断连/GPU reset 等 OOB 检测。

11. **Watch 接线 Phase 4** — Phase 2+3 已完成（PodCacheBridge/NodeCacheBridge + InformerAdapter），Phase 4（RV 自动传递 + 默认启用 KVCache）留 v3.3。

12. **Pod affinity/anti-affinity 拓扑约束** — 设计提到外层 `ConstraintChecker`，未实现。影响 GPU NVLink 亲和调度准确性。

13. ~~**Deployment Pod 迁移 target label-based 匹配**~~ ✅ v3.3 — webhook 双路查找 (name + label)，rescheduler 双 key 存储。Deployment Pod 改名后通过 app.kubernetes.io/name 回退匹配。

### 部署

14. **跨 namespace 依赖部署** — `planner` 已支持 `/` 分隔的跨 ns 声明（`depends_on: [other-ns/svc]`），但 `kp deploy` 执行层不做跨 ns 资源 reconcile。

15. **不支持 per-service rollback** — `kp rollback` 是项目级（helm release 前缀），不能单 service 回滚。

16. **Controller chart 默认 disabled** — 需用户手动 build 镜像 + 填 image 字段 + enable chart。无开箱即用的 controller 部署。

17. **Etcd emptyDir 非 PVC** — 生产持久化需手动改 PVC。pod 重启丢 etcd 数据。

### 数据模型

18. **GPU fields in resources.yaml** — 已设计 `gpu` / `gpuCount` / `migProfile` 字段，`resources.go` Resource 结构体未包含。DCGM 数据流就绪时需一并补齐。

19. ~~**FNV hash 分布不均**~~ ✅ v3.3 — FNV-32a % N → Google jump consistent hash (2014)。连续短名场景（kp-bench-001..050）从 <5 shard → 9/10 shard 命中。

---

## 编辑记录

```
2026-05-08  v3.2 收尾定稿
            - 全量 94 项扫描 → 压缩为 19 项 P0+P1
            - P0 7 项：shard 可靠性 / Webhook TLS / 背压 / 死循环 / Prometheus / etcd config
            - P1 12 项：OOM 接线 / jitter 校准 / Fencer / Watch Phase 4 / 拓扑约束 / label-based 匹配
                        跨 ns / per-svc rollback / chart disabled / etcd PVC / GPU fields / FNV hash
            - P2 Ops + P3 Polish + 长期演进 → 不提（规模化时再扫）
2026-05-09  v3.3 P0 扫荡
            - #5 自愈死循环 ✅ rollbackTracker 指数退避
            - #7 etcd config ✅ compact/defrag 接入 config 系统
            - #1 shard gap 事件盲区 ✅ ForceResync + OnShardChanged 接线
```
