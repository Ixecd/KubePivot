package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func runDiff(args []string) {
	flags := flag.NewFlagSet("diff", flag.ExitOnError)
	namespace := flags.String("namespace", "", "kubernetes namespace")
	context := flags.String("context", "", "kubernetes context")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 文件路径")
	from := flags.Int("from", 0, "起始 revision（默认 latest-1）")
	to := flags.Int("to", 0, "目标 revision（默认 latest）")
	migrate := flags.Bool("migrate", false, "同时分析迁移建议（需要 DATABASE_URL）")
	service := flags.String("service", "", "指定服务名（多服务模式下必填）")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "解析参数失败:", err)
		os.Exit(1)
	}
	*kubeconfig = expandHome(*kubeconfig)

	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	cfg := &deployConfig{
		namespace:  *namespace,
		context:    *context,
		kubeconfig: *kubeconfig,
	}
	resolveDeployConfig(cfg, env, root)
	releaseName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))

	if *service != "" {
		releaseName = releaseName + "-" + *service
	}

	// 查 helm history 确定 revision 范围
	history, err := getHelmHistory(cfg, releaseName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "查询 helm history 失败:", err)
		os.Exit(1)
	}
	if len(history) < 2 {
		fmt.Println("revision 数量不足，无法对比（至少需要 2 个）")
		os.Exit(0)
	}

	latest := history[len(history)-1].Revision
	prev := history[len(history)-2].Revision

	fromRev := prev
	toRev := latest
	if *from > 0 {
		fromRev = *from
	}
	if *to > 0 {
		toRev = *to
	}

	fmt.Printf("对比 %s revision %d → %d\n\n", releaseName, fromRev, toRev)

	fromValues, err := getHelmValues(cfg, releaseName, fromRev)
	if err != nil {
		fmt.Fprintln(os.Stderr, "获取 revision", fromRev, "values 失败:", err)
		os.Exit(1)
	}
	toValues, err := getHelmValues(cfg, releaseName, toRev)
	if err != nil {
		fmt.Fprintln(os.Stderr, "获取 revision", toRev, "values 失败:", err)
		os.Exit(1)
	}

	diffs := diffValues(fromValues, toValues, "")
	if len(diffs) == 0 {
		fmt.Println("  （无差异）")
		return
	}
	for _, d := range diffs {
		fmt.Println(d)
	}

	if *migrate {
		fmt.Println()
		printMigrateSuggestions(root, env)
	}
}

// getHelmValues 获取指定 revision 的 values
func getHelmValues(cfg *deployConfig, releaseName string, revision int) (map[string]interface{}, error) {
	args := []string{"helm", "get", "values", releaseName,
		"--namespace", cfg.namespace,
		"--revision", fmt.Sprintf("%d", revision),
		"--output", "yaml",
	}
	if cfg.kubeconfig != "" {
		args = append(args, "--kubeconfig", cfg.kubeconfig)
	}
	if cfg.context != "" {
		args = append(args, "--kube-context", cfg.context)
	}

	out, err := runOutput(args...)
	if err != nil {
		return nil, err
	}

	var values map[string]interface{}
	if err := yaml.Unmarshal(out, &values); err != nil {
		return nil, err
	}
	if values == nil {
		values = make(map[string]interface{})
	}
	return values, nil
}

// diffValues 递归对比两个 map，返回差异行
func diffValues(from, to map[string]interface{}, prefix string) []string {
	var diffs []string
	seen := make(map[string]bool)

	for k, fromVal := range from {
		seen[k] = true
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}

		toVal, exists := to[k]
		if !exists {
			diffs = append(diffs, fmt.Sprintf("  - %-40s %v → (removed)", key, fromVal))
			continue
		}

		// 递归处理嵌套 map
		fromMap, fromIsMap := fromVal.(map[string]interface{})
		toMap, toIsMap := toVal.(map[string]interface{})
		if fromIsMap && toIsMap {
			diffs = append(diffs, diffValues(fromMap, toMap, key)...)
			continue
		}

		if fmt.Sprintf("%v", fromVal) != fmt.Sprintf("%v", toVal) {
			diffs = append(diffs, fmt.Sprintf("  ~ %-40s %v → %v", key, fromVal, toVal))
		}
	}

	// 新增的 key
	for k, toVal := range to {
		if seen[k] {
			continue
		}
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		diffs = append(diffs, fmt.Sprintf("  + %-40s (added) → %v", key, toVal))
	}

	return diffs
}

