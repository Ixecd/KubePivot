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

// TestUpdateComponentsSizingBatch_Success 验证批量原子写入的核心业务语义
// 业务场景: 多 Pod 同时优化 → 一次性解析/修改/写入，避免多次读写竞争
// 测试重点: 值是否正确 + 原子性 (无临时文件残留)，不测试 YAML 格式化细节
func TestUpdateComponentsSizingBatch_Success(t *testing.T) {
	// 1. 准备临时文件 (模拟真实 components.yaml)
	dir := t.TempDir()
	path := filepath.Join(dir, "components.yaml")
	original := `components:
  - name: backend
    cpu: "200m"
    memory: "256Mi"
  - name: frontend
    cpu: "100m"
    memory: "128Mi"
  - name: worker
    cpu: "500m"
    memory: "512Mi"
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. 构造批量更新建议 (模拟 sizing.Compute 返回)
	//    业务语义: backend + worker 需要优化，frontend 保持原样
	updates := map[string]*sizing.Suggestion{
		"backend": {
			RecommendedCPU: 400,       // 200m → 400m
			RecommendedMem: 512 << 20, // 256Mi → 512Mi
		},
		"worker": {
			RecommendedCPU: 800,        // 500m → 800m
			RecommendedMem: 1024 << 20, // 512Mi → 1Gi
		},
		// frontend 不在 updates 中 → 保持原值
	}

	// 3. 调用被测函数 (批量原子写入)
	if err := updateComponentsSizingBatch(path, updates); err != nil {
		t.Fatal(err)
	}

	// 4. 验证业务语义：解析输出 + 断言字段值（不匹配字符串格式）
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

	// 验证组件数量不变
	if len(result.Components) != 3 {
		t.Errorf("expected 3 components, got %d", len(result.Components))
	}

	// 验证每个组件的值 (按 name 查找，不依赖顺序)
	components := make(map[string]planner.Component)
	for _, c := range result.Components {
		components[c.Name] = c
	}

	// backend: 应被更新
	if c, ok := components["backend"]; ok {
		if c.CPU != "400m" {
			t.Errorf("backend: expected CPU=400m, got %q", c.CPU)
		}
		if c.Memory != "512Mi" {
			t.Errorf("backend: expected Memory=512Mi, got %q", c.Memory)
		}
	} else {
		t.Error("backend component not found in output")
	}

	// frontend: 应保持不变 (不在 updates 中)
	if c, ok := components["frontend"]; ok {
		if c.CPU != "100m" {
			t.Errorf("frontend: expected CPU=100m (unchanged), got %q", c.CPU)
		}
		if c.Memory != "128Mi" {
			t.Errorf("frontend: expected Memory=128Mi (unchanged), got %q", c.Memory)
		}
	} else {
		t.Error("frontend component not found in output")
	}

	// worker: 应被更新
	if c, ok := components["worker"]; ok {
		if c.CPU != "800m" {
			t.Errorf("worker: expected CPU=800m, got %q", c.CPU)
		}
		if c.Memory != "1Gi" { // 1024Mi = 1Gi，yaml.Marshal 可能简化单位
			// 兼容两种格式: "1024Mi" 或 "1Gi"
			if c.Memory != "1024Mi" && c.Memory != "1Gi" {
				t.Errorf("worker: expected Memory=1024Mi/1Gi, got %q", c.Memory)
			}
		}
	} else {
		t.Error("worker component not found in output")
	}

	// 验证原子写入: 无临时文件残留
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("expected temp file to be cleaned up after atomic write")
	}
}

// TestUpdateComponentsSizingBatch_PartialSuccess 验证部分 Pod 不存在时的行为
// 业务场景: updates 包含不存在的组件名 → 跳过 + 不报错 (幂等性)
// 测试重点: 存在的组件被正确更新，不存在的被静默跳过
func TestUpdateComponentsSizingBatch_PartialSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "components.yaml")
	original := `components:
  - name: existing-app
    cpu: "200m"
    memory: "256Mi"
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	// updates 包含: 1 个存在 + 2 个不存在
	updates := map[string]*sizing.Suggestion{
		"existing-app": {
			RecommendedCPU: 400,
			RecommendedMem: 512 << 20,
		},
		"nonexistent-1": {RecommendedCPU: 100, RecommendedMem: 128 << 20},
		"nonexistent-2": {RecommendedCPU: 200, RecommendedMem: 256 << 20},
	}

	// 调用: 不应返回错误 (部分成功 = 成功)
	if err := updateComponentsSizingBatch(path, updates); err != nil {
		t.Errorf("expected no error for partial success, got: %v", err)
	}

	// 验证: 存在的组件被更新
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
		t.Errorf("expected 1 component (unchanged count), got %d", len(result.Components))
	}
	c := result.Components[0]
	if c.CPU != "400m" {
		t.Errorf("expected CPU=400m, got %q", c.CPU)
	}
	if c.Memory != "512Mi" {
		t.Errorf("expected Memory=512Mi, got %q", c.Memory)
	}

	// 验证: 无临时文件残留
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("expected temp file to be cleaned up")
	}
}

