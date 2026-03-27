package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── replaceInDir ──────────────────────────────────────────────────────────────

func TestReplaceInDir_SingleReplacement(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package github.com/Ixecd/dev-toolkit")

	err := replaceInDir(dir, map[string]string{
		"github.com/Ixecd/dev-toolkit": "github.com/me/myapp",
	})
	require.NoError(t, err)
	assertFileContains(t, dir, "main.go", "github.com/me/myapp")
	assertFileNotContains(t, dir, "main.go", "dev-toolkit")
}

func TestReplaceInDir_MultipleReplacements(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.go", "dev-toolkit github.com/Ixecd/dev-toolkit")

	err := replaceInDir(dir, map[string]string{
		"github.com/Ixecd/dev-toolkit": "github.com/me/myapp",
		"dev-toolkit":                  "myapp",
	})
	require.NoError(t, err)
	content := readFile(t, dir, "config.go")
	assert.Contains(t, content, "myapp")
	assert.Contains(t, content, "github.com/me/myapp")
	assert.NotContains(t, content, "dev-toolkit")
}

func TestReplaceInDir_NestedFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, filepath.Join("internal", "api", "handler.go"),
		"import \"github.com/Ixecd/dev-toolkit/internal\"")

	err := replaceInDir(dir, map[string]string{
		"github.com/Ixecd/dev-toolkit": "github.com/me/myapp",
	})
	require.NoError(t, err)
	assertFileContains(t, dir, filepath.Join("internal", "api", "handler.go"),
		"github.com/me/myapp")
}

func TestReplaceInDir_SkipBinaryFiles(t *testing.T) {
	dir := t.TempDir()
	// 写一个包含 null byte 的"二进制"文件
	path := filepath.Join(dir, "binary.bin")
	require.NoError(t, os.WriteFile(path, []byte("dev-toolkit\x00binary"), 0644))

	err := replaceInDir(dir, map[string]string{"dev-toolkit": "myapp"})
	require.NoError(t, err)

	data, _ := os.ReadFile(path)
	assert.Contains(t, string(data), "dev-toolkit", "二进制文件不应被替换")
}

func TestReplaceInDir_NoMatchNoChange(t *testing.T) {
	dir := t.TempDir()
	original := "package main\n\nfunc main() {}"
	writeFile(t, dir, "main.go", original)

	err := replaceInDir(dir, map[string]string{"notexist": "replacement"})
	require.NoError(t, err)
	assert.Equal(t, original, readFile(t, dir, "main.go"))
}

func TestReplaceInDir_NonexistentDirSkipped(t *testing.T) {
	err := replaceInDir("/nonexistent/path", map[string]string{"a": "b"})
	assert.NoError(t, err, "不存在的目录应该优雅跳过")
}

func TestReplaceInDir_SkipGitDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, filepath.Join(".git", "config"), "dev-toolkit")

	err := replaceInDir(dir, map[string]string{"dev-toolkit": "myapp"})
	require.NoError(t, err)
	assertFileContains(t, dir, filepath.Join(".git", "config"), "dev-toolkit")
}

// ── fixChartYAMLs ─────────────────────────────────────────────────────────────

func TestFixChartYAMLs_RemovesDependencies(t *testing.T) {
	dir := t.TempDir()
	chartDir := filepath.Join(dir, "deployments", "myapp")
	require.NoError(t, os.MkdirAll(chartDir, 0755))

	content := `apiVersion: v2
name: myapp
dependencies:
  - name: postgresql
    version: 12.x.x
    repository: https://charts.bitnami.com
maintainers:
  - name: qc
`
	writeFile(t, chartDir, "Chart.yaml", content)

	fixChartYAMLs(dir, "myapp")

	result := readFile(t, chartDir, "Chart.yaml")
	assert.Contains(t, result, "dependencies: []")
	assert.NotContains(t, result, "postgresql")
	assert.Contains(t, result, "maintainers:")
}

func TestFixChartYAMLs_NoDependencies_NoChange(t *testing.T) {
	dir := t.TempDir()
	chartDir := filepath.Join(dir, "deployments", "myapp")
	require.NoError(t, os.MkdirAll(chartDir, 0755))

	content := `apiVersion: v2
name: myapp
version: 0.1.0
`
	writeFile(t, chartDir, "Chart.yaml", content)

	fixChartYAMLs(dir, "myapp")

	result := readFile(t, chartDir, "Chart.yaml")
	assert.Equal(t, content, result)
}

func TestFixChartYAMLs_FileNotExist_NoError(t *testing.T) {
	// 不应 panic 或报错
	fixChartYAMLs(t.TempDir(), "myapp")
}

// ── writeResourcesConfig ──────────────────────────────────────────────────────

func TestWriteResourcesConfig_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	configsDir := filepath.Join(dir, "configs")
	require.NoError(t, os.MkdirAll(configsDir, 0755))

	err := writeResourcesConfig(dir, "myapp")
	require.NoError(t, err)

	content := readFile(t, configsDir, "resources.yaml")
	assert.Contains(t, content, "kind: Deployment")
	assert.Contains(t, content, "name: myapp")
	assert.Contains(t, content, "on-missing: auto-heal")
	assert.Contains(t, content, "max-retry: 3")
}

func TestWriteResourcesConfig_ContainsExamples(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "configs"), 0755))

	err := writeResourcesConfig(dir, "myapp")
	require.NoError(t, err)

	content := readFile(t, filepath.Join(dir, "configs"), "resources.yaml")
	assert.Contains(t, content, "StatefulSet")
	assert.Contains(t, content, "PersistentVolumeClaim")
	assert.Contains(t, content, "alert")
}

