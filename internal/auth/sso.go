// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.

// Package auth 提供 OAuth/OIDC 身份认证抽象。
//
// v2.8 SSO 模块 (一锅端):
//   - AuthProvider 接口: Dex / Google / GitHub
//   - OAuth2 标准流程 (Authorization Code + PKCE)
//   - Token / UserInfo 持久化到 ~/.kp/credentials/
//
// 与 KubePivot 工程哲学一致:
//   - 0 client-go: 用标准库 net/http 直接调 OAuth endpoints
//   - 不引入 OAuth 第三方库 (golang.org/x/oauth2 也不引入,自己实现 PKCE)
//   - 跟 KPEnv (~/.kp/envs/) 同样的 yaml 文件存储模式
//
// 详细设计: docs/design/enterprise-governance.md (后续 commit)
package auth

import (
	"context"
	"time"
)

// AuthProvider 抽象 OAuth/OIDC 后端.
//
// 已知实现:
//   - DexProvider     internal/auth/dex.go     (OIDC discovery)
//   - GoogleProvider  internal/auth/google.go  (Google OAuth + OIDC)
//   - GitHubProvider  internal/auth/github.go  (GitHub OAuth, 非 OIDC)
//
// 实现类必须线程安全 (kp login 是顺序流程,但 token verify 可能并发).
type AuthProvider interface {
	// Name 返回 provider 标识名 ("dex" / "google" / "github").
	// 用于 credentials 文件命名: ~/.kp/credentials/<name>.yaml
	Name() string

	// Login 执行完整 OAuth2 授权码流程:
	//   1. 生成 state + PKCE code_verifier
	//   2. 起本地 callback server (端口 18888, Q-A1=B 固定端口)
	//   3. 打开浏览器到 authorization endpoint
	//   4. 等用户授权 → 收到 code
	//   5. POST 换 token
	//   6. 调 userinfo endpoint 获取 UserInfo
	//
	// 失败时返回明确错误 (用户 deny / 端口冲突 / 网络失败 / token exchange 失败).
	Login(ctx context.Context) (*Token, *UserInfo, error)

	// VerifyToken 验证 token 有效性 + 解析 UserInfo.
	//
	// 用于:
	//   - controller 端 token 校验 (留给 B RBAC, 本模块不接入)
	//   - kp 命令行复用 token 时确认有效性 (Q-A4=A whoami 默认不调用此方法)
	VerifyToken(ctx context.Context, token *Token) (*UserInfo, error)
}

// Token OAuth2 token 数据.
//
// 持久化到 ~/.kp/credentials/<provider>.yaml
type Token struct {
	AccessToken  string    `yaml:"access_token"`
	RefreshToken string    `yaml:"refresh_token,omitempty"`
	TokenType    string    `yaml:"token_type"`            // "Bearer"
	ExpiresAt    time.Time `yaml:"expires_at"`            // 绝对时间, 跨重启稳定
	IDToken      string    `yaml:"id_token,omitempty"`    // OIDC only (Dex/Google)
}

// Expired 判断 token 是否已过期.
//
// Q-A2=B 拍板: 过期不自动刷新, 直接 fail 让用户 kp login.
// 留 30s 缓冲避免边界场景误判.
func (t *Token) Expired() bool {
	if t == nil {
		return true
	}
	return time.Now().Add(30 * time.Second).After(t.ExpiresAt)
}

// UserInfo OAuth/OIDC userinfo endpoint 返回的用户信息.
//
// 字段约定 (与 OIDC standard claims 对齐):
//   - Subject: 唯一标识 (OIDC sub claim)
//   - Email:   邮箱 (可能未验证)
//   - Name:    显示名
//   - Groups:  组列表 (provider 自定义, Dex 通过 connector 提供)
type UserInfo struct {
	Subject string   `yaml:"subject"`            // OAuth sub claim
	Email   string   `yaml:"email"`
	Name    string   `yaml:"name"`
	Groups  []string `yaml:"groups,omitempty"`   // 用于 B RBAC 的团队映射
}
