# kp history

查看部署历史记录。

## 用法

```
kp history [flags]
```

## Flag

| Flag | 默认值 | 说明 |
|---|---|---|
| `-n` | `20` | 显示最近 N 条 |
| `--namespace` | (project.env) | K8s namespace |
| `--context` | (project.env) | kube context |

## 相关命令

- `kp status --history` — 状态 + 历史
- `kp diff` — 版本对比
