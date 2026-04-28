// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ════════════════════════════════════════════════════════════════════════════
// 通用 OAuth2 流程实现 (Authorization Code + PKCE)
//
// 不引入 golang.org/x/oauth2 (KubePivot 哲学: 标准库优先).
// PKCE = Proof Key for Code Exchange (RFC 7636).
// ════════════════════════════════════════════════════════════════════════════

const (
	// Q-A1=B 拍板: 固定端口 18888 (kp 谐音, 不撞常用端口)
	DefaultCallbackPort = 18888
	DefaultCallbackPath = "/callback"

	// 默认 OAuth 流程总超时 (用户授权窗口)
	defaultOAuthTimeout = 5 * time.Minute
)

// OAuth2Config OAuth2 provider 通用配置.
type OAuth2Config struct {
	ClientID     string
	ClientSecret string
	AuthURL      string   // authorization endpoint
	TokenURL     string   // token exchange endpoint
	UserInfoURL  string   // userinfo endpoint
	Scopes       []string // OAuth scopes
	UsePKCE      bool     // 是否启用 PKCE (推荐 true)
}

// runOAuth2Flow 执行完整 OAuth2 授权码流程.
//
// 流程:
//
//  1. 生成 state (CSRF 防护) + PKCE code_verifier
//  2. 起本地 callback server (端口 DefaultCallbackPort)
//  3. 浏览器打开 authorization URL
//  4. 用户授权 → 重定向到 callback → 拿到 code
//  5. POST 换 token (带 code_verifier 完成 PKCE)
//  6. 关闭 callback server
//
// 错误场景:
//
//   - 端口冲突: returned 错误带 hint "端口 18888 被占用"
//   - 用户取消: returned 错误说明
//   - code 交换失败: 完整保留 OAuth provider 错误信息
func runOAuth2Flow(ctx context.Context, cfg *OAuth2Config) (*Token, error) {
	// 1. 生成 state + PKCE
	state, err := generateState()
	if err != nil {
		return nil, fmt.Errorf("生成 state 失败: %w", err)
	}

	var codeVerifier, codeChallenge string
	if cfg.UsePKCE {
		codeVerifier, codeChallenge, err = generatePKCE()
		if err != nil {
			return nil, fmt.Errorf("生成 PKCE 失败: %w", err)
		}
	}

	// 2. 起本地 callback server
	redirectURI := fmt.Sprintf("http://localhost:%d%s", DefaultCallbackPort, DefaultCallbackPath)
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	server, err := startCallbackServer(state, codeCh, errCh)
	if err != nil {
		return nil, fmt.Errorf("启动回调 server 失败 (端口 %d 可能被占用): %w",
			DefaultCallbackPort, err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	// 3. 构造 authorization URL
	authURL := buildAuthURL(cfg, state, codeChallenge, redirectURI)

	// 4. 打开浏览器 (失败也继续, 用户可以手动复制 URL)
	if err := openBrowser(authURL); err != nil {
		fmt.Printf("⚠️  浏览器打开失败,请手动访问以下 URL 完成授权:\n%s\n\n", authURL)
	}

	// 5. 等待 callback (有超时 + ctx 取消)
	timeoutCtx, cancel := context.WithTimeout(ctx, defaultOAuthTimeout)
	defer cancel()

	var code string
	select {
	case code = <-codeCh:
		// got it
	case err := <-errCh:
		return nil, err
	case <-timeoutCtx.Done():
		return nil, fmt.Errorf("OAuth 流程超时 (%v): 用户未在窗口内完成授权",
			defaultOAuthTimeout)
	}

	// 6. POST 换 token (with code_verifier)
	token, err := exchangeCodeForToken(timeoutCtx, cfg, code, codeVerifier, redirectURI)
	if err != nil {
		return nil, fmt.Errorf("token 交换失败: %w", err)
	}

	return token, nil
}

// startCallbackServer 起本地 HTTP server 监听 OAuth callback.
//
// 收到合法 callback 后通过 codeCh 返回 code, 错误通过 errCh.
// state 必须匹配, 否则 errCh 报 CSRF 错误.
func startCallbackServer(expectedState string, codeCh chan<- string, errCh chan<- error) (*http.Server, error) {
	mux := http.NewServeMux()
	mux.HandleFunc(DefaultCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		// 校验 state
		if r.URL.Query().Get("state") != expectedState {
			http.Error(w, "state 不匹配 (CSRF)", http.StatusBadRequest)
			errCh <- fmt.Errorf("OAuth state 不匹配 (CSRF 防护触发)")
			return
		}

		// 检查 OAuth provider 错误
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			desc := r.URL.Query().Get("error_description")
			http.Error(w, fmt.Sprintf("授权失败: %s (%s)", errMsg, desc), http.StatusBadRequest)
			errCh <- fmt.Errorf("OAuth provider 报错: %s (%s)", errMsg, desc)
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "code 为空", http.StatusBadRequest)
			errCh <- fmt.Errorf("OAuth callback 没有 code 参数")
			return
		}

		// 成功页 (用户看到的)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!DOCTYPE html>
<html><body style="font-family: sans-serif; text-align: center; padding-top: 100px;">
<h1>✅ KubePivot 登录成功</h1>
<p>请关闭此页面回到终端.</p>
</body></html>`)
		codeCh <- code
	})

	// 监听端口
	addr := fmt.Sprintf("localhost:%d", DefaultCallbackPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = server.Serve(listener)
	}()
	return server, nil
}

// buildAuthURL 构造 OAuth authorization URL (含 state + PKCE).
func buildAuthURL(cfg *OAuth2Config, state, codeChallenge, redirectURI string) string {
	params := url.Values{}
	params.Set("client_id", cfg.ClientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("response_type", "code")
	params.Set("scope", strings.Join(cfg.Scopes, " "))
	params.Set("state", state)
	if cfg.UsePKCE && codeChallenge != "" {
		params.Set("code_challenge", codeChallenge)
		params.Set("code_challenge_method", "S256")
	}
	return cfg.AuthURL + "?" + params.Encode()
}

// exchangeCodeForToken 用 code 换 token (with PKCE code_verifier).
func exchangeCodeForToken(ctx context.Context, cfg *OAuth2Config, code, codeVerifier, redirectURI string) (*Token, error) {
	params := url.Values{}
	params.Set("grant_type", "authorization_code")
	params.Set("code", code)
	params.Set("redirect_uri", redirectURI)
	params.Set("client_id", cfg.ClientID)
	if cfg.ClientSecret != "" {
		params.Set("client_secret", cfg.ClientSecret)
	}
	if codeVerifier != "" {
		params.Set("code_verifier", codeVerifier)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", cfg.TokenURL,
		strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("构造 token 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token 端点请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body := make([]byte, 1024)
		n, _ := resp.Body.Read(body)
		return nil, fmt.Errorf("token 端点返回 %d: %s", resp.StatusCode, string(body[:n]))
	}

	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		IDToken      string `json:"id_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("解析 token 响应失败: %w", err)
	}

	expiresAt := time.Now()
	if raw.ExpiresIn > 0 {
		expiresAt = expiresAt.Add(time.Duration(raw.ExpiresIn) * time.Second)
	} else {
		// 没有 expires_in 字段, 默认 1 小时
		expiresAt = expiresAt.Add(time.Hour)
	}

	return &Token{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		TokenType:    raw.TokenType,
		ExpiresAt:    expiresAt,
		IDToken:      raw.IDToken,
	}, nil
}

