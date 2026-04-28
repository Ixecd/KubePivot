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

// mockCmd 模拟 exec.Cmd，用于测试时返回预定义的输出
type mockCmd struct {
	stdout []byte
	stderr []byte
	err    error
}

// Output 模拟 exec.Cmd.Output() 行为
func (m *mockCmd) Output() ([]byte, error) {
	if m.err != nil {
		// 如果是 ExitError，需要让调用方能提取 stderr
		return m.stdout, m.err
	}
	return m.stdout, nil
}

// mockExecCommandFunc 返回一个预配置的 mockCmd
func mockExecCommandFunc(stdout, stderr []byte, err error) func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// 创建真实的 Cmd 但替换其 Output 方法行为
		// 技巧：用 /bin/echo 或 /usr/bin/true 作为占位，实际输出由 mockCmd 控制
		cmd := exec.CommandContext(ctx, "true")
		// 通过闭包捕获预定义输出
		// 注意：这里需要反射或接口注入才能完全 mock，但为了最小侵入，我们用另一种方式：
		// 直接测试 extractCosignError 和 JSON 解析逻辑，exec 调用留给集成测试
		_ = stdout
		_ = stderr
		_ = err
		return cmd
	}
}

// 更实用的方案：直接测试 Verify 的内部逻辑分支
// 通过替换 execCommandFunc + 自定义 mockCmd 实现

// --- 测试用例开始 ---

// TestCosignVerifier_Verify_Success 验证 cosign 返回有效签名时的解析
func TestCosignVerifier_Verify_Success(t *testing.T) {
	// 保存原始函数，测试后恢复
	origFunc := execCommandFunc
	defer func() { execCommandFunc = origFunc }()

	// cosign --output json 的成功响应示例（简化版，实际字段可能更多）
	mockJSON := `[{"critical":{"identity":{"repository":"registry.io/repo"},"type":"cosign container signature"},"optional":null}]`

	// 注入 mock：返回成功输出，无错误
	execCommandFunc = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// 创建一个会输出 mockJSON 的 cmd
		// 技巧：用 echo + pipe，但跨平台复杂，改用自定义类型
		// 这里我们用更直接的方式：测试解析逻辑，exec 调用用集成测试覆盖
		// 但为了单元测试完整性，我们构造一个能返回预定义输出的 Cmd
		cmd := exec.CommandContext(ctx, "echo", mockJSON)
		return cmd
	}

	v := NewCosignVerifier("cosign")
	result, err := v.Verify("registry.io/repo:v1.2.3", "/path/to/cosign.pub")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !result.Verified {
		t.Errorf("expected Verified=true, got false")
	}
	if result.Image != "registry.io/repo:v1.2.3" {
		t.Errorf("expected Image=%q, got %q", "registry.io/repo:v1.2.3", result.Image)
	}
	if result.VerifiedAt.IsZero() {
		t.Error("expected VerifiedAt to be set")
	}
}

// TestCosignVerifier_Verify_SignatureMismatch 验证签名不匹配时的友好错误
func TestCosignVerifier_Verify_SignatureMismatch(t *testing.T) {
	origFunc := execCommandFunc
	defer func() { execCommandFunc = origFunc }()

	// 模拟 cosign 验证失败：exit 1 + 特定 stderr
	execCommandFunc = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// 用 /bin/sh -c 模拟 exit 1 + stderr
		cmd := exec.CommandContext(ctx, "sh", "-c", "echo 'Error: no matching signatures' >&2; exit 1")
		return cmd
	}

	v := NewCosignVerifier("cosign")
	result, err := v.Verify("registry.io/repo:v1.2.3", "/wrong/key.pub")

	// 签名不匹配是"业务失败"，不是"工具故障"，所以 err 应该为 nil
	if err != nil {
		t.Fatalf("expected no error (business failure), got %v", err)
	}
	if result.Verified {
		t.Errorf("expected Verified=false, got true")
	}
	if result.Error == "" {
		t.Error("expected Error message to be populated")
	}
	if !strings.Contains(result.Error, "no valid signatures") {
		t.Errorf("expected error to mention signature issue, got %q", result.Error)
	}
}

