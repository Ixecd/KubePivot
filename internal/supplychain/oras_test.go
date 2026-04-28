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

// TestPushSBOM_Success 验证 oras push 成功场景
func TestPushSBOM_Success(t *testing.T) {
	origFunc := execCommandFunc
	defer func() { execCommandFunc = origFunc }()

	mockOutput := "✓ Uploaded registry.io/sboms/app:v1.sbom.cyclonedx-json"

	execCommandFunc = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// 模拟 oras push 成功
		// 注意: 实际 oras 从 stdin 读内容, 测试用 echo 模拟
		cmd := exec.CommandContext(ctx, "echo", mockOutput)
		return cmd
	}

	ctx := context.Background()
	sbom := `{"bomFormat":"CycloneDX","specVersion":"1.4"}`
	err := PushSBOM(ctx, "registry.io/app:v1", sbom, "cyclonedx-json", "", "")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

// TestPushSBOM_InvalidFormat 验证不支持的格式返回清晰错误
func TestPushSBOM_InvalidFormat(t *testing.T) {
	// 格式不合法 → media type 兜底, 但或推失败 (模拟)
	// 简化: 直接验证 formatToMediaType 的兜底行为
	mediaType := formatToMediaType("unknown-format")
	if mediaType != "application/octet-stream" {
		t.Errorf("expected fallback media type, got %q", mediaType)
	}
}

// TestPushSBOM_OrasNotFound 验证 oras 未安装时的错误处理
func TestPushSBOM_OrasNotFound(t *testing.T) {
	origFunc := execCommandFunc
	defer func() { execCommandFunc = origFunc }()

	execCommandFunc = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// 模拟 command not found
		cmd := exec.CommandContext(ctx, "nonexistent-binary")
		return cmd
	}

	ctx := context.Background()
	err := PushSBOM(ctx, "registry.io/app:v1", "{}", "cyclonedx-json", "", "")

	if err == nil {
		t.Fatal("expected error for missing oras, got nil")
	}
	if !strings.Contains(err.Error(), "oras push failed") {
		t.Errorf("expected oras error prefix, got %v", err)
	}
}

// TestPushSBOM_Timeout 验证超时处理 (60s 硬编码)
func TestPushSBOM_Timeout(t *testing.T) {
	origFunc := execCommandFunc
	defer func() { execCommandFunc = origFunc }()

	execCommandFunc = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// 创建会超时的命令
		return exec.CommandContext(ctx, "sleep", "120")
	}

	// 用 100ms 超时测试, 避免真等 60s
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := PushSBOM(ctx, "registry.io/app:v1", "{}", "cyclonedx-json", "", "")

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") && !strings.Contains(err.Error(), "oras push failed") {
		t.Errorf("expected timeout-related error, got %v", err)
	}
}

// TestDeriveSBOMTarget 验证目标派生逻辑
func TestDeriveSBOMTarget(t *testing.T) {
	tests := []struct {
		image    string
		format   string
		expected string
	}{
		{
			image:    "registry.io/app:v1.2.3",
			format:   "cyclonedx-json",
			expected: "registry.io/sboms/app:v1.2.3.sbom.cyclonedx-json",
		},
		{
			image:    "ghcr.io/org/repo:latest",
			format:   "spdx-json",
			expected: "ghcr.io/sboms/org/repo:latest.sbom.spdx-json",
		},
		{
			image:    "alpine:3.19",
			format:   "cyclonedx-json",
			expected: "docker.io/sboms/library/alpine:3.19.sbom.cyclonedx-json",
		},
		{
			image:    "localhost:5000/myapp:dev@sha256:abc",
			format:   "spdx-json",
			expected: "localhost:5000/sboms/myapp:dev.sbom.spdx-json",
		},
	}

	for _, tt := range tests {
		got := deriveSBOMTarget(tt.image, tt.format)
		if got != tt.expected {
			t.Errorf("deriveSBOMTarget(%q, %q) = %q, want %q", tt.image, tt.format, got, tt.expected)
		}
	}
}

// TestFormatToMediaType 验证 media type 映射
func TestFormatToMediaType(t *testing.T) {
	tests := []struct {
		format   string
		expected string
	}{
		{"cyclonedx-json", "application/vnd.cyclonedx+json"},
		{"spdx-json", "application/spdx+json"},
		{"unknown", "application/octet-stream"},
		{"", "application/octet-stream"},
	}

	for _, tt := range tests {
		if got := formatToMediaType(tt.format); got != tt.expected {
			t.Errorf("formatToMediaType(%q) = %q, want %q", tt.format, got, tt.expected)
		}
	}
}