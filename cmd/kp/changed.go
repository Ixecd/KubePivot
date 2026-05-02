package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/Ixecd/kubepivot/internal/executor"
	"github.com/Ixecd/kubepivot/internal/planner"
)

func detectChangedServices(root string, plans []planner.Plan) (map[string]bool, error) {
	ctx := context.Background()
	out, err := executor.GetExecutor().Generic(ctx, "git", "", "-C", root, "diff", "--name-only", "HEAD~1", "HEAD")
	if err != nil {
		slog.Debug("git diff fail", "err", err)
		return nil, nil
	}
	slog.Debug("git diff files", "count", len(strings.Fields(string(out))))

	files := strings.Fields(strings.TrimSpace(string(out)))
	if len(files) == 0 {
		return map[string]bool{}, nil
	}
	return classifyChangedFiles(files, plans), nil
}

// filterChangedPlans 过滤出有变更的服务，保留拓扑依赖
func filterChangedPlans(plans []planner.Plan, changed map[string]bool) []planner.Plan {
	if changed == nil {
		return plans // 全量
	}

	var filtered []planner.Plan
	for _, p := range plans {
		if changed[p.Name] {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

// printChangedSummary 打印变更摘要
func printChangedSummary(all []planner.Plan, changed map[string]bool) {
	if changed == nil {
		P.Info("🔄", "检测到全局变更（configs/go.mod/internal），全量部署")
		return
	}
	if len(changed) == 0 {
		P.Info("✅", "未检测到服务变更，跳过部署")
		return
	}

	names := make([]string, 0, len(changed))
	for k := range changed {
		names = append(names, k)
	}
	P.Info("🎯", fmt.Sprintf("增量部署（%d/%d 个服务有变更）: %s",
		len(changed), len(all), strings.Join(names, ", ")))

	// 列出跳过的服务
	var skipped []string
	for _, p := range all {
		if p.Image != "" && !changed[p.Name] {
			skipped = append(skipped, p.Name)
		}
	}
	if len(skipped) > 0 {
		fmt.Printf("  %s 跳过（无变更）: %s\n",
			colorize(colorGray, "⏭ "),
			strings.Join(skipped, ", "))
	}
}

// hasGitHistory 检查是否有足够的 git 历史（至少 2 个 commit）
func hasGitHistory(root string) bool {
	ctx := context.Background()
	out, err := executor.GetExecutor().Generic(ctx, "git", "", "-C", root, "rev-list", "--count", "HEAD")
	if err != nil {
		return false
	}

	count := strings.TrimSpace(string(out))
	return count != "0" && count != "1"
}

// resolveChangedPath 用于测试的辅助函数
func resolveChangedPath(f string) string {
	return filepath.ToSlash(f)
}

// classifyChangedFiles 对变更文件列表进行分类，返回需要部署的服务集合
// nil = 全量部署，空 map = 无需部署
func classifyChangedFiles(files []string, plans []planner.Plan) map[string]bool {
	changed := make(map[string]bool)
	for _, f := range files {
		f = resolveChangedPath(f)

		// 全局变更 → 全量
		if strings.HasPrefix(f, "configs/") ||
			f == "go.mod" || f == "go.sum" ||
			strings.HasPrefix(f, "scripts/") ||
			strings.HasPrefix(f, "build/") {
			return nil
		}

		// internal/** → 所有有 image 的服务
		if strings.HasPrefix(f, "internal/") {
			for _, p := range plans {
				if p.Image != "" {
					changed[p.Name] = true
				}
			}
			continue
		}

		// cmd/<service>/**
		if strings.HasPrefix(f, "cmd/") {
			parts := strings.SplitN(f, "/", 3)
			if len(parts) >= 2 {
				svcName := parts[1]
				for _, p := range plans {
					if p.Name == svcName || p.Image == svcName {
						changed[p.Name] = true
					}
				}
			}
			continue
		}

		// deployments/<project>/<service>/**
		if strings.HasPrefix(f, "deployments/") {
			parts := strings.SplitN(f, "/", 4)
			if len(parts) >= 3 {
				svcName := parts[2]
				changed[svcName] = true
			}
			continue
		}
	}
	return changed
}
