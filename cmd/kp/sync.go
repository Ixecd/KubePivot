package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ixecd/kubepivot/internal/scaffold"
)

// syncCategory 文件同步策略
type syncCategory int

const (
	syncForce  syncCategory = iota // 强制覆盖
	syncMerge                      // 合并更新（只追加新 key）
	syncNotify                     // 提示用户，不自动修改
	syncSkip                       // 永远不动
)

// syncRule 同步规则
type syncRule struct {
	pattern  string
	category syncCategory
	desc     string
}

var syncRules = []syncRule{
	// 强制覆盖：kp 完全管理
	{"Makefile", syncForce, "构建系统"},
	{"scripts/make-rules/", syncForce, "make-rules"},
	{".githooks/", syncForce, "git hooks"},

	// 合并更新：只追加新 key
	{"configs/project.env", syncMerge, "项目配置（新增字段）"},

	// 提示用户：可能有结构变化
	{"deployments/", syncNotify, "helm charts（请人工确认）"},
	{"configs/system.yaml", syncForce, "系统配置"},
	{"configs/components.yaml", syncNotify, "服务配置（请人工确认）"},
	{"configs/resources.yaml", syncNotify, "资源配置（请人工确认）"},

	// 永远不动
	{"cmd/", syncSkip, "业务入口"},
	{"internal/", syncSkip, "业务逻辑"},
	{"migrations/", syncSkip, "数据库迁移"},
	{"go.mod", syncSkip, "Go 模块"},
	{"go.sum", syncSkip, "Go 依赖锁"},
	{"test/", syncSkip, "测试代码"},
}

