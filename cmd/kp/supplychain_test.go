// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// cmd/kp/supplychain_test.go 替换整个 captureOutput 函数：

// captureOutput 重定向 stdout/stderr 并执行函数，返回捕获的输出 + 捕获的退出码
// 如果函数正常返回（无 os.Exit），exitCode = -1
func captureOutput(f func()) (stdout, stderr string, exitCode int) {
	// 保存原始输出流
	origStdout := os.Stdout
	origStderr := os.Stderr
	origExit := osExitFunc

	// 创建管道
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()

	// 重定向
	os.Stdout = wOut
	os.Stderr = wErr
	exitCode = -1 // 默认：函数正常返回

	// 用 channel 异步读取输出（防止阻塞）
	outCh := make(chan string)
	errCh := make(chan string)

	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, rOut)
		outCh <- buf.String()
	}()
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, rErr)
		errCh <- buf.String()
	}()

	// 替换 osExitFunc 捕获退出码
	osExitFunc = func(code int) {
		exitCode = code
		// 关键：关闭写入端，让读取端收到 EOF
		_ = wOut.Close()
		_ = wErr.Close()
	}

	// 执行测试函数 + recover 捕获 panic（如果 osExitFunc 用 panic 实现）
	func() {
		defer func() {
			if r := recover(); r != nil {
				if code, ok := r.(int); ok {
					exitCode = code
				}
			}
		}()
		f()
	}()

	// 关键：如果函数正常返回（没调 os.Exit），手动关闭写入端
	_ = wOut.Close()
	_ = wErr.Close()

	// 恢复原始输出流
	os.Stdout = origStdout
	os.Stderr = origStderr
	osExitFunc = origExit

	// 等待异步读取完成（现在管道已关闭，会收到 EOF）
	stdout = <-outCh
	stderr = <-errCh

	return stdout, stderr, exitCode
}

// TestSupplyChainVerify_Help 验证 --help 输出包含关键字段
func TestSupplyChainVerify_Help(t *testing.T) {
	stdout, _, exitCode := captureOutput(func() {
		runSupplyChainVerify([]string{"--help"})
	})

	// --help 应该正常返回（退出码 0），输出到 stdout
	if exitCode != 0 && exitCode != -1 {
		t.Errorf("expected exit code 0 or -1 (no exit), got %d", exitCode)
	}
	if !strings.Contains(stdout, "Usage: kp supply-chain verify") {
		t.Errorf("expected stdout to contain help usage, got %q", stdout)
	}

	// 验证关键帮助字段
	required := []string{
		"Usage: kp supply-chain verify",
		"<IMAGE>",
		"--key",
		"--output",
		"Exit codes:",
		"0  Verification succeeded",
		"1  Verification failed",
		"2  Tool error",
	}
	for _, want := range required {
		if !strings.Contains(stdout, want) {
			t.Errorf("help output missing %q", want)
		}
	}
}

// TestSupplyChainVerify_MissingArgs 验证缺参数时退出码=1 + 错误消息
func TestSupplyChainVerify_MissingArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantErr  string // 期望的錯誤消息片段
		wantExit int    // 期望的退出码
	}{
		{
			name:     "no args",
			args:     []string{},
			wantErr:  "--image and --key are required",
			wantExit: 1,
		},
		{
			name:     "only image",
			args:     []string{"registry.io/img:v1"},
			wantErr:  "--image and --key are required",
			wantExit: 1,
		},
		{
			name:     "only key",
			args:     []string{"--key", "cosign.pub"},
			wantErr:  "--image and --key are required",
			wantExit: 1,
		},
		{
			name:     "empty image flag",
			args:     []string{"--image", "", "--key", "cosign.pub"},
			wantErr:  "--image and --key are required",
			wantExit: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, exitCode := captureOutput(func() {
				runSupplyChainVerify(tt.args)
			})
	
			if exitCode != tt.wantExit {
				t.Errorf("expected exit code %d, got %d", tt.wantExit, exitCode)
			}
			if !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("expected stderr to contain %q, got %q", tt.wantErr, stderr)
			}
			if !strings.Contains(stdout, "Usage: kp supply-chain verify") {
				t.Errorf("expected stdout to contain help usage, got %q", stdout)
			}
		})
	}
}

// TestSupplyChainVerify_UnknownFlag 验证未知 flag 的处理（可选增强）
func TestSupplyChainVerify_UnknownFlag(t *testing.T) {
	// 简单手动解析未知 flag 会忽略，但 --help 应该还能用
	// 如果未来改用 flag.FlagSet，可以加严格测试
	_, _, exitCode := captureOutput(func() {
		runSupplyChainVerify([]string{"--unknown", "value"})
	})
	// 当前实现：未知 flag 被忽略，继续执行 → 缺参数错误 → exit 1
	if exitCode != 1 {
		t.Errorf("expected exit code 1 for unknown flag + missing args, got %d", exitCode)
	}
}