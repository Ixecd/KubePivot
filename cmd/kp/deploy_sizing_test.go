// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Ixecd/kubepivot/internal/planner"
	"github.com/Ixecd/kubepivot/internal/sizing"
	"gopkg.in/yaml.v3"
)

// TestUpdateComponentsSizingAtomic_Success 验证原子写入的核心业务语义
// 不测试 YAML 格式化细节（引号/缩进/字段顺序），只验证值是否正确
func TestUpdateComponentsSizingAtomic_Success(t *testing.T) {
	// 1. 准备临时文件
	dir := t.TempDir()
	path := filepath.Join(dir, "components.yaml")
	original := `components:
  - name: myapp
    cpu: "200m"
    memory: "256Mi"
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. 调用被测函数
	sug := &sizing.Suggestion{
		RecommendedCPU: 500,          // 500 millicores
		RecommendedMem: 512 << 20,    // 512 MiB in bytes
	}
	if err := updateComponentsSizingAtomic(path, "myapp", sug); err != nil {
		t.Fatal(err)
	}

	// 3. 验证业务语义：解析输出 + 断言字段值（不匹配字符串格式）
	//    这是「为业务写测试」的核心：验证值，不验证序列化细节
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	
	var result struct {
		Components []planner.Component `yaml:"components"`
	}
	if err := yaml.Unmarshal(data, &result); err != nil {
		t.Fatalf("parse output yaml: %v", err)
	}
	
	if len(result.Components) != 1 {
		t.Errorf("expected 1 component, got %d", len(result.Components))
	}
	
	c := result.Components[0]
	// 🔧 断言业务值，不关心是否带引号/缩进/字段顺序
	if c.CPU != "500m" {
		t.Errorf("expected CPU=500m, got %q", c.CPU)
	}
	if c.Memory != "512Mi" {
		t.Errorf("expected Memory=512Mi, got %q", c.Memory)
	}
	
	// 额外验证：原子写入无临时文件残留
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("expected temp file to be cleaned up after atomic write")
	}
}

func TestUpdateComponentsSizingAtomic_ComponentNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "components.yaml")
	original := `components:
  - name: other-app
    cpu: "200m"
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	sug := &sizing.Suggestion{RecommendedCPU: 500, RecommendedMem: 512 << 20}
	err := updateComponentsSizingAtomic(path, "nonexistent", sug)
	
	// 验证错误语义，不验证错误消息的具体格式
	if err == nil {
		t.Error("expected error for nonexistent component")
	}
	// 只验证错误包含关键信息，不匹配完整消息
	if err != nil && !containsError(err, "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestUpdateComponentsSizingAtomic_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "components.yaml")
	// 故意写非法 YAML
	if err := os.WriteFile(path, []byte("invalid: yaml: [unclosed"), 0644); err != nil {
		t.Fatal(err)
	}

	sug := &sizing.Suggestion{RecommendedCPU: 500, RecommendedMem: 512 << 20}
	err := updateComponentsSizingAtomic(path, "myapp", sug)
	
	// 验证解析失败，不验证错误消息的具体格式
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
	if err != nil && !containsError(err, "parse") {
		t.Errorf("expected parse error, got: %v", err)
	}
}

// containsError 辅助函数：验证错误消息包含关键语义（不匹配完整字符串）
func containsError(err error, substr string) bool {
	return err != nil && len(err.Error()) >= len(substr) && 
		(err.Error() == substr || len(substr) == 0 || 
		indexOf(err.Error(), substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}