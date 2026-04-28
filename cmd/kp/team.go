// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ixecd/kubepivot/internal/rbac"
	"gopkg.in/yaml.v3"
)

// ════════════════════════════════════════════════════════════════════════════
// kp team — v2.8 B.4 多团队 RBAC 管理 CLI
//
// 8 个子命令 (Q-B4.1 完整覆盖管理员闭环):
//   kp team list                        列出所有 team
//   kp team show <name>                 显示 team 详情
//   kp team add <name> [--flags]        新增 team (双模式 Q-B4.3)
//   kp team remove <name>               删除 team (二次确认)
//   kp team member add <team> <member>  增加 member
//   kp team member remove <team> <member> 删除 member
//   kp team check <user> <ns> <perm>    校验权限 (详细决策路径 Q-B4.5)
//   kp team validate                    校验 teams.yaml 语法
//
// 设计哲学:
//   - 默认项目级 teams.yaml (Q-B4.2)
//   - --user 标志切换到 ~/.kp/teams.yaml
//   - 修改后告警 yaml 注释/排序丢失 (Q-B4.4)
//   - kp team * 不走 mustCheck (Q-B4.6 文件权限即治理边界)
// ════════════════════════════════════════════════════════════════════════════

// runTeam kp team 子命令分发入口.
//
// main.go 调用: case "team": runTeam(args[1:])
func runTeam(args []string) {
	if len(args) == 0 {
		printTeamUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		runTeamList(args[1:])
	case "show":
		runTeamShow(args[1:])
	case "add":
		runTeamAdd(args[1:])
	case "remove":
		runTeamRemove(args[1:])
	case "member":
		runTeamMember(args[1:])
	case "check":
		runTeamCheck(args[1:])
	case "validate":
		runTeamValidate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n", args[0])
		printTeamUsage()
		os.Exit(1)
	}
}

func printTeamUsage() {
	fmt.Println("用法: kp team <子命令> [flags]")
	fmt.Println()
	fmt.Println("查询：")
	fmt.Println("  kp team list                                 列出所有 team")
	fmt.Println("  kp team show <name>                          显示 team 详情")
	fmt.Println("  kp team check <user> <ns> <perm>             校验用户权限 (详细决策路径)")
	fmt.Println("  kp team validate                             校验 teams.yaml 语法")
	fmt.Println()
	fmt.Println("修改 (默认项目级 configs/teams.yaml, 可加 --user 切换 ~/.kp/teams.yaml)：")
	fmt.Println("  kp team add <name> [--members ... --namespaces ... --permissions ...]")
	fmt.Println("                                               新增 team (无 flag 走交互式)")
	fmt.Println("  kp team remove <name>                        删除 team (二次确认)")
	fmt.Println("  kp team member add <team> <member>           增加成员 (email 或 group:xxx)")
	fmt.Println("  kp team member remove <team> <member>        删除成员")
	fmt.Println()
	fmt.Println("通用 flags：")
	fmt.Println("  --user                                       操作用户级 ~/.kp/teams.yaml")
	fmt.Println("  --json                                       JSON 输出 (CI 友好)")
}

// ════════════════════════════════════════════════════════════════════════════
// teams.yaml 加载/保存 helper
// ════════════════════════════════════════════════════════════════════════════

// teamsFilePath 按 Q-B4.2 决定操作目标 yaml.
//
//	useUser=false  → <projectRoot>/configs/teams.yaml
//	useUser=true   → ~/.kp/teams.yaml
func teamsFilePath(useUser bool) (string, error) {
	if useUser {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("获取 home 目录失败: %w", err)
		}
		return filepath.Join(home, ".kp", "teams.yaml"), nil
	}
	root, err := projectRoot()
	if err != nil {
		return "", fmt.Errorf("找不到项目根 (用 --user 操作用户级配置): %w", err)
	}
	return filepath.Join(root, "configs", "teams.yaml"), nil
}

