# kp explain

解释 KubePivot 的调度决策和资源推荐依据。

## 用法

```
kp explain [flags]
kp explain --list-pods
kp explain --pod <name>
```

## Flag

| Flag | 默认值 | 说明 |
|------|--------|------|
| `--list-pods` | `false` | 列出项目中所有 Pod 的决策摘要 |
| `--pod` | | 查看指定 Pod 的详细决策溯源 |
