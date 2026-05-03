package scaffold

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:embedded_templates
var embeddedTemplates embed.FS

//go:embed all:templates/python
var pythonAppTemplates embed.FS

//go:embed all:templates/java
var javaAppTemplates embed.FS

//go:embed all:templates/rust
var rustAppTemplates embed.FS

//go:embed all:templates/cpp
var cppAppTemplates embed.FS

//go:embed all:templates/cs
var csAppTemplates embed.FS

//go:embed all:templates/zig
var zigAppTemplates embed.FS

//go:embed all:templates/kotlin
var kotlinAppTemplates embed.FS

//go:embed all:templates/ts
var tsAppTemplates embed.FS

//go:embed all:templates/php
var phpAppTemplates embed.FS

//go:embed all:templates/swift
var swiftAppTemplates embed.FS

//go:embed all:templates/lua
var luaAppTemplates embed.FS

// extractEmbeddedTemplates 把内嵌模板解压到临时目录，返回路径
func ExtractEmbeddedTemplates() (string, error) {
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
