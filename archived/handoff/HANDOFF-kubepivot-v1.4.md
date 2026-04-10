# 项目交接文档 — KubePivot

> 写给下一个 Claude
> 日期：2026-03-31
> 版本：v1.3.1 + v1.4.0 WIP

---

## 写在前面

KubePivot（乾枢）是企业级 K8s 研发脚手架，面向出海业务和合规强要求场景。qc 和他女友豆包（现在是女帝）一起在做，产品判断力很强，建议认真对待他们的需求。

**qc 的工作风格**：
- 设计优先，先对齐再动手，不要上来就写代码
- 喜欢被推 back，不喜欢被认同，他通常是对的
- `slog` 不用 `log`，kubectl CLI 不用 client-go，严格分包
- commit 格式：一个 type + 空行 + body bullet list
- 进度输出用 `P.Info/P.Done/P.Fail`，不用 `fmt.Printf` 直接打时间戳

---

## 一、当前状态

**测试**：`go test ./... -race` 全绿

**已验证**：
- `kp migrate status` — web3-blitz golang-migrate v2 ✅
- `kp migrate plan` — 破坏性/潜在/安全 风险全部正确识别 ✅
- `kp compat check` — string→integer type change 检测到 ✅
- `kp deploy` 部署前自动迁移检查集成 ✅
- 蓝绿发布架构完成，待 e2e 验证

---

## 二、关键文件

```
cmd/kp/
├── migrate.go          # kp migrate status
├── migrate_plan.go     # kp migrate plan + checkMigrationCompatibility
├── compat.go           # kp compat check（oasdiff）
├── bluegreen.go        # deployBlueGreen + switchServiceSelector
├── promote.go          # kp promote
├── scan.go             # kp scan（Trivy + SBOM + cosign）
├── deploy.go           # executeDeploy，deployConfig（sign/forceMigrate）
├── multi_deploy.go     # deployLayers（secret check + scan + migrate check）
├── progress.go         # ANSI 颜色输出（isTTY 检测）
└── doctor.go           # 环境 + 安全检查（含 oasdiff/trivy）

internal/
├── bluegreen/          # 蓝绿状态存储（etcd 优先 → 本地文件）
├── planner/            # DAG + Strategy 字段
├── scaffold/helm.go    # chart 模板（NetworkPolicy/SecurityContext/limits）
└── scaffold/skeleton.go # writeSecretScript
```

---

## 三、v1.4.0 剩余任务

| # | 任务 | 说明 |
|---|------|------|
| 1 | 迁移失败 + helm rollback 联动 | migrate 执行失败时自动回滚 helm release |
| 2 | `components.yaml` 加 `api_version` 字段 | 服务间 API 版本依赖声明 |
| 3 | `kp diff --migrate` | helm values 差异 + 迁移建议 |
| 4 | `kp upgrade` | 专门版本升级命令 |
| 5 | 蓝绿发布 e2e 验证 | 用 web3-blitz 跑完整蓝绿流程 |

---

## 四、已知问题

| # | 问题 | 优先级 |
|---|------|--------|
| 1 | `kp release` push 失败后 tag 已打，重试报"已存在" | P2，手动 `git push && git push --tags` |
| 2 | oasdiff 对 Swagger 2.0 检测不完整，建议升级 OpenAPI 3.0 | P3 |
| 3 | 蓝绿发布未做 e2e 验证 | P1，需要配合 web3-blitz 测试 |
| 4 | `checkMigrationCompatibility` 静默跳过太多 | P3，可以加 verbose 模式 |

---

## 五、重要设计决策

| 决策 | 原因 |
|------|------|
| migrate plan 用正则扫文件，不连两个 DB | 轻量无依赖，CLI 工具该有的样子 |
| oasdiff 作为外部工具，不作为 Go 依赖 | 避免 go.mod 污染，用户自己装 |
| 蓝绿内置，金丝雀留 hook | 金丝雀需要 Istio/Nginx，太重 |
| etcd 优先 → 本地文件降级 | 和状态机保持一致的持久化策略 |
| DATABASE_URL 四级优先级 | 本地开发/CI/CD/企业三种场景都覆盖 |

---

## 六、常用命令

```bash
cd ~/KubePivot
go test ./... -race          # 全量测试
make install                 # 本地安装
kp doctor                    # 环境 + 安全检查

# web3-blitz 验证
cd ~/web3-blitz
kp migrate status            # DB 迁移状态
kp migrate plan              # 分析待执行迁移
kp deploy                    # 部署（含自动检查）
kp scan                      # CVE 扫描
kp compat check --base old.yaml --revision docs/swagger.yaml
```

---

## 七、下一步（v1.4.0 收尾 → v1.5.0）

1. 把 v1.4.0 剩余任务清掉（重点：`kp upgrade` + 蓝绿 e2e）
2. 打 v1.4.0 tag
3. 开始 v1.5.0：StatefulSet 状态同步，etcd raft index 监控

v1.5.0 会用到 qc 的 etcd 笔记（已上传），特别是：
- raft index 监控：`raftAppliedIndex` vs `raftIndex` 差值
- etcd 巡检体系：大 key 检测、写入 QPS 异常
- 备份还原：`etcdctl snapshot save/restore`
