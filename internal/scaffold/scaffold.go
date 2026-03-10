package scaffold

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

type InitOptions struct {
	Name         string
	Module       string
	OutputDir    string
	TemplateRoot string
	Force        bool
	Stdout       io.Writer
}

var (
	projectNamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)
	copyEntries        = []string{
		".editorconfig",
		".gitignore",
		".gitlint",
		".golangci.yaml",
		".vscode",
		"build",
		"configs",
		"deployments",
		"docs",
		"githooks",
		"scripts",
		"templates",
		"tools",
		"docker-compose.yml",
		"LICENSE",
		"Makefile",
		"README.md",
	}
)

func InitProject(opts InitOptions) error {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return errors.New("missing project name: use --name")
	}
	if !projectNamePattern.MatchString(name) {
		return fmt.Errorf("invalid project name %q: use lowercase letters, numbers, and '-' only", name)
	}
	module := strings.TrimSpace(opts.Module)
	if module == "" {
		module = name
	}
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}

	templateRoot, err := resolveTemplateRoot(opts.TemplateRoot)
	if err != nil {
		return err
	}

	outputDir, err := resolveOutputDir(opts.OutputDir, name)
	if err != nil {
		return err
	}

	if err := ensureOutputDir(outputDir, opts.Force); err != nil {
		return err
	}

	for _, entry := range copyEntries {
		src := filepath.Join(templateRoot, entry)
		dst := filepath.Join(outputDir, entry)
		if err := copyPath(src, dst); err != nil {
			return fmt.Errorf("copy %s: %w", entry, err)
		}
	}

	if err := writeGoMod(filepath.Join(templateRoot, "go.mod"), filepath.Join(outputDir, "go.mod"), module); err != nil {
		return err
	}
	if err := writeServiceMain(filepath.Join(outputDir, "cmd", name, "main.go"), name); err != nil {
		return err
	}
	if err := writeComponentsConfig(filepath.Join(outputDir, "configs", "components.yaml"), name); err != nil {
		return err
	}

	if err := renameDir(filepath.Join(outputDir, "build", "docker", "helloworld"), filepath.Join(outputDir, "build", "docker", name)); err != nil {
		return err
	}
	if err := renameDir(filepath.Join(outputDir, "deployments", "project"), filepath.Join(outputDir, "deployments", name)); err != nil {
		return err
	}

	if err := replaceInDir(outputDir, map[string]string{
		"github.com/Ixecd/dev-toolkit": module,
		"dev-toolkit":                  name,
	}); err != nil {
		return err
	}
	if err := replaceInDir(filepath.Join(outputDir, "deployments", name), map[string]string{
		"project": name,
	}); err != nil {
		return err
	}

	fmt.Fprintf(opts.Stdout, "项目已生成：%s\n", outputDir)
	fmt.Fprintf(opts.Stdout, "模块名：%s\n", module)
	fmt.Fprintf(opts.Stdout, "入口服务：cmd/%s\n", name)
	fmt.Fprintf(opts.Stdout, "下一步：cd %s && go mod tidy\n", outputDir)
	return nil
}

func resolveTemplateRoot(explicit string) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	if env := strings.TrimSpace(os.Getenv("DTK_TEMPLATE_ROOT")); env != "" {
		return filepath.Abs(env)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get cwd: %w", err)
	}
	if isTemplateRoot(cwd) {
		return cwd, nil
	}
	return "", errors.New("template root not found: use --template or set DTK_TEMPLATE_ROOT")
}

func isTemplateRoot(path string) bool {
	required := []string{
		"Makefile",
		filepath.Join("scripts", "make-rules", "common.mk"),
		filepath.Join("githooks", "pre-commit.sh"),
	}
	for _, item := range required {
		if _, err := os.Stat(filepath.Join(path, item)); err != nil {
			return false
		}
	}
	return true
}

func resolveOutputDir(explicit, name string) (string, error) {
	if explicit == "" {
		explicit = name
	}
	return filepath.Abs(explicit)
}

func ensureOutputDir(path string, force bool) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return os.MkdirAll(path, 0o755)
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("output path exists and is not a directory: %s", path)
	}
	if force {
		return nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("output directory is not empty: %s (use --force to continue)", path)
	}
	return nil
}

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if shouldSkip(filepath.Base(src)) {
		return nil
	}
	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst)
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		base := filepath.Base(path)
		if shouldSkip(base) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func writeGoMod(templatePath, outputPath, module string) error {
	data, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("read go.mod template: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "module ") {
			lines[i] = "module " + module
			break
		}
	}
	return os.WriteFile(outputPath, []byte(strings.Join(lines, "\n")), 0o644)
}

func writeServiceMain(path, name string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	port := getenv("PORT", "8080")
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	addr := ":" + port
	fmt.Printf("service %%s listening on %%s\n", %q, addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		panic(err)
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
`, name)
	return os.WriteFile(path, []byte(content), 0o644)
}

func writeComponentsConfig(path, name string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`components:
  - name: %s
    port: 8080
    image: %s
`, name, name)
	return os.WriteFile(path, []byte(content), 0o644)
}

func renameDir(oldPath, newPath string) error {
	if _, err := os.Stat(oldPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return err
	}
	return os.Rename(oldPath, newPath)
}


func isText(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	return !bytesContainsZero(data)
}

func bytesContainsZero(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

func shouldSkip(name string) bool {
	switch name {
	case ".git", ".cursor", "_output", "node_modules", ".DS_Store":
		return true
	default:
		return false
	}
}
func replaceInDir(root string, replacements map[string]string) error {
	// 目录不存在时优雅跳过
	// 常见于模板中没有 deployments/project 子目录的情况
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !isText(data) {
			return nil
		}
		updated := string(data)
		for oldValue, newValue := range replacements {
			updated = strings.ReplaceAll(updated, oldValue, newValue)
		}
		if updated == string(data) {
			return nil
		}
		return os.WriteFile(path, []byte(updated), 0o644)
	})
}
