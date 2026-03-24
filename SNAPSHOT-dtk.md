# dev-toolkit 当前状态快照

> 最后更新：2026-03-24
> 版本：v0.3.3

---

## 项目定位

Go 云原生项目脚手架：`dtk init` 生成完整项目骨架，`dtk deploy` 一键 AI 规划 + K8s 部署 + 状态追踪。

---

## 当前功能

| 命令 | 功能 |
|------|------|
| `dtk init` | 生成完整 Go 项目骨架 |
| `dtk deploy` | AI 规划 → build → push → helm → validate |
| `dtk resume` | 从中断点恢复 |
| `dtk rollback` | 手动回滚 |

## 状态机

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                          ↓              ↓
                    ROLLING_BACK ←───────┘
```

- etcd 持久化，降级到 ~/.dtk/state/
- 首次失败 → 清理 namespace → IDLE
- 更新失败 → 自动回滚 → RUNNING
- VALIDATING：所有 pod Ready + kubectl exec healthz 200

## 目录结构

```
dev-toolkit/
├── cmd/dtk/
│   ├── main.go        # CLI 入口（init/deploy/resume/rollback）
│   ├── deploy.go      # 状态机接入
│   ├── runner.go      # kubectl/helm 辅助
│   └── preflight.go   # 前置检查
├── internal/
│   ├── planner/       # AI 规划
│   ├── scaffold/      # 项目生成（6 个文件）
│   ├── state/         # 状态机
│   └── logger/
└── docs/
    ├── guide/zh-CN/   # deploy / helm / kubeconfig
    └── design/        # scaffold / helm-chart / state-machine
```

## 已知遗留问题

- `is_first` 判断逻辑：helm 会自动创建 namespace，导致 `namespaceExists` 判断不准确
- `detectActualState` 用 project 名查 deployment，名字不一致时失败

## 下一步 P0

- [ ] 集成测试：init → deploy → rollback 完整流程
- [ ] `is_first` 判断修复
- [ ] `dtk deploy --dry-run` 输出完整 make 命令
- [ ] 版本管理：`dtk release`
