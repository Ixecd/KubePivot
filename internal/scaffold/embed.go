package scaffold

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:embedded_templates
var embeddedTemplates embed.FS

// extractEmbeddedTemplates 把内嵌模板解压到临时目录，返回路径
func extractEmbeddedTemplates() (string, error) {
	tmpDir, err := os.MkdirTemp("", "kp-templates-*")
	if err != nil {
		return "", err
	}

	err = fs.WalkDir(embeddedTemplates, "embedded_templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("embedded_templates", path)
		dst := filepath.Join(tmpDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := embeddedTemplates.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", err
	}
	return tmpDir, nil
}