func TestWriteResourcesConfig_ProjectNameInjected(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "configs"), 0755))

	err := writeResourcesConfig(dir, "web3-blitz")
	require.NoError(t, err)

	content := readFile(t, filepath.Join(dir, "configs"), "resources.yaml")
	assert.Contains(t, content, "name: web3-blitz")
}

// ── renderHandoff ─────────────────────────────────────────────────────────────

func TestRenderHandoff_ContainsProjectInfo(t *testing.T) {
	result := renderHandoff("myapp", "github.com/me/myapp", false)

	assert.Contains(t, result, "github.com/me/myapp")
	assert.Contains(t, result, "myapp")
	assert.Contains(t, result, "写给下一个 Claude")
}

func TestRenderHandoff_ContainsDate(t *testing.T) {
	result := renderHandoff("myapp", "github.com/me/myapp", false)
	// 日期格式 2006-01-02
	assert.Regexp(t, `\d{4}-\d{2}-\d{2}`, result)
}

func TestRenderHandoff_WithFrontend(t *testing.T) {
	withFE := renderHandoff("myapp", "github.com/me/myapp", true)
	withoutFE := renderHandoff("myapp", "github.com/me/myapp", false)

	assert.Contains(t, withFE, "web/")
	assert.NotContains(t, withoutFE, "web/")
	assert.Contains(t, withoutFE, "docs/")
}

func TestRenderHandoff_ContainsCommands(t *testing.T) {
	result := renderHandoff("myapp", "github.com/me/myapp", false)

	assert.Contains(t, result, "dtk deploy")
	assert.Contains(t, result, "dtk resume")
	assert.Contains(t, result, "dtk rollback")
	assert.Contains(t, result, "dtk release")
}

func TestRenderHandoff_ContainsDirTree(t *testing.T) {
	result := renderHandoff("myapp", "github.com/me/myapp", false)

	assert.Contains(t, result, "cmd/")
	assert.Contains(t, result, "internal/")
	assert.Contains(t, result, "configs/")
	assert.Contains(t, result, "deployments/")
	assert.Contains(t, result, "handoff/")
}

// ── friendlyPath ──────────────────────────────────────────────────────────────

func TestFriendlyPath_HomeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	path := filepath.Join(home, "myapp")
	result := friendlyPath(path)
	assert.Equal(t, "~/myapp", result)
}

func TestFriendlyPath_HomeRoot(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	result := friendlyPath(home)
	assert.Equal(t, "~", result)
}

func TestFriendlyPath_RelativeFromCwd(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)

	path := filepath.Join(cwd, "subdir", "myapp")
	result := friendlyPath(path)

	// 结果应该包含 subdir/myapp，不管是相对路径还是 ~/... 形式
	assert.Contains(t, result, "subdir/myapp")
}

func TestFriendlyPath_AbsoluteWhenNoMatch(t *testing.T) {
	path := "/tmp/some/absolute/path"
	result := friendlyPath(path)
	// 如果不在 home 下也不在 cwd 下，返回绝对路径
	assert.True(t, filepath.IsAbs(result) || strings.HasPrefix(result, "~"))
}

// ── isText / shouldSkip ───────────────────────────────────────────────────────

func TestIsText_ValidUTF8(t *testing.T) {
	assert.True(t, isText([]byte("package main\n")))
	assert.True(t, isText([]byte("你好世界")))
	assert.True(t, isText([]byte("")))
}

func TestIsText_BinaryWithNullByte(t *testing.T) {
	assert.False(t, isText([]byte("hello\x00world")))
}

func TestIsText_InvalidUTF8(t *testing.T) {
	assert.False(t, isText([]byte{0xff, 0xfe, 0x00}))
}

func TestShouldSkip(t *testing.T) {
	cases := []struct {
		name     string
		expected bool
	}{
		{".git", true},
		{".cursor", true},
		{"_output", true},
		{"node_modules", true},
		{".DS_Store", true},
		{"main.go", false},
		{"configs", false},
		{"deployments", false},
		{".gitignore", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, shouldSkip(tc.name))
		})
	}
}

// ── ensureOutputDir ───────────────────────────────────────────────────────────

func TestEnsureOutputDir_CreatesIfNotExist(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "newdir")
	err := ensureOutputDir(dir, false, "myapp")
	assert.NoError(t, err)
	_, statErr := os.Stat(dir)
	assert.NoError(t, statErr)
}

func TestEnsureOutputDir_ExistingNonEmptyWithoutForce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "existing.go", "package main")

	err := ensureOutputDir(dir, false, "myapp")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--force")
}

func TestEnsureOutputDir_ExistingWithForce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "existing.go", "package main")

	err := ensureOutputDir(dir, true, "myapp")
	assert.NoError(t, err)
}

func TestEnsureOutputDir_NotADirectory(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("content"), 0644))

	err := ensureOutputDir(filePath, false, "myapp")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不是目录")
}

// ── 工具函数 ──────────────────────────────────────────────────────────────────

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	return string(data)
}

func assertFileContains(t *testing.T, dir, name, substr string) {
	t.Helper()
	content := readFile(t, dir, name)
	assert.Contains(t, content, substr)
}

func assertFileNotContains(t *testing.T, dir, name, substr string) {
	t.Helper()
	content := readFile(t, dir, name)
	assert.NotContains(t, content, substr)
}
