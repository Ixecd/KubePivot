// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// ════════════════════════════════════════════════════════════════════════════
// GitHub OAuth Provider
//
// 重要: GitHub 不是 OIDC, 是纯 OAuth2.
//   - 没有 ID Token
//   - userinfo endpoint 是 https://api.github.com/user (非标准 OIDC userinfo)
//   - 字段映射不同 (login → Subject, name → Name, ...)
//   - 默认不返回 email, 需要 user:email scope + 调 /user/emails
//   - 不支持 PKCE (但支持 client_secret)
//
// OAuth App 注册:
//   https://github.com/settings/developers
//   Authorization callback URL: http://localhost:18888/callback
//
// 配置示例:
//   kp login --provider github //     --client-id Iv1.xxx //     --client-secret xxx
// ════════════════════════════════════════════════════════════════════════════

const (
	githubAuthURL     = "https://github.com/login/oauth/authorize"
	githubTokenURL    = "https://github.com/login/oauth/access_token"
	githubUserInfoURL = "https://api.github.com/user"
	githubEmailsURL   = "https://api.github.com/user/emails"
)

// GitHubProvider GitHub OAuth 实现.
type GitHubProvider struct {
	cfg *OAuth2Config
}

// NewGitHubProvider 构造 GitHub provider.
func NewGitHubProvider(clientID, clientSecret string) (*GitHubProvider, error) {
	if clientID == "" {
		return nil, fmt.Errorf("GitHub client_id 不能为空 (从 https://github.com/settings/developers 获取)")
	}
	if clientSecret == "" {
		return nil, fmt.Errorf("GitHub client_secret 不能为空")
	}

	return &GitHubProvider{
		cfg: &OAuth2Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			AuthURL:      githubAuthURL,
			TokenURL:     githubTokenURL,
			UserInfoURL:  githubUserInfoURL,
			Scopes:       []string{"read:user", "user:email"},
			UsePKCE:      false, // GitHub OAuth App 不支持 PKCE
		},
	}, nil
}

func (p *GitHubProvider) Name() string {
	return "github"
}

// Login GitHub 流程 (OAuth + 自定义 userinfo).
func (p *GitHubProvider) Login(ctx context.Context) (*Token, *UserInfo, error) {
	token, err := runOAuth2Flow(ctx, p.cfg)
	if err != nil {
		return nil, nil, err
	}

	info, err := fetchGitHubUserInfo(ctx, token)
	if err != nil {
		return nil, nil, fmt.Errorf("获取 GitHub userinfo 失败: %w", err)
	}

	return token, info, nil
}

// VerifyToken 调 /user 端点验证 token.
func (p *GitHubProvider) VerifyToken(ctx context.Context, token *Token) (*UserInfo, error) {
	if token.Expired() {
		return nil, fmt.Errorf("token 已过期")
	}
	return fetchGitHubUserInfo(ctx, token)
}

// fetchGitHubUserInfo 调 /user + /user/emails 拼出 UserInfo.
//
// GitHub /user 字段:
//   id        (int)     → Subject (转 string)
//   login     (string)  → fallback Subject
//   name      (string)  → Name
//   email     (string?) → Email (可能为空, 私有邮箱)
//
// 邮箱为空时调 /user/emails 找 primary verified email.
func fetchGitHubUserInfo(ctx context.Context, token *Token) (*UserInfo, error) {
	// 1. 调 /user
	userReq, err := http.NewRequestWithContext(ctx, "GET", githubUserInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造 GitHub /user 请求失败: %w", err)
	}
	userReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	userReq.Header.Set("Accept", "application/vnd.github+json")
	userReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(userReq)
	if err != nil {
		return nil, fmt.Errorf("/user 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/user 返回 %d", resp.StatusCode)
	}

	var raw struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("解析 /user 响应失败: %w", err)
	}

	info := &UserInfo{
		Subject: strconv.FormatInt(raw.ID, 10),
		Name:    raw.Name,
		Email:   raw.Email,
		// GitHub 没有 groups 概念, 留空 (B RBAC 时如需 group 可改用 GitHub Teams API)
	}
	if info.Name == "" {
		info.Name = raw.Login
	}

	// 2. 邮箱为空 → 调 /user/emails 找 primary verified
	if info.Email == "" {
		email, err := fetchGitHubPrimaryEmail(ctx, token)
		if err == nil {
			info.Email = email
		}
		// 调用失败不阻断, email 留空
	}

	return info, nil
}

// fetchGitHubPrimaryEmail 找用户的 primary + verified email.
func fetchGitHubPrimaryEmail(ctx context.Context, token *Token) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", githubEmailsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("/user/emails 返回 %d", resp.StatusCode)
	}

	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return "", err
	}

	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	return "", fmt.Errorf("没有 primary + verified 邮箱")
}
