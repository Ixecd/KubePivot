// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package supplychain

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// SupplyChainConfig 定义供应链策略配置
// 设计原则：扁平嵌套，YAML/JSON 解析友好，字段零指针避免空值歧义
type SupplyChainConfig struct {
	// Registries 注册表访问策略
	Registries struct {
		Allow []string `yaml:"allow" json:"allow"` // 白名单 (空=允许所有)
		Deny  []string `yaml:"deny" json:"deny"`   // 黑名单 (优先级高于 allow)
	} `yaml:"registries" json:"registries"`

	// Signing 签名验证策略
	Signing struct {
		Enforce   bool   `yaml:"enforce" json:"enforce"`       // 是否强制验证签名
		CosignKey string `yaml:"cosign-key" json:"cosign_key"` // 公钥路径 (相对项目根目录)
		Keyless *struct {
			Identity string `yaml:"identity" json:"identity"`           // OIDC subject (如 ci@org.com)
			Issuer   string `yaml:"issuer" json:"issuer"`               // OIDC issuer URL
			RegExp   bool   `yaml:"regexp,omitempty" json:"regexp"`     // identity 是否为正则
		} `yaml:"keyless,omitempty" json:"keyless,omitempty"`
	} `yaml:"signing" json:"signing"`

	// SBOM 物料清单策略
	SBOM struct {
		Require bool   `yaml:"require" json:"require"` // 是否要求存在 SBOM
		Format  string `yaml:"format" json:"format"`   // 期望格式: cyclonedx-json/spdx-json
	} `yaml:"sbom" json:"sbom"`

	// CVE 漏洞扫描策略
	CVE struct {
		MaxSeverity string   `yaml:"max-severity" json:"max_severity"` // critical/high/medium/low/none
		Exceptions  []string `yaml:"exceptions" json:"exceptions"`     // 豁免的 CVE ID 列表
	} `yaml:"cve" json:"cve"`
}

// IsValid 验证配置是否完整 (用于前置校验)
func (c *SupplyChainConfig) IsValid() error {
	if c.Signing.Enforce && c.Signing.CosignKey == "" {
		return fmt.Errorf("signing.enforce=true requires cosign-key")
	}
	if c.SBOM.Require && c.SBOM.Format == "" {
		return fmt.Errorf("sbom.require=true requires format")
	}
	// CVE 严重级别校验
	validSeverities := []string{"critical", "high", "medium", "low", "none", ""}
	if !contains(validSeverities, c.CVE.MaxSeverity) {
		return fmt.Errorf("invalid cve.max-severity: %s", c.CVE.MaxSeverity)
	}
	return nil
}

// Merge 合并配置：other 覆盖 c (用于命令行/环境变量覆盖 YAML)
// 设计原则：浅拷贝 + 字段级覆盖，避免深层嵌套的复杂合并逻辑
func (c *SupplyChainConfig) Merge(other *SupplyChainConfig) {
	if other == nil {
		return
	}
	// Registries: 非空切片才覆盖
	if len(other.Registries.Allow) > 0 {
		c.Registries.Allow = other.Registries.Allow
	}
	if len(other.Registries.Deny) > 0 {
		c.Registries.Deny = other.Registries.Deny
	}
	// Signing: 布尔值/字符串按"有值即覆盖"原则
	if other.Signing.Enforce {
		c.Signing.Enforce = true
	}
	if other.Signing.CosignKey != "" {
		c.Signing.CosignKey = other.Signing.CosignKey
	}
	// SBOM
	if other.SBOM.Require {
		c.SBOM.Require = true
	}
	if other.SBOM.Format != "" {
		c.SBOM.Format = other.SBOM.Format
	}
	// CVE
	if other.CVE.MaxSeverity != "" {
		c.CVE.MaxSeverity = other.CVE.MaxSeverity
	}
	if len(other.CVE.Exceptions) > 0 {
		c.CVE.Exceptions = other.CVE.Exceptions
	}
}

// ValidatePolicy 执行供应链策略验证 (核心入口)
// 返回:
//
//	nil: 策略通过
//	error: 策略违反，包含人类可读的错误消息
//
// 设计原则:
//  1. 快速失败: 注册表检查 → 签名验证 → SBOM 检查 → CVE 检查 (按成本升序)
//  2. 缓存友好: 同一镜像+配置的验证结果缓存 5 分钟
//  3. 错误聚合: 多个策略违反时返回首个关键错误 (避免信息过载)
func ValidatePolicy(ctx context.Context, image string, cfg *SupplyChainConfig) error {
	if cfg == nil {
		return nil // 无策略 = 放行
	}
	// 1. 注册表检查 (最快，无网络)
	if err := checkRegistry(image, cfg.Registries.Allow, cfg.Registries.Deny); err != nil {
		return fmt.Errorf("registry policy violated: %w", err)
	}

	// 2. 缓存检查 (避免重复调用外部 CLI)
	//    缓存 key: image + cosign-key + sbom-format (配置指纹)
	cacheKey := fmt.Sprintf("%s|%s|%s", image, cfg.Signing.CosignKey, cfg.SBOM.Format)
	if cached, ok := getCache(cacheKey); ok {
		return cached
	}

	// 3. 签名验证 (如果启用)
	if cfg.Signing.Enforce {
		if err := verifySignatureWithCache(ctx, image, cfg.Signing.CosignKey); err != nil {
			setCache(cacheKey, err, 5*time.Minute)
			return fmt.Errorf("signature policy violated: %w", err)
		}
	}

	// 4. SBOM 检查 (如果要求)
	//    Level3 先做存在性检查，Level4 再做内容解析
	if cfg.SBOM.Require {
		if err := checkSBOMExists(ctx, image, cfg.SBOM.Format); err != nil {
			setCache(cacheKey, err, 5*time.Minute)
			return fmt.Errorf("SBOM policy violated: %w", err)
		}
	}

	// 5. CVE 策略 (Level3 先占位，Level4 实现完整扫描)
	//    当前: 仅记录日志，不阻断 (避免依赖外部扫描器)
	//    TODO: 集成 trivy/grype 实现阈值拦截

	// 全部通过: 缓存成功结果
	setCache(cacheKey, nil, 5*time.Minute)
	return nil
}

