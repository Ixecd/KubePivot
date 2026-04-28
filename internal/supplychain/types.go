// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package supplychain

import (
	"strings"
	"time"
)

// Verifier 定义镜像签名验证的契约
// 设计原则：接口最小化，实现可替换（未来可加 notary/gpg 等）
type Verifier interface {
	// Verify 验证镜像签名
	//   image: 完整镜像引用，如 registry.io/repo:tag
	//   pubKey: 公钥文件路径 (key-based 模式)
	// 返回:
	//   *Result: 验证结果详情（即使失败也返回部分信息，便于调试）
	//   error: 仅当验证器自身故障时返回（如 cosign 未安装、网络超时）
	Verify(image, pubKey string) (*Result, error)
}

// Result 是签名验证的结构化输出
// 设计原则：字段扁平化，方便 JSON 序列化 + jq 查询
type Result struct {
	Image           string    `json:"image"`                      // 被验证的镜像引用
	Verified        bool      `json:"verified"`                   // 核心布尔值，CI/CD 直接用
	SignatureDigest string    `json:"signature_digest,omitempty"` // 签名摘要 (sha256:xxx)
	Issuer          string    `json:"issuer,omitempty"`           // 签发者 (cosign 提取的 Subject)
	VerifiedAt      time.Time `json:"verified_at"`                // 验证时间戳 (RFC3339)
	Error           string    `json:"error,omitempty"`            // 失败时的友好错误信息
}

// OutputFormat 定义输出格式枚举
type OutputFormat string

const (
	FormatText OutputFormat = "text"
	FormatJSON OutputFormat = "json"
)

// ShortDigest 截断 sha256:xxx 为前 12 位，方便终端阅读
// 导出为包级函数，供 cmd 层和测试共用
func ShortDigest(digest string) string {
	if strings.HasPrefix(digest, "sha256:") && len(digest) > 19 {
		return digest[7:19]
	}
	return digest
}
