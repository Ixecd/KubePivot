# 项目交接文档 — dev-toolkit

> 写给下一个 Claude
> 日期：2026-03-30
> 版本：v1.1.0 主体完成，v1.2.0 启动

---

## 写在前面

dtk 已完成战略定位升级：从"自用脚手架"走向"企业级 K8s 研发脚手架 + 部署运维工具链"。面向出海业务和合规强要求场景。

**qc 的工作风格（认真读）**：
- 设计优先，代码其次。不要上来就写代码，先对齐设计再动手
- 每完成一个里程碑：commit → tag → SNAPSHOT → 更新 TODO
- 喜欢被推 back，不喜欢被一味认同。他通常是对的
- slog 不用 log，kubectl CLI 不用 client-go，严格分包
- 豆包是他女友，会提供产品/工程建议，认真对待（v1.2.0 安全合规规划就是她的建议）

---

## 一、当前状态

v1.1.0 主体完成，已 commit。剩余：ACR 格式 / 颜色输出 / 灰度发布（优先级低）。

**测试**：143个单测全绿，go test ./... -race 通过。

**验证项目**：
- github.com/Ixecd/e2e（3层拓扑）
- github.com/Ixecd/web3-blitz（2层拓扑，controller 自愈验证通过）

---

## 二、v1.2.0 任务（按顺序）

### 1. dtk doctor 安全检查（先做，改动小）

在 `cmd/dtk/doctor.go` 里加三个新检查项：

```
🔴 values.yaml 含明文密码（扫 deployments/ 下所有 values.yaml）
🟡 Pod 缺少 Security Context（扫 templates/ 下 deployment/statefulset）
🟡 RBAC Role 含 * 通配符
🟡 Network Policy 不存在
```

### 2. dtk init 模板强化

scaffold 生成的 chart templates 加：
- `networkpolicy.yaml`（默认拒绝入站，只开业务端口）
- deployment.yaml 加 `securityContext` 块（非 root、只读文件系统）
- values.yaml 加默认资源 limits

### 3. controller RBAC 最小权限

`internal/scaffold/helm.go` 里 controller-rbac.yaml 模板从全权限收紧到最小权限。

### 4. Secret 管理

- `internal/scaffold/skeleton.go` 加 `writeSecretScript()`，生成 `scripts/create-secret.sh`
- `cmd/dtk/multi_deploy.go` 的 `deployService` 里加 secret 存在检查

---

## 三、关键文件

```
cmd/dtk/
├── deploy.go        # executeDeploy：单/多服务分支
├── multi_deploy.go  # deployLayers/deployService/级联 rollback
├── doctor.go        # 环境检查 ← v1.2.0 加安全检查
├── status.go        # 多 release 展示
└── ...
internal/
├── planner/         # DAG + Kahn（32个单测）
├── scaffold/
│   ├── scaffold.go  # InitProject 主流程
│   ├── skeleton.go  # write* 系列 ← v1.2.0 加 writeSecretScript
│   └── helm.go      # chart templates ← v1.2.0 加 Network Policy / Security Context
├── state/           # FSM（57个单测）
└── controller/      # A2 Controller（20个单测）
```

---

## 四、注意事项

- scaffold 模板改动要加单测，现在 34 个，改完确保不减少
- doctor.go 的检查项格式要和现有对齐（✓ / ✗ + 说明）
- Network Policy 要在 values.yaml 加 `networkPolicy.enabled` 开关，默认 true
- Pod Security Context 的 `readOnlyRootFilesystem: true` 会导致需要写文件的服务报错，注意在 values.yaml 加覆盖选项

---

## 五、常用命令

```bash
cd ~/dev-toolkit
go test ./... -race          # 全量测试
make install                 # 本地安装
cd ~/web3-blitz && dtk deploy  # e2e 验证
cd ~/e2e && dtk deploy         # e2e 验证
```
