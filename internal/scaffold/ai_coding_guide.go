package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writeAICodingGuide 生成 handoff/AI-CODING-GUIDE.md
// 帮助 AI 在 kp 框架内正确填充业务逻辑，不破坏脚手架约束
func writeAICodingGuide(outputDir, name, module string) error {
	dir := filepath.Join(outputDir, "handoff")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建 handoff 目录失败: %w", err)
	}
	content := renderAICodingGuide(name, module)
	path := filepath.Join(dir, "AI-CODING-GUIDE.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("写入 AI-CODING-GUIDE.md 失败: %w", err)
	}
	return nil
}

func renderAICodingGuide(name, module string) string {
	var b strings.Builder

	b.WriteString("# AI 编码指南\n\n")
	b.WriteString("> 本文档写给协助开发的 AI。\n")
	b.WriteString("> 项目骨架由 `kp init` 生成，请严格在框架内填充业务逻辑，不要重构脚手架结构。\n\n")
	b.WriteString("---\n\n")

	// 一、项目框架总览
	b.WriteString("## 一、项目框架总览\n\n")
	b.WriteString("```\n")
	b.WriteString(name + "/\n")
	b.WriteString("├── cmd/" + name + "/\n")
	b.WriteString("│   └── main.go              # 服务入口，只做初始化和启动，不写业务逻辑\n")
	b.WriteString("├── internal/\n")
	b.WriteString("│   ├── api/\n")
	b.WriteString("│   │   ├── handler.go       # HTTP handler，业务入口\n")
	b.WriteString("│   │   └── server.go        # 路由注册，HTTP 服务启动\n")
	b.WriteString("│   ├── auth/\n")
	b.WriteString("│   │   ├── auth.go          # JWT 签发/验证\n")
	b.WriteString("│   │   ├── middleware.go    # 认证中间件\n")
	b.WriteString("│   │   └── rbac.go          # 权限控制\n")
	b.WriteString("│   ├── db/\n")
	b.WriteString("│   │   ├── connect.go       # 数据库连接初始化\n")
	b.WriteString("│   │   └── migrations/      # embed.FS 加载的 SQL 文件目录\n")
	b.WriteString("│   ├── metrics/\n")
	b.WriteString("│   │   └── metrics.go       # Prometheus 指标定义\n")
	b.WriteString("│   └── pkg/\n")
	b.WriteString("│       └── code/            # 业务错误码\n")
	b.WriteString("├── configs/\n")
	b.WriteString("│   ├── project.env          # 部署配置（VERSION、KUBE_* 等）\n")
	b.WriteString("│   ├── components.yaml      # kp AI 规划依据\n")
	b.WriteString("│   ├── resources.yaml       # controller 监控的 K8s 资源\n")
	b.WriteString("│   └── system.yaml          # KubePivot 控制器配置（etcd/调度/缓存参数）\n")
	b.WriteString("├── deployments/" + name + "/  # Helm chart，含 postgres、etcd、业务服务\n")
	b.WriteString("├── build/docker/" + name + "/# Dockerfile + build.sh\n")
	b.WriteString("├── migrations/              # SQL 迁移文件（golang-migrate 格式）\n")
	b.WriteString("└── handoff/\n")
	b.WriteString("    ├── HANDOFF.md           # 项目上下文，写给下一个 AI\n")
	b.WriteString("    └── AI-CODING-GUIDE.md   # 本文件\n")
	b.WriteString("```\n\n")
	b.WriteString("---\n\n")

	// 二、业务逻辑该填在哪里
	b.WriteString("## 二、业务逻辑该填在哪里\n\n")

	b.WriteString("### API Handler\n\n")
	b.WriteString("`internal/api/handler.go` 是业务入口，新增接口在这里加 handler 函数：\n\n")
	b.WriteString("```go\n")
	b.WriteString("// 示例：新增一个查询接口\n")
	b.WriteString("func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {\n")
	b.WriteString("    // 1. 解析请求参数\n")
	b.WriteString("    // 2. 调用业务逻辑（建议抽到 internal/service/ 或直接写在这里）\n")
	b.WriteString("    // 3. 统一用 h.respond(w, code, data) 返回\n")
	b.WriteString("}\n")
	b.WriteString("```\n\n")
	b.WriteString("路由注册在 `internal/api/server.go`，新增接口后在这里加一行。\n\n")

	b.WriteString("### 数据库操作\n\n")
	b.WriteString("`internal/db/` 负责连接管理，具体查询建议按业务模块拆包，例如：\n\n")
	b.WriteString("```\n")
	b.WriteString("internal/\n")
	b.WriteString("└── db/\n")
	b.WriteString("    ├── connect.go           # 已有，不动\n")
	b.WriteString("    ├── migrations/          # 已有，不动\n")
	b.WriteString("    ├── user.go              # 新增：用户相关查询\n")
	b.WriteString("    └── wallet.go            # 新增：钱包相关查询\n")
	b.WriteString("```\n\n")

	b.WriteString("### 数据库迁移\n\n")
	b.WriteString("`migrations/` 目录存放 SQL 文件，**严格遵守 golang-migrate 命名格式**：\n\n")
	b.WriteString("```\n")
	b.WriteString("migrations/\n")
	b.WriteString("├── 000001_init_schema.up.sql\n")
	b.WriteString("├── 000001_init_schema.down.sql\n")
	b.WriteString("├── 000002_add_users.up.sql\n")
	b.WriteString("└── 000002_add_users.down.sql\n")
	b.WriteString("```\n\n")
	b.WriteString("迁移在服务启动时自动执行，不需要手动触发。\n\n")

	b.WriteString("### 扩展业务模块\n\n")
	b.WriteString("复杂业务建议在 `internal/` 下按模块新增包，例如：\n\n")
	b.WriteString("```\n")
	b.WriteString("internal/\n")
	b.WriteString("├── service/                 # 业务逻辑层（可选）\n")
	b.WriteString("│   ├── user.go\n")
	b.WriteString("│   └── wallet.go\n")
	b.WriteString("└── model/                   # 数据模型（可选）\n")
	b.WriteString("    └── user.go\n")
	b.WriteString("```\n\n")
	b.WriteString("包名和目录名保持一致，不要在 `internal/` 外新增业务代码。\n\n")
	b.WriteString("---\n\n")

	// 三、不要动的地方
	b.WriteString("## 三、不要动的地方\n\n")
	b.WriteString("以下文件和目录由 kp 框架管理，**不要修改**，否则会破坏部署流程：\n\n")
	b.WriteString("| 文件/目录 | 原因 |\n")
	b.WriteString("|---|---|\n")
	b.WriteString("| `configs/project.env` | kp deploy 读取，手动改会导致部署参数错乱 |\n")
	b.WriteString("| `configs/components.yaml` | AI 规划依据，改了会影响资源估算 |\n")
	b.WriteString("| `configs/resources.yaml` | controller 监控配置，改了需要重新 deploy |\n")
	b.WriteString("| `configs/system.yaml` | KubePivot 控制器参数（可通过 env 覆盖，文件不要乱改）|\n")
	b.WriteString("| `deployments/` 目录结构 | Helm chart 骨架，新增配置只改 `values.yaml` |\n")
	b.WriteString("| `scripts/make-rules/deploy.mk` | kp 部署流程，不要改 helm upgrade 参数 |\n")
	b.WriteString("| `internal/db/migrations/` | embed.FS 挂载点，目录不能改名 |\n")
	b.WriteString("| `cmd/" + name + "/main.go` | 只做初始化，业务逻辑不要写在这里 |\n\n")
	b.WriteString("---\n\n")

	// 四、加新功能的标准姿势
	b.WriteString("## 四、加新功能的标准姿势\n\n")

	b.WriteString("### 加一个新 API 接口\n\n")
	b.WriteString("```\n")
	b.WriteString("1. internal/api/handler.go   → 加 handler 函数\n")
	b.WriteString("2. internal/api/server.go    → 注册路由\n")
	b.WriteString("3. internal/db/{module}.go   → 加数据库操作（如需要）\n")
	b.WriteString("4. migrations/               → 加迁移文件（如需要改表结构）\n")
	b.WriteString("```\n\n")

	b.WriteString("### 加一张新数据库表\n\n")
	b.WriteString("```\n")
	b.WriteString("1. 新建 migrations/00000N_add_{table}.up.sql   → 建表 SQL\n")
	b.WriteString("2. 新建 migrations/00000N_add_{table}.down.sql → 回滚 SQL\n")
	b.WriteString("3. internal/db/{module}.go                     → 加查询函数\n")
	b.WriteString("```\n\n")
	b.WriteString("序号 N 必须连续递增，不能跳号。\n\n")

	b.WriteString("### 加环境变量\n\n")
	b.WriteString("```\n")
	b.WriteString("1. deployments/" + name + "/values.yaml  → 在 env 里加\n")
	b.WriteString("2. configs/project.env               → 本地开发时加（不提交敏感值）\n")
	b.WriteString("```\n\n")
	b.WriteString("不要硬编码配置值，所有配置通过环境变量注入。\n\n")

	b.WriteString("### 加 Prometheus 指标\n\n")
	b.WriteString("```\n")
	b.WriteString("1. internal/metrics/metrics.go  → 定义新指标\n")
	b.WriteString("2. internal/api/handler.go      → 在对应 handler 里埋点\n")
	b.WriteString("```\n\n")
	b.WriteString("---\n\n")

	// 五、代码风格约束
	b.WriteString("## 五、代码风格约束\n\n")
	b.WriteString("**日志**：统一用 `log/slog`，不用 `log` 包。结构化输出，key 用小写英文：\n\n")
	b.WriteString("```go\n")
	b.WriteString("// ✓ 正确\n")
	b.WriteString("slog.Info(\"用户登录\", \"user_id\", uid, \"ip\", ip)\n")
	b.WriteString("slog.Error(\"数据库查询失败\", \"err\", err, \"table\", \"users\")\n\n")
	b.WriteString("// ✗ 不要用\n")
	b.WriteString("log.Printf(\"user %d login\", uid)\n")
	b.WriteString("fmt.Println(\"error:\", err)\n")
	b.WriteString("```\n\n")

	b.WriteString("**错误处理**：错误向上传递，加上下文信息，不要在中间层吞掉：\n\n")
	b.WriteString("```go\n")
	b.WriteString("// ✓ 正确\n")
	b.WriteString("if err != nil {\n")
	b.WriteString("    return fmt.Errorf(\"查询用户失败: %w\", err)\n")
	b.WriteString("}\n\n")
	b.WriteString("// ✗ 不要这样\n")
	b.WriteString("if err != nil {\n")
	b.WriteString("    log.Println(err) // 吞掉了\n")
	b.WriteString("    return nil\n")
	b.WriteString("}\n")
	b.WriteString("```\n\n")

	b.WriteString("**包组织**：严格分包，不要跨层调用。依赖方向：\n\n")
	b.WriteString("```\n")
	b.WriteString("api → service → db\n")
	b.WriteString("api → auth\n")
	b.WriteString("api → metrics\n")
	b.WriteString("```\n\n")
	b.WriteString("`db` 包不能 import `api` 包，`auth` 包不能 import `db` 包，以此类推。\n\n")

	b.WriteString("**模块路径**：项目模块路径是 `" + module + "`，import 内部包时用完整路径：\n\n")
	b.WriteString("```go\n")
	b.WriteString("import (\n")
	b.WriteString("    \"" + module + "/internal/db\"\n")
	b.WriteString("    \"" + module + "/internal/auth\"\n")
	b.WriteString(")\n")
	b.WriteString("```\n\n")
	b.WriteString("---\n\n")

	// 六、部署相关
	b.WriteString("## 六、改了代码要同步改哪里\n\n")
	b.WriteString("| 改动类型 | 需要同步的地方 |\n")
	b.WriteString("|---|---|\n")
	b.WriteString("| 新增环境变量 | `deployments/" + name + "/values.yaml` 的 `env` 字段 |\n")
	b.WriteString("| 改了服务端口 | `values.yaml` 的 `service.port` + liveness/readiness probe 端口 |\n")
	b.WriteString("| 加了新的 K8s 资源需要监控 | `configs/resources.yaml` 加一行，然后 `kp deploy` |\n")
	b.WriteString("| 改了数据库表结构 | 新增迁移文件，不要修改已有迁移文件 |\n")
	b.WriteString("| 要发布新版本 | 运行 `kp release --version vX.Y.Z`，不要手动改 VERSION |\n")
	b.WriteString("| 同步框架文件 | `kp sync`，KubePivot 升级后同步 Makefile/scripts/configs |\n")
	b.WriteString("| 性能基准 | `kp bench all`，跑全量 KVCache/调度/内存基准 |\n\n")

	b.WriteString("---\n\n")
	b.WriteString("> 遇到不确定的地方，先看 `handoff/HANDOFF.md` 了解项目背景，再动手。\n")
	b.WriteString("> 不确定该不该改某个文件，默认答案是：不改，先问。\n")

	return b.String()
}
