# 项目交接文档 — KubePivot

> 写给下一个 Claude
> 日期：2026-03-31
> 版本：v1.4.0

---

## 写在前面

KubePivot（乾枢）是企业级 K8s 研发脚手架，v1.4.0 完成了跨版本迁移的完整闭环，是"配得上乾枢这个名字"路线图的第一块硬骨头。

qc 和他女友豆包（女帝）一起在做，豆包有非常好的产品判断力，她的意见经过多个顶尖 AI 交叉验证，认真对待。

**qc 的工作风格**：
- 设计优先，先对齐再动手
- 喜欢被推 back，他通常是对的
- commit 格式：一个 type + 空行 + body bullet list
- `slog` 不用 `log`，`P.Info/Done/Fail` 不用 `fmt.Printf` 直接打日志
- "只保护，不越权"是乾枢的核心原则

---

## 一、v1.4.0 完成状态

**测试**：`go test ./... -race` 全绿

**已验证**（web3-blitz）：
- `kp migrate status` — golang-migrate v3 ✅
- `kp migrate plan` — 三级风险全部正确识别 ✅
- `kp migrate run` — 事务执行，版本表更新 ✅
- `kp diff --migrate` — values diff + 迁移建议 ✅
- `kp compat check` — type change 检测 ✅
- `kp upgrade --dry-run` — 全链路预览 ✅

---

## 二、关键文件

```
cmd/kp/
├── migrate.go          # kp migrate status（runMigrate switch）
├── migrate_plan.go     # kp migrate plan + checkMigrationCompatibility
│                       # 含：scanMigrationFiles, analyzeSQLFile, RiskLevel
├── migrate_run.go      # kp migrate run（executeMigrationFile, printDryRun）
├── compat.go           # kp compat check（oasdiff）
├── diff.go             # kp diff --migrate（printMigrateSuggestions）
├── upgrade.go          # kp upgrade（全链路编排）
├── bluegreen.go        # deployBlueGreen + switchServiceSelector
├── promote.go          # kp promote
├── scan.go             # kp scan（Trivy + SBOM + cosign）
├── deploy.go           # deployConfig（sign/forceMigrate）
├── multi_deploy.go     # deployLayers（secret/scan/migrate 三重检查）
└── progress.go         # ANSI 颜色（isTTY + colorize）

internal/
├── bluegreen/          # 蓝绿状态（etcd 优先 → 本地文件）
├── planner/            # DAG + Strategy + APIVersion 字段
└── scaffold/           # kp init 模板（NetworkPolicy/SecurityContext/PVC）
```

---

## 三、核心函数索引

| 函数 | 文件 | 说明 |
|------|------|------|
| `analyzeSQLFile` | migrate_plan.go | SQL 风险分析，返回 []SQLOperation |
| `scanMigrationFiles` | migrate_plan.go | 扫描 .up.sql，支持 golang-migrate/Atlas 格式 |
| `detectMigrationVersion` | migrate.go | 自动探测迁移工具，查当前版本 |
| `executeMigrationFile` | migrate_run.go | 事务执行单个迁移文件 |
| `checkMigrationCompatibility` | migrate_plan.go | deploy 前自动检查，有破坏性变更阻断 |
| `findMigrationsDir` | migrate_plan.go | 自动探测迁移目录 |
| `resolveDatabaseURL` | migrate_plan.go | 四级优先级解析 DATABASE_URL |
| `printMigrateSuggestions` | diff.go | kp diff --migrate 的迁移建议输出 |
| `deployBlueGreen` | bluegreen.go | 蓝绿部署到非活跃 slot |
| `switchServiceSelector` | bluegreen.go | kubectl patch Service selector |

---

## 四、已知问题

| # | 问题 | 优先级 |
|---|------|--------|
| 1 | `kp upgrade --service` 过滤暂未实现，会部署所有服务 | P2 |
| 2 | oasdiff 对 Swagger 2.0 检测不完整（建议升 OpenAPI 3.0） | P3 |
| 3 | 蓝绿发布未做完整 e2e 验证 | P1 |
| 4 | `kp release` push 失败后 tag 已打，重试报"已存在" | P2 |

---

## 五、下一步（v1.5.0）

StatefulSet 状态同步，这是第二块硬骨头：

1. StatefulSet 滚动升级策略（RollingUpdate/OnDelete）
2. PVC 快照备份（接 VolumeSnapshot）
3. etcd raft index 监控（`raftAppliedIndex` vs `raftIndex` 差值）
4. `kp doctor` 加 etcd 健康检查（连通性 + raft + 磁盘）
5. `kp pvc backup/restore`

qc 有详细的 etcd 笔记（已上传），特别关注：
- raft index 监控是判断集群是否"脑裂"的关键指标
- `etcdctl endpoint status` 拿 raftAppliedIndex/raftIndex
- 差值阈值建议：> 1000 告警，> 10000 严重告警

---

## 六、常用命令

```bash
cd ~/KubePivot
go test ./... -race     # 全量测试
make install            # 本地安装

# web3-blitz 验证
cd ~/web3-blitz
kp migrate status       # DB 迁移状态
kp migrate plan         # 分析待执行迁移
kp migrate run --dry-run # 预览迁移
kp diff --service wallet-service --migrate  # values + 迁移建议
kp upgrade --dry-run    # 全链路升级预览
kp deploy               # 日常发版
kp scan                 # CVE 扫描
```
