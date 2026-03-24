# dev-toolkit 当前状态快照

> 最后更新：2026-03-24
> 版本：v0.4.0

---

## 项目定位

Go 云原生项目脚手架：`dtk init` 生成完整项目骨架，`dtk deploy` 一键 AI 规划 + K8s 部署 + 状态追踪。

---

## 命令全览

| 命令 | 功能 |
|------|------|
| `dtk init` | 生成完整 Go 项目骨架 |
| `dtk deploy` | AI 规划 → build → push → helm → validate |
| `dtk resume` | 从中断点恢复 |
| `dtk rollback` | 手动回滚 |
| `dtk release` | 打 tag 发布，更新 VERSION，可选触发部署 |

---

## 状态机

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
                          ↓              ↓
                    ROLLING_BACK ←───────┘
```

etcd 持久化 → 降级到 `~/.dtk/state/`

---

## 目录结构

```
dev-toolkit/
├── cmd/dtk/
│   ├── main.go        # CLI 入口
│   ├── deploy.go      # runDeploy/runResume/runRollback
│   ├── release.go     # runRelease
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

---

## 测试覆盖

```
go test ./...  全绿
- cmd/dtk              deploy/release 集成测试
- internal/state       15 个状态机测试
- internal/scaffold    scaffold 单元测试
- test/integration     e2e 集成测试
```

---

## 下一步

- [ ] web3-blitz Deposit/Withdraw/Dashboard 接真实 API
- [ ] web3-blitz 作为 dtk 推广 demo 完整跑通
