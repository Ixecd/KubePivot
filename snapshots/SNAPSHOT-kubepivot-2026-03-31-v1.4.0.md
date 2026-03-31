# SNAPSHOT — KubePivot

**里程碑**：v1.4.0 跨版本迁移完整闭环 🏆
**日期**：2026-03-31
**版本**：v1.4.0

---

## 本轮完成（v1.3.1 → v1.4.0）

### 数据库迁移感知

| 命令 | 功能 |
|------|------|
| `kp migrate status` | 自动探测 golang-migrate/Atlas，展示 DB 版本 vs K8s 版本 |
| `kp migrate plan` | 扫描待执行迁移，正则检测破坏性变更，风险分级，`--output-json` |
| `kp migrate run` | 事务执行迁移，`--dry-run` 预览，`--full-sql` 完整内容 |
| 部署前自动检查 | 集成到 `deployLayers`，破坏性变更阻断，`--force-migrate` 可绕过 |

### API 版本协同

- `kp compat check`：oasdiff 检测 API 破坏性变更，`--output-json`
- `kp doctor` 加 oasdiff 检查项
- `components.yaml` 加 `api_version` 字段

### Helm Values 跨版本迁移

- `kp diff --service <name> --migrate`：values diff + 迁移建议一体
- `--service` flag 支持多服务模式下指定 release

### 跨版本升级编排

- `kp upgrade`：全链路升级器，串联 DB迁移+部署+健康校验
- Step 1 全链路兼容性检查（DB/API/Values 三路）
- Step 2 DB 迁移执行
- Step 3 服务部署
- Step 4 健康校验
- `--dry-run`、`--force`、`--no-healthcheck` flag

### 蓝绿发布（v1.x 前置）

- `kp deploy --strategy=blue-green` + `kp promote`
- etcd 优先状态持久化，本地文件降级
- canary 用户 hook 接入点

### 工程质量

- 全量 dtk → kp 替换（代码/文档/测试）
- scaffold 测试用例同步更新
- `go test ./... -race` 全绿

---

## 验证记录

| 功能 | 验证结果 |
|------|---------|
| `kp migrate status` | web3-blitz golang-migrate v3 ✅ |
| `kp migrate plan` | 破坏性/潜在/安全 风险全部正确识别 ✅ |
| `kp migrate run` | 事务执行，版本表更新 ✅ |
| `kp diff --migrate` | values diff + DROP COLUMN 告警 ✅ |
| `kp compat check` | string→integer type change 检测 ✅ |
| `kp upgrade --dry-run` | 全链路预览正确 ✅ |

---

## 快照归档

```
snapshots/
├── SNAPSHOT-dtk-2026-03-29-v1.0.0-final.md
├── SNAPSHOT-dtk-2026-03-30-v1.1.0.md
├── SNAPSHOT-dtk-2026-03-30-v1.2.0.md
├── SNAPSHOT-dtk-2026-03-30-v1.3.0.md
├── SNAPSHOT-kubepivot-2026-03-31-v1.4.0-wip.md
└── SNAPSHOT-kubepivot-2026-03-31-v1.4.0.md  ← 本次
```