// ════════════════════════════════════════════════════════════════════════════
// PKCE / state 生成
// ════════════════════════════════════════════════════════════════════════════

// generatePKCE 生成 PKCE code_verifier 和 code_challenge.
//
// RFC 7636:
//
//	code_verifier = high-entropy random string (43-128 chars, [A-Z][a-z][0-9]-._~)
//	code_challenge = base64url(SHA256(code_verifier))
//
// 我们用 32 字节随机 → base64url 后 43 字符.
func generatePKCE() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)

	hash := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(hash[:])
	return verifier, challenge, nil
}

// generateState 生成随机 state (CSRF 防护).
func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ════════════════════════════════════════════════════════════════════════════
// 跨平台浏览器打开
// ════════════════════════════════════════════════════════════════════════════

// openBrowser 跨平台打开 URL.
//
// macOS:   open <url>
// Linux:   xdg-open <url>
// Windows: rundll32 url.dll,FileProtocolHandler <url>
func openBrowser(rawURL string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{rawURL}
	case "linux":
		cmd = "xdg-open"
		args = []string{rawURL}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", rawURL}
	default:
		return fmt.Errorf("不支持的操作系统: %s", runtime.GOOS)
	}

	return exec.Command(cmd, args...).Start()
}

// ════════════════════════════════════════════════════════════════════════════
// userinfo endpoint 通用调用
// ════════════════════════════════════════════════════════════════════════════

// fetchUserInfo 调用 userinfo endpoint (Bearer token).
//
// 标准 OIDC userinfo 字段映射:
//
//	sub   → UserInfo.Subject
//	email → UserInfo.Email
//	name  → UserInfo.Name
//	groups → UserInfo.Groups (Dex 自定义)
//
// 非标 provider (如 GitHub) 通过其他函数处理.
func fetchUserInfo(ctx context.Context, userInfoURL string, token *Token) (*UserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", userInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造 userinfo 请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body := make([]byte, 512)
		n, _ := resp.Body.Read(body)
		return nil, fmt.Errorf("userinfo 端点返回 %d: %s", resp.StatusCode, string(body[:n]))
	}

	var raw struct {
		Sub    string   `json:"sub"`
		Email  string   `json:"email"`
		Name   string   `json:"name"`
		Groups []string `json:"groups"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("解析 userinfo 响应失败: %w", err)
	}

	return &UserInfo{
		Subject: raw.Sub,
		Email:   raw.Email,
		Name:    raw.Name,
		Groups:  raw.Groups,
	}, nil
}