// loadTeamConfig 读 teams.yaml 解析为 TeamConfig.
//
// 文件不存在返回 empty config (允许从零开始 add).
func loadTeamConfig(path string) (*rbac.TeamConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &rbac.TeamConfig{Teams: []rbac.Team{}}, nil
		}
		return nil, fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	var cfg rbac.TeamConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	if cfg.Teams == nil {
		cfg.Teams = []rbac.Team{}
	}
	return &cfg, nil
}

// saveTeamConfig 写 teams.yaml.
//
// Q-B4.4=C: 写入前打印告警 (注释/排序可能丢失).
func saveTeamConfig(path string, cfg *rbac.TeamConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建父目录失败: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("序列化 yaml 失败: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", path, err)
	}
	return nil
}

// warnYAMLOverwrite Q-B4.4=C: 修改前告警.
//
// 文件已存在时打印 warn, 让用户知道注释/排序可能变化.
func warnYAMLOverwrite(path string) {
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintln(os.Stderr,
			"⚠️  注意: 即将重写 yaml 文件, 既有注释/字段排序可能丢失")
		fmt.Fprintf(os.Stderr, "   %s\n", path)
	}
}

// findTeamIndex 在 teams 列表中找指定 name 的索引, -1 表示不存在.
func findTeamIndex(teams []rbac.Team, name string) int {
	for i, t := range teams {
		if t.Name == name {
			return i
		}
	}
	return -1
}

// ════════════════════════════════════════════════════════════════════════════
// kp team list - 列出所有 team
// ════════════════════════════════════════════════════════════════════════════

func runTeamList(args []string) {
	flags := flag.NewFlagSet("team list", flag.ExitOnError)
	useUser := flags.Bool("user", false, "操作用户级 ~/.kp/teams.yaml")
	asJSON := flags.Bool("json", false, "JSON 输出")
	flags.Parse(args)

	path, err := teamsFilePath(*useUser)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg, err := loadTeamConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if *asJSON {
		out, _ := json.MarshalIndent(cfg, "", "  ")
		fmt.Println(string(out))
		return
	}

	if len(cfg.Teams) == 0 {
		fmt.Printf("📋 %s 没有任何 team\n", path)
		fmt.Println("   用 kp team add <name> 添加")
		return
	}

	fmt.Printf("📋 %s (%d teams):\n\n", path, len(cfg.Teams))
	fmt.Printf("  %-20s  %-10s  %-25s  %s\n", "NAME", "MEMBERS", "NAMESPACES", "PERMISSIONS")
	fmt.Printf("  %-20s  %-10s  %-25s  %s\n",
		"────", "───────", "──────────", "───────────")
	for _, t := range cfg.Teams {
		nss := strings.Join(t.Namespaces, ",")
		if len(nss) > 24 {
			nss = nss[:21] + "..."
		}
		perms := joinPerms(t.Permissions)
		fmt.Printf("  %-20s  %-10d  %-25s  %s\n",
			t.Name, len(t.Members), nss, perms)
	}
}

// joinPerms 把 permissions 合成逗号字符串 (Team.Permissions 是 []string).
func joinPerms(perms []string) string {
	return strings.Join(perms, ",")
}

// ════════════════════════════════════════════════════════════════════════════
// kp team show - 显示 team 详情
// ════════════════════════════════════════════════════════════════════════════

