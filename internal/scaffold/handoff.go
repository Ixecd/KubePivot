package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func writeHandoffSkeleton(outputDir, name, module string, withFrontend bool) error {
	dir := filepath.Join(outputDir, "handoff")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建 handoff 目录失败: %w", err)
	}
	content := renderHandoff(name, module, withFrontend)
	if err := os.WriteFile(filepath.Join(dir, "HANDOFF.md"), []byte(content), 0644); err != nil {
		return fmt.Errorf("写入 HANDOFF.md 失败: %w", err)
	}
	return nil
}

func renderHandoff(name, module string, withFrontend bool) string {
	date := time.Now().Format("2006-01-02")
	var b strings.Builder

	b.WriteString("# 项目交接文档\n\n")
	b.WriteString("> 写给下一个 Claude\n")
	b.WriteString("> 日期：" + date + "\n")
	b.WriteString("> 作者：<!-- 填写作者 -->\n\n")
	b.WriteString("---\n\n")

	b.WriteString("## 写在前面\n\n")
	b.WriteString("<!-- 描述你的工作风格和偏好，Claude 会据此调整协作方式。例如：\n")
	b.WriteString("- 设计优先，代码其次。不要上来就写代码，先对齐设计再动手\n")
	b.WriteString("- 每完成一个里程碑：commit → tag → SNAPSHOT → 更新 TODO\n")
	b.WriteString("- 喜欢被推 back，不喜欢被一味认同\n")
	b.WriteString("- 代码风格偏好（日志库、包组织方式等）\n")
	b.WriteString("-->\n\n")
	b.WriteString("---\n\n")

	b.WriteString("## 一、项目概览\n\n")
	b.WriteString("**仓库**：" + module + "\n")
	b.WriteString("**当前版本**：<!-- vX.Y.Z，与 configs/project.env 中 VERSION 保持一致 -->\n")
	b.WriteString("**定位**：<!-- 一句话说清楚这个项目是什么、解决什么问题 -->\n\n")

	b.WriteString("**命令全览**：\n\n")
	b.WriteString(buildCommands(name))
	b.WriteString("\n\n")

	b.WriteString("**目录结构**：\n\n")
	b.WriteString(buildDirTree(name, withFrontend))
	b.WriteString("\n\n")
	b.WriteString("---\n\n")

	b.WriteString("## 二、架构设计\n\n")
	b.WriteString("<!-- 描述核心架构决策，重点说「为什么这么设计」而不是「是什么」。\n")
	b.WriteString("Claude 理解了设计意图，才不会给出破坏架构的建议。\n")
	b.WriteString("-->\n\n")
	b.WriteString("---\n\n")

	b.WriteString("## 三、当前状态\n\n")
	b.WriteString("<!-- 描述项目现在处于什么阶段，哪些功能已经可用，哪些在开发中。\n")
	b.WriteString("让 Claude 对「完成度」有准确认知，避免重复已完成的工作。\n")
	b.WriteString("-->\n\n")
	b.WriteString("---\n\n")

	b.WriteString("## 四、已知问题\n\n")
	b.WriteString("<!-- 列出已知但暂未修复的 Bug 或设计缺陷，注明优先级。\n")
	b.WriteString("格式建议：\n")
	b.WriteString("1. [P0] 问题描述 — 根因（如果已知）\n")
	b.WriteString("2. [P1] 问题描述\n")
	b.WriteString("-->\n\n")
	b.WriteString("---\n\n")

	b.WriteString("## 五、接下来要做的事\n\n")
	b.WriteString("<!-- 按优先级列出下一步计划。\n")
	b.WriteString("写得足够具体，避免「优化性能」这种模糊描述，\n")
	b.WriteString("要写「把 handler.go 按 auth/wallet/admin 拆分」。\n")
	b.WriteString("-->\n\n")
	b.WriteString("### P0（最优先）\n\n")
	b.WriteString("### P1\n\n")
	b.WriteString("---\n\n")

	b.WriteString("## 六、常用命令速查\n\n")
	b.WriteString(buildQuickRef(name, withFrontend))
	b.WriteString("\n\n")
	b.WriteString("---\n\n")

	b.WriteString("## 七、快照归档位置\n\n")
	b.WriteString("`snapshots/` 目录下按日期和里程碑命名，格式：\n\n")
	b.WriteString("```\n")
	b.WriteString("SNAPSHOT-{项目}-{日期}-{里程碑}.md\n")
	b.WriteString("```\n\n")
	b.WriteString("---\n\n")

	b.WriteString("## 八、致下一个 Claude\n\n")
	b.WriteString("<!-- 写给 Claude 的话。可以说说这个项目对你的意义，\n")
	b.WriteString("或者特别需要 Claude 注意的地方。这部分会影响 Claude 的协作态度。\n")
	b.WriteString("-->\n")

	return b.String()
}

