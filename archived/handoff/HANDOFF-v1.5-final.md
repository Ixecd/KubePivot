# 项目交接文档 — KubePivot

> 写给下一个 Claude
> 日期：2026-03-31
> 版本：v1.5.0 + 蓝绿 e2e 验证完成

---

## 写在前面

KubePivot（乾枢）是企业级 K8s 研发脚手架。qc 和他女友豆包（女帝）一起在做，设计文档质量很高，认真对待。

**qc 的工作风格**：
- 设计优先，先对齐再动手
- 喜欢被推 back，不喜欢被纯认同
- `slog` 不用 `log`，`P.Info/Done/Fail` 做进度输出
- "只保护，不越权" 是乾枢核心原则
- 不搞技术债，宁可留 TODO 也不临时方案
- 喜欢 KWOK 压测，v1.6.0 计划万节点验证

---

## 一、当前状态

**测试**：`go test ./... -race` 全绿

**已验证**（web3-blitz，k3s + local-path + OrbStack）：
- 蓝绿发布完整 e2e：`kp deploy` → green slot 部署 → `kp promote` 切换 selector ✅
- `kp status` StatefulSet pod 详情 ✅
- `kp doctor` etcd 健康检查 ✅
- `kp migrate status/plan/run` ✅
- `kp upgrade --dry-run` ✅
- `kp pvc` 骨架 + CSI 不可用正确提示 ✅

---

## 二、关键文件

```
cmd/kp/
├── bluegreen.go         # deployBlueGreen + switchServiceSelector
│                        # slot 用 targetRelease 作 deployment 名
│                        # --set bluegreen.skipService=true 跳过 Service
├── promote.go           # kp promote
├── doctor.go            # 主检查入口
├── doctor_etcd.go       # etcd 健康检查
├── doctor_pvc.go        # VolumeSnapshot 环境检查
├── status.go            # kp status（含 StatefulSet 详情）
├── status_statefulset.go # StatefulSet pod 列表
├── multi_deploy.go      # deployLayers（rollout 路由 + 迁移检查）
├── pvc.go               # kp pvc 完整实现（待 CSI 验证）
├── upgrade.go           # kp upgrade（--service 过滤已实现）
├── migrate.go           # kp migrate status
├── migrate_plan.go      # kp migrate plan + checkMigrationCompatibility
├── migrate_run.go       # kp migrate run
└── compat.go            # kp compat check
```

---

## 三、已知 Bug（P1，已修复）

### Bug 1：蓝绿失败后状态机停在 DEPLOYING

**现象**：蓝绿 rollout 失败 → 级联 rollback 完成 → 状态机应回 RUNNING，但停在 DEPLOYING

**临时解法**：`kp resume`（会检测实际状态重新部署）

**根因**：`deployLayers` 的 cascadeOK 路径 `sm.Transition(StateRunning)` 在蓝绿场景下判断不匹配

**位置**：`cmd/kp/multi_deploy.go` 103 行

### Bug 2：resume IDLE 判断未检查 Deployment

**现象**：蓝绿失败后 resume，只看 StatefulSet，rolling Deployment 还在但被判为 IDLE，从头重新部署

**临时解法**：`kp rollback` 让状态回 RUNNING，再 `kp deploy`

**根因**：`resume.go` 的 IDLE 检测逻辑只看 StatefulSet

### Bug 3：kp release push 失败后 tag 已打

重试时报"tag 已存在"，手动 `git push && git push --tags`

---

## 四、蓝绿发布 Chart 要求

蓝绿发布要求 chart 模板满足：

1. `metadata.name` 全部用 `{{ .Release.Name }}`（不能硬编码服务名）
2. `service.yaml` 加 `{{- if not .Values.bluegreen.skipService }}` 条件
3. `values.yaml` 加 `bluegreen.slot: ""` 和 `bluegreen.skipService: false`

kp init 生成的模板已自动满足，存量项目需手动改。

---

## 五、下一步（v1.5.2 → v1.6.0）

**v1.5.1（需要 CSI 集群）**：
- `kp pvc backup/restore/list` 完整验证

**v1.6.0 大规模场景**：
- KWOK 万节点压测（qc 强烈感兴趣）
- 增量部署（`--changed-only`，基于 git diff）
- 并行度控制（`--parallelism`）
- 部署耗时统计

---

## 六、常用命令

```bash
cd ~/KubePivot
go test ./... -race
make install

cd ~/web3-blitz
kp doctor
ETCD_ENDPOINTS=localhost:2379 kp doctor   # 含 etcd 检查
kp status                                 # 含 StatefulSet 详情
kp deploy                                 # 蓝绿：部署到 inactive slot
kp promote --service wallet-service       # 切换流量
kp migrate status
kp upgrade --dry-run
```
