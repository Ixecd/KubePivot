// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package auth

import (
	"context"
	"fmt"
)

// ════════════════════════════════════════════════════════════════════════════
// Google OAuth Provider
//
// Google 是 OIDC 兼容 provider, 但为简化使用直接写死 endpoints
// (避免每次都调 discovery, 且 Google endpoint 极稳定).
//
// OAuth Client 注册:
//   https://console.cloud.google.com/apis/credentials
//   类型选 "Desktop app" 或 "Web application"
//   redirect_uri 填 http://localhost:18888/callback
//
// 配置示例:
//   kp login --provider google //     --client-id <xxx>.apps.googleusercontent.com //     --client-secret <yyy>
// ════════════════════════════════════════════════════════════════════════════

const (
	googleAuthURL     = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL    = "https://oauth2.googleapis.com/token"
	googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
)

// GoogleProvider Google OAuth + OIDC 实现.
type GoogleProvider struct {
	cfg *OAuth2Config
}

// NewGoogleProvider 构造 Google provider.
//
// clientID / clientSecret 来自 Google Cloud Console 的 OAuth 2.0 Client ID.
func NewGoogleProvider(clientID, clientSecret string) (*GoogleProvider, error) {
	if clientID == "" {
		return nil, fmt.Errorf("Google client_id 不能为空 (从 https://console.cloud.google.com/apis/credentials 获取)")
	}

	return &GoogleProvider{
		cfg: &OAuth2Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			AuthURL:      googleAuthURL,
			TokenURL:     googleTokenURL,
			UserInfoURL:  googleUserInfoURL,
			Scopes:       []string{"openid", "email", "profile"},
			UsePKCE:      true,
		},
	}, nil
}

func (p *GoogleProvider) Name() string {
	return "google"
}

// Login 完整 OAuth2 + Google userinfo 流程.
func (p *GoogleProvider) Login(ctx context.Context) (*Token, *UserInfo, error) {
	token, err := runOAuth2Flow(ctx, p.cfg)
	if err != nil {
		return nil, nil, err
	}

	info, err := fetchUserInfo(ctx, p.cfg.UserInfoURL, token)
	if err != nil {
		return nil, nil, fmt.Errorf("获取 Google userinfo 失败: %w", err)
	}

	return token, info, nil
}

// VerifyToken 通过 Google userinfo endpoint 验证 token.
func (p *GoogleProvider) VerifyToken(ctx context.Context, token *Token) (*UserInfo, error) {
	if token.Expired() {
		return nil, fmt.Errorf("token 已过期")
	}
	return fetchUserInfo(ctx, p.cfg.UserInfoURL, token)
}
