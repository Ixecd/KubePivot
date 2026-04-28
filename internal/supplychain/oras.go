// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package supplychain

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// PushSBOM 调用 oras CLI 上传 SBOM 到 OCI registry
// 设计原则:
//  1. 纯函数：不隐式依赖全局状态，方便测试
//  2. Context 透传：调用方控制超时/取消
//  3. 目标派生：image → sbom target 的默认规则 + 显式覆盖
//  4. 错误区分：工具故障 (error) vs 业务失败 (清晰消息)
//
// 参数:
//   - image: 原始镜像引用 (如 registry.io/app:v1.2.3)
//   - sbomContent: SBOM 内容 (JSON 字符串)
//   - format: SBOM 格式 (cyclonedx-json / spdx-json)
//   - target: 显式上传目标 (可选, 空则用派生规则)
//   - auth: 认证参数 (可选, 如 "user:pass" 或空用 ~/.docker/config.json)
//
// 返回:
//   - error: 仅当上传失败时返回 (包含人类可读消息)
//
// 派生规则 (target 为空时):
//
//	<registry>/sboms/<repo>:<tag>.sbom.<format>
//	例: registry.io/app:v1.2.3 → registry.io/sboms/app:v1.2.3.sbom.cyclonedx-json
func PushSBOM(ctx context.Context, image, sbomContent, format, target, auth string) error {
	if target == "" {
		target = deriveSBOMTarget(image, format)
	}

	// 1. 构建 oras push 命令
	//    oras push <target> --artifact-type <media-type> - < content.json
	mediaType := formatToMediaType(format)
	args := []string{
		"push", target,
		"--artifact-type", mediaType,
		"-", // stdin
	}

	// 认证参数 (可选)
	if auth != "" {
		args = append(args, "--username", strings.Split(auth, ":")[0])
		// 注意: 密码通过 stdin 或环境变量传递更安全, 简化实现先直接传
		// 生产环境建议: 用 --password-stdin 或预登录
	}

	// 2. 执行 (可被测试注入)
	//    默认 60s 超时 (上传可能较慢)
	cmdCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := execCommandFunc(cmdCtx, "oras", args...)
	cmd.Stdin = strings.NewReader(sbomContent)
	output, err := cmd.CombinedOutput()

	if err != nil {
		return fmt.Errorf("oras push failed: %w (output: %s)", err, truncateOutput(string(output)))
	}

	return nil
}

// deriveSBOMTarget 根据镜像引用派生 SBOM 上传目标
// 规则: <registry>/sboms/<repo>:<tag>.sbom.<format>
func deriveSBOMTarget(image, format string) string {
	// 解析 image: [registry/][repo/]name:tag[@digest]
	// 简化: 用 strings.Split 处理常见格式
	// 生产环境建议: 用 github.com/google/go-containerregistry/pkg/name 精确解析

	// 提取 registry + repo + tag
	parts := strings.SplitN(image, "/", 3)
	var registry, repo, tag string

	switch len(parts) {
	case 1:
		// alpine:latest → docker.io/library/alpine:latest
		registry = "docker.io"
		repo = "library/" + parts[0]
	case 2:
		if strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost" {
			// registry.io/app:tag
			registry = parts[0]
			repo = parts[1]
		} else {
			// library/alpine:latest → docker.io/library/alpine:latest
			registry = "docker.io"
			repo = image
		}
	case 3:
		// ghcr.io/org/repo:tag
		registry = parts[0]
		repo = parts[1] + "/" + parts[2]
	}

	// 分离 repo 和 tag (处理 @digest)
	repoTag := strings.SplitN(repo, ":", 2)
	repoName := repoTag[0]
	tag = "latest"
	if len(repoTag) > 1 {
		// 处理 tag@digest
		tagParts := strings.SplitN(repoTag[1], "@", 2)
		tag = tagParts[0]
	}

	// 派生目标: <registry>/sboms/<repo>:<tag>.sbom.<format>
	return fmt.Sprintf("%s/sboms/%s:%s.sbom.%s", registry, repoName, tag, format)
}


// formatToMediaType 映射 SBOM 格式到 OCI artifact media type
// 修复: 用字符串字面量直接比较，避免 SBOMFormat vs string 类型冲突
func formatToMediaType(format string) string {
	switch format {
	case "cyclonedx-json": 
		return "application/vnd.cyclonedx+json"
	case "spdx-json":
		return "application/spdx+json"
	default:
		return "application/octet-stream" // 兜底
	}
}