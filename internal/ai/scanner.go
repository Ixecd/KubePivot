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
	MultiLangDeps    []LangDep       // v2.0: 多语言依赖文件内容
	GPULibs          []string        // v2.0: 检测到的 GPU 库名
	DockerBaseImages []string        // v2.0: Dockerfile FROM 镜像
}

// ServiceInfo cmd/ 下发现的服务
type ServiceInfo struct {
	Name    string
	MainGo  string // main.go 前 50 行
}

// LangDep 多语言依赖文件信息。
type LangDep struct {
	Path    string // e.g. requirements.txt, package.json
	Content string // 文件内容（截断到 2000 字符）
}

// ScanRepo 扫描项目仓库，收集 LLM 需要的上下文。
// v2.0 增强：多语言依赖扫描 + GPU 库检测。
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
	ctx.DockerBaseImages = scanDockerBaseImages(root)

	// README 前 50 行
	if data, err := os.ReadFile(filepath.Join(root, "README.md")); err == nil {
		lines := strings.Split(string(data), "\n")
		if len(lines) > 50 {
			lines = lines[:50]
		}
		ctx.ReadmeSummary = strings.Join(lines, "\n")
	}

	// v2.0: 多语言依赖扫描
	ctx.MultiLangDeps = scanLangDeps(root)
	ctx.GPULibs = detectGPULibs(ctx)

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

// ─── v2.0: 多语言依赖扫描 ───────────────────────────────────────

// langDepFiles 多语言依赖文件名列表（按优先级排列）。
var langDepFiles = []string{
	"requirements.txt", "pyproject.toml", "package.json",
	"pom.xml", "Cargo.toml", "CMakeLists.txt",
}

func scanLangDeps(root string) []LangDep {
	var deps []LangDep
	for _, name := range langDepFiles {
		path := filepath.Join(root, name)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			// 提取 GPU 关键行后再截断，避免关键依赖在末尾被截掉
			gpuLines := extractGPULines(content)
			full := content
			if len(full) > 2000 {
				full = full[:2000] + "\n... (truncated)"
			}
			if gpuLines != "" {
				full = "[GPU-relevant lines]\n" + gpuLines + "\n" + full
			}
			deps = append(deps, LangDep{Path: name, Content: full})
		}
	}
	return deps
}

// extractGPULines 从依赖文件内容中提取 GPU 相关行（流式扫描，避免大文件内存压力）。
func extractGPULines(content string) string {
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		// 跳过注释行
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		for _, pattern := range gpuImportPatterns {
			if strings.Contains(trimmed, pattern) {
				lines = append(lines, trimmed)
				break
			}
		}
		// 依赖文件中 "torch" 不指定 .cuda 也视为 GPU 信号
		for _, broad := range gpuBroadPatterns {
			if strings.Contains(trimmed, broad) {
				lines = append(lines, trimmed)
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

// gpuImportPatterns GPU 相关代码库的关键词（精确匹配，用于代码扫描）。
var gpuImportPatterns = []string{
	"torch.cuda", "tensorflow", "jax", "vllm",
	"onnxruntime-gpu", "cupy", "numba.cuda",
	"nvidia.nccl", "cudf", "triton",
	"transformers", "diffusers",
}

// gpuBroadPatterns 依赖文件中的宽松匹配（torch 不写 .cuda 也是 GPU 信号）。
var gpuBroadPatterns = []string{
	"torch", "tensorflow-gpu", "jax[cuda]",
}

// detectGPULibs 跨仓库上下文检测 GPU 相关库。
func detectGPULibs(ctx *RepoContext) []string {
	found := make(map[string]bool)

	// 扫描多语言依赖
	for _, dep := range ctx.MultiLangDeps {
		for _, pattern := range gpuImportPatterns {
			if strings.Contains(dep.Content, pattern) {
				found[pattern] = true
			}
		}
		// 宽松匹配：dep 文件中的 torch 视为 GPU 信号
		for _, broad := range gpuBroadPatterns {
			if strings.Contains(dep.Content, broad) {
				found[broad] = true
			}
		}
	}

	// 扫描 Dockerfile
	for _, df := range ctx.Dockerfiles {
		for _, pattern := range gpuImportPatterns {
			if strings.Contains(df, pattern) {
				found[pattern] = true
			}
		}
	}

	// 扫描 go.mod
	for _, pattern := range gpuImportPatterns {
		if strings.Contains(ctx.GoMod, pattern) {
			found[pattern] = true
		}
	}

	libs := make([]string, 0, len(found))
	for lib := range found {
		libs = append(libs, lib)
	}
	return libs
}

// scanDockerBaseImages 提取所有 Dockerfile 的 FROM 镜像。
func scanDockerBaseImages(root string) []string {
	var images []string
	buildDir := filepath.Join(root, "build", "docker")
	entries, _ := os.ReadDir(buildDir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		df := filepath.Join(buildDir, e.Name())
		data, err := os.ReadFile(df)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToUpper(line), "FROM ") {
				img := strings.TrimSpace(line[5:])
				if img != "" {
					images = append(images, img)
				}
			}
		}
	}
	return images
}
