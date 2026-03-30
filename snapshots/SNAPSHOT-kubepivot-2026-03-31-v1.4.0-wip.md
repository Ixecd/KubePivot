# SNAPSHOT — KubePivot

**里程碑**：v1.4.0 跨版本迁移（主体完成）
**日期**：2026-03-31
**版本**：v1.3.1 + v1.4.0 WIP

---

## 本轮完成（v1.3.1 → v1.4.0 WIP）

### v1.x 前置任务全部清掉

- SBOM 生成（`kp scan --sbom`，CycloneDX 格式）
- cosign keyless 签名（`kp deploy --sign`，`kp scan --verify`）
- ACR 格式支持（`.aliyuncs.com` 自动跳过 `-arch` 后缀）
- 进度输出带颜色（TTY 自动检测，pipe 降级纯文本）
- 蓝绿发布（`kp deploy --strategy=blue-green` + `kp promote`）

### v1.4.0 跨版本迁移（主体）

**kp migrate**
- `kp migrate status`：自动探测 golang-migrate/Atlas，显示 DB 版本 vs K8s 版本对齐状态
- `kp migrate plan`：扫描待执行迁移文件，正则检测破坏性变更（DROP/ALTER TYPE/SET NOT NULL），风险分级，`--output-json` CI/CD 友好
- 部署前自动迁移检查：集成到 `deployLayers`，破坏性变更阻断部署，`--force-migrate` 可绕过

**kp compat**
- `kp compat check`：基于 oasdiff 检测 API 破坏性变更
- 自动探测 swagger/openapi 文件
- `--output-json` 输出结构化结果
- `kp doctor` 加 oasdiff 版本检查

**蓝绿发布**
- `internal/bluegreen`：状态存储（etcd 优先 → 本地文件降级）
- `deployBlueGreen`：build/push 到非活跃 slot，rollout 验证
- `kp promote`：切换 Service selector，更新活跃 slot
- canary 留用户 hook（`scripts/canary-hook.sh`）

### 文档
- `docs/design/bluegreen.md` 新增
- `docs/design/migrate.md` 新增
- `docs/design/compat.md` 新增
- 全量文档 dtk → kp 替换完成

---

## v1.4.0 剩余

| # | 任务 |
|---|------|
| 1 | `kp diff --migrate` — helm values 迁移建议 |
| 2 | `kp upgrade` — 专门版本升级命令 |
| 3 | 迁移失败 + helm rollback 联动 |

---

## 当前状态

```
go test ./... -race  全绿
143+ 单测
kp migrate status  验证 ✅（web3-blitz golang-migrate v2）
kp migrate plan    验证 ✅（破坏性/潜在/安全 全部正确识别）
kp compat check    验证 ✅（string→integer type change 检测到）
```

---

## 快照归档

```
snapshots/
├── SNAPSHOT-dtk-2026-03-29-v1.0.0-final.md
├── SNAPSHOT-dtk-2026-03-30-v1.1.0.md
├── SNAPSHOT-dtk-2026-03-30-v1.2.0.md
├── SNAPSHOT-dtk-2026-03-30-v1.3.0.md
└── SNAPSHOT-kubepivot-2026-03-31-v1.4.0-wip.md  ← 本次
```