// getHelmHistory 获取 helm release 历史
func getHelmHistory(cfg *deployConfig, releaseName string) ([]struct {
	Revision int `json:"revision"`
}, error) {
	args := []string{"helm", "history", releaseName,
		"--namespace", cfg.namespace,
		"--output", "json",
	}
	if cfg.kubeconfig != "" {
		args = append(args, "--kubeconfig", cfg.kubeconfig)
	}
	if cfg.context != "" {
		args = append(args, "--kube-context", cfg.context)
	}

	out, err := runOutput(args...)
	if err != nil {
		return nil, err
	}

	var history []struct {
		Revision int `json:"revision"`
	}
	if err := json.Unmarshal(out, &history); err != nil {
		return nil, err
	}
	return history, nil
}

// formatDiffLine 格式化差异输出
func formatDiffSymbol(symbol string) string {
	switch symbol {
	case "+":
		return "  \033[32m+\033[0m"
	case "-":
		return "  \033[31m-\033[0m"
	case "~":
		return "  \033[33m~\033[0m"
	}
	return "  " + symbol
}

// printMigrateSuggestions 分析待执行迁移并给出升级建议
func printMigrateSuggestions(root string, env map[string]string) {
	fmt.Printf("── 迁移建议 %s\n\n", strings.Repeat("─", 50))

	// 连接 DB
	dbURL := resolveDatabaseURL(&migrateConfig{}, root, env)
	if dbURL == "" {
		P.Info("⏭ ", "未配置 DATABASE_URL，跳过迁移建议")
		return
	}
	db, err := sql.Open("postgres", dbURL)
	if err != nil || db.Ping() != nil {
		P.Info("⏭ ", "数据库不可达，跳过迁移建议")
		return
	}
	defer db.Close()

	_, versionStr, err := detectMigrationVersion(db, "auto")
	if err != nil || versionStr == "" {
		P.Info("⏭ ", "未检测到迁移表，跳过迁移建议")
		return
	}
	var currentVersion int64
	clean := strings.TrimSpace(strings.Split(versionStr, " ")[0])
	fmt.Sscanf(clean, "%d", &currentVersion)

	migDir := findMigrationsDir(root)
	if migDir == "" {
		P.Info("⏭ ", "未找到迁移目录，跳过迁移建议")
		return
	}

	files, err := scanMigrationFiles(migDir, currentVersion, -1)
	if err != nil || len(files) == 0 {
		P.Info("✅", "无待执行迁移")
		return
	}

	fmt.Printf("  发现 %d 个待执行迁移（版本 %d 之后）：\n\n", len(files), currentVersion)

	hasDestructive := false
	for _, f := range files {
		f.Operations = analyzeSQLFile(f.Path)
		for _, op := range f.Operations {
			if op.Risk > f.MaxRisk {
				f.MaxRisk = op.Risk
			}
		}
		riskStr := colorize(colorGreen, "✅ 安全")
		if f.MaxRisk == RiskPotential {
			riskStr = colorize(colorYellow, "⚠️  潜在风险")
		} else if f.MaxRisk == RiskDestructive {
			riskStr = colorize(colorRed, "❌ 破坏性")
			hasDestructive = true
		}
		fmt.Printf("  %s  版本 %-6d  %s\n", riskStr, f.Version, filepath.Base(f.Path))
		for _, op := range f.Operations {
			if op.Advice != "" {
				fmt.Printf("    %s %s\n", colorize(colorYellow, "💡"), op.Advice)
			}
		}
	}

	fmt.Println()
	if hasDestructive {
		fmt.Printf("%s 包含破坏性迁移，建议先运行 kp migrate plan 确认风险\n",
			colorize(colorRed, "❌"))
		fmt.Printf("%s 确认无误后运行 kp migrate run --dry-run 预览，再执行 kp migrate run\n",
			colorize(colorYellow, "💡"))
	} else {
		fmt.Printf("%s 可以安全执行：kp migrate run\n", colorize(colorGreen, "✅"))
	}
}
