# kp audit

操作审计。查看部署、配置变更等操作历史。

## 用法

```
kp audit [--format table|json]
```

## Flag

| Flag | 默认值 | 说明 |
|---|---|---|
| `--format` | `table` | 输出格式：`table` / `json` |

## 数据来源

`~/.kp/audit/rbac.jsonl` — RBAC 拒绝记录也写在这里。

## 相关命令

- `kp secret audit` — Secret 引用审计
