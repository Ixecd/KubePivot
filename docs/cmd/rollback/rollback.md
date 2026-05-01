# kp rollback

回滚部署到上一个版本。

## 用法

```
kp rollback [flags]
```

## Flag

| Flag | 默认值 | 说明 |
|---|---|---|
| `--namespace` | (project.env) | K8s namespace |
| `--context` | (project.env) | kube context |
| `--kubeconfig` | (project.env) | kubeconfig 路径 |

**多服务模式**：按 `components.yaml` 拓扑分层逆序回滚。

## RBAC

`PermRollback`

## 相关命令

- `kp deploy` — 部署
- `kp down` — 下线
- `kp history` — 部署历史
