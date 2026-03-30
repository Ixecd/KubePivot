package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/lib/pq"
)

type migrateConfig struct {
	databaseURL   string
	service       string
	migrationTool string // golang-migrate / atlas / auto
}

func runMigrate(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "用法: kp migrate <子命令>")
		fmt.Fprintln(os.Stderr, "  kp migrate status   查看迁移状态")
		os.Exit(1)
	}
	switch args[0] {
	case "status":
		runMigrateStatus(args[1:])
	case "plan":
		runMigratePlan(args[1:])
	case "run":
		runMigrateRun(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n", args[0])
		os.Exit(1)
	}
}

func runMigrateStatus(args []string) {
	flags := flag.NewFlagSet("migrate status", flag.ExitOnError)
	cfg := &migrateConfig{}
	flags.StringVar(&cfg.databaseURL, "database-url", "", "数据库连接串（优先于 KP_DATABASE_URL 和 .env）")
	flags.StringVar(&cfg.service, "service", "", "只检查指定服务，留空检查所有")
	flags.StringVar(&cfg.migrationTool, "migration-tool", "auto", "迁移工具：golang-migrate / atlas / auto")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	version := envOrDefault(env, "VERSION", "v0.1.0")

	// DATABASE_URL 优先级：flag > KP_DATABASE_URL env > .env
	dbURL := cfg.databaseURL
	if dbURL == "" {
		dbURL = os.Getenv("KP_DATABASE_URL")
	}
	if dbURL == "" {
		// 从 .env 读
		localEnv, _ := readEnvFile(filepath.Join(root, ".env"))
		dbURL = localEnv["DATABASE_URL"]
	}
	if dbURL == "" {
		dbURL = env["DATABASE_URL"]
	}

	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "未找到 DATABASE_URL，请通过以下方式之一提供：")
		fmt.Fprintln(os.Stderr, "  --database-url postgres://user:pass@host:5432/db")
		fmt.Fprintln(os.Stderr, "  export KP_DATABASE_URL=postgres://user:pass@host:5432/db")
		fmt.Fprintln(os.Stderr, "  在 .env 文件里设置 DATABASE_URL=...")
		os.Exit(1)
	}

	P.Info("🔍", fmt.Sprintf("检查数据库迁移状态（项目版本: %s）", version))
	fmt.Println()

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "连接数据库失败:", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		fmt.Fprintln(os.Stderr, "数据库不可达:", err)
		fmt.Fprintln(os.Stderr, "提示：本地开发请先确认 postgres 已启动，或使用 kubectl port-forward")
		os.Exit(1)
	}

	// 探测迁移工具 + 查版本
	tool, migVersion, err := detectMigrationVersion(db, cfg.migrationTool)
	if err != nil {
		P.Fail(fmt.Sprintf("查询迁移版本失败: %v", err))
		os.Exit(1)
	}

	// 打印结果
	fmt.Printf("  %-20s %-15s %-15s %-10s\n", "服务", "迁移工具", "DB 迁移版本", "K8s 版本")
	fmt.Printf("  %s\n", strings.Repeat("─", 65))

	svcName := envOrDefault(env, "PROJECT_NAME", "unknown")
	if cfg.service != "" {
		svcName = cfg.service
	}

	if migVersion == "" {
		fmt.Printf("  %-20s %-15s %-15s %-10s  %s\n",
			svcName, tool, "（无迁移表）", version,
			colorize(colorYellow, "⚠ 未检测到迁移"))
	} else {
		fmt.Printf("  %-20s %-15s %-15s %-10s  %s\n",
			svcName, tool, migVersion, version,
			colorize(colorGreen, "✓ 已迁移"))
	}

	fmt.Println()
}

// detectMigrationVersion 自动探测迁移工具并查询当前版本
func detectMigrationVersion(db *sql.DB, tool string) (string, string, error) {
	if tool == "golang-migrate" || tool == "auto" {
		v, err := queryGoMigrateVersion(db)
		if err == nil {
			return "golang-migrate", v, nil
		}
		if tool == "golang-migrate" {
			return "", "", fmt.Errorf("golang-migrate 表不存在: %w", err)
		}
	}

	if tool == "atlas" || tool == "auto" {
		v, err := queryAtlasVersion(db)
		if err == nil {
			return "atlas", v, nil
		}
		if tool == "atlas" {
			return "", "", fmt.Errorf("atlas 表不存在: %w", err)
		}
	}

	// auto 模式下两个都没找到
	return "unknown", "", nil
}

// queryGoMigrateVersion 查询 golang-migrate 当前版本
func queryGoMigrateVersion(db *sql.DB) (string, error) {
	var version int64
	var dirty bool
	err := db.QueryRow(
		`SELECT version, dirty FROM schema_migrations ORDER BY version DESC LIMIT 1`,
	).Scan(&version, &dirty)
	if err != nil {
		return "", err
	}
	v := fmt.Sprintf("%d", version)
	if dirty {
		v += colorize(colorRed, " (dirty)")
	}
	return v, nil
}

// queryAtlasVersion 查询 Atlas 当前版本
func queryAtlasVersion(db *sql.DB) (string, error) {
	var version string
	err := db.QueryRow(
		`SELECT version FROM atlas_schema_revisions ORDER BY applied_at DESC LIMIT 1`,
	).Scan(&version)
	if err != nil {
		return "", err
	}
	return version, nil
}