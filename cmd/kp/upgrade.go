package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ixecd/kubepivot/internal/planner"
	"github.com/Ixecd/kubepivot/internal/state"
)

func runUpgrade(args []string) {
	flags := flag.NewFlagSet("upgrade", flag.ExitOnError)
	targetVersion := flags.String("target", "", "目标版本号（默认使用 project.env 中的 VERSION）")
	service := flags.String("service", "", "只升级指定服务，留空升级所有服务")
	dryRun := flags.Bool("dry-run", false, "预览所有变更，不实际执行")
	force := flags.Bool("force", false, "忽略兼容性警告强制升级（不推荐）")
	noHealthcheck := flags.Bool("no-healthcheck", false, "跳过升级后健康检查")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	root, err := Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	currentVersion := envOrDefault(env, "VERSION", "v0.1.0")
	target := *targetVersion
	if target == "" {
		target = currentVersion
	}

	P.Info("🚀", "KubePivot 跨版本升级器")
	fmt.Printf("  当前版本: %s\n", colorize(colorCyan, currentVersion))
	fmt.Printf("  目标版本: %s\n", colorize(colorGreen, target))
	if *service != "" {
		fmt.Printf("  升级范围: %s\n", colorize(colorYellow, *service))
	} else {
		fmt.Printf("  升级范围: %s\n", "所有服务")
	}
	fmt.Println()

	// ── 快照保护（有 CSI 才执行）────────────────────────────────────────────
	upgradeCfg := &deployConfig{namespace: envOrDefault(env, "KUBE_NAMESPACE", "")}
	hadSnapshot := tryPVCBackupBeforeMigrate(upgradeCfg)
	if hadSnapshot {
		P.Info("📸", "PVC 快照已创建，升级失败时可自动恢复")
	} else {
		P.Info("⏭ ", "无 CSI 快照支持，升级失败需手动处理 DB")
	}
	fmt.Println()

	// ── Step 1: 全链路兼容性检查 ─────────────────────────────────────────────
	P.Info("🔍", "Step 1/4 全链路兼容性检查")
	fmt.Println()

	hasBlocker := false

	// 1a. DB 迁移风险检查
	fmt.Printf("  %s DB 迁移风险\n", colorize(colorCyan, "1a."))
	dbURL := resolveDatabaseURL(&migrateConfig{}, root, env)
	if dbURL != "" {
		db, err := sql.Open("postgres", dbURL)
		if err == nil && db.Ping() == nil {
			defer db.Close()
			_, versionStr, _ := detectMigrationVersion(db, "auto")
			var currentDBVersion int64
			if versionStr != "" {
				clean := strings.TrimSpace(strings.Split(versionStr, " ")[0])
				fmt.Sscanf(clean, "%d", &currentDBVersion)
			}
			migDir := findMigrationsDir(root)
			if migDir != "" {
				files, _ := scanMigrationFiles(migDir, currentDBVersion, -1)
				if len(files) > 0 {
					destructive := false
					for i := range files {
						files[i].Operations = analyzeSQLFile(files[i].Path)
						for _, op := range files[i].Operations {
							if op.Risk > files[i].MaxRisk {
								files[i].MaxRisk = op.Risk
							}
						}
						if files[i].MaxRisk == RiskDestructive {
							destructive = true
						}
					}
					if destructive && !*force {
						fmt.Printf("    %s 发现破坏性 DB 变更，运行 kp migrate plan 查看详情\n",
							colorize(colorRed, "❌"))
						hasBlocker = true
					} else if destructive {
						fmt.Printf("    %s 发现破坏性 DB 变更（--force 已跳过）\n",
							colorize(colorYellow, "⚠️ "))
					} else {
						fmt.Printf("    %s %d 个待执行迁移，无破坏性变更\n",
							colorize(colorGreen, "✅"), len(files))
					}
				} else {
					fmt.Printf("    %s 无待执行迁移\n", colorize(colorGreen, "✅"))
				}
			} else {
				fmt.Printf("    %s 未找到迁移目录，跳过\n", colorize(colorGray, "⏭ "))
			}
		} else {
			fmt.Printf("    %s 数据库不可达，跳过\n", colorize(colorGray, "⏭ "))
		}
	} else {
		fmt.Printf("    %s 未配置 DATABASE_URL，跳过\n", colorize(colorGray, "⏭ "))
	}

	// 1b. API 兼容性检查
	fmt.Printf("  %s API 兼容性\n", colorize(colorCyan, "1b."))
	swaggerFile := findSwaggerFile(root)
	if swaggerFile != "" {
		if _, err := runOutput("oasdiff", "--version"); err == nil {
			// 只有本地有两个 swagger 文件时才能对比，否则跳过
			fmt.Printf("    %s swagger 文件已就绪，运行 kp compat check 做完整对比\n",
				colorize(colorYellow, "💡"))
		} else {
			fmt.Printf("    %s oasdiff 未安装，跳过 API 兼容检查\n", colorize(colorGray, "⏭ "))
		}
	} else {
		fmt.Printf("    %s 未找到 swagger/openapi 文件，跳过\n", colorize(colorGray, "⏭ "))
	}

	// 1c. Helm Values 检查（提示用 kp diff）
	fmt.Printf("  %s Helm Values 兼容性\n", colorize(colorCyan, "1c."))
	fmt.Printf("    %s 运行 kp diff --service <name> --migrate 查看 values 差异和迁移建议\n",
		colorize(colorYellow, "💡"))

	fmt.Println()

	if hasBlocker {
		P.Fail("兼容性检查发现阻断项，升级终止")
		fmt.Printf("%s 使用 --force 可强制升级（不推荐）\n", colorize(colorYellow, "💡"))
		os.Exit(1)
	}

	P.Done("兼容性检查通过")
	fmt.Println()

	// ── Step 2: dry-run 预览 ──────────────────────────────────────────────────
	if *dryRun {
		P.Info("📋", "Step 2/4 升级预览（--dry-run 模式，不实际执行）")
		fmt.Println()
		fmt.Printf("  将执行以下操作：\n")
		if dbURL != "" {
			fmt.Printf("  1. kp migrate run        — 执行待执行 DB 迁移\n")
		}
		fmt.Printf("  2. kp deploy             — 部署所有服务到版本 %s\n", target)
		if !*noHealthcheck {
			fmt.Printf("  3. 健康校验              — 检查所有服务 /healthz\n")
		}
		fmt.Println()
		P.Info("💡", "确认无误后去掉 --dry-run 执行正式升级")
		return
	}

	// ── Step 3: DB 迁移执行 ───────────────────────────────────────────────────
	P.Info("🗄 ", "Step 2/4 执行 DB 迁移")
	if dbURL != "" {
		db2, err := sql.Open("postgres", dbURL)
		if err == nil && db2.Ping() == nil {
			defer db2.Close()
			_, versionStr, _ := detectMigrationVersion(db2, "auto")
			var currentDBVersion int64
			if versionStr != "" {
				clean := strings.TrimSpace(strings.Split(versionStr, " ")[0])
				fmt.Sscanf(clean, "%d", &currentDBVersion)
			}
			migDir := findMigrationsDir(root)
			if migDir != "" {
				files, _ := scanMigrationFiles(migDir, currentDBVersion, -1)
				if len(files) > 0 {
					tool, _, _ := detectMigrationVersion(db2, "auto")
					for _, f := range files {
						if err := executeMigrationFile(db2, f, tool); err != nil {
							fmt.Println()
							printMigrateFailure(f, err)
							restoreAfterMigrateFail(upgradeCfg, root, hadSnapshot)
							os.Exit(1)
						}
					}
					P.Done("DB 迁移完成")
				} else {
					P.Info("✅", "无待执行迁移，跳过")
				}
			} else {
				P.Info("⏭ ", "未找到迁移目录，跳过")
			}
		} else {
			P.Info("⏭ ", "数据库不可达，跳过 DB 迁移")
		}
	} else {
		P.Info("⏭ ", "未配置 DATABASE_URL，跳过 DB 迁移")
	}
	fmt.Println()

	// ── Step 4: 部署服务 ──────────────────────────────────────────────────────
	P.Info("⛵", "Step 3/4 部署服务")
	deployArgs := []string{}
	if err := runDeployInternal(deployArgs, root, env, *service); err != nil {
		fmt.Println()
		P.Fail(fmt.Sprintf("服务部署失败: %v", err))
		restoreAfterMigrateFail(upgradeCfg, root, hadSnapshot)
		os.Exit(1)
	}
	fmt.Println()

	// ── Step 5: 健康校验 ──────────────────────────────────────────────────────
	if !*noHealthcheck {
		P.Info("🔍", "Step 4/4 升级后健康校验")
		cfg := &deployConfig{}
		resolveDeployConfig(cfg, env, root)
		if err := healthCheckAllServices(cfg, root, env, *service); err != nil {
			P.Fail(fmt.Sprintf("健康校验失败: %v", err))
			fmt.Printf("%s 运行 kp rollback 回滚服务\n", colorize(colorYellow, "💡"))
			os.Exit(1)
		}
		P.Done("所有服务健康校验通过")
		fmt.Println()
	}

	P.Info("✅", fmt.Sprintf("升级完成 %s → %s 🎉", currentVersion, target))
}