func runTeamShow(args []string) {
	flags := flag.NewFlagSet("team show", flag.ExitOnError)
	useUser := flags.Bool("user", false, "操作用户级 ~/.kp/teams.yaml")
	asJSON := flags.Bool("json", false, "JSON 输出")
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "用法: kp team show <name> [--user] [--json]")
		os.Exit(1)
	}
	name := args[0]
	flags.Parse(args[1:])

	path, err := teamsFilePath(*useUser)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg, err := loadTeamConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	idx := findTeamIndex(cfg.Teams, name)
	if idx < 0 {
		fmt.Fprintf(os.Stderr, "❌ team %q 不存在 (路径 %s)\n", name, path)
		os.Exit(1)
	}
	t := cfg.Teams[idx]

	if *asJSON {
		out, _ := json.MarshalIndent(t, "", "  ")
		fmt.Println(string(out))
		return
	}

	fmt.Printf("📋 Team: %s\n\n", t.Name)
	fmt.Println("  Members:")
	if len(t.Members) == 0 {
		fmt.Println("    (none)")
	} else {
		for _, m := range t.Members {
			fmt.Printf("    - %s\n", m)
		}
	}

	fmt.Println("  Namespaces:")
	if len(t.Namespaces) == 0 {
		fmt.Println("    (none)")
	} else {
		for _, ns := range t.Namespaces {
			fmt.Printf("    - %s\n", ns)
		}
	}

	fmt.Println("  Permissions:")
	if len(t.Permissions) == 0 {
		fmt.Println("    (none)")
	} else {
		for _, p := range t.Permissions {
			fmt.Printf("    - %s\n", p)
		}
	}

	if len(t.Excluded) > 0 {
		fmt.Println("  Excluded (黑名单, 优先级最高):")
		for _, p := range t.Excluded {
			fmt.Printf("    - %s\n", p)
		}
	}

	fmt.Printf("\n  Source: %s\n", path)
}

// ════════════════════════════════════════════════════════════════════════════
// kp team add - 新增 team (Q-B4.3 双模式)
// ════════════════════════════════════════════════════════════════════════════

func runTeamAdd(args []string) {
	flags := flag.NewFlagSet("team add", flag.ExitOnError)
	useUser := flags.Bool("user", false, "操作用户级 ~/.kp/teams.yaml")
	membersFlag := flags.String("members", "", "成员列表, 逗号分隔 (email 或 group:xxx)")
	namespacesFlag := flags.String("namespaces", "", "namespace 列表, 逗号分隔 (shell glob)")
	permsFlag := flags.String("permissions", "", "权限列表, 逗号分隔")
	excludedFlag := flags.String("excluded", "", "黑名单权限, 逗号分隔 (优先级最高)")
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "用法: kp team add <name> [flags]")
		os.Exit(1)
	}
	name := args[0]
	flags.Parse(args[1:])

	path, err := teamsFilePath(*useUser)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg, err := loadTeamConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if findTeamIndex(cfg.Teams, name) >= 0 {
		fmt.Fprintf(os.Stderr, "❌ team %q 已存在\n", name)
		os.Exit(1)
	}

	// Q-B4.3=C: 双模式. 任一关键 flag 为空 → 走交互
	hasFlags := *membersFlag != "" || *namespacesFlag != "" || *permsFlag != ""

	var team rbac.Team
	team.Name = name

	if hasFlags {
		// CLI 模式
		team.Members = splitCSV(*membersFlag)
		team.Namespaces = splitCSV(*namespacesFlag)
		team.Permissions = splitCSV(*permsFlag)
		team.Excluded = splitCSV(*excludedFlag)
	} else {
		// 交互模式
		team = interactiveAddTeam(name)
	}

	// 验证
	if len(team.Members) == 0 {
		fmt.Fprintln(os.Stderr, "❌ team 必须至少有一个 member")
		os.Exit(1)
	}
	if len(team.Permissions) == 0 {
		fmt.Fprintln(os.Stderr, "❌ team 必须至少有一个 permission")
		os.Exit(1)
	}

	cfg.Teams = append(cfg.Teams, team)

	warnYAMLOverwrite(path)
	if err := saveTeamConfig(path, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("✅ team %q 已添加到 %s\n", name, path)
}

