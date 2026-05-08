# TODO — KubePivot v3.1+

> 当前 TODO（持续演化）
> 编写日期：2026-05-05
> Last release: v3.1.0
> Total commits: ~510

---

## 版本号约定

```
v{major}.{minor}

major  架构变更（v2 → v3）
minor  新功能落地（v3.0 → v3.1）
不打 patch 版本

发布节奏：tag → 实现 → tag（详见 commits/README.md）
```

---

## ✅ v3.1 已完成

### 自适应控制器规模（kp controller update）

```
✅ kp controller update — P→S→C 三维分片推荐
✅ Recommend() 算法 + GetSizingInfo() + ApplySizing()
✅ dry-run 压力变化趋势表 + 重平衡风暴预警
✅ 原子变更 + 失败回滚 + 孤儿 Lease 清理
✅ 权限预检（kubectl auth can-i）+ etcd 健康度
✅ 18 个单测全覆盖
```

### Controller StatefulSet 迁移

```
✅ Deployment → StatefulSet + headless Service
✅ etcd Learner sidecar（pod-0 bootstrap + pod-N join）
✅ 稳定 Pod 身份为 CBA 提供基础设施前提
```

### 12 语言脚手架

```
✅ kp init --lang <lang> 支持 go/python/java/rust/cpp/cs/zig/kotlin/ts/php/swift/lua
✅ 维护 Go/Python/Rust/C++ 四语言全链路（init→build→deploy→自愈）
✅ 多语言 Makefile + Helm chart 自动生成
✅ 项目根检测兼容非 Go 项目（configs/project.env）
✅ C++ 切换为 cpp-httplib（单头文件，零系统依赖）
✅ tools/test_multi_lang.sh 重写（结构验证 + helm template + docker build）
```

### 决策可解释性 + 元数据

```
✅ kp explain — 项目级概览 / 单 Pod 决策溯源
✅ components.yaml # Reason: sizing: 富元数据注释
✅ kp explain --list-pods + bash completion
✅ Suggestion.SampleCount 字段
```

### 设计文档刷新

```
✅ decision-stack.md — Layer 2/3 状态更新 + 代码归档
✅ sharding-tuning.md — 自适应分片替代手动调优
✅ traffic-multi-env.md — 从 draft 演化为正式文档
✅ controller-update-sizing-draft.md — 5 边缘情况补全 + 3 实施微调
✅ cell-based-architecture.md — CBA 设计草案（8 项关键设计）
✅ templates/README.md — 维护策略 + Dockerfile 约定
✅ commits/README.md — 发布节奏 + tag 工作流
```

### 修复

```
✅ kubectl top → kubectl get --raw /apis/metrics.k8s.io/（v1.33+ 兼容）
✅ 支持 nanocores CPU 单位（"320509n"）
✅ Java/Kotlin/Rust/Zig Dockerfile 模板修复
✅ watcher 重连日志 WARN→INFO
✅ dp.go 死代码清理
```

---

## ⏳ 当前进行中（v3.1 → v3.2）

### 优先级 1：Rescheduler + CLI ✅ 已完成

```
✅ Rescheduler Pod 驱逐（Eviction API）
    evictPodFunc 函数变量注入 → kubectl delete pod --grace-period=30 --wait=false
    Rescheduler.RunOnce() 手动触发接口
    see: b891c40

✅ kp scheduler 独立 CLI
    kp scheduler status   — 集群利用率 + 节点/Pod 统计
    kp scheduler reschedule — 手动触发一次重调度
    computeClusterSummary() 抽取为可测函数
    see: b891c40
```

### 当前聚焦：GPU + 三维 DP + 碳排放感知

```
[ ] GPU 资源感知层 — DCGM Exporter → PrometheusClient → NodeInfo.GPUInfo
[ ] Sizing 引擎三维 DP — dp[cpu][mem] → dp[cpu][mem][gpu]（仅 profile=training）
    关键边界：Web 服务（profile=web/batch/db）不走 GPU 维度，保持 2D DP
    只有 resources.yaml 声明 gpu 字段 + profile=training 才激活三维 DP
[ ] 碳排放感知 — CarbonIntensityProvider + kp scheduler status 展示
```

### 优先级 2：sizing 链路完善

```
[ ] kp sizing recommend 独立运行模式
    当前已可独立运行（cmd/kp/sizing.go），需验证端到端
    工作量：~半天

[ ] Confidence-based 自动 apply
    当前已实现阈值门控（deploy_sizing.go），需验证
    工作量：~半天
```

## 📋 远期（v3.2+）

### Layer 2/3 补完

```
[ ] kp explain Layer 3 决策溯源
    当前写死 "not yet available"
    → 读取 scheduler PlanWriter 输出 + Pod placement 数据
    工作量：~1 天

[ ] 多项目 sizing 批量报告
    单项目已通，缺少跨项目汇总视图
    工作量：~1 天

[ ] Layer 3 sizing 输出对接（Prometheus）
    sizing_adapter.go QueryRange stub
    → 接入 PrometheusClient 历史查询
    工作量：~2 天
```

### 多语言模板

```
[ ] 8 种语言 Dockerfile 社区维护（仅提供源码骨架）
    目录：java/cs/zig/kotlin/ts/php/swift/lua
    详见 templates/README.md
```

### 架构演化

```
[ ] Cell-based Architecture (CBA)
    详见 docs/design/cell-based-architecture.md
    前置条件：集群规模 ≥ 100 ns + 有状态资源保护引入
    重新评估时机：v3.2+ 设计阶段
```

### FUTURE.md 种子

```
F1: CBA — 已毕业为正式设计文档（cell-based-architecture.md）
    重新评估时机：v3.2+
```

---

## ✗ 已废弃 / 不做

```
✗ 单 leader 模式回退 — v2.5.0 起分片是唯一模式
✗ 引入 client-go — 自研 informer 已实现（v2.7.0）
✗ 手动分片调优 — kp controller update 自动化
✗ Drogon C++ 模板 — 切换为 cpp-httplib（零依赖）
✗ kubectl top pod -o json — 改用 raw metrics API
```

---

## 编辑记录

```
2026-05-05  v3.1 刷新
            - 归档 v2.7 版到 archived/todo/TODO-v2.7.md
            - 全量重写：反映 v3.1 实际完成内容
            - 4 项 ❌ 待办列为优先级 1/2
            - CBA 种子已毕业为正式设计文档
            - 移除过期 v2.5/v2.6/v2.7 章节
```