// TestCosignVerifier_Verify_CosignExecutionError 验证 cosign 工具自身故障（如网络超时）
func TestCosignVerifier_Verify_CosignExecutionError(t *testing.T) {
	origFunc := execCommandFunc
	defer func() { execCommandFunc = origFunc }()

	// 模拟网络超时：context deadline exceeded
	execCommandFunc = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// 创建一个会超时的 cmd
		ctxShort, cancel := context.WithTimeout(ctx, 1*time.Nanosecond)
		defer cancel()
		cmd := exec.CommandContext(ctxShort, "sleep", "1")
		return cmd
	}

	v := NewCosignVerifier("cosign")
	result, err := v.Verify("registry.io/repo:v1.2.3", "/path/to/key.pub")

	// 工具故障应该返回 error，而不是 Result
	if err == nil {
		t.Fatal("expected error for execution failure, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result on execution error, got %+v", result)
	}
}

// TestCosignVerifier_Verify_InvalidJSON 验证 cosign 输出畸形 JSON 时的处理
func TestCosignVerifier_Verify_InvalidJSON(t *testing.T) {
	origFunc := execCommandFunc
	defer func() { execCommandFunc = origFunc }()

	// 模拟 cosign 输出非 JSON（比如人类可读文本）
	execCommandFunc = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "echo", "This is not JSON")
		return cmd
	}

	v := NewCosignVerifier("cosign")
	_, err := v.Verify("registry.io/repo:v1.2.3", "/path/to/key.pub")

	// JSON 解析失败应该返回 error
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "parse cosign json") {
		t.Errorf("expected parse error, got %v", err)
	}
}

// TestCosignVerifier_Verify_EmptyResult 验证 cosign 返回空数组时的处理
func TestCosignVerifier_Verify_EmptyResult(t *testing.T) {
	origFunc := execCommandFunc
	defer func() { execCommandFunc = origFunc }()

	execCommandFunc = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "echo", "[]")
		return cmd
	}

	v := NewCosignVerifier("cosign")
	result, err := v.Verify("registry.io/repo:v1.2.3", "/path/to/key.pub")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Verified {
		t.Errorf("expected Verified=false for empty result, got true")
	}
	if result.Error == "" {
		t.Error("expected Error message for empty result")
	}
}

// TestExtractCosignError 单独测试错误提取逻辑（纯函数，易测）
func TestExtractCosignError(t *testing.T) {
	tests := []struct {
		name     string
		stderr   string
		expected string
	}{
		{
			name:     "no matching signatures",
			stderr:   "Error: no matching signatures\n",
			expected: "no valid signatures found for the given key",
		},
		{
			name:     "certificate identity mismatch",
			stderr:   "Error: certificate identity does not match\n",
			expected: "OIDC identity mismatch (keyless mode not supported in Level1)",
		},
		{
			name:     "generic error with multiple lines",
			stderr:   "Warning: something\nError: real error\n",
			expected: "real error",
		},
		{
			name:     "empty stderr",
			stderr:   "",
			expected: "signature verification failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractCosignError(tt.stderr)
			if got != tt.expected {
				t.Errorf("extractCosignError(%q) = %q, want %q", tt.stderr, got, tt.expected)
			}
		})
	}
}

// TestShortDigest 测试摘要截断函数（纯函数）
func TestShortDigest(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"sha256:a1b2c3d4e5f6789012345678", "a1b2c3d4e5f6"},
		{"sha256:short", "sha256:short"}, // 太短不截断
		{"not-sha256:xxx", "not-sha256:xxx"},
		{"", ""},
	}

	for _, tt := range tests {
		got := ShortDigest(tt.input)
		if got != tt.expected {
			t.Errorf("shortDigest(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
