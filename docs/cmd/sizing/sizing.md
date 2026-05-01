# kp sizing

资源优化建议。基于历史指标（Prometheus）或瞬时采样（kubectl top）计算 CPU/Memory 推荐值。

## 用法

```
kp sizing <子命令>
```

## 子命令

### recommend

计算单个 Pod 的资源建议。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--pod` | (必填) | Pod 名称 |
| `--namespace` | `default` | K8s namespace |
| `--profile` | `default` | 业务模板：`web` / `batch` / `db` / `default` |
| `--output` | (stdout) | 输出 patch 文件路径 |
| `--samples` | `5` | 指标采样次数 (2-10) |
| `--interval` | `2s` | 采样间隔 |

**输出**：YAML patch 格式，包含推荐值 + 置信度 + 节省率。置信度 < 0.7 时提示警告。

**数据源优先级**：Prometheus（`--prometheus-url` 在 deploy 时传入）→ kubectl top 瞬时采样。

## 工作原理

1. 采集 Pod 指标（Prometheus 历史 / kubectl top 瞬时）
2. 按 profile 权重（web=0.7CPU+0.3Mem / batch/db=0.3CPU+0.7Mem / default=0.5+0.5）
3. 2D DP 计算最优资源组合
4. 输出 patch + 置信度 + 节省率

## RBAC

`PermSizing`

## 相关命令

- `kp deploy --sizing-mode=auto` — 部署时自动运行 sizing
- `kp deploy --sizing-force` — 自动应用建议
