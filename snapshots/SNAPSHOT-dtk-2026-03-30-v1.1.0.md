# SNAPSHOT — dev-toolkit

**里程碑**：v1.1.0 controller 自愈 e2e 验证 + dtk status 多 release 展示
**日期**：2026-03-30
**版本**：v1.1.0（进行中）

---

## 本轮完成（v1.0.0 → v1.1.0）

### controller 镜像统一构建

构建 `qingchun22/dev-toolkit-controller:v1.0.0`，所有项目共用，无需各自构建：

```bash
cd ~/dev-toolkit
docker build --no-cache \
  -f build/docker/controller/Dockerfile \
  -t qingchun22/dev-toolkit-controller:v1.0.0 .
docker push qingchun22/dev-toolkit-controller:v1.0.0
```

### web3-blitz controller chart 迁移

从老的单 chart templates 迁移到多 chart 独立结构：

```
deployments/web3-blitz/web3-blitz-controller/
├── Chart.yaml
├── values.yaml
└── templates/
    ├── deployment.yaml   # dev-toolkit-controller，共用镜像
    ├── rbac.yaml         # ServiceAccount + Role + RoleBinding
    └── configmap.yaml    # resources.yaml 挂进 pod
```

删掉 `deployments/web3-blitz/templates/` 里的三个老 controller 文件：
- `controller-deployment.yaml`
- `controller-rbac.yaml`
- `resources-configmap.yaml`

`components.yaml` 新增 controller 条目，拓扑变为 3 层：

```
层级 1（并行）：web3-blitz-postgres, web3-blitz-etcd
层级 2：wallet-service
层级 3：web3-blitz-controller（image 为空，跳过 build/push，直接 helm）
```

### controller 自愈 e2e 验证 ✅

在 web3-blitz 完整跑通：

```
kubectl delete deployment wallet-service -n web3-blitz
→ controller 8s 内检测到缺失
→ 执行 helm rollback web3-blitz-wallet-service from=3 to=2
→ wallet-service 2/2 Running（恢复时间 ~13s）✅
```

### dtk status 多 release 展示

从 `components.yaml` 读取服务列表，逐个查 helm release 状态，表格对齐展示：

```
Helm:
  Release                                   Revision    Status        Updated
  ----------------------------------------  --------    ------------  -------------------
  web3-blitz-web3-blitz-postgres            3           deployed      2026-03-30 08:47:17
  web3-blitz-web3-blitz-etcd                3           deployed      2026-03-30 08:47:17
  web3-blitz-wallet-service                 4           deployed      2026-03-30 08:59:38
  web3-blitz-chain-miner                    -           not found     -
  web3-blitz-web3-blitz-controller          2           deployed      2026-03-30 08:47:17
```

降级逻辑：`components.yaml` 不存在时回退到单 release 模式，向后兼容。

---

## Bug 修复

| # | bug | 根因 | 修复位置 |
|---|-----|------|---------|
| 1 | resources.yaml 策略永远不匹配，自愈不触发 | `on_missing`（下划线）vs 代码 `on-missing`（连字符），`recreate` vs `auto-heal` | web3-blitz resources.yaml + configmap |
| 2 | controller panic nil pointer dereference | `NewReconciler` 漏掉 `helm: &RealHelmClient{}` | `internal/controller/reconciler.go` |
| 3 | 查不到 helm release，无法自愈 | `healRecreate` 直接用 `PROJECT_NAME` 当 release 名，忽略 v1.0.0 的 `{project}-{service}` 命名规则 | `internal/controller/heal.go` |

---

## 遗留（v1.1.0 剩余）

| # | 任务 |
|---|------|
| 1 | controller SSA 冲突处理（CLI 层已处理，controller 层待补） |
| 2 | dtk rollback 打印拓扑逆序进度 |
| 3 | dtk init --dry-run |
| 4 | 统一进度输出带颜色（终端支持时） |
| 5 | REGISTRY_PREFIX 支持阿里云 ACR 格式 |
| 6 | 灰度发布支持 |

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
