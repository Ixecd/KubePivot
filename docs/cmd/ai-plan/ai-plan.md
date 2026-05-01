# kp ai-plan

AI 扫描仓库，自动规划组件配置。

## 用法

```
kp ai-plan [flags]
```

## Flag

| Flag | 默认值 | 说明 |
|---|---|---|
| `--suggest-only` | `false` | 只建议不写入 |
| `--desc` | | 项目描述 |

## 工作原理

1. 扫描仓库（目录结构 / go.mod / Dockerfile / README）
2. 调 LLM 分析服务架构
3. 生成 components.yaml 建议

## 相关命令

- `kp init` — 脚手架
- `kp deploy` — 部署
