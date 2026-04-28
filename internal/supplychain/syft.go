// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package supplychain

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SBOMFormat 定义支持的 SBOM 输出格式
type SBOMFormat string

const (
	// CycloneDXJSON: 默认格式，结构化强，生态兼容好
	CycloneDXJSON SBOMFormat = "cyclonedx-json"
	// SPDXJSON: Linux Foundation 标准，通用性强
	SPDXJSON SBOMFormat = "spdx-json"
	// Table: 人类可读，适合终端预览
	Table SBOMFormat = "table"
)

// IsValid 验证格式是否受支持
func (f SBOMFormat) IsValid() bool {
	switch f {
	case CycloneDXJSON, SPDXJSON, Table:
		return true
	default:
		return false
	}
}

// SBOMResult 是 syft 扫描的结构化输出
// 设计原则：字段扁平化，方便后续关联分析 + JSON 序列化
type SBOMResult struct {
	Image        string    `json:"image"`
	Format       string    `json:"format"`
	Content      string    `json:"content"`
	GeneratedAt  time.Time `json:"generated_at"`
	PackageCount int       `json:"package_count,omitempty"` // 可选：解析后填充
}

// Scan 调用 syft CLI 生成 SBOM
// 设计原则:
//  1. 纯函数：不隐式依赖全局状态，方便测试
//  2. Context 透传：调用方控制超时/取消
//  3. 错误区分：工具故障 (error) vs 业务失败 (Result 里标记)
func Scan(ctx context.Context, image string, format SBOMFormat) (*SBOMResult, error) {
	if !format.IsValid() {
		return nil, fmt.Errorf("unsupported SBOM format: %s", format)
	}

	// 1. 构建命令：syft <image> -o <format> -q
	//    -q: quiet 模式，减少无关日志干扰
	//    注意：syft 的 -o 参数格式：-o <format>=<path> 或 -o <format> (stdout)
	args := []string{image, "-o", string(format), "-q"}

	// 2. 执行 (可被测试注入)
	//    默认 30s 超时，防止 Registry 网络问题挂起
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := execCommandFunc(cmdCtx, "syft", args...)
	output, err := cmd.CombinedOutput()

	if err != nil {
		// 区分：syft 未安装 / 镜像不存在 / 网络超时 / 格式不支持
		// 统一返回 error，让上层决定如何提示用户
		return nil, fmt.Errorf("syft scan failed: %w (output: %s)", err, truncateOutput(string(output)))
	}

	// 3. 解析结果 (Table 格式不解析，直接返回原始文本)
	//    JSON 格式可尝试提取包数量等元数据（可选增强）
	result := &SBOMResult{
		Image:       image,
		Format:      string(format),
		Content:     string(output),
		GeneratedAt: time.Now(),
	}

	// 4. 可选：解析 JSON 提取 package_count（增强体验）
	if format == CycloneDXJSON || format == SPDXJSON {
		if count := extractPackageCount(string(output), string(format)); count > 0 {
			result.PackageCount = count
		}
	}

	return result, nil
}

// truncateOutput 截断过长输出，避免错误消息刷屏
func truncateOutput(s string) string {
	const maxLen = 200
	if len(s) <= maxLen {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(s[:maxLen]) + "..."
}

// extractPackageCount 从 JSON SBOM 中提取包数量（防御性解析）
func extractPackageCount(content, format string) int {
	// CycloneDX: {"components": [...]}
	// SPDX: {"packages": [...]}
	// 简单字符串匹配，避免引入 encoding/json 依赖（Level2 保持轻量）
	// 如需精确解析，可后续引入轻量解析器
	if format == string(CycloneDXJSON) && strings.Contains(content, `"components"`) {
		// 粗略计数：统计 "bom-ref" 出现次数（每个组件一个）
		return strings.Count(content, `"bom-ref"`)
	}
	if format == string(SPDXJSON) && strings.Contains(content, `"SPDXID"`) {
		return strings.Count(content, `"SPDXID"`) - 1 // 减去文档自身
	}
	return 0
}
