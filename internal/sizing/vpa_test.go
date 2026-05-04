// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package sizing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVPASuggestion 验证 VPA YAML 生成的核心业务语义
// 测试重点: 格式正确 + 关键片段存在 + 模式校验 + 分支覆盖
func TestVPASuggestion(t *testing.T) {
	sug := &Suggestion{
		RecommendedCPU: 500,       // 500m
		RecommendedMem: 512 << 20, // 512Mi
		Profile:        ProfileWeb,
		Confidence:     0.82,
		SampleCount:    672,
		SavingsCPU:     15.5,
		SavingsMem:     -10.2, // 负值=增加
	}

	// --- 分支 1: 默认模式 (Off) ---
	t.Run("default mode (Off)", func(t *testing.T) {
		yaml, err := VPASuggestion("default", "myapp", sug, "")
		if err != nil {
			t.Fatal(err)
		}
		// 验证关键片段 (不验证完整格式，避免脆弱测试)
		// 这是「为业务写测试」原则：验证语义，不验证缩进/顺序
		if !strings.Contains(yaml, `updateMode: "Off"`) {
			t.Errorf("expected updateMode=Off, got:\n%s", yaml)
		}
		if !strings.Contains(yaml, `cpu: "500m"`) {
			t.Errorf("expected cpu=500m, got:\n%s", yaml)
		}
		if !strings.Contains(yaml, `memory: "512Mi"`) {
			t.Errorf("expected memory=512Mi, got:\n%s", yaml)
		}
		if !strings.Contains(yaml, `profile: web`) {
			t.Errorf("expected profile comment, got:\n%s", yaml)
		}
		if !strings.Contains(yaml, `confidence: 0.82`) {
			t.Errorf("expected confidence comment, got:\n%s", yaml)
		}
	})

	// --- 分支 2: 显式模式 (Initial/Auto) ---
	t.Run("explicit mode: Initial", func(t *testing.T) {
		yaml, err := VPASuggestion("prod", "backend", sug, "Initial")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(yaml, `updateMode: "Initial"`) {
			t.Errorf("expected updateMode=Initial, got:\n%s", yaml)
		}
	})

	t.Run("explicit mode: Auto", func(t *testing.T) {
		yaml, err := VPASuggestion("prod", "backend", sug, "Auto")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(yaml, `updateMode: "Auto"`) {
			t.Errorf("expected updateMode=Auto, got:\n%s", yaml)
		}
	})

	// --- 分支 3: 非法模式 → 返回错误 ---
	t.Run("invalid mode", func(t *testing.T) {
		_, err := VPASuggestion("default", "myapp", sug, "Invalid")
		if err == nil {
			t.Error("expected error for invalid mode")
		}
		if err != nil && !strings.Contains(err.Error(), "invalid VPA mode") {
			t.Errorf("expected 'invalid VPA mode' in error, got: %v", err)
		}
	})

	// --- 分支 4: 空参数 → 返回错误 ---
	t.Run("empty namespace", func(t *testing.T) {
		_, err := VPASuggestion("", "myapp", sug, "Off")
		if err == nil {
			t.Error("expected error for empty namespace")
		}
	})

	t.Run("empty name", func(t *testing.T) {
		_, err := VPASuggestion("default", "", sug, "Off")
		if err == nil {
			t.Error("expected error for empty name")
		}
	})

	t.Run("nil suggestion", func(t *testing.T) {
		_, err := VPASuggestion("default", "myapp", nil, "Off")
		if err == nil {
			t.Error("expected error for nil suggestion")
		}
	})
}

// TestWriteVPASuggestion 验证原子写入逻辑 (集成测试)
// 测试重点: 临时文件 + rename 原子性 + 错误路径
func TestWriteVPASuggestion(t *testing.T) {
	sug := &Suggestion{
		RecommendedCPU: 500,
		RecommendedMem: 512 << 20,
		Profile:        ProfileWeb,
		Confidence:     0.82,
		SampleCount:    672,
	}

	// --- Case 1: 正常写入 ---
	t.Run("success write", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "vpa.yaml")

		err := WriteVPASuggestion(path, "default", "myapp", sug, "Off")
		if err != nil {
			t.Fatal(err)
		}

		// 验证文件存在 + 内容正确
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content := string(data)
		if !strings.Contains(content, `updateMode: "Off"`) {
			t.Errorf("expected updateMode=Off in file, got:\n%s", content)
		}

		// 验证原子性: 无临时文件残留
		if _, err := os.Stat(path + ".tmp"); err == nil {
			t.Error("expected temp file to be cleaned up")
		}
	})

	// --- Case 2: 非法路径 → 返回错误 ---
	// 注意: 不同系统权限行为不同，用 /proc/1 (Linux) 或 / (macOS) 模拟不可写路径
	t.Run("invalid path", func(t *testing.T) {
		// 简化: 用空字符串模拟非法路径
		err := WriteVPASuggestion("", "default", "myapp", sug, "Off")
		if err == nil {
			t.Error("expected error for empty path")
		}
		// 不验证具体错误消息，避免依赖系统行为
	})
}
