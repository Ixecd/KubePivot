package main

import (
	"fmt"
	"path/filepath"
)

// tryPVCBackupBeforeMigrate 迁移前尝试触发 PVC 快照
// 有 CSI 才执行，没有则跳过不阻断
func tryPVCBackupBeforeMigrate(cfg *deployConfig) bool {
	// 检查 CSI VolumeSnapshot CRD
	args := kubectlBaseArgs(cfg.kubeconfig, cfg.context, "")
	args = append(args, "get", "crd",
		"volumesnapshots.snapshot.storage.k8s.io",
		"--ignore-not-found", "-o", "name")
	out, err := runOutput(args...)
	if err != nil || len(out) == 0 {
		P.Info("⏭ ", "未检测到 CSI VolumeSnapshot，跳过 PVC 快照")
		return false
	}

	P.Start("📸", "触发 PVC 快照（迁移保护）")
	backupArgs := []string{"kp", "pvc", "backup",
		"--namespace", cfg.namespace,
	}
	if cfg.context != "" {
		backupArgs = append(backupArgs, "--context", cfg.context)
	}
	if cfg.kubeconfig != "" {
		backupArgs = append(backupArgs, "--kubeconfig", cfg.kubeconfig)
	}
	if _, err := runOutput(backupArgs...); err != nil {
		P.Fail(fmt.Sprintf("PVC 快照失败（不阻断迁移）: %v", err))
		return false
	}
	P.Done("PVC 快照完成")
	return true
}

// restoreAfterMigrateFail 迁移失败后双层回滚：
// 1. kp pvc restore（有快照才执行）
// 2. kp rollback（helm rollback）
func restoreAfterMigrateFail(cfg *deployConfig, root string, hadSnapshot bool) {
	P.Info("⏪", "迁移失败，触发双层回滚")

	// Layer 1：PVC 恢复
	if hadSnapshot {
		P.Start("💾", "恢复 PVC 快照")
		restoreArgs := []string{"kp", "pvc", "restore",
			"--namespace", cfg.namespace,
		}
		if cfg.context != "" {
			restoreArgs = append(restoreArgs, "--context", cfg.context)
		}
		if _, err := runOutput(restoreArgs...); err != nil {
			P.Fail(fmt.Sprintf("PVC 恢复失败: %v，请手动恢复", err))
		} else {
			P.Done("PVC 已恢复到迁移前状态")
		}
	}

	// Layer 2：helm rollback
	P.Start("⏪", "helm rollback 回滚服务")
	rollbackArgs := []string{"kp", "rollback",
		"--namespace", cfg.namespace,
	}
	if cfg.context != "" {
		rollbackArgs = append(rollbackArgs, "--context", cfg.context)
	}
	if _, err := runOutput(rollbackArgs...); err != nil {
		P.Fail(fmt.Sprintf("helm rollback 失败: %v，请手动回滚", err))
	} else {
		P.Done("helm rollback 完成")
	}
}

// runMigrateFixDirty 交互式修复 dirty 状态
func runMigrateFixDirty(args []string) {
	root, err := Root()
	if err != nil {
		fmt.Println("找不到项目根目录:", err)
		return
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	cfg := &migrateConfig{}
	dbURL := resolveDatabaseURL(cfg, root, env)
	if dbURL == "" {
		P.Fail("未配置 DATABASE_URL")
		return
	}

	P.Info("🔧", "检测 dirty 迁移状态")

	// 检测 dirty 状态
	checkArgs := []string{"kp", "migrate", "status"}
	out, _ := runOutput(checkArgs...)

	if len(out) == 0 {
		P.Info("✅", "未检测到 dirty 状态")
		return
	}

	fmt.Println(string(out))
	fmt.Println()
	fmt.Printf("%s 检测到 dirty 迁移，修复步骤：\n\n", colorize(colorYellow, "⚠️ "))
	fmt.Printf("  %s 先确认失败的迁移 SQL 是否已手动处理完毕\n", colorize(colorCyan, "1."))
	fmt.Printf("  %s 确认后运行以下命令清除 dirty 标记：\n", colorize(colorCyan, "2."))
	fmt.Printf("\n    psql $DATABASE_URL -c \\\n")
	fmt.Printf("      \"UPDATE schema_migrations SET dirty=false WHERE version=<版本号>\"\n\n")
	fmt.Printf("  %s 清除后重新运行：kp migrate run\n\n", colorize(colorCyan, "3."))
	fmt.Printf("%s kp 不会自动修改迁移表，请人工确认后再操作（只保护，不越权）\n",
		colorize(colorGray, "ℹ️ "))
}
