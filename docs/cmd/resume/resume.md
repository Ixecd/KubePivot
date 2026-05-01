# kp resume

恢复中断的部署。从当前状态机状态继续执行。

## 用法

```
kp resume [flags]
```

## Flag

| Flag | 默认值 | 说明 |
|---|---|---|
| `--namespace` | (project.env) | K8s namespace |
| `--context` | (project.env) | kube context |
| `--kubeconfig` | (project.env) | kubeconfig 路径 |

## 工作流程

1. 检测 K8s 实际状态
2. 如果 RUNNING → 同步状态
3. 如果 IDLE（服务不存在）→ 重新部署
4. 其他状态 → 提示手动处理

## RBAC

`PermDeploy`（与 deploy 同级）

## 相关命令

- `kp deploy` — 新部署
- `kp rollback` — 回滚
