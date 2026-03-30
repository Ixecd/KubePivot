package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ── 风险级别 ──────────────────────────────────────────────────────────────────

type RiskLevel int

const (
	RiskSafe        RiskLevel = iota // ✅ 安全
	RiskPotential                    // ⚠️ 潜在风险
	RiskDestructive                  // ❌ 破坏性
)

func (r RiskLevel) String() string {
	switch r {
	case RiskSafe:
		return colorize(colorGreen, "✅ 安全")
	case RiskPotential:
		return colorize(colorYellow, "⚠️  潜在风险")
	case RiskDestructive:
		return colorize(colorRed, "❌ 破坏性")
	}
	return "未知"
}

// ── SQL 操作分析结果 ──────────────────────────────────────────────────────────

type SQLOperation struct {
	Statement string
	Risk      RiskLevel
	Advice    string
}

// ── 迁移文件 ──────────────────────────────────────────────────────────────────

type MigrationFile struct {
	Version    int64
	Name       string
	Path       string
	Operations []SQLOperation
	MaxRisk    RiskLevel
}

// ── JSON 输出结构 ─────────────────────────────────────────────────────────────

type MigratePlanJSON struct {
	CurrentVersion int64               `json:"current_version"`
	TargetVersion  int64               `json:"target_version"`
	Migrations     []MigrationFileJSON `json:"migrations"`
	HasDestructive bool                `json:"has_destructive"`
	HasPotential   bool                `json:"has_potential"`
}

type MigrationFileJSON struct {
	Version    int64              `json:"version"`
	Name       string             `json:"name"`
	Operations []OperationJSON    `json:"operations"`
	MaxRisk    string             `json:"max_risk"`
}

type OperationJSON struct {
	Statement string `json:"statement"`
	Risk      string `json:"risk"`
	Advice    string `json:"advice,omitempty"`
}

// ── 主命令 ───────────────────────────────────────────────────────────────────

func runMigratePlan(args []string) {
	flags := flag.NewFlagSet("migrate plan", flag.ExitOnError)
	cfg := &migrateConfig{}
	flags.StringVar(&cfg.databaseURL, "database-url", "", "数据库连接串")
	flags.StringVar(&cfg.migrationTool, "migration-tool", "auto", "迁移工具：golang-migrate / atlas / auto")
	migrationsDir := flags.String("migrations-dir", "", "迁移文件目录（默认自动探测）")
	targetVersion := flags.Int64("target", -1, "目标版本号，-1 表示最新")
	outputJSON := flags.Bool("output-json", false, "输出 JSON（CI/CD 集成）")
	force := flags.Bool("force", false, "忽略破坏性变更警告（不推荐）")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))

	// 解析 DATABASE_URL
	dbURL := resolveDatabaseURL(cfg, root, env)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "未找到 DATABASE_URL，请通过 --database-url、KP_DATABASE_URL 或 .env 提供")
		os.Exit(1)
	}

	// 连接 DB 获取当前迁移版本
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

	_, currentVersionStr, err := detectMigrationVersion(db, cfg.migrationTool)
	if err != nil {
		fmt.Fprintln(os.Stderr, "查询当前迁移版本失败:", err)
		os.Exit(1)
	}
	var currentVersion int64
	if currentVersionStr != "" {
		// 去掉可能的 dirty 标记
		clean := strings.TrimSpace(strings.Split(currentVersionStr, " ")[0])
		currentVersion, _ = strconv.ParseInt(clean, 10, 64)
	}

	// 探测迁移目录
	migDir := *migrationsDir
	if migDir == "" {
		migDir = findMigrationsDir(root)
	}
	if migDir == "" {
		fmt.Fprintln(os.Stderr, "未找到迁移目录，请通过 --migrations-dir 指定")
		os.Exit(1)
	}

	// 扫描迁移文件
	files, err := scanMigrationFiles(migDir, currentVersion, *targetVersion)
	if err != nil {
		fmt.Fprintln(os.Stderr, "扫描迁移文件失败:", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		P.Info("✅", fmt.Sprintf("当前版本 %d 已是最新，无待执行迁移", currentVersion))
		return
	}

	// 分析每个文件
	for i := range files {
		files[i].Operations = analyzeSQLFile(files[i].Path)
		for _, op := range files[i].Operations {
			if op.Risk > files[i].MaxRisk {
				files[i].MaxRisk = op.Risk
			}
		}
	}

	targetVer := files[len(files)-1].Version

	// JSON 输出
	if *outputJSON {
		printMigratePlanJSON(files, currentVersion, targetVer)
		return
	}

	// 人类可读输出
	printMigratePlanHuman(files, currentVersion, targetVer, *force)
}

// ── 输出 ──────────────────────────────────────────────────────────────────────

