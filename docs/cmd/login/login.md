# kp login

SSO 登录。支持 Google / GitHub / Dex。

## 用法

```
kp login --provider <google|github|dex> [flags]
```

## Flag

| Flag | 默认值 | 说明 |
|---|---|---|
| `--provider` | (必填) | `google` / `github` / `dex` |
| `--client-id` | (必填) | OAuth client ID |
| `--client-secret` | (大部分必填) | OAuth client secret（Dex 可选） |
| `--issuer` | (Dex 必填) | Dex issuer URL |

## 流程

1. 调 OAuth provider 跑完整 OAuth2 流程（浏览器 → callback → token）
2. 保存 token + userinfo 到 `~/.kp/credentials/<provider>.yaml`
3. 设置 `default.yaml` 指向当前 provider

## 相关命令

- `kp whoami` — 查看当前身份
- `kp team` — RBAC 管理
