# kp init

项目脚手架。生成完整的 KubePivot 项目结构。

## 用法

```
kp init --name <project> --module <module> [flags]
```

## Flag

| Flag | 默认值 | 说明 |
|---|---|---|
| `--name` | (必填) | 项目名（小写） |
| `--module` | (必填) | Go module 路径 |
| `--output` | `./<name>` | 输出目录 |
| `--template` | (内置模板) | 自定义模板根目录 |
| `--force` | `false` | 允许非空目录 |
| `--with-frontend` | `false` | 生成 React + Vite + Tailwind 前端骨架 |
| `--dry-run` | `false` | 只打印生成内容 |

## 生成内容

- `configs/` — project.env / components.yaml / resources.yaml / teams.yaml
- `deployments/<project>/` — Helm chart 模板
- `Makefile` — deploy.build / deploy.push / deploy.install / deploy.run.all
- `cmd/<project>/` — Go 入口
- `.gitignore`

## 相关命令

- `kp deploy` — 部署项目
- `kp controller enroll` — 接入全局 controller
