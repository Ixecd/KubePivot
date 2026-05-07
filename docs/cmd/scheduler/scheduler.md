# kp scheduler

触发 KubePivot 调度器。查看集群利用率和重调度状态。

## 用法

```
kp scheduler <subcommand> [flags]
```

## 子命令

| 子命令 | 说明 |
|--------|------|
| `status` | 集群利用率 + 节点/Pod 统计 |
| `reschedule` | 手动触发一次运行时重调度 |

## 示例

```bash
kp scheduler status
kp scheduler reschedule
```

## 性能基准

池化调度计算四路对比 (100k Pods, 2k Nodes)：

| 方法 | 延迟 | 加速 |
|------|------|------|
| Exact | 1805 ms | 1x |
| Sampled | 1099 ms | 1.6x |
| O(1) Counters | 75.5 ms | 23.9x |

详见 `kp bench scale`。
