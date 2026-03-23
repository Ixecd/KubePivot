# SNAPSHOT — dev-toolkit

**里程碑**：高优先级错误处理全部完成
**日期**：2026-03-23

---

## 本次完成（4 项）

### `dtk deploy` 前置检查
- 新增 `cmd/dtk/preflight.go`
- `runDeploy` 最顶部调用 `checkDeps(deployDeps)`
- 检测 docker / kubectl / helm，全部缺失一次列完，附安装链接
- 单元测试 4 个 case（全有 / 全缺 / 部分缺 / 空列表）

```
❌ 以下工具未安装或不在 PATH 中：

  docker      https://docs.docker.com/engine/install/
  helm        https://helm.sh/docs/intro/install/

缺少必要工具，请安装后重试
```

### `dtk init` 半成品清理
- `scaffold.go` `InitProject` 改为具名返回 `(err error)`
- `ensureOutputDir` 调用从 `:=` 改为 `=`（不遮蔽具名返回变量）
- `defer` 在失败时自动 `os.RemoveAll` 本次新建的目录
- `--force` 时目录已存在，失败不清理（保护用户已有文件）
- 单元测试 3 个 case（新建清理 / force 不清理 / 预存在不清理）

### `LoadComponents` 换 `gopkg.in/yaml.v3`
- 删除 80 行手写 parser，换 20 行 yaml.v3 struct unmarshal
- `image: ""` 引号坑彻底消失，yaml.v3 原生处理
- `image:` 缺字段 → zero value 空字符串，行为一致

### `deploy.mk` 失败上下文
- `docker build` / `docker push` / `helm upgrade` / `kubectl set image` / `rollout status` 失败时统一打印：
  - `context` / `namespace` / `image`
  - 针对性 hint（helm → `helm status`，kubectl → `kubectl describe`）

---

## 文件变动清单

```
新增：
- cmd/dtk/preflight.go
- cmd/dtk/preflight_test.go
- internal/scaffold/init_test.go

修改：
- cmd/dtk/main.go               ← runDeploy 加 checkDeps
- internal/scaffold/scaffold.go ← 具名返回 + defer 清理
- internal/ai/ai.go             ← 换 yaml.v3
- scripts/make-rules/deploy.mk  ← 失败上下文输出
- go.mod / go.sum               ← 新增 gopkg.in/yaml.v3
```

---

## 历史快照

```
snapshots/
├── SNAPSHOT-dtk-2026-03-18-scaffold-complete.md
├── SNAPSHOT-dtk-2026-03-18.md
├── SNAPSHOT-dtk-2026-03-19-1.md
├── SNAPSHOT-dtk-2026-03-19-A.md
├── SNAPSHOT-dtk-2026-03-19-B.md
├── SNAPSHOT-dtk-2026-03-19.md
├── SNAPSHOT-dtk-2026-03-20-deploy-e2e(!!!).md
├── SNAPSHOT-dtk-2026-03-20-frontend-skeleton-generic.md
├── SNAPSHOT-dtk-2026-03-20-monitoring.md
├── SNAPSHOT-dtk-2026-03-20-with-frontend.md
├── SNAPSHOT-dtk-2026-03-23-slog.md
└── SNAPSHOT-dtk-2026-03-23-error-handling.md   ← 本次
```
