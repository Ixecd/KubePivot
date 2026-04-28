// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package supplychain

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestScan_Success_CycloneDX 验证 cyclonedx-json 格式成功生成
func TestScan_Success_CycloneDX(t *testing.T) {
	origFunc := execCommandFunc
	defer func() { execCommandFunc = origFunc }()

	// Mock syft 输出：最小有效 CycloneDX JSON
	mockOutput := `{"bomFormat":"CycloneDX","specVersion":"1.4","components":[{"bom-ref":"pkg:gem/rails@7.0.0"}]}`

	execCommandFunc = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// 用 echo 模拟成功输出
		cmd := exec.CommandContext(ctx, "echo", mockOutput)
		return cmd
	}

	ctx := context.Background()
	result, err := Scan(ctx, "registry.io/app:v1", CycloneDXJSON)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Image != "registry.io/app:v1" {
		t.Errorf("expected Image=%q, got %q", "registry.io/app:v1", result.Image)
	}
	if result.Format != string(CycloneDXJSON) {
		t.Errorf("expected Format=%q, got %q", CycloneDXJSON, result.Format)
	}
	if !strings.Contains(result.Content, "CycloneDX") {
		t.Error("expected Content to contain CycloneDX marker")
	}
	if result.PackageCount != 1 {
		t.Errorf("expected PackageCount=1, got %d", result.PackageCount)
	}
}

// TestScan_InvalidFormat 验证不支持的格式返回清晰错误
func TestScan_InvalidFormat(t *testing.T) {
	ctx := context.Background()
	_, err := Scan(ctx, "registry.io/app:v1", SBOMFormat("invalid-format"))

	if err == nil {
		t.Fatal("expected error for invalid format, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported SBOM format") {
		t.Errorf("expected format error, got %v", err)
	}
}

// TestScan_SyftNotFound 验证 syft 未安装时的错误处理
func TestScan_SyftNotFound(t *testing.T) {
	origFunc := execCommandFunc
	defer func() { execCommandFunc = origFunc }()

	execCommandFunc = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// 模拟 command not found
		cmd := exec.CommandContext(ctx, "nonexistent-binary")
		return cmd
	}

	ctx := context.Background()
	_, err := Scan(ctx, "registry.io/app:v1", CycloneDXJSON)

	if err == nil {
		t.Fatal("expected error for missing syft, got nil")
	}
	if !strings.Contains(err.Error(), "syft scan failed") {
		t.Errorf("expected syft error prefix, got %v", err)
	}
}

// TestScan_Timeout 验证超时处理（30s 硬编码）
func TestScan_Timeout(t *testing.T) {
	origFunc := execCommandFunc
	defer func() { execCommandFunc = origFunc }()

	execCommandFunc = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// 创建会超时的命令
		return exec.CommandContext(ctx, "sleep", "60")
	}

	// 用 100ms 超时测试，避免真等 30s
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := Scan(ctx, "registry.io/app:v1", CycloneDXJSON)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") && !strings.Contains(err.Error(), "syft scan failed") {
		t.Errorf("expected timeout-related error, got %v", err)
	}
}

// TestExtractPackageCount 单独测试解析逻辑（纯函数）
func TestExtractPackageCount(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		format   string
		expected int
	}{
		{
			name:     "cyclonedx with 3 components",
			content:  `{"components":[{"bom-ref":"a"},{"bom-ref":"b"},{"bom-ref":"c"}]}`,
			format:   string(CycloneDXJSON),
			expected: 3,
		},
		{
			name:     "spdx with 2 packages (+1 document)",
			content:  `{"packages":[{"SPDXID":"SPDXRef-1"},{"SPDXID":"SPDXRef-2"},{"SPDXID":"SPDXRef-DOCUMENT"}]}`,
			format:   string(SPDXJSON),
			expected: 2,
		},
		{
			name:     "empty cyclonedx",
			content:  `{"components":[]}`,
			format:   string(CycloneDXJSON),
			expected: 0,
		},
		{
			name:     "malformed json",
			content:  `not json at all`,
			format:   string(CycloneDXJSON),
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPackageCount(tt.content, tt.format)
			if got != tt.expected {
				t.Errorf("extractPackageCount() = %d, want %d", got, tt.expected)
			}
		})
	}
}

// TestSBOMFormat_IsValid 验证格式校验逻辑
func TestSBOMFormat_IsValid(t *testing.T) {
	tests := []struct {
		format   SBOMFormat
		expected bool
	}{
		{CycloneDXJSON, true},
		{SPDXJSON, true},
		{Table, true},
		{"", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		if got := tt.format.IsValid(); got != tt.expected {
			t.Errorf("%q.IsValid() = %v, want %v", tt.format, got, tt.expected)
		}
	}
}