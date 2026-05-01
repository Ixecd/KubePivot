# kp status

查看部署状态。支持单环境和多环境对比。

## 用法

```
kp status [flags]
```

## Flag

| Flag | 默认值 | 说明 |
|---|---|---|
| `--namespace` | (project.env) | K8s namespace |
| `--context` | (project.env) | kube context |
| `--kubeconfig` | (project.env) | kubeconfig 路径 |
| `--history` | `false` | 同时显示部署历史 |
| `--all-envs` | `false` | 多环境对比 |

## 相关命令

- `kp deploy` — 部署
- `kp history` — 部署历史
- `kp diff` — 环境对比