// TestUpdateComponentsSizingBatch_InvalidYAML 验证非法 YAML 时的清晰错误
// 业务场景: components.yaml 损坏 → 返回可理解的错误，不崩溃
// 测试重点: 错误消息包含关键语义 ("parse")，便于用户排查
func TestUpdateComponentsSizingBatch_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "components.yaml")
	// 故意写非法 YAML
	if err := os.WriteFile(path, []byte("invalid: yaml: [unclosed"), 0644); err != nil {
		t.Fatal(err)
	}

	updates := map[string]*sizing.Suggestion{
		"any-app": {RecommendedCPU: 500, RecommendedMem: 512 << 20},
	}

	err := updateComponentsSizingBatch(path, updates)

	// 验证: 返回错误，且消息包含关键语义
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
	// 只验证错误包含关键信息，不匹配完整消息 (避免依赖具体实现)
	if err != nil && !containsError(err, "parse") {
		t.Errorf("expected 'parse' in error message, got: %v", err)
	}
}

// TestUpdateComponentsSizingBatch_EmptyUpdates 验证空更新列表的幂等性
// 业务场景: 无组件需要优化 → 文件业务值不变 + 无错误
// 测试重点: 函数幂等，不产生副作用；验证业务值，不验证序列化格式
func TestUpdateComponentsSizingBatch_EmptyUpdates(t *testing.T) {
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

	// 空更新列表
	updates := map[string]*sizing.Suggestion{}

	// 调用: 不应返回错误
	if err := updateComponentsSizingBatch(path, updates); err != nil {
		t.Errorf("expected no error for empty updates, got: %v", err)
	}

	// ✅ 修复: 解析输出 YAML 后断言业务值，不比较原始字符串
	// 原理: yaml.Marshal 可能重排字段/填充默认值，但业务语义应不变
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

	// 验证: 组件数量不变
	if len(result.Components) != 1 {
		t.Errorf("expected 1 component, got %d", len(result.Components))
	}

	// 验证: 业务值不变 (不关心字段顺序/缩进/默认值填充)
	c := result.Components[0]
	if c.Name != "myapp" {
		t.Errorf("expected name=myapp, got %q", c.Name)
	}
	if c.CPU != "200m" {
		t.Errorf("expected CPU=200m (unchanged), got %q", c.CPU)
	}
	if c.Memory != "256Mi" {
		t.Errorf("expected Memory=256Mi (unchanged), got %q", c.Memory)
	}
	// 其他字段 (Type/Image/Port 等) 可能被 yaml.Marshal 填充默认值，不验证
}

// containsError 辅助函数：验证错误消息包含关键语义（不匹配完整字符串）
// 设计原则: 测试业务语义，不测试错误消息的具体格式
func containsError(err error, substr string) bool {
	if err == nil || substr == "" {
		return false
	}
	msg := err.Error()
	// 简单子串匹配，不依赖 strings 包 (避免跨文件重复)
	for i := 0; i <= len(msg)-len(substr); i++ {
		if msg[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
