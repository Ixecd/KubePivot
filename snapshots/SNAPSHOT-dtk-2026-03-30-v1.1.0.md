# SNAPSHOT — kubepivot

**里程碑**：v1.1.0 主体完成
**日期**：2026-03-30
**版本**：v1.1.0（进行中）

---

## 本轮完成（v1.0.0 → v1.1.0）

### controller 镜像统一构建 + e2e 验证

构建 `qingchun22/kubepivot-controller:v1.0.0`，所有项目共用：

```bash
docker build --no-cache -f build/docker/controller/Dockerfile \
  -t qingchun22/kubepivot-controller:v1.0.0 .
docker push qingchun22/kubepivot-controller:v1.0.0
```

web3-blitz 自愈 e2e 验证通过：

```
kubectl delete deployment wallet-service -n web3-blitz
→ controller 8s 内检测到缺失
→ helm rollback web3-blitz-wallet-service from=3 to=2
→ wallet-service 2/2 Running（恢复时间 ~13s）✅
```

### web3-blitz controller chart 迁移

从老的单 chart templates 迁移到独立 chart `web3-blitz-controller/`，
删掉 `deployments/web3-blitz/templates/` 里的三个老 controller 文件。
`components.yaml` 新增 controller 条目，拓扑变为 3 层。

### controller SSA 冲突处理

`healRecreate` 里检测 rollback 错误是否为 SSA 冲突，
清除 namespace 下所有资源 managedFields 后重试一次：

```
rollback 失败 → isSSAConflict → clearNamespaceManagedFields → retry rollback
```

### dtk status 多 release 展示

从 `components.yaml` 读服务列表，逐个查 helm release 状态，表格对齐展示：

```
Helm:
  Release                                   Revision    Status        Updated
  web3-blitz-web3-blitz-postgres            3           deployed      2026-03-30 08:47:17
  web3-blitz-wallet-service                 4           deployed      2026-03-30 08:59:38
  web3-blitz-chain-miner                    -           not found     -
```

降级：`components.yaml` 不存在时回退到单 release 模式。

### dtk rollback 拓扑逆序进度

多服务模式下按拓扑逆序逐层并行 rollback，打印每步进度：

```
⏪ 多服务模式：3 层，逆序回滚
⏪ 回滚第 1 层（共 3 层，1 个服务）
  ✓ rollback web3-blitz-web3-blitz-controller 完成
⏪ 回滚第 2 层（共 3 层，1 个服务）
  ✓ rollback web3-blitz-wallet-service 完成
⏪ 回滚第 3 层（共 3 层，2 个服务）
  ✓ rollback web3-blitz-web3-blitz-postgres 完成
  ✓ rollback web3-blitz-web3-blitz-etcd 完成
✅ 全部服务回滚完成
```

降级：`components.yaml` 不存在时回退到单 release rollback。

### dtk init --dry-run

打印将生成的目录结构，不执行任何文件写入、go mod tidy、git init：

```
[dry-run] dtk init --name myapp --module github.com/me/myapp

将生成项目：./myapp
  模块路径：github.com/me/myapp
  前端骨架：否

目录结构：
  myapp/
  ├── cmd/myapp/
  ├── deployments/myapp/
  │   ├── myapp-postgres/
  │   └── ...
  ...

不会执行：go mod tidy / git init / git commit / 文件写入
```

---

## Bug 修复

| # | bug | 根因 | 修复 |
|---|-----|------|------|
| 1 | resources.yaml 策略永远不匹配 | `on_missing` vs `on-missing`，`recreate` vs `auto-heal` | 改 resources.yaml 和 configmap |
| 2 | controller panic nil pointer | `NewReconciler` 漏掉 `helm: &RealHelmClient{}` | `reconciler.go` 补注入 |
| 3 | 查不到 helm release，无法自愈 | `healRecreate` 直接用 `PROJECT_NAME` 当 release 名 | 改为 `PROJECT_NAME + "-" + res.Name` |

---

## 遗留（v1.1.0 剩余）

| # | 任务 |
|---|------|
| 1 | `REGISTRY_PREFIX` 支持阿里云 ACR 格式 |
| 2 | 统一进度输出带颜色 |
| 3 | 灰度发布支持 |

---

## 快照归档

```
snapshots/
├── SNAPSHOT-dtk-2026-03-27-v0.6.0.md
├── SNAPSHOT-dtk-2026-03-27-v0.7.0.md
├── SNAPSHOT-dtk-2026-03-27-v0.8.0.md
├── SNAPSHOT-dtk-2026-03-28-v0.9.0.md
├── SNAPSHOT-dtk-2026-03-29-v1.0.0-final.md
└── SNAPSHOT-dtk-2026-03-30-v1.1.0.md  ← 本次
```
