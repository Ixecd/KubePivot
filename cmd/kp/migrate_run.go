package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ixecd/kubepivot/internal/audit"
	"github.com/Ixecd/kubepivot/internal/rbac"
)

func runMigrateRun(args []string) {
	flags := flag.NewFlagSet("migrate run", flag.ExitOnError)
	cfg := &migrateConfig{}
	flags.StringVar(&cfg.databaseURL, "database-url", "", "数据库连接串")
	flags.StringVar(&cfg.migrationTool, "migration-tool", "auto", "迁移工具：golang-migrate / atlas / auto")
	migrationsDir := flags.String("migrations-dir", "", "迁移文件目录（默认自动探测）")
	targetVersion := flags.Int64("target", -1, "目标版本号，-1 表示最新")
	dryRun := flags.Bool("dry-run", false, "预览将要执行的迁移，不实际执行")
	fullSQL := flags.Bool("full-sql", false, "dry-run 时打印完整 SQL 内容")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	root, err := Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))

	// RBAC 检查
	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))
	mustCheck(audit.ResolveActor(), projectName, rbac.PermMigrate)

	dbURL := resolveDatabaseURL(cfg, root, env)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "未找到 DATABASE_URL，请通过 --database-url、KP_DATABASE_URL 或 .env 提供")
		os.Exit(1)
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "连接数据库失败:", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		fmt.Fprintln(os.Stderr, "数据库不可达:", err)
		os.Exit(1)
	}

	// 获取当前版本
	tool, versionStr, err := detectMigrationVersion(db, cfg.migrationTool)
	if err != nil {
		fmt.Fprintln(os.Stderr, "查询当前迁移版本失败:", err)
		os.Exit(1)
	}
	var currentVersion int64
	if versionStr != "" {
		clean := strings.TrimSpace(strings.Split(versionStr, " ")[0])
		fmt.Sscanf(clean, "%d", &currentVersion)
	}

	// 找迁移目录
	migDir := *migrationsDir
	if migDir == "" {
		migDir = findMigrationsDir(root)
	}
	if migDir == "" {
		fmt.Fprintln(os.Stderr, "未找到迁移目录，请通过 --migrations-dir 指定")
		os.Exit(1)
	}

	// 扫描待执行文件
	files, err := scanMigrationFiles(migDir, currentVersion, *targetVersion)
	if err != nil {
		fmt.Fprintln(os.Stderr, "扫描迁移文件失败:", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		P.Info("✅", fmt.Sprintf("当前版本 %d 已是最新，无待执行迁移", currentVersion))
		return
	}

	targetVer := files[len(files)-1].Version

	// dry-run 模式
	if *dryRun {
		printDryRun(files, currentVersion, targetVer, *fullSQL)
		return
	}

	// 执行迁移前触发 PVC 快照（有 CSI 才执行）
	migrCfg := &deployConfig{namespace: envOrDefault(env, "KUBE_NAMESPACE", "")}
	hadSnapshot := tryPVCBackupBeforeMigrate(migrCfg)

	// 执行迁移
	P.Info("🚀", fmt.Sprintf("开始执行迁移（当前版本: %d → 目标版本: %d，共 %d 个文件）",
		currentVersion, targetVer, len(files)))
	fmt.Println()

	for _, f := range files {
		if err := executeMigrationFile(db, f, tool); err != nil {
			fmt.Println()
			printMigrateFailure(f, err)
			restoreAfterMigrateFail(migrCfg, hadSnapshot)
			os.Exit(1)
		}
	}

	fmt.Println()
	P.Info("✅", fmt.Sprintf("迁移完成，当前版本: %d", targetVer))
}

// executeMigrationFile 执行单个迁移文件
func executeMigrationFile(db *sql.DB, f MigrationFile, tool string) error {
	P.Start("⚡", fmt.Sprintf("执行 %s", filepath.Base(f.Path)))

	data, err := os.ReadFile(f.Path)
	if err != nil {
		P.Fail(fmt.Sprintf("读取文件失败: %v", err))
		return err
	}

	// 在事务里执行
	tx, err := db.Begin()
	if err != nil {
		P.Fail(fmt.Sprintf("开启事务失败: %v", err))
		return err
	}

	sql := string(data)
	if _, err := tx.Exec(sql); err != nil {
		tx.Rollback()
		P.Fail(fmt.Sprintf("SQL 执行失败: %v", err))
		return fmt.Errorf("版本 %d 执行失败: %w", f.Version, err)
	}

	// 更新版本表
	if err := updateVersionTable(tx, f.Version, tool); err != nil {
		tx.Rollback()
		P.Fail(fmt.Sprintf("更新版本表失败: %v", err))
		return err
	}

	if err := tx.Commit(); err != nil {
		P.Fail(fmt.Sprintf("提交事务失败: %v", err))
		return err
	}

	P.Done(fmt.Sprintf("版本 %d 执行完成", f.Version))
	return nil
}