// interactiveAddTeam 交互式收集 team 字段.
func interactiveAddTeam(name string) rbac.Team {
	reader := bufio.NewReader(os.Stdin)

	fmt.Printf("📝 创建 team %q (按 Ctrl+C 取消)\n\n", name)

	members := promptCSV(reader, "Members (email 或 group:xxx, 逗号分隔)")
	namespaces := promptCSV(reader, "Namespaces (shell glob 如 kp-prod-*, 逗号分隔)")
	perms := promptCSV(reader, "Permissions (deploy/sandbox/rollback/status/controller-install/controller-uninstall/*, 逗号分隔)")
	excluded := promptCSV(reader, "Excluded (黑名单, 可选, 直接回车跳过)")

	return rbac.Team{
		Name:        name,
		Members:     members,
		Namespaces:  namespaces,
		Permissions: perms,
		Excluded:    excluded,
	}
}

// promptCSV 提示输入逗号分隔列表, 返回 trim 后的 slice.
func promptCSV(reader *bufio.Reader, label string) []string {
	fmt.Printf("  %s: ", label)
	line, _ := reader.ReadString('\n')
	return splitCSV(strings.TrimSpace(line))
}

// splitCSV 解析逗号分隔字符串, 跳过空字符串.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// parsePermissions 暂留, 但当前直接传 []string 即可
// (Team.Permissions / Team.Excluded yaml schema 是 []string, 不需要转换)

// ════════════════════════════════════════════════════════════════════════════
// kp team remove - 删除 team (二次确认)
// ════════════════════════════════════════════════════════════════════════════

func runTeamRemove(args []string) {
	flags := flag.NewFlagSet("team remove", flag.ExitOnError)
	useUser := flags.Bool("user", false, "操作用户级 ~/.kp/teams.yaml")
	force := flags.Bool("force", false, "跳过二次确认")
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "用法: kp team remove <name> [--force]")
		os.Exit(1)
	}
	name := args[0]
	flags.Parse(args[1:])

	path, err := teamsFilePath(*useUser)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg, err := loadTeamConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	idx := findTeamIndex(cfg.Teams, name)
	if idx < 0 {
		fmt.Fprintf(os.Stderr, "❌ team %q 不存在\n", name)
		os.Exit(1)
	}

	if !*force {
		fmt.Printf("⚠️  即将删除 team %q (含 %d 成员)\n",
			name, len(cfg.Teams[idx].Members))
		fmt.Printf("确认删除？(y/N): ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(input)) != "y" {
			fmt.Println("已取消")
			return
		}
	}

	cfg.Teams = append(cfg.Teams[:idx], cfg.Teams[idx+1:]...)

	warnYAMLOverwrite(path)
	if err := saveTeamConfig(path, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("✅ team %q 已删除\n", name)
}

// ════════════════════════════════════════════════════════════════════════════
// kp team member add/remove - 成员管理
// ════════════════════════════════════════════════════════════════════════════

func runTeamMember(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "用法: kp team member <add|remove> <team> <member>")
		os.Exit(1)
	}
	switch args[0] {
	case "add":
		runTeamMemberAdd(args[1:])
	case "remove":
		runTeamMemberRemove(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "未知 member 子命令: %s\n", args[0])
		os.Exit(1)
	}
}

func runTeamMemberAdd(args []string) {
	flags := flag.NewFlagSet("team member add", flag.ExitOnError)
	useUser := flags.Bool("user", false, "操作用户级 ~/.kp/teams.yaml")
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: kp team member add <team> <member>")
		fmt.Fprintln(os.Stderr, "  member 格式: email (如 alice@x.com) 或 group:xxx")
		os.Exit(1)
	}
	teamName := args[0]
	member := args[1]
	flags.Parse(args[2:])

	path, err := teamsFilePath(*useUser)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg, err := loadTeamConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	idx := findTeamIndex(cfg.Teams, teamName)
	if idx < 0 {
		fmt.Fprintf(os.Stderr, "❌ team %q 不存在\n", teamName)
		os.Exit(1)
	}

	// 重复检测
	for _, m := range cfg.Teams[idx].Members {
		if m == member {
			fmt.Fprintf(os.Stderr, "❌ %s 已是 team %q 成员\n", member, teamName)
			os.Exit(1)
		}
	}

	cfg.Teams[idx].Members = append(cfg.Teams[idx].Members, member)

	warnYAMLOverwrite(path)
	if err := saveTeamConfig(path, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("✅ %s 已加入 team %q\n", member, teamName)
}

