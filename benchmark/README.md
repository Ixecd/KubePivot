# KubePivot Benchmark 套件

## KP KVCache benchmark

```bash
# 运行 KP 端全部 benchmark
make bench.kp

# 保存基线（用于 prune gate 回归检查）
make bench.baseline

# 回归检查（与 saved baseline 对比）
make bench.regression
```

## Prune Gate

每个 feature PR 应附带 benchmark 对比。性能退步 >10% 自动 block。

```bash
# 在 feature 分支上跑
make bench.baseline        # 保存当前分支基线
# git checkout main
make bench.baseline        # 保存 main 基线
make bench.regression      # 对比
```

目标阈值：
- PodCache_Get: 退步 >20% → block
- PodCache_ListAll (5k): 退步 >70% → block（零分配读，大退步说明代码 bug）
- PodCache_ColdStart (5k): 退步 >30% → block
- PodCache_Memory (5k): RSS 增长 >20% → block

## Real YAML benchmark（v3.3）

当前 fake data（10 容器模板 + 均匀分布）不反映生产集群特性。
v3.3 工具链：

```bash
# 从真实 K8s 集群 dump Pod YAML fixtures
kp bench dump --kubeconfig ~/.kube/config --output benchmark/fixtures/

# 使用真实 fixtures 跑 benchmark
go test -bench=. -benchtime=10s \
  -fixtures=benchmark/fixtures/pods-5k.json \
  ./internal/eventstream/
```

真实数据带来：
- GPU 异构节点（A100×8, H100×4, L40S×2）
- StatefulSet 固定名 + Deployment 随机名混合
- 资源碎片化分布（非均匀）
- 生产规模的 Labels 数量和长度