// checkRegistry 验证镜像注册表是否符合白/黑名单
// 匹配策略:
//   - 模式含 "/": 作为镜像路径前缀匹配 (如 "docker.io/malicious" 匹配 "docker.io/malicious:latest")
//   - 模式不含 "/": 作为注册表前缀匹配 (如 "docker.io" 匹配 registry="docker.io")
//   - 黑名单优先: 先检查 deny, 再检查 allow
func checkRegistry(image string, allow, deny []string) error {
	// 提取注册表用于注册表级匹配
	// 例: "alpine:latest" → "docker.io", "registry.io/app:v1" → "registry.io"
	registry := extractRegistry(image)
	
	// 1. 黑名单优先 (安全原则: 显式拒绝 > 隐式允许)
	for _, pattern := range deny {
		matched := false
		if strings.Contains(pattern, "/") {
			// 镜像路径前缀匹配
			// 例: pattern="docker.io/malicious" 匹配 image="docker.io/malicious:latest"
			matched = strings.HasPrefix(image, pattern)
		} else {
			// 注册表前缀匹配
			// 例: pattern="docker.io" 匹配 registry="docker.io"
			matched = strings.HasPrefix(registry, pattern)
		}
		if matched {
			return fmt.Errorf("registry %q is explicitly denied", pattern)
		}
	}
	
	// 2. 白名单: 空列表 = 允许所有
	if len(allow) == 0 {
		return nil
	}
	
	// 3. 白名单匹配 (同样策略)
	for _, pattern := range allow {
		matched := false
		if strings.Contains(pattern, "/") {
			matched = strings.HasPrefix(image, pattern)
		} else {
			matched = strings.HasPrefix(registry, pattern)
		}
		if matched {
			return nil
		}
	}
	
	// 4. 未匹配任何白名单
	return fmt.Errorf("registry not in allow list: %s (allowed: %v)", registry, allow)
}

// extractRegistry 从镜像引用提取注册表前缀
// 例: registry.io/repo:tag → registry.io
//
//	ghcr.io/org/img → ghcr.io
//	alpine:latest → docker.io (默认)
func extractRegistry(image string) string {
	parts := strings.SplitN(image, "/", 2)
	if len(parts) == 1 {
		return "docker.io" // 默认官方库
	}
	// 判断第一部分是否是注册表 (含.或:或=localhost)
	first := parts[0]
	if strings.ContainsAny(first, ".:") || first == "localhost" {
		return first
	}
	return "docker.io" // 如 library/alpine → docker.io
}

// --- 内存缓存实现 (简单 TTL) ---

var (
	verifyCache  = make(map[string]cacheEntry)
	cacheMu      sync.RWMutex
	cacheCleanup = time.NewTicker(10 * time.Minute)
)

type cacheEntry struct {
	err       error
	expiresAt time.Time
}

func init() {
	// 后台协程定期清理过期缓存
	go func() {
		for range cacheCleanup.C {
			cacheMu.Lock()
			now := time.Now()
			for key, entry := range verifyCache {
				if now.After(entry.expiresAt) {
					delete(verifyCache, key)
				}
			}
			cacheMu.Unlock()
		}
	}()
}

func getCache(key string) (error, bool) {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	entry, ok := verifyCache[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.err, true
}

func setCache(key string, err error, ttl time.Duration) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	verifyCache[key] = cacheEntry{
		err:       err,
		expiresAt: time.Now().Add(ttl),
	}
}

// verifySignatureWithCache 包装 cosign 验证 + 缓存
func verifySignatureWithCache(ctx context.Context, image, pubKey string) error {
	// 复用 Level1 的 Verifier 接口
	// 注意: 生产环境应通过依赖注入传入 Verifier 实例
	// Level3 先直接调用，保持简单
	// 测试时可通过替换 execCommandFunc mock
	verifier := NewCosignVerifier("cosign") // 路径由 doctor 确保
	_, err := verifier.Verify(image, pubKey)
	return err
}

// checkSBOMExists 占位: 检查镜像是否有关联 SBOM
// Level3 先返回 nil (不阻断), Level4 实现 OCI registry 查询
func checkSBOMExists(ctx context.Context, image, format string) error {
	// TODO: 实现 SBOM 存在性检查
	// 方案:
	//   1. 查询 OCI registry 的 referrers API (if supported)
	//   2. 查询本地缓存目录 (~/.kubepivot/sboms/)
	//   3. 调用 syft 重新生成 (成本较高)
	// Level3 先放行，记录日志
	_ = image
	_ = format
	return nil
}

// contains 辅助函数
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
