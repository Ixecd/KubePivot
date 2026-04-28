// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package supplychain

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// execCommandFunc 允许测试时替换 exec.CommandContext 行为
// 默认实现：直接调用 exec.CommandContext
var execCommandFunc = exec.CommandContext

// cosignVerifier 是 Verifier 接口的 cosign-cli 实现
// 设计原则：wrapper 模式，不重复实现签名算法，只负责调用 + 解析
type cosignVerifier struct {
	binaryPath string // cosign 二进制路径，由 doctor 确保存在
}

// NewCosignVerifier 创建 verifier 实例
// 调用方需先确保 cosign 可用（通过 diagnosis.EnsureCosign）
func NewCosignVerifier(binaryPath string) Verifier {
	return &cosignVerifier{binaryPath: binaryPath}
}

// Verify 实现 Verifier 接口
// 关键设计：
//  1. 使用 CommandContext + 30s timeout，防止 Registry 网络问题导致挂起
//  2. 强制 --output json 解析结构化结果，避免 parse 人类文本的脆弱性
//  3. 区分 "命令执行失败" (error) vs "签名验证失败" (Result.Verified=false)
func (v *cosignVerifier) Verify(image, pubKey string) (*Result, error) {
	// 1. 构建命令：cosign verify --key <pubKey> --output json <image>
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := execCommandFunc(ctx, v.binaryPath, "verify", "--key", pubKey, "--output", "json", image)

	// 2. 执行 + 捕获输出
	stdout, err := cmd.Output()
	if err != nil {
		// 区分：超时/未安装/网络错误 (返回 error) vs 签名不匹配 (返回 Result{Verified:false})
		if exitErr, ok := err.(*exec.ExitError); ok {
			// cosign 验证失败时退出码=1，且 stderr 包含 "Error: no matching signatures"
			stderr := string(exitErr.Stderr)
			if strings.Contains(stderr, "no matching signatures") || strings.Contains(stderr, "signature verification failed") {
				// 这是"业务逻辑失败"，不是"工具故障"，返回结构化结果
				return &Result{
					Image:      image,
					Verified:   false,
					VerifiedAt: time.Now(),
					Error:      extractCosignError(stderr),
				}, nil
			}
		}
		// 其他错误：cosign 未找到/超时/网络问题 → 返回 error 让上层处理
		return nil, fmt.Errorf("cosign execution failed: %w", err)
	}

	// 3. 解析 cosign 的 JSON 输出
	// cosign --output json 格式: [{"critical":{"identity":{"repository":"..."}}, "optional":{...}}]
	var cosignResults []map[string]interface{}
	if err := json.Unmarshal(stdout, &cosignResults); err != nil {
		return nil, fmt.Errorf("parse cosign json output failed: %w", err)
	}

	if len(cosignResults) == 0 {
		return &Result{
			Image:      image,
			Verified:   false,
			VerifiedAt: time.Now(),
			Error:      "cosign returned empty result",
		}, nil
	}

	// 4. 提取关键字段 (防御性解析，避免 panic)
	result := &Result{
		Image:      image,
		Verified:   true,
		VerifiedAt: time.Now(),
	}

	// 尝试提取 signature digest (cosign 可能放在不同位置，按优先级尝试)
	if critical, ok := cosignResults[0]["critical"].(map[string]interface{}); ok {
		if identity, ok := critical["identity"].(map[string]interface{}); ok {
			if repo, ok := identity["repository"].(string); ok && repo != "" {
				// 这里可以扩展提取更多元数据
			}
		}
		// cosign 的 json 输出里，签名摘要可能在 "optional" 或顶层，需要根据实际输出调整
		// 保守做法：如果解析失败，至少返回 Verified=true
	}

	// 简单处理：如果解析到任何结果，就认为验证通过
	// TODO: 根据 cosign 实际输出格式，精确提取 SignatureDigest 和 Issuer
	return result, nil
}

// extractCosignError 从 cosign stderr 提取人类可读的错误信息
// 优先级:
//   1. 已知错误模式匹配 (no matching signatures / certificate identity)
//   2. 提取 "Error: <msg>" 格式的实际消息
//   3. 回退到第一行非空内容
//   4. 兜底默认消息
func extractCosignError(stderr string) string {
	// 1. 已知错误模式匹配
	if strings.Contains(stderr, "no matching signatures") {
		return "no valid signatures found for the given key"
	}
	if strings.Contains(stderr, "certificate identity") {
		return "OIDC identity mismatch (keyless mode not supported in Level1)"
	}

	// 2. 解析 stderr 行
	lines := strings.Split(strings.TrimSpace(stderr), "\n")

	// 2a. 优先提取 "Error: xxx" 后的实际消息
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Error:") {
			msg := strings.TrimSpace(strings.TrimPrefix(line, "Error:"))
			if msg != "" {
				return msg
			}
		}
	}

	// 2b. 回退：返回第一行非空内容
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}

	// 3. 兜底
	return "signature verification failed"
}