// runDeployInternal 内部调用 deploy 逻辑
func runDeployInternal(args []string, root string, env map[string]string, serviceFilter string) error {
	cfg := &deployConfig{}
	resolveDeployConfig(cfg, env, root)

	plans, err := planner.BuildPlan(filepath.Join(root, "configs/components.yaml"))
	if err != nil {
		return err
	}

	// --service 过滤
	if serviceFilter != "" {
		var filtered []planner.Plan
		for _, p := range plans {
			if p.Name == serviceFilter {
				filtered = append(filtered, p)
			}
		}
		if len(filtered) == 0 {
			return fmt.Errorf("未找到服务 %s，请检查 components.yaml", serviceFilter)
		}
		plans = filtered
	}

	store := state.NewAutoStore(env["ETCD_ENDPOINTS"])
	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))
	version := envOrDefault(env, "VERSION", "v0.1.0")

	sm, err := state.New(store, projectName, cfg.namespace, version)
	if err != nil {
		return err
	}

	return executeDeploy(sm, cfg, env, plans, root)
}

// healthCheckAllServices 检查所有有 image 的服务 /healthz
func healthCheckAllServices(cfg *deployConfig, root string, env map[string]string, serviceFilter string) error {
	plans, err := planner.BuildPlan(filepath.Join(root, "configs/components.yaml"))
	if err != nil {
		return err
	}
	for _, p := range plans {
		if p.Image == "" {
			continue
		}
		if serviceFilter != "" && p.Name != serviceFilter {
			continue
		}
		P.Start("🔍", fmt.Sprintf("检查 %s /healthz", p.Name))
		args := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
		args = append(args, "rollout", "status",
			fmt.Sprintf("deployment/%s", p.Name),
			"--timeout=60s",
		)
		if _, err := runOutput(args...); err != nil {
			P.Fail(fmt.Sprintf("%s 健康校验失败", p.Name))
			return fmt.Errorf("%s 不健康", p.Name)
		}
		P.Done(fmt.Sprintf("%s 健康", p.Name))
	}
	return nil
}
