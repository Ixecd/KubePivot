package ai

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RepoContext 扫描仓库收集的上下文
type RepoContext struct {
	DirTree          string // 目录结构
	GoMod            string // go.mod 内容
	Services         []ServiceInfo
	ExistingPlan     string // 现有 components.yaml
	Dockerfiles      []string
	ReadmeSummary    string
	UserDescription  string // 用户补充描述（可选）
}

// ServiceInfo cmd/ 下发现的服务
type ServiceInfo struct {
	Name    string
	MainGo  string // main.go 前 50 行
}

// ScanRepo 扫描项目仓库，收集 LLM 需要的上下文
func ScanRepo(root, userDesc string) (*RepoContext, error) {
	ctx := &RepoContext{UserDescription: userDesc}

	// 目录结构
	ctx.DirTree = buildDirTree(root, 3)

	// go.mod
	if data, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		ctx.GoMod = truncate(string(data), 2000)
	}

	// cmd/ 下的服务
	ctx.Services = scanServices(root)

	// components.yaml
	if data, err := os.ReadFile(filepath.Join(root, "configs", "components.yaml")); err == nil {
		ctx.ExistingPlan = string(data)
	}

	// Dockerfile
	ctx.Dockerfiles = findDockerfiles(root)

	// README 前 50 行
	if data, err := os.ReadFile(filepath.Join(root, "README.md")); err == nil {
		lines := strings.Split(string(data), "\n")
		if len(lines) > 50 {
			lines = lines[:50]
		}
		ctx.ReadmeSummary = strings.Join(lines, "\n")
	}

	return ctx, nil
}

// buildDirTree 生成目录树，限制深度和文件数量
func buildDirTree(root string, maxDepth int) string {
	var sb strings.Builder
	sb.WriteString(filepath.Base(root) + "/\n")
	walkDir(&sb, root, "", 0, maxDepth)
	return sb.String()
}

func walkDir(sb *strings.Builder, dir, prefix string, depth, maxDepth int) {
	if depth >= maxDepth {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// 跳过不相关的目录
	skip := map[string]bool{
		".git": true, "node_modules": true, "_output": true,
		".cursor": true, "vendor": true, ".cache": true,
		"monitoring": true, "snapshots": true,
	}

	var visible []os.DirEntry
	for _, e := range entries {
		if !skip[e.Name()] {
			visible = append(visible, e)
		}
	}

	for i, entry := range visible {
		isLast := i == len(visible)-1
		connector := "├── "
		childPrefix := prefix + "│   "
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		}
		sb.WriteString(prefix + connector + entry.Name())
		if entry.IsDir() {
			sb.WriteString("/\n")
			walkDir(sb, filepath.Join(dir, entry.Name()), childPrefix, depth+1, maxDepth)
		} else {
			sb.WriteString("\n")
		}
	}
}

// scanServices 扫描 cmd/ 目录，发现所有服务
func scanServices(root string) []ServiceInfo {
	cmdDir := filepath.Join(root, "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		return nil
	}

	var services []ServiceInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		svc := ServiceInfo{Name: e.Name()}

		// 读 main.go 前 50 行
		mainPath := filepath.Join(cmdDir, e.Name(), "main.go")
		if data, err := os.ReadFile(mainPath); err == nil {
			lines := strings.Split(string(data), "\n")
			if len(lines) > 50 {
				lines = lines[:50]
			}
			svc.MainGo = strings.Join(lines, "\n")
		}

		services = append(services, svc)
	}
	return services
}

// findDockerfiles 找到所有 Dockerfile
func findDockerfiles(root string) []string {
	var files []string
	buildDir := filepath.Join(root, "build", "docker")
	entries, err := os.ReadDir(buildDir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		df := filepath.Join(buildDir, e.Name(), "Dockerfile")
		if data, err := os.ReadFile(df); err == nil {
			rel := fmt.Sprintf("build/docker/%s/Dockerfile:\n%s", e.Name(), truncate(string(data), 500))
			files = append(files, rel)
		}
	}
	return files
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... (truncated)"
}
