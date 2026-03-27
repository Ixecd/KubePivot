# SNAPSHOT — dev-toolkit

**里程碑**：v0.6.0 稳定性全面提升 + 大测试通过
**日期**：2026-03-27
**版本**：v0.6.0

---

## 本轮完成

### 1. 新增命令

**`dtk history`**：查看部署状态转换历史
- `-n N` 限制显示条数（默认 20，0 = 全部）
- tabwriter 对齐，含序号、时间、From/To、版本、原因
- 数据来自状态机 history 字段，etcd 和本地文件都支持

### 2. 稳定性提升

**etcd Watch 断线重连**：
- 指数退避，初始 1s，上限 30s
- 重连成功后 delay 重置为 1s
- 抽出 `watch()` 函数，清晰区分连接循环和事件循环
- ctx 取消在两层都正确处理

**SSA managedFields 冲突自动处理**：
- `isSSAConflict()` 检测 helm 错误关键词
- 检测到冲突时清除 namespace 下所有 helm 管理资源的 managedFields（幂等）
- 清除后自动重试一次 deploy
- 重试失败走原有回滚逻辑
- `runCmd` 改用 `io.MultiWriter` 同时输出终端和捕获 stderr，供关键词检测使用

**本地状态→etcd 自动迁移**：
- `state.New()` 里检测：store 是 etcd 且 etcd 无记录但本地文件有记录
- 自动迁移，打印提示和备份路径
- 迁移失败非致命，warn 后继续

**`dtk status` 时区修复**：
- helm Updated 字段 `time.Parse` 后加 `.Local()`，正确显示本地时间

### 3. 大测试验证（web3-blitz）

| 步骤 | 结果 |
|---|---|
| `dtk doctor` | ✅ 全绿 |
| `dtk status` | ✅ 三层信息正确 |
| `dtk history` | ✅ 历史记录完整 |
| `dtk history -n 2` | ✅ 索引连续，提示正确 |
| `dtk deploy` | ✅ 部署成功 |
| controller 自愈 | ✅ 删除 wallet-service，10s 内自动恢复 |
| `dtk status`（自愈后） | ✅ 状态同步正确 |
| etcd 自动迁移 | ✅ 填上 ETCD_ENDPOINTS，下次 deploy 自动迁移，数据完整 |

**controller 自愈时间线**：
```
0s  kubectl delete deployment wallet-service
1s  新 pod Pending → Init:0/2（wait-postgres）
2s  Init:1/2（wait-etcd）
4s  Running
10s 1/1 Ready ✅
```

### 4. 遗留发现

- web3-blitz `deploy.mk` 没有镜像存在检查（老项目，待同步 dtk 最新 mk）

---

## 遗留问题

| # | 问题 | 优先级 |
|---|------|--------|
| 1 | controller 单元测试缺失 | P1 |
| 2 | `dtk init --dry-run` | P2 |
| 3 | 多服务支持 | P1 |
| 4 | `dtk diff` 版本对比 | P1 |
| 5 | web3-blitz deploy.mk 同步 dtk 最新版本 | P1 |

---

## 历史快照

```
snapshots/
├── ...
├── SNAPSHOT-dtk-2026-03-27-v0.5.0.md
├── SNAPSHOT-dtk-2026-03-27-v0.5.1.md
└── SNAPSHOT-dtk-2026-03-27-v0.6.0.md  ← 本次
```
