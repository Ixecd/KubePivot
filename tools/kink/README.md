# KinK — K8s in K8s

乾枢调度器大规模压测加速器。

在 K8s 集群内启动 fake K8s API Server，模拟 N 个 GPU 节点 + M 个 Pod。乾枢调度器通过标准 K8s API 交互，不感知真假。

## 快速开始

```bash
# 基础整卡调度 (100 节点 × 8×A100)
go run tools/kink/*.go --scenario=s1

# NVLink 全碎降级测试
go run tools/kink/*.go --scenario=s3 --nodes=200 --topology-frag=1.0

# 碳延迟批量测试
go run tools/kink/*.go --scenario=s4 --nodes=500 --carbon-jobs=200

# 万节点压测
go run tools/kink/*.go --scenario=s5 --nodes=10000

# 时空折叠 (MPS + 碳 + NVLink 组合)
go run tools/kink/*.go --scenario=s6 --nodes=1000 --mps-pods=5000 --carbon-jobs=100 --topology-frag=0.2
```

## 场景

| 场景 | 节点 | 特点 | 预期 |
|---|---|---|---|
| S1 | 100 × 8×A100 | 基础整卡 | BinPack < 1s |
| S2 | 50 × 8×A100 | MPS 2400 Pod | 48 client 上限 |
| S3 | 200 × 8×A100 | NVLink 全碎 | 降级 warn |
| S4 | 500 × 4×V100 | 碳延迟 200 Job | Queue 排序 |
| S5 | 10000 × 混合 | 万节点 | < 5s, < 2GB |
| S6 | 1000 × 8×A100 | 时空折叠 | 组合通过 |

## 设计

- **Fake GPU 节点生成** (`gpu_fake.go`): 支持 `TopologyFrag` 参数控制 NVLink 碎片度
- **碳强度模拟器** (`carbon_fake.go`): 模拟一天 24h 电网碳强度波动
- **Fake K8s API Server** (`apiserver.go`): 暴露标准 K8s List API
- **压测运行器** (`stress.go`): 6 个场景 + 结果验证

## 不做什么

- 不替代真实 GPU 集群的端到端测试
- 不模拟 CUDA kernel 执行
- 不模拟 GPU 显存分配
- 仅验证乾枢调度器的算法逻辑和计算性能

## 编译

```bash
# 独立编译 (不打入 kp 二进制)
go build -o kink tools/kink/*.go
./kink --scenario=s5 --nodes=10000
```
