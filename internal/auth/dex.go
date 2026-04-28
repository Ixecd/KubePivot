// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ════════════════════════════════════════════════════════════════════════════
// Dex / OIDC Provider
//
// Dex (https://github.com/dexidp/dex) 是企业 OIDC 兜底:
//   - 支持上游 connector (LDAP / SAML / GitHub / Google / Microsoft 等)
//   - KubePivot 通过标准 OIDC 协议跟 Dex 集成
//   - 不依赖 Dex 特定 API, 任何 OIDC 兼容 provider 都能用此实现
//
// 配置示例:
//   kp login --provider dex //     --issuer https://dex.example.com //     --client-id kubepivot //     --client-secret xxx
// ════════════════════════════════════════════════════════════════════════════

// DexProvider OIDC discovery + 标准 OAuth2 流程.
type DexProvider struct {
	issuer string
	cfg    *OAuth2Config
}

// NewDexProvider 通过 OIDC discovery 自动获取 endpoints.
//
// 调用 GET <issuer>/.well-known/openid-configuration
// 解析 authorization_endpoint / token_endpoint / userinfo_endpoint.
//
// issuer 通常以 https:// 开头, 如 "https://dex.example.com"
func NewDexProvider(issuer, clientID, clientSecret string) (*DexProvider, error) {
	if issuer == "" {
		return nil, fmt.Errorf("Dex issuer 不能为空 (示例: https://dex.example.com)")
	}
	if clientID == "" {
		return nil, fmt.Errorf("Dex client_id 不能为空 (在 Dex 配置中注册的 OIDC client)")
	}

	// 调 OIDC discovery endpoint
	discovery, err := fetchOIDCDiscovery(context.Background(), issuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery 失败 (issuer=%s): %w", issuer, err)
	}

	cfg := &OAuth2Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		AuthURL:      discovery.AuthorizationEndpoint,
		TokenURL:     discovery.TokenEndpoint,
		UserInfoURL:  discovery.UserInfoEndpoint,
		Scopes:       []string{"openid", "email", "profile", "groups"},
		UsePKCE:      true,
	}
	return &DexProvider{
		issuer: issuer,
		cfg:    cfg,
	}, nil
}

// Name 返回 provider 标识名 ("dex").
func (p *DexProvider) Name() string {
	return "dex"
}

// Login 完整 OAuth2 + OIDC 流程.
func (p *DexProvider) Login(ctx context.Context) (*Token, *UserInfo, error) {
	token, err := runOAuth2Flow(ctx, p.cfg)
	if err != nil {
		return nil, nil, err
	}

	info, err := fetchUserInfo(ctx, p.cfg.UserInfoURL, token)
	if err != nil {
		return nil, nil, fmt.Errorf("获取 userinfo 失败 (token 可能成功但 userinfo 端点不可用): %w", err)
	}

	return token, info, nil
}

// VerifyToken 调 userinfo endpoint 校验 token 有效性.
//
// 注: Q-A4=A 拍板 whoami 默认不调用此方法 (本地读 token 即可).
// VerifyToken 只在显式需要确认 token 仍有效时被调用 (如 controller 端 RBAC 校验).
func (p *DexProvider) VerifyToken(ctx context.Context, token *Token) (*UserInfo, error) {
	if token.Expired() {
		return nil, fmt.Errorf("token 已过期")
	}
	return fetchUserInfo(ctx, p.cfg.UserInfoURL, token)
}

// ════════════════════════════════════════════════════════════════════════════
// OIDC Discovery
// ════════════════════════════════════════════════════════════════════════════

// oidcDiscoveryDocument OIDC discovery endpoint 返回结构.
//
// 完整规范: https://openid.net/specs/openid-connect-discovery-1_0.html
type oidcDiscoveryDocument struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
	JWKSUri               string `json:"jwks_uri"`
}

// fetchOIDCDiscovery 调 <issuer>/.well-known/openid-configuration.
func fetchOIDCDiscovery(ctx context.Context, issuer string) (*oidcDiscoveryDocument, error) {
	// 标准 OIDC discovery URL
	discoveryURL := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"

	req, err := http.NewRequestWithContext(ctx, "GET", discoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造 discovery 请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discovery endpoint 不可达 (%s): %w", discoveryURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery endpoint 返回 %d (%s)", resp.StatusCode, discoveryURL)
	}

	var doc oidcDiscoveryDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("解析 discovery 文档失败: %w", err)
	}

	// sanity check
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" {
		return nil, fmt.Errorf("discovery 文档缺少必需字段 (authorization_endpoint / token_endpoint)")
	}

	return &doc, nil
}