func runTeamMemberRemove(args []string) {
	flags := flag.NewFlagSet("team member remove", flag.ExitOnError)
	useUser := flags.Bool("user", false, "操作用户级 ~/.kp/teams.yaml")
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: kp team member remove <team> <member>")
		os.Exit(1)
	}
	teamName := args[0]
	member := args[1]
	flags.Parse(args[2:])

	path, err := teamsFilePath(*useUser)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg, err := loadTeamConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	idx := findTeamIndex(cfg.Teams, teamName)
	if idx < 0 {
		fmt.Fprintf(os.Stderr, "❌ team %q 不存在\n", teamName)
		os.Exit(1)
	}

	memberIdx := -1
	for i, m := range cfg.Teams[idx].Members {
		if m == member {
			memberIdx = i
			break
		}
	}
	if memberIdx < 0 {
		fmt.Fprintf(os.Stderr, "❌ %s 不是 team %q 成员\n", member, teamName)
		os.Exit(1)
	}

	cfg.Teams[idx].Members = append(
		cfg.Teams[idx].Members[:memberIdx],
		cfg.Teams[idx].Members[memberIdx+1:]...)

	warnYAMLOverwrite(path)
	if err := saveTeamConfig(path, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("✅ %s 已从 team %q 移除\n", member, teamName)
}

// ════════════════════════════════════════════════════════════════════════════
// kp team check - 校验权限 (Q-B4.5 详细决策路径)
// ════════════════════════════════════════════════════════════════════════════

func runTeamCheck(args []string) {
	flags := flag.NewFlagSet("team check", flag.ExitOnError)
	useUser := flags.Bool("user", false, "操作用户级 ~/.kp/teams.yaml")
	asJSON := flags.Bool("json", false, "JSON 输出")
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "用法: kp team check <user> <namespace> <permission>")
		fmt.Fprintln(os.Stderr, "示例: kp team check alice@example.com kp-prod deploy")
		os.Exit(1)
	}
	user := args[0]
	namespace := args[1]
	perm := rbac.Permission(args[2])
	flags.Parse(args[3:])

	path, err := teamsFilePath(*useUser)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	checker, err := rbac.NewFileBasedChecker(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "构造 checker 失败:", err)
		os.Exit(1)
	}

	uctx := &rbac.UserContext{Email: user}
	checkErr := checker.Check(context.Background(), uctx, namespace, perm)

	if *asJSON {
		out := map[string]interface{}{
			"user":       user,
			"namespace":  namespace,
			"permission": string(perm),
			"allowed":    checkErr == nil,
		}
		if checkErr != nil {
			out["error"] = checkErr.Error()
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(data))
		return
	}

	// Q-B4.5=B 详细决策路径输出
	fmt.Printf("🔍 权限校验\n\n")
	fmt.Printf("  User:        %s\n", user)
	fmt.Printf("  Namespace:   %s\n", namespace)
	fmt.Printf("  Permission:  %s\n\n", perm)

	if checkErr == nil {
		fmt.Printf("✅ ALLOWED\n")
		// 详细解释 (查找命中的 team)
		explainAllowedDecision(path, user, namespace, perm)
	} else {
		fmt.Printf("❌ DENIED: %s\n", checkErr.Error())
	}
}

