# FORGET.md — 待修复项（P0 + P1）

> 扫描日期：2026-05-08 / last update 2026-05-09
> 范围：`docs/design/` 下 34 个设计文档 + v3.2 收尾扫描
> 原则：只列 P0（生产命门）和 P1（功能受限），P2 Ops / P3 Polish / 长期演进不提

---

## P0 — 生产命门（上生产前必修）— 8/8 ✅ 全清

### 可靠性

1. ~~**Shard 切换 gap**~~ ✅ v3.3-v3.4 — 事件盲区 ForceResync + handoff ReleaseAll + rebalance yield。gap 15-20s → <5s。

2. ~~**Quota 分配不均**~~ ✅ v3.3-v3.4 — hash 分布 jump hash + rebalance 主动 yield。4:4:2 → 趋向 3:3:4。

3. ~~**Webhook TLS 证书缺失**~~ ✅ v3.3 — installer 自动生成自签证书写入 Secret，deployment volume mount 挂载 `/etc/kubepivot`。

4. ~~**WorkerPool 无背压机制**~~ ✅ v3.3 — token bucket (10 tokens/sec) 入队限流，config 可配。

5. ~~**自愈死循环保护**~~ ✅ v3.3 — rollbackTracker 指数退避 (RWMutex, 20 worker 并发读零争用)。

### 可观测性

6. ~~**Prometheus 指标未注册**~~ ✅ v3.3 — HTTP /metrics server :9090，scheduler + informer 16 指标注册到 DefaultRegisterer。

7. ~~**etcdmanager compact/defrag 未接入 config**~~ ✅ v3.3 — EtcdManager 类型 int→time.Duration，controller.Start() 自动启动维护循环。

---

## P1 — 功能受限（规模化/企业级前必做）— 3/12 清

### Controller

8. **OOM 事件自动接线** — `ReportOOM()` 接口已存在，但未从 controller 的 Pod 状态监听器调用。

9. **Jitter 阈值生产校准** — `jitterThreshold=0.95` / `jitterSpikeCount=3` 经验值。需生产数据反馈。

### Scheduler

10. **Fencer 接口 OOB 隔离确认** — `SetFencer(fn)` + `DefaultLeaseFencer()` 已就位，但 `ConfirmIsolated` 闭环未完成。

11. ~~**Watch 接线 Phase 4**~~ ✅ v3.4 — `kvcache.enabled: true` 默认启用，InformerAdapter nil-safe。cache=nil → 纯 kubectl fallback。

12. **Pod affinity/anti-affinity 拓扑约束** — 外层 `ConstraintChecker` 未实现。影响 GPU NVLink 亲和。

13. ~~**Deployment Pod 迁移 label-based 匹配**~~ ✅ v3.3 — webhook 双路查找 (name + label)，rescheduler 双 key 存储。

### 部署

14. **跨 namespace 依赖部署** — planner 已支持跨 ns 声明，`kp deploy` 执行层不做跨 ns reconcile。

15. **不支持 per-service rollback** — `kp rollback` 项目级，不能单 service 回滚。

16. **Controller chart 默认 disabled** — 需手动 build 镜像 + enable chart。

17. **Etcd emptyDir 非 PVC** — pod 重启丢 etcd 数据。

### 数据模型

18. **GPU fields in resources.yaml** — Resource 结构体未包含 `gpu`/`gpuCount`/`migProfile`。

19. ~~**FNV hash 分布不均**~~ ✅ v3.3 — FNV-32a % N → Google jump consistent hash (2014)。连续短名 ±40% → 均匀。

---

## 编辑记录

```
2026-05-08  v3.2 收尾定稿
            - 全量 94 项扫描 → 压缩为 19 项 P0+P1
            - P0 7 项：shard 可靠性 / Webhook TLS / 背压 / 死循环 / Prometheus / etcd config
            - P1 12 项：OOM 接线 / jitter 校准 / Fencer / Watch Phase 4 / 拓扑约束 / label-based 匹配
                        跨 ns / per-svc rollback / chart disabled / etcd PVC / GPU fields / FNV hash
            - P2 Ops + P3 Polish + 长期演进 → 不提（规模化时再扫）
2026-05-09  并发设计文档创建 (11 构造 + 7 已知漏洞)
2026-05-09  v3.4 handoff + rebalance 落地, P0 8/8 全清
2026-05-09  v3.3 P0 扫荡 (6/8 + 2 半项)
            - #5  自愈死循环 ✅ rollbackTracker 指数退避
            - #7  etcd config ✅ compact/defrag 接入 config 系统
            - #19 FNV hash ✅ jump consistent hash
            - #4  WorkerPool 背压 ✅ token bucket 10/s
            - #3  Webhook TLS ✅ 自签 cert + volume mount
            - #6  Prometheus ✅ /metrics HTTP server + 16 指标
            - #1  shard gap 🟡 事件盲区归零, reconcile gap 待 lease handoff
            - #2  Quota 不均 🟡 hash 均匀化, lease rebalance 待
            P1 #13 label migration hint ✅ name+label 双路查找
```
