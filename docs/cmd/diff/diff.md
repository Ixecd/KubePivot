# kp diff

对比两个部署版本或两个环境的配置差异。

## 用法

```
kp diff [flags]
```

## Flag

| Flag | 默认值 | 说明 |
|---|---|---|
| `--from` | (上一版本) | 起始版本号 |
| `--to` | (最新) | 目标版本号 |
| `--from-env` | | 起始环境 |
| `--to-env` | | 目标环境 |
| `--namespace` | (project.env) | K8s namespace |
| `--context` | (project.env) | kube context |

## 相关命令

- `kp status --all-envs` — 多环境状态对比
- `kp history` — 部署历史
