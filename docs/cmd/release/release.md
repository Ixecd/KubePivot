# kp release

自动 release：commit + tag + push。

## 用法

```
kp release --version <vX.Y.Z> [flags]
```

## Flag

| Flag | 默认值 | 说明 |
|---|---|---|
| `--version` | (必填) | 版本号，格式 `v\d+\.\d+\.\d+` |
| `--deploy` | `false` | release 后自动部署 |
| `--no-push` | `false` | 不 push 到远程 |

## 工作流程

1. 校验版本号格式
2. 检查工作区干净
3. 检查 tag 不存在
4. 改 `configs/project.env`: `VERSION=`
5. 改 `cmd/kp/version.go`: `kpVersion =`
6. `git add` + `git commit -m "chore: release vX.Y.Z"`
7. `git tag -a vX.Y.Z`
8. `git push` + `git push --tags`

## 相关命令

- `kp version` — 查看当前版本
