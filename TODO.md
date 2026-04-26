# TODO — KubePivot v2.6.0+

> 当前 TODO（持续演化）
> 编写日期：2026-04-26
> Last release: v2.6.0 (commit 1f780c2)
> Total commits: 404

---

## 版本号约定

```
v{major}.{minor}.{patch}

major  项目名/范围根本性改变（v1 dev-toolkit → v2 KubePivot）
minor  新功能 / 新接口（v2.5 → v2.6 = 流量层引入）
patch  bug 修复 / 文档 / 内部优化（不打 tag）

实践：
- 真 tag 只打 minor：v2.0.0 / v2.5.0 / v2.6.0
- patch 版本是"持续改进"，跨 minor 之间可能 100+ 个内部 commit
- v2.5.1 / v2.6.1 不会打 tag，是 minor 之间的工作集合
```

---

## ✅ 已完成

### v2.6.0 — 流量层抽象 + 声明式蓝绿 (2026-04-26 release)

**核心代码**

```
✅ Step 1  internal/route/ 子包 (commit 786b59d)
   - Provider 接口 + Route + Match + Validate 校验
   - IngressProvider (networking.k8s.io/v1)
   - GatewayAPIProvider (gateway.networking.k8s.io/v1)
   - AutoDetect / NewProvider / ProviderForKind 三个工厂函数
   - 27 个 sub-cases 单测全 PASS
   - 内部 Error struct + NewError/WrapError，复用 internal/code 错误码

✅ Step 2  Sandbox 蓝绿流量切换接入 (commit f0244cc)
   - internal/controller/resources.go 加 Traffic / TrafficRefs / TrafficRoute / TrafficValidation
   - HasBlueGreen() 方法 + 5 个 case 单测
   - cmd/kp/sandbox.go 加 runBlueGreenSwitch / waitDeploymentReady
   - COMMITTING 内分两步执行（不动状态机，符合 Q10 拍板）
   - 失败统一 RESTORING（复用既有 runSandboxRestore）

✅ Step 3  example-blue-green demo 工程 (commit ea48e5c)
   - 13 个文件：README / Makefile / resources.yaml / Helm chart / 3 个 shell 脚本
   - 不依赖 web3-blitz，任何 K8s 集群 < 2 分钟跑通
   - traefik/whoami 镜像 + --name 参数注入版本标识
```

**文档**

```
✅ docs/design/traffic-layer.md (~430 行，去 -draft 后缀)
✅ docs/design/state-machine.md 加"v2.6 流量层与状态机协同"章节
✅ docs/design/architecture.md 加第 9 条核心设计决策"流量层抽象"
✅ README.md 蓝绿章节扩展（v2.0 命令式 + v2.6 声明式双模式）
✅ CHANGELOG.md 新建（v2.6.0 起统一格式）
```

**错误码扩展**

```
✅ internal/code/error.go 加 5 个 ErrRoute* 错误码
   编号区段 110000-110099 留给 internal/route 包
✅ internal/code/code_generated.go 手工补 case
   (codegen 工具不支持多 const block，临时 workaround，详见 v2.6.1)
```

**设计 Q 拍板（14 个，全部一次过）**

```
Q1=A 升级（TrafficProvider 接口）
Q2=D（仅蓝绿，canary 留 v2.7+）
Q3=C（Sandbox 状态机扩展）
Q4=C（多环境配置传播 v2.6.1）
Q5=A（独立子包 internal/route/）
Q6=C（双标记 annotation+label）
Q7=C（Sandbox 状态机原子性兜底）
Q8=B（v2.6.0 + v2.6.1 拆分）
Q9=A（后缀法命名 -blue/-green）
Q10=A（COMMITTING 内分两步）
Q11=A（失败 cleanup Green）
Q12=B（仅 Pod ready 判定）
Q13=A（~1100 代码 + 800 文档）
Q14=B（独立 demo 工程）
```

**v2.6.0 commit 链**

```
786b59d  Step 1: 流量层抽象 + 双 Provider
f0244cc  Step 2: Sandbox 蓝绿接入
ea48e5c  Step 3: demo 工程
e35bb0e  Step 4: 文档收尾 + CHANGELOG
1f780c2  chore: release v2.6.0  ← TAG ✨
```

**实施节奏**

```
2026-04-26 上午 6:08 起床 → 12:08 v2.6.0 release
6 小时 / 5 个 commit / ~3000 行新代码 + 文档
"踏踏实实 + 闪电战"——设计先行让实施无返工
```

---

### v2.5.0 — Controller 分片机制 (2026-04-25 release)

**核心代码**