// updateVersionTable 更新迁移版本记录
func updateVersionTable(tx *sql.Tx, version int64, tool string) error {
	switch tool {
	case "atlas":
		_, err := tx.Exec(
			`INSERT INTO atlas_schema_revisions (version, applied_at) VALUES ($1, NOW())
			 ON CONFLICT (version) DO UPDATE SET applied_at = NOW()`,
			fmt.Sprintf("%d", version),
		)
		return err
	default: // golang-migrate
		_, err := tx.Exec(
			`INSERT INTO schema_migrations (version, dirty) VALUES ($1, false)
			 ON CONFLICT (version) DO UPDATE SET dirty = false`,
			version,
		)
		return err
	}
}

// printDryRun 打印迁移预览
func printDryRun(files []MigrationFile, current, target int64, fullSQL bool) {
	P.Info("📋", fmt.Sprintf("迁移预览（当前版本: %d → 目标版本: %d，共 %d 个文件）",
		current, target, len(files)))
	fmt.Printf("  %s\n\n", strings.Repeat("━", 60))

	if fullSQL {
		// 完整 SQL 内容
		for _, f := range files {
			fmt.Printf("  %s\n", colorize(colorCyan, filepath.Base(f.Path)))
			fmt.Printf("  %s\n", strings.Repeat("─", 54))
			data, err := os.ReadFile(f.Path)
			if err != nil {
				fmt.Printf("  （读取失败: %v）\n", err)
			} else {
				for _, line := range strings.Split(string(data), "\n") {
					fmt.Printf("  %s\n", line)
				}
			}
			fmt.Println()
		}
	} else {
		// 文件名 + 操作摘要
		fmt.Printf("  %-45s %s\n", "迁移文件", "操作摘要")
		fmt.Printf("  %s\n", strings.Repeat("─", 80))
		for _, f := range files {
			summary := buildOperationSummary(f.Path)
			fmt.Printf("  %-45s %s\n", filepath.Base(f.Path), summary)
		}
	}
}

// buildOperationSummary 提取 SQL 文件的操作摘要
func buildOperationSummary(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "（读取失败）"
	}
	sql := stripComments(string(data))
	stmts := splitStatements(sql)

	var ops []string
	for _, stmt := range stmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		upper := strings.ToUpper(stmt)
		switch {
		case strings.HasPrefix(upper, "CREATE TABLE"):
			name := extractTableName(stmt, "CREATE TABLE")
			ops = append(ops, "CREATE TABLE "+name)
		case strings.HasPrefix(upper, "DROP TABLE"):
			name := extractTableName(stmt, "DROP TABLE")
			ops = append(ops, colorize(colorRed, "DROP TABLE "+name))
		case strings.Contains(upper, "ADD COLUMN"):
			ops = append(ops, "ADD COLUMN")
		case strings.Contains(upper, "DROP COLUMN"):
			ops = append(ops, colorize(colorRed, "DROP COLUMN"))
		case strings.Contains(upper, "ALTER COLUMN"):
			ops = append(ops, "ALTER COLUMN")
		case strings.HasPrefix(upper, "CREATE INDEX"):
			ops = append(ops, "CREATE INDEX")
		}
	}

	if len(ops) == 0 {
		return "（无可识别操作）"
	}
	result := strings.Join(ops, "; ")
	if len(result) > 60 {
		result = result[:57] + "..."
	}
	return result
}

// extractTableName 从 SQL 语句里提取表名
func extractTableName(stmt, prefix string) string {
	upper := strings.ToUpper(stmt)
	idx := strings.Index(upper, strings.ToUpper(prefix))
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(stmt[idx+len(prefix):])
	// 去掉 IF EXISTS / IF NOT EXISTS
	upperRest := strings.ToUpper(rest)
	if strings.HasPrefix(upperRest, "IF NOT EXISTS ") {
		rest = rest[len("IF NOT EXISTS "):]
	} else if strings.HasPrefix(upperRest, "IF EXISTS ") {
		rest = rest[len("IF EXISTS "):]
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// printMigrateFailure 打印迁移失败信息
func printMigrateFailure(f MigrationFile, err error) {
	fmt.Printf("%s\n", colorize(colorRed, "❌ 数据库迁移失败！"))
	fmt.Printf("  %s\n", strings.Repeat("━", 40))
	fmt.Printf("  版本: %s\n", colorize(colorRed, fmt.Sprintf("%d", f.Version)))
	fmt.Printf("  文件: %s\n", filepath.Base(f.Path))
	fmt.Printf("  错误: %s\n", colorize(colorRed, err.Error()))
	fmt.Println()
	fmt.Printf("%s 建议操作：\n", colorize(colorYellow, "💡"))
	fmt.Println("  1. 检查并修复迁移 SQL")
	fmt.Println("  2. 如需回滚整个部署，请执行：")
	fmt.Printf("     %s\n", colorize(colorCyan, "kp rollback"))
}