func buildDirTree(name string, withFrontend bool) string {
	var b strings.Builder
	b.WriteString("```\n")
	b.WriteString(name + "/\n")
	b.WriteString("├── cmd/\n")
	b.WriteString("│   └── " + name + "/\n")
	b.WriteString("│       └── main.go          # 服务入口\n")
	b.WriteString("├── internal/                # 业务逻辑（按模块拆分）\n")
	b.WriteString("├── configs/\n")
	b.WriteString("│   ├── project.env          # 项目配置（VERSION、KUBE_*、ETCD_ENDPOINTS 等）\n")
	b.WriteString("│   ├── components.yaml      # kp 部署组件声明\n")
	b.WriteString("│   └── resources.yaml       # controller 监控的 K8s 资源列表\n")
	b.WriteString("├── deployments/\n")
	b.WriteString("│   └── " + name + "/        # 自包含 Helm chart\n")
	b.WriteString("│       ├── Chart.yaml\n")
	b.WriteString("│       ├── values.yaml\n")
	b.WriteString("│       └── templates/\n")
	b.WriteString("├── migrations/              # SQL 迁移文件（golang-migrate）\n")
	b.WriteString("├── snapshots/               # 里程碑快照归档\n")
	b.WriteString("├── handoff/\n")
	b.WriteString("│   └── HANDOFF.md           # 本文件\n")
	if withFrontend {
		b.WriteString("└── web/                     # 前端（React + Vite + Tailwind）\n")
		b.WriteString("    ├── src/\n")
		b.WriteString("    └── package.json\n")
	} else {
		b.WriteString("└── docs/                    # 设计文档\n")
	}
	b.WriteString("```")
	return b.String()
}

func buildCommands(name string) string {
	var b strings.Builder
	b.WriteString("```\n")
	b.WriteString("kp init       --name " + name + " --module <module> [--with-frontend]\n")
	b.WriteString("kp deploy     [--namespace] [--context] [--kubeconfig] [--dry-run]\n")
	b.WriteString("kp resume     # 从中断点恢复\n")
	b.WriteString("kp rollback   # 手动触发 helm rollback\n")
	b.WriteString("kp release    --version vX.Y.Z [--deploy]\n")
	b.WriteString("```")
	return b.String()
}

func buildQuickRef(name string, withFrontend bool) string {
	var b strings.Builder
	b.WriteString("```bash\n")
	b.WriteString("# 部署\n")
	b.WriteString("kp deploy\n\n")
	b.WriteString("# 查看 pods\n")
	b.WriteString("kubectl get pods -n " + name + "\n\n")
	b.WriteString("# 查看服务日志\n")
	b.WriteString("kubectl logs -n " + name + " deployment/" + name + "\n\n")
	b.WriteString("# port-forward 本地调试\n")
	b.WriteString("kubectl port-forward -n " + name + " deployment/" + name + " <local-port>:<container-port>\n\n")
	b.WriteString("# 查看 helm 历史\n")
	b.WriteString("helm history " + name + " -n " + name + "\n\n")
	b.WriteString("# 运行测试\n")
	b.WriteString("go test ./...\n")
	if withFrontend {
		b.WriteString("\n# 前端开发\n")
		b.WriteString("cd web && npm run dev\n")
	}
	b.WriteString("```")
	return b.String()
}