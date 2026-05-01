# kp compat

API 兼容性检测。基于 oasdiff。

## 用法

```
kp compat <子命令>
```

## 子命令

### check

检测 API 破坏性变更。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--base` | | 基线版本 OpenAPI spec |
| `--revision` | | 新版本 OpenAPI spec |
| `--output-json` | `false` | JSON 格式输出 |

## 相关命令

- `kp migrate` — DB 迁移（不同层面，都是变更管理）