func runSync(args []string) {
	flags := flag.NewFlagSet("sync", flag.ExitOnError)
	dryRun := flags.Bool("dry-run", false, "预览变更，不实际执行")
	only := flags.String("only", "", "只同步指定类型：scripts | makefile | hooks | env")
	flags.Parse(args)

	root, err := Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	// 提取内嵌模板到临时目录
	tmpDir, err := scaffold.ExtractEmbeddedTemplates()
	if err != nil {
		fmt.Fprintln(os.Stderr, "提取内嵌模板失败:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	if *dryRun {
		fmt.Printf("%s dry-run 模式，以下是将要执行的变更：\n\n",
			colorize(colorYellow, "📋"))
	} else {
		fmt.Printf("%s 同步框架文件（kp %s）\n\n",
			colorize(colorCyan, "🔄"), kpVersion)
	}

	var (
		updated  []string
		skipped  []string
		notified []string
	)

	for _, rule := range syncRules {
		// --only 过滤
		if *only != "" && !matchOnly(*only, rule.pattern) {
			continue
		}

		switch rule.category {
		case syncSkip:
			continue

		case syncForce:
			src := filepath.Join(tmpDir, rule.pattern)
			dst := filepath.Join(root, rule.pattern)

			// 检查源是否存在
			if _, err := os.Stat(src); os.IsNotExist(err) {
				continue
			}

			changed, err := syncForceFiles(src, dst, *dryRun)
			if err != nil {
				P.Info("⚠️ ", fmt.Sprintf("同步 %s 失败: %v", rule.pattern, err))
				continue
			}
			if changed {
				updated = append(updated, rule.pattern)
				if *dryRun {
					fmt.Printf("  %s %-35s %s\n",
						colorize(colorGreen, "✓ 更新"),
						rule.pattern, rule.desc)
				} else {
					P.Done(fmt.Sprintf("更新 %s（%s）", rule.pattern, rule.desc))
				}
			} else {
				skipped = append(skipped, rule.pattern)
			}

		case syncMerge:
			src := filepath.Join(tmpDir, rule.pattern)
			dst := filepath.Join(root, rule.pattern)

			added, err := syncMergeEnv(src, dst, *dryRun)
			if err != nil {
				P.Info("⚠️ ", fmt.Sprintf("合并 %s 失败: %v", rule.pattern, err))
				continue
			}
			if len(added) > 0 {
				updated = append(updated, rule.pattern)
				if *dryRun {
					fmt.Printf("  %s %-35s 新增 %d 个字段: %s\n",
						colorize(colorGreen, "✓ 合并"),
						rule.pattern, len(added), strings.Join(added, ", "))
				} else {
					P.Done(fmt.Sprintf("合并 %s（新增: %s）", rule.pattern, strings.Join(added, ", ")))
				}
			} else {
				skipped = append(skipped, rule.pattern)
			}

		case syncNotify:
			notified = append(notified, rule.pattern)
			fmt.Printf("  %s %-35s %s\n",
				colorize(colorYellow, "⚠ 请确认"),
				rule.pattern, rule.desc)
		}
	}

	// 汇总
	fmt.Println()
	if len(updated) > 0 {
		if *dryRun {
			fmt.Printf("  将更新 %d 个文件/目录\n", len(updated))
		} else {
			P.Info("✅", fmt.Sprintf("已更新 %d 个文件/目录", len(updated)))
		}
	}
	if len(notified) > 0 {
		fmt.Printf("  %s %d 个文件需人工确认（运行 kp diff 查看差异）\n",
			colorize(colorYellow, "⚠️ "), len(notified))
	}
	if len(updated) == 0 && len(notified) == 0 {
		P.Info("✅", "框架文件已是最新版本，无需更新")
	}
}

// syncForceFiles 强制覆盖文件或目录，返回是否有变更
func syncForceFiles(src, dst string, dryRun bool) (bool, error) {
	info, err := os.Stat(src)
	if err != nil {
		return false, nil
	}

	if info.IsDir() {
		return syncForceDir(src, dst, dryRun)
	}

	// 单文件：对比内容
	srcData, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}
	dstData, _ := os.ReadFile(dst)
	if string(srcData) == string(dstData) {
		return false, nil // 内容相同，无需更新
	}
	if dryRun {
		return true, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(dst, srcData, 0o644)
}

// syncForceDir 强制同步目录
func syncForceDir(src, dst string, dryRun bool) (bool, error) {
	changed := false
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		dstPath := filepath.Join(dst, rel)

		if info.IsDir() {
			if !dryRun {
				return os.MkdirAll(dstPath, 0o755)
			}
			return nil
		}

		srcData, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		dstData, _ := os.ReadFile(dstPath)
		if string(srcData) == string(dstData) {
			return nil
		}
		changed = true
		if dryRun {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dstPath, srcData, info.Mode())
	})
	return changed, err
}

// syncMergeEnv 合并 project.env：只追加新 key，不覆盖已有值
func syncMergeEnv(src, dst string, dryRun bool) ([]string, error) {
	// 读取模板 key
	srcKeys, err := readEnvKeys(src)
	if err != nil {
		return nil, nil // 模板没有 env 文件，跳过
	}

	// 读取现有 key
	dstKeys, _ := readEnvKeys(dst)

	// 找出新增的 key
	var added []string
	for k, v := range srcKeys {
		if _, exists := dstKeys[k]; !exists {
			added = append(added, k)
			if !dryRun {
				// 追加到文件末尾
				f, err := os.OpenFile(dst, os.O_APPEND|os.O_WRONLY, 0o644)
				if err != nil {
					continue
				}
				fmt.Fprintf(f, "\n# 由 kp sync 新增\n%s=%s\n", k, v)
				f.Close()
			}
		}
	}
	return added, nil
}

// readEnvKeys 读取 .env 文件的 key=value 对
func readEnvKeys(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return result, nil
}

// matchOnly 检查 --only 过滤条件
func matchOnly(only, pattern string) bool {
	switch only {
	case "scripts":
		return strings.Contains(pattern, "scripts/")
	case "makefile":
		return pattern == "Makefile"
	case "hooks":
		return strings.Contains(pattern, ".githooks")
	case "env":
		return strings.Contains(pattern, ".env")
	}
	return true
}