func printMigratePlanHuman(files []MigrationFile, current, target int64, force bool) {
	P.Info("📋", fmt.Sprintf("迁移计划（当前版本: %d → 目标版本: %d，共 %d 个文件）",
		current, target, len(files)))
	fmt.Println()

	fmt.Printf("  %-45s %-30s %s\n", "迁移文件", "操作", "风险")
	fmt.Printf("  %s\n", strings.Repeat("─", 90))

	hasDestructive := false
	hasPotential := false

	for _, f := range files {
		if len(f.Operations) == 0 {
			fmt.Printf("  %-45s %-30s %s\n", filepath.Base(f.Path), "（无可识别操作）",
				colorize(colorGray, "─"))
			continue
		}
		for i, op := range f.Operations {
			fileName := ""
			if i == 0 {
				fileName = filepath.Base(f.Path)
			}
			fmt.Printf("  %-45s %-30s %s\n", fileName, truncate(op.Statement, 30), op.Risk.String())
			if op.Advice != "" {
				fmt.Printf("  %-45s %s\n", "", colorize(colorYellow, "💡 "+op.Advice))
			}
			if op.Risk == RiskDestructive {
				hasDestructive = true
			}
			if op.Risk == RiskPotential {
				hasPotential = true
			}
		}
	}

	fmt.Println()

	if hasDestructive {
		fmt.Printf("%s 发现破坏性变更，升级前请备份数据库\n", colorize(colorRed, "❌"))
		if !force {
			fmt.Printf("%s 使用 --force 可忽略此警告继续生成计划（不推荐）\n", colorize(colorYellow, "⚠️ "))
			os.Exit(1)
		}
	} else if hasPotential {
		fmt.Printf("%s 发现潜在风险变更，建议在测试环境验证后再升级生产\n", colorize(colorYellow, "⚠️ "))
	} else {
		fmt.Printf("%s 所有变更均为安全操作，可以放心升级\n", colorize(colorGreen, "✅"))
	}
}

func printMigratePlanJSON(files []MigrationFile, current, target int64) {
	plan := MigratePlanJSON{
		CurrentVersion: current,
		TargetVersion:  target,
	}
	for _, f := range files {
		mf := MigrationFileJSON{
			Version: f.Version,
			Name:    filepath.Base(f.Path),
			MaxRisk: riskName(f.MaxRisk),
		}
		for _, op := range f.Operations {
			mf.Operations = append(mf.Operations, OperationJSON{
				Statement: op.Statement,
				Risk:      riskName(op.Risk),
				Advice:    op.Advice,
			})
		}
		plan.Migrations = append(plan.Migrations, mf)
		if f.MaxRisk == RiskDestructive {
			plan.HasDestructive = true
		}
		if f.MaxRisk == RiskPotential {
			plan.HasPotential = true
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(plan)
}

func riskName(r RiskLevel) string {
	switch r {
	case RiskSafe:
		return "safe"
	case RiskPotential:
		return "potential"
	case RiskDestructive:
		return "destructive"
	}
	return "unknown"
}

// ── SQL 分析 ──────────────────────────────────────────────────────────────────

var (
	reDropTable      = regexp.MustCompile(`(?i)DROP\s+TABLE(\s+IF\s+EXISTS)?\s+(\S+)`)
	reDropColumn     = regexp.MustCompile(`(?i)DROP\s+COLUMN(\s+IF\s+EXISTS)?\s+(\S+)`)
	reAlterColumnType = regexp.MustCompile(`(?i)ALTER\s+COLUMN\s+(\S+)\s+TYPE\s+(\S+)`)
	reSetNotNull     = regexp.MustCompile(`(?i)ALTER\s+COLUMN\s+(\S+)\s+SET\s+NOT\s+NULL`)
	reCreateTable    = regexp.MustCompile(`(?i)CREATE\s+TABLE`)
	reAddColumn      = regexp.MustCompile(`(?i)ADD\s+COLUMN`)
	reCreateIndex    = regexp.MustCompile(`(?i)CREATE\s+(UNIQUE\s+)?INDEX`)
)

// safeTypeExpansions 类型扩容（安全）
var safeTypeExpansions = map[string][]string{
	"int":      {"bigint", "int8"},
	"varchar":  {"text"},
	"char":     {"varchar", "text"},
	"smallint": {"int", "integer", "bigint"},
}

func analyzeSQLFile(path string) []SQLOperation {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	sql := stripComments(string(data))
	stmts := splitStatements(sql)

	var ops []SQLOperation
	for _, stmt := range stmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}

		// DROP TABLE
		if m := reDropTable.FindStringSubmatch(stmt); m != nil {
			ops = append(ops, SQLOperation{
				Statement: fmt.Sprintf("DROP TABLE %s", m[2]),
				Risk:      RiskDestructive,
				Advice:    fmt.Sprintf("删除表 %s 是不可逆操作，请先备份数据", m[2]),
			})
			continue
		}

		// DROP COLUMN
		if m := reDropColumn.FindStringSubmatch(stmt); m != nil {
			ops = append(ops, SQLOperation{
				Statement: fmt.Sprintf("DROP COLUMN %s", m[2]),
				Risk:      RiskDestructive,
				Advice:    fmt.Sprintf("删除列 %s 是不可逆操作，建议先将数据备份到历史表", m[2]),
			})
			continue
		}

		// ALTER COLUMN TYPE
		if m := reAlterColumnType.FindStringSubmatch(stmt); m != nil {
			col, newType := m[1], strings.ToLower(m[2])
			risk, advice := classifyTypeChange(newType)
			ops = append(ops, SQLOperation{
				Statement: fmt.Sprintf("ALTER COLUMN %s TYPE %s", col, newType),
				Risk:      risk,
				Advice:    advice,
			})
			continue
		}

		// SET NOT NULL
		if m := reSetNotNull.FindStringSubmatch(stmt); m != nil {
			ops = append(ops, SQLOperation{
				Statement: fmt.Sprintf("ALTER COLUMN %s SET NOT NULL", m[1]),
				Risk:      RiskPotential,
				Advice:    fmt.Sprintf("请确保列 %s 存量数据中无 NULL，否则迁移会失败", m[1]),
			})
			continue
		}

		// CREATE TABLE
		if reCreateTable.MatchString(stmt) {
			ops = append(ops, SQLOperation{
				Statement: "CREATE TABLE",
				Risk:      RiskSafe,
			})
			continue
		}

		// ADD COLUMN
		if reAddColumn.MatchString(stmt) {
			ops = append(ops, SQLOperation{
				Statement: "ADD COLUMN",
				Risk:      RiskSafe,
			})
			continue
		}

		// CREATE INDEX
		if reCreateIndex.MatchString(stmt) {
			advice := ""
			if !strings.Contains(strings.ToUpper(stmt), "CONCURRENTLY") {
				advice = "建议使用 CREATE INDEX CONCURRENTLY 避免锁表"
			}
			ops = append(ops, SQLOperation{
				Statement: "CREATE INDEX",
				Risk:      RiskSafe,
				Advice:    advice,
			})
			continue
		}
	}
	return ops
}