```
✅ A.1 Step 1  internal/sharding/ 子包
   - shard.go 分片基础（fnv32 hash 落桶）
   - multi_lease.go K8s Lease 持有
   - lease_helpers.go 重试 / 续约
   
✅ A.1 Step 2  GlobalState 接入分片
   - GlobalState 加 shardOwner 字段
   - reconcile 跳过非自己持有的 namespace
   - OnShardChanged 回调
   
✅ A.1 Step 3  孤儿清理 (commit 99f316d)
   - GlobalState.RemoveOrphanProjects(isOwned func) 方法
   - OnShardChanged 回调即时清理（5s grace period）
   - orphanSweeper 周期兜底（30s 一次）
   - 4 个孤儿清理测试
```

**性能基准 + 设计文档**

```
✅ sha256 热加载去重验证（100 次幂等 apply → 0 reconcile）
✅ 10 项目实测（avg CPU 7.27%，集群总 21.81%）
✅ 50 项目实测（avg CPU 33.80%，集群总 101.40%）
   5x 项目 → 4.65x CPU（接近线性，符合预期）
✅ P=99 数据废弃（macOS 内存压力扭曲，详见 sharding-tuning.md 1.2）

✅ docs/design/sharding.md (605 行完整设计文档 + HA 边界 Q&A)
✅ docs/design/performance.md (v2.3/v2.4/v2.5 累计数据)
✅ docs/design/sharding-tuning.md 雏形（环境约束章节完成）
```

**工程基础设施**

```
✅ commits/ 目录工程规范（HANDOFF.md 3.2 节）
✅ 版本号约定（HANDOFF.md 3.3 节，只打 minor tag）
✅ benchmark/scripts/ 工具链
   - matrix.sh 雏形（v2.5.1 持续改进）
   - cleanup.sh 大规模改造（≥30 项目自动停 controller）
   - hot-reload.sh / setup.sh 修复（中文标点变量定界）
```

---

## ⏳ 当前持续改进（不打 tag）

### v2.6.1 候选（v2.6.0 之后的内部任务）

```
[ ] 多环境流量配置传播
    kp deploy --env prod --from-env staging
    从 staging 读"已验证的 traffic 配置"应用到 prod
    设计：traffic-layer.md 第七章（已写）
    工作量：~3 天

[ ] tools/codegen 支持多 const block
    实测发现：v2.6.0 添加 ErrRoute* 时 codegen 只识别第一个 const block
    报 "no values defined for type ErrorCode"
    修法：codegen.go genDecl 函数 ~30 行 ast.Inspect 改造
    递归收集所有匹配 typeName 的 const block
    工作量：~30 分钟

[ ] codegen -doc 模式同步
    -doc 输出的错误码 markdown 表与 error.go 同步
    含 ErrRoute* 5 个新错误码
    工作量：~30 分钟

[ ] flaky test 调研（如有发现）
```

### v2.5.1 持续（前置依赖：测试环境升级）

**当前已完成的零散任务**

```
✅ Bash 脚本踩坑修完（hot-reload.sh / cleanup.sh / setup.sh）
✅ HANDOFF.md 3.7 节扩展（中文标点紧贴变量名陷阱）
✅ HANDOFF.md 3.7 节再扩展（codegen / kp release / sandbox fork，v2.6 实施时发现）
✅ docs/design/sharding-tuning.md 雏形（环境约束 + 运维注意章节）
✅ benchmark/scripts/cleanup.sh 大规模友好（≥30 项目自动停 controller）
```

**受阻于环境升级（前置条件）**

```
[ ] ⏸ 测试环境升级（v2.5.1 性能立方体的前置条件）
    当前 8 GiB orbstack 限制 P_max ≈ 50（详见 sharding-tuning.md 1.2）
    选项：
      A. 升级 orbstack memory 到 16 GiB（最小变更）
      B. 多节点 K3d 集群（docker 多 container 模拟）
      C. 真实多节点 K8s 集群（云上）
    推荐 A
    
[ ] ⏸ benchmark/scripts/matrix.sh 完整跑通（前置：环境升级）
    阶段 1: P 维度（已 P=10 / P=50，待 P=100 / P=200）
    阶段 2: R 维度（待 R=5 / R=10）
    阶段 3: N 维度（待 N=3 / N=15）
    
[ ] ⏸ 数据可视化（前置：跑完 matrix.sh）
    把性能数据画成 heatmap / 折线图
    放到 sharding-tuning.md 第三章

[ ] ⏸ docs/design/sharding-tuning.md 性能立方体章节补完
    最优配置公式 + 配置建议（前置：完整数据）
```

**Backoff 队列 (A.1.5) — 暂时搁置**

