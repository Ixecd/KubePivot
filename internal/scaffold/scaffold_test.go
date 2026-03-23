package scaffold

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestInitProject_CleanupOnFailure 验证：
// 当 InitProject 因无效 TemplateRoot 失败时，
// 本次新建的输出目录应被自动清理。
func TestInitProject_CleanupOnFailure(t *testing.T) {
	tmp := t.TempDir()
	outputDir := filepath.Join(tmp, "new-project") // 由本次 init 创建

	err := InitProject(InitOptions{
		Name:         "new-project",
		Module:       "github.com/test/new-project",
		OutputDir:    outputDir,
		TemplateRoot: "/nonexistent-template-root", // 必然失败
		Force:        false,
		Stdout:       io.Discard,
	})

	if err == nil {
		t.Fatal("无效 TemplateRoot 应返回 error")
	}

	if _, statErr := os.Stat(outputDir); !os.IsNotExist(statErr) {
		t.Errorf("初始化失败后输出目录应被清理，但 %s 仍然存在", outputDir)
	}
}

// TestInitProject_NoCleanupWhenForce 验证：
// --force 时目录已存在，失败后不清理（保护用户已有文件）。
func TestInitProject_NoCleanupWhenForce(t *testing.T) {
	tmp := t.TempDir()
	outputDir := filepath.Join(tmp, "existing-project")

	// 预先创建目录并写入文件，模拟已有项目
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outputDir, "important.txt")
	if err := os.WriteFile(sentinel, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := InitProject(InitOptions{
		Name:         "existing-project",
		Module:       "github.com/test/existing-project",
		OutputDir:    outputDir,
		TemplateRoot: "/nonexistent-template-root", // 必然失败
		Force:        true,
		Stdout:       io.Discard,
	})

	if err == nil {
		t.Fatal("无效 TemplateRoot 应返回 error")
	}

	// 目录必须还在
	if _, statErr := os.Stat(outputDir); os.IsNotExist(statErr) {
		t.Errorf("--force 模式下失败不应清理已有目录 %s", outputDir)
	}
	// 原有文件必须还在
	if _, statErr := os.Stat(sentinel); os.IsNotExist(statErr) {
		t.Errorf("--force 模式下失败不应删除已有文件 %s", sentinel)
	}
}

// TestInitProject_NoCleanupWhenDirPreexisted 验证：
// 目录已存在但未用 --force（会被 ensureOutputDir 拒绝），
// 不应清理目录（因为不是本次创建的）。
func TestInitProject_NoCleanupWhenDirPreexisted(t *testing.T) {
	tmp := t.TempDir()
	outputDir := filepath.Join(tmp, "preexist")

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outputDir, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 不加 --force，ensureOutputDir 直接返回 error
	err := InitProject(InitOptions{
		Name:         "preexist",
		OutputDir:    outputDir,
		TemplateRoot: "/nonexistent",
		Force:        false,
		Stdout:       io.Discard,
	})

	if err == nil {
		t.Fatal("已存在目录且不加 --force 应返回 error")
	}

	if _, statErr := os.Stat(sentinel); os.IsNotExist(statErr) {
		t.Errorf("目录非本次创建，失败后不应被清理，但 %s 不见了", sentinel)
	}
}