// classifyTypeChange 判断类型变更风险
func classifyTypeChange(newType string) (RiskLevel, string) {
	newType = strings.ToLower(strings.Split(newType, "(")[0])
	// 检查是否是已知安全扩容
	for _, expansions := range safeTypeExpansions {
		for _, t := range expansions {
			if t == newType {
				return RiskSafe, ""
			}
		}
	}
	return RiskDestructive, fmt.Sprintf("类型变更为 %s 可能导致数据丢失，请在测试环境验证", newType)
}

// stripComments 去掉 SQL 注释
func stripComments(sql string) string {
	// 去掉单行注释 --
	lines := strings.Split(sql, "\n")
	var result []string
	for _, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		result = append(result, line)
	}
	// 去掉块注释 /* */
	s := strings.Join(result, "\n")
	blockComment := regexp.MustCompile(`(?s)/\*.*?\*/`)
	return blockComment.ReplaceAllString(s, "")
}

// splitStatements 按分号拆分 SQL 语句
func splitStatements(sql string) []string {
	return strings.Split(sql, ";")
}

// ── 文件扫描 ──────────────────────────────────────────────────────────────────

func scanMigrationFiles(dir string, currentVersion, targetVersion int64) ([]MigrationFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var files []MigrationFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// 只处理 .up.sql 文件
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		version := extractVersion(name)
		if version <= currentVersion {
			continue
		}
		if targetVersion > 0 && version > targetVersion {
			continue
		}
		files = append(files, MigrationFile{
			Version: version,
			Name:    name,
			Path:    filepath.Join(dir, name),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Version < files[j].Version
	})
	return files, nil
}

// extractVersion 从文件名提取版本号（支持 000001_ 和 20240330120000_ 两种格式）
func extractVersion(name string) int64 {
	re := regexp.MustCompile(`^(\d+)_`)
	m := re.FindStringSubmatch(name)
	if m == nil {
		return 0
	}
	v, _ := strconv.ParseInt(m[1], 10, 64)
	return v
}

// findMigrationsDir 自动探测迁移目录
func findMigrationsDir(root string) string {
	candidates := []string{
		filepath.Join(root, "db", "migrations"),
		filepath.Join(root, "internal", "db", "migrations"),
		filepath.Join(root, "migrations"),
		filepath.Join(root, "database", "migrations"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// resolveDatabaseURL 按优先级解析 DATABASE_URL
func resolveDatabaseURL(cfg *migrateConfig, root string, env map[string]string) string {
	if cfg.databaseURL != "" {
		return cfg.databaseURL
	}
	if v := os.Getenv("KP_DATABASE_URL"); v != "" {
		return v
	}
	localEnv, _ := readEnvFile(filepath.Join(root, ".env"))
	if v := localEnv["DATABASE_URL"]; v != "" {
		return v
	}
	return env["DATABASE_URL"]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}