```
[ ] internal/controller/backoff_queue.go
    指数退避队列，避免 reconcile 风暴
    数据驱动决定优先级（先看 v2.5.0 实际生产数据是否需要）
```

**client-go 对比基准 (A.2) — 暂时搁置**

```
[ ] fork feature/client-go-comparison 分支
    用 client-go 重写 GlobalState reconcile
    跑同样的 benchmark 矩阵
    数据驱动决定 v2.7.0 自研 informer 是否启动
```

**v2.4.0 P2 性能脚本验证（部分完成）**

```
✅ hot-reload.sh 脚本层（v2.5.1 已修）
[ ] watch-reconnect.sh 实测（受阻：orbstack VM 模式 PID 隔离）
[ ] concurrent-chaos.sh 实测（受阻：mock 项目无 helm release）
```

---

## 🔮 远期规划

### v2.7 — 自研 Informer + Canary

```
[ ] internal/informer/ 新独立包
    解决 watcher 框架开销
    每个 shard 一个 informer 实例

[ ] benchmark：自研 informer vs client-go vs v2.4.0 直 kubectl
    数据驱动决定是否真上自研

[ ] canary 完整实现（依赖 informer 高频事件流）
    含 metrics 健康度判定（5xx rate / p99 latency）
    在 v2.6 蓝绿基础上扩展 strategy: canary

[ ] shadow 流量镜像
    流量复制 + 不影响主线
    用于"生产流量验证新版本"场景
```

### v2.8 — 数据保护 + web3-blitz 升级

```
[ ] 数据敏感资源保护
    resources.yaml 加 protect: true 标记
    reconcile 检测到删除/重建意图时阻断
    kp confirm <ns>/<resource> 显式确认

[ ] kp release 阻断
    含 protected 资源时 kp release 要额外确认
    避免 release 操作误删敏感数据

[ ] web3-blitz 升级到 v2.6
    改造为声明式蓝绿（resources.yaml + sandbox commit）
    作为 KubePivot 的"真实生产案例"
    替换 v2.0 命令式蓝绿（kp promote）
```

### v2.8.0+ — 待规划

```
[ ] 多集群管理增强
    跨集群 deployment 同步
    集群健康度聚合
    
[ ] Audit / 审计日志
    所有 kp 命令的执行日志归档
    对接 SOC2/ISO27001 合规
    
[ ] Web UI（？）
    KubePivot 故意保持 CLI-first
    但企业用户场景可能需要 dashboard
    研究 / 评估优先级
```

---

## ✗ 已废弃 / 不做

```
✗ 单 leader 模式回退选项
   v2.5.0 起分片是默认且唯一模式
   v2.4.0 单 leader 仅作为线性外推参考

✗ etcd 作为 leader 选举的唯一选项
   v2.4.0 起加了 K8s Lease fallback
   etcd 现在是可选的（生产推荐，开发可省）

✗ v2.5.0 阶段引入 client-go
   永久原则：不引入 client-go
   自研 informer 是 v2.7+ 远期方向
   v2.6 流量层 Provider 也走 kubectl 路径

✗ v2.6.0 阶段做 canary
   仅蓝绿（确定性高）
   canary 健康度判定是大头（metrics + 渐进逻辑）
   留 v2.7（与自研 informer 一起做）

✗ v2.6.0 nginx-ingress 特有 canary annotation
   IngressProvider 改用"切换 backend.service.name"
   兼容任何 Ingress controller
   
✗ v2.6.0 metrics 监控（5xx / p99）
   仅 Pod ready 健康判定
   metrics 留 v2.7+
```

---

## 📈 工作流（按版本推进）

```
v2.5.0 (2026-04-25) ✅
   ↓
v2.5.1 (持续改进)
   ↓ Bash 修复 ✅ / sharding-tuning ✅ / 性能立方体 ⏸（环境升级前阻塞）
   ↓
v2.6.0 (2026-04-26) ✅
   ↓
v2.6.1 (持续改进)
   ↓ 多环境传播 / codegen 多 const block / docs 同步
   ↓
v2.7.0
   ↓ 自研 informer + canary（数据驱动决定启动时机）
   ↓
v2.8.0
   ↓ 数据保护 + web3-blitz 升级
   ↓
v2.8.0+
   待规划
```

---

## 编辑记录

```
2026-04-26  v2.6.0 release 后大改
            - 归档前一版到 archived/todo/TODO-v2.5.md
            - v2.6.0 整章 ⏳ → ✅
            - 新增 v2.6.1 候选章节
            - v2.5.1 受阻项加 ⏸ 标记
            - 工作流箭头链更新到 v2.6.0
```