// explainAllowedDecision Q-B4.5=B: 解释为什么被允许 (命中哪个 team).
//
// 注: 当前 rbac 包没暴露"命中 team"信息,
// 这里通过加载 teams.yaml 自己分析展示
func explainAllowedDecision(path, user, namespace string, perm rbac.Permission) {
	cfg, err := loadTeamConfig(path)
	if err != nil {
		return
	}

	for _, t := range cfg.Teams {
		// 检查 user 匹配
		userMatch := false
		matchType := ""
		for _, m := range t.Members {
			if m == user {
				userMatch = true
				matchType = "email"
				break
			}
			if strings.HasPrefix(m, "group:") {
				// group 匹配本期不展示 (UserContext.Groups 调用方未填)
				continue
			}
		}
		if !userMatch {
			continue
		}

		// 检查 namespace 匹配
		nsMatch := false
		nsMatchHow := ""
		for _, p := range t.Namespaces {
			if matchNamespacePattern(p, namespace) {
				nsMatch = true
				nsMatchHow = p
				break
			}
		}
		if !nsMatch {
			continue
		}

		// 检查 permission 命中
		hasPerm := false
		for _, p := range t.Permissions {
			if p == string(perm) || p == string(rbac.PermAll) {
				hasPerm = true
				break
			}
		}
		if !hasPerm {
			continue
		}

		// 完整命中
		fmt.Printf("\n  Decision Path:\n")
		fmt.Printf("    Matched team:     %s\n", t.Name)
		fmt.Printf("    User match:       %s (as %s)\n", user, matchType)
		fmt.Printf("    Namespace match:  %s (via pattern %q)\n", namespace, nsMatchHow)
		fmt.Printf("    Permission:       %s (granted by team permissions)\n", perm)
		return
	}
}

// matchNamespacePattern 简易 namespace 匹配 (跟 rbac 包 nsMatcher 等价行为).
//
// 用于 explainAllowedDecision 输出 - rbac 包内部预编译 nsMatcher 没暴露,
// 这里复用基本匹配逻辑.
func matchNamespacePattern(pattern, ns string) bool {
	if pattern == ns {
		return true
	}
	if strings.HasSuffix(pattern, "*") && !strings.ContainsAny(pattern[:len(pattern)-1], "*?") {
		return strings.HasPrefix(ns, pattern[:len(pattern)-1])
	}
	// glob 兜底走 path.Match (跟 rbac 包同模式)
	matched, _ := filepathMatch(pattern, ns)
	return matched
}

// filepathMatch 简易包装 path.Match (path 包跟 cmd/kp 既有 import 不冲突).
func filepathMatch(pattern, name string) (bool, error) {
	return filepath.Match(pattern, name)
}

// ════════════════════════════════════════════════════════════════════════════
// kp team validate - 校验 teams.yaml 语法
// ════════════════════════════════════════════════════════════════════════════

func runTeamValidate(args []string) {
	flags := flag.NewFlagSet("team validate", flag.ExitOnError)
	useUser := flags.Bool("user", false, "操作用户级 ~/.kp/teams.yaml")
	flags.Parse(args)

	path, err := teamsFilePath(*useUser)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 借用 NewFileBasedChecker 内部完整解析 + compile 流程
	_, err = rbac.NewFileBasedChecker(path)
	if err != nil {
		fmt.Printf("❌ teams.yaml 校验失败 (%s):\n  %v\n", path, err)
		os.Exit(1)
	}

	// 额外: 检查未知 permission (warn-only)
	cfg, _ := loadTeamConfig(path)
	hasWarning := false
	for _, t := range cfg.Teams {
		for _, p := range t.Permissions {
			if !rbac.Permission(p).IsValid() {
				fmt.Printf("⚠️  team %q permissions 含未知权限 %q (允许扩展, 但建议核对拼写)\n",
					t.Name, p)
				hasWarning = true
			}
		}
		for _, p := range t.Excluded {
			if !rbac.Permission(p).IsValid() {
				fmt.Printf("⚠️  team %q excluded 含未知权限 %q\n", t.Name, p)
				hasWarning = true
			}
		}
	}

	if !hasWarning {
		fmt.Printf("✅ teams.yaml 校验通过 (%d teams, %s)\n", len(cfg.Teams), path)
	} else {
		fmt.Printf("\n✅ teams.yaml 语法正确, 但有 warning (上方)\n")
	}
}
