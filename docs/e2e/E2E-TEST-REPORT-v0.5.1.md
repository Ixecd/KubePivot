# E2E 全流程验证报告

**日期**：2026-03-27
**版本**：v0.5.1
**测试项目**：e2e（github.com/Ixecd/e2e）
**跳过**：controller 自愈（需要构建 controller 镜像，留 P1）

---

## 测试环境

| 工具 | 版本 |
|---|---|
| Go | 1.25.7 |
| Docker | 28.5.2 |
| kubectl | v1.35.0 |
| helm | v4.0.1 |
| K8s 集群 | OrbStack 本地集群 |

---

## 流程记录

### 1. dtk init

```bash
dtk init --name e2e --module github.com/Ixecd/e2e
```

**结果**：✅

```
✅ 项目已成功生成！

  路径    ~/e2e
  模块    github.com/Ixecd/e2e
  入口    cmd/e2e
```

验证生成内容：

```
handoff/
├── AI-CODING-GUIDE.md  ✓
└── HANDOFF.md          ✓

configs/
├── components.yaml     ✓
├── project.env         ✓
└── resources.yaml      ✓
```

---

### 2. dtk deploy（v0.1.0）

```bash
dtk deploy
```

**结果**：✅

NOTES 输出：

```
✅ e2e 部署成功！

命名空间: e2e
版本:     v0.1.0
时间:     2026-03-27 09:15:26

组件状态:
  业务服务   ✓ running
  postgres  ✓ enabled
  etcd      ✓ enabled
  controller ✗ disabled
```

Pod 状态：

```
NAME                          READY   STATUS    RESTARTS   AGE
e2e-68dc84cb47-jppws          1/1     Running   0          30s
e2e-etcd-5bd65499ff-twvk8     1/1     Running   0          30s
e2e-postgres-0                1/1     Running   0          30s
```

状态机：`IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING`

---

### 3. dtk status

```bash
dtk status
```

**结果**：✅

```
项目: e2e       命名空间: e2e       版本: v0.1.0

部署状态: ✅ RUNNING
  最后更新: 2026-03-27 09:15:48
  原因:     部署验证通过

K8s 实际状态:
  ✓  e2e-68dc84cb47-jppws       Running
  ✓  e2e-etcd-5bd65499ff-twvk8  Running
  ✓  e2e-postgres-0             Running

Helm:
  Release:   e2e
  Revision:  1
  Updated:   "2026-03-27T09:15:26.549953+08:00"
```

---

### 4. dtk status --history

```bash
dtk status --history
```

**结果**：✅

```
历史记录（共 4 条）:
  时间                   从               →  到               原因
  -------------------  --------------  -  --------------  --------------------
  2026-03-27 09:14:51  IDLE            →  INITIALIZING    开始部署 v0.1.0
  2026-03-27 09:14:51  INITIALIZING    →  DEPLOYING       执行 helm upgrade
  2026-03-27 09:15:48  DEPLOYING       →  VALIDATING      验证部署结果
  2026-03-27 09:15:48  VALIDATING      →  RUNNING         部署验证通过
```

---

### 5. dtk doctor

```bash
dtk doctor
```

**结果**：✅

```
检查环境依赖...

环境依赖：
  ✓ Go               1.25.7
  ✓ Docker           28.5.2 (running)
  ✓ kubectl          v1.35.0
  ✓ helm             v4.0.1
  ✓ K8s 集群         https://127.0.0.1:26443 (reachable)

项目配置：
  ✓ project.env      存在
  ✓ REGISTRY_PREFIX  qingchun22

✅ 环境检查通过，可以开始使用 dtk
```

---

### 6. dtk deploy（v0.2.0）

```bash
sed -i '' 's/VERSION=v0.1.0/VERSION=v0.2.0/' configs/project.env
dtk deploy
```

**结果**：✅，REVISION: 2

---

### 7. dtk rollback

```bash
dtk rollback
```

**结果**：✅，回滚到 REVISION: 1

```
当前状态: RUNNING，发起回滚...
✅ 回滚完成
```

**本步骤修复的 Bug**：

| Bug | 根因 | 修复 |
|---|---|---|
| `RUNNING → ROLLING_BACK` 非法转换 | 转换表缺失该路径 | validTransitions 补上 |
| `helm rollback` 报 `release has no 0 version` | revision 传 0，helm 不接受 | 改为查 history 取 latest-1 |
| `--namespace` 重复传 | runner.go 拼参数时重复 | 去掉重复 |
| rollback 失败转 CLEANING | 错误处理逻辑错误 | 失败时保持 RUNNING |

---

### 8. dtk release --deploy

```bash
git add . && git commit -m "chore: update to v0.2.0"
dtk release --version v0.3.1 --push=false --deploy
```

**结果**：✅，REVISION: 4

```
✅ 已更新 configs/project.env → VERSION=v0.3.1
✅ 已提交：chore: release v0.3.1
✅ 已打 tag：v0.3.1
🎉 发布完成：v0.3.1
🚀 触发部署...
✅ 部署完成，状态: RUNNING (version=v0.3.1)
```

---

### 9. dtk down

```bash
dtk down
```

**结果**：✅

```
⚠️  即将删除以下资源：
  namespace          : e2e
  ClusterRole        : e2e-controller
  ClusterRoleBinding : e2e-controller
  本地状态文件       : /Users/qc/.dtk/state/e2e/e2e.json

确认删除？(y/N): y
✓ ClusterRole e2e-controller 已删除
✓ ClusterRoleBinding e2e-controller 已删除
✓ namespace e2e 已删除
✓ 本地状态文件已删除

✅ e2e 已完全下线
```

验证：

```bash
kubectl get ns | grep e2e
# 无输出，namespace 已删除
```

---

## 汇总

| 步骤 | 命令 | 结果 |
|---|---|---|
| 1 | `dtk init` | ✅ |
| 2 | `dtk deploy` | ✅ |
| 3 | `dtk status` | ✅ |
| 4 | `dtk status --history` | ✅ |
| 5 | `dtk doctor` | ✅ |
| 6 | `dtk deploy`（版本升级） | ✅ |
| 7 | `dtk rollback` | ✅ |
| 8 | `dtk release --deploy` | ✅ |
| 9 | `dtk down` | ✅ |
| - | controller 自愈 | ⏭️ 跳过（待 P1） |

---

## 遗留问题

| # | 问题 | 优先级 |
|---|------|--------|
| 1 | `dtk status` Helm Status 字段为空（yaml 嵌套层级解析问题） | P1 |
| 2 | `dtk status` pod 名字前多一个空格（tabwriter 对齐问题） | P2 |
| 3 | controller 自愈流程未验证 | P1 |
