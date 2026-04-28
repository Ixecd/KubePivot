// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ════════════════════════════════════════════════════════════════════════════
// Token 测试 (基础类型)
// ════════════════════════════════════════════════════════════════════════════

func TestToken_Expired_Future(t *testing.T) {
	tok := &Token{ExpiresAt: time.Now().Add(time.Hour)}
	assert.False(t, tok.Expired(), "1 小时后过期 → 当前未过期")
}

func TestToken_Expired_Past(t *testing.T) {
	tok := &Token{ExpiresAt: time.Now().Add(-time.Hour)}
	assert.True(t, tok.Expired(), "1 小时前已过期")
}

func TestToken_Expired_NearExpiry(t *testing.T) {
	// 30s 缓冲: 距离过期不到 30s 也算过期 (避免边界)
	tok := &Token{ExpiresAt: time.Now().Add(10 * time.Second)}
	assert.True(t, tok.Expired(), "10s 后过期 → 30s 缓冲内已视为过期")
}

func TestToken_Expired_NilSafe(t *testing.T) {
	var tok *Token
	assert.True(t, tok.Expired(), "nil token 视为过期 (防御性)")
}

// ════════════════════════════════════════════════════════════════════════════
// PKCE / state 生成
// ════════════════════════════════════════════════════════════════════════════

func TestGeneratePKCE_Format(t *testing.T) {
	verifier, challenge, err := generatePKCE()
	require.NoError(t, err)

	// verifier 长度 = base64url(32 bytes) = 43 chars
	assert.Len(t, verifier, 43, "verifier 应为 43 字符")

	// challenge 长度 = base64url(SHA256 = 32 bytes) = 43 chars
	assert.Len(t, challenge, 43, "challenge 应为 43 字符")

	// base64url 字符集校验: [A-Za-z0-9_-]
	for _, c := range verifier {
		assert.True(t,
			(c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
				(c >= '0' && c <= '9') || c == '-' || c == '_',
			"verifier 含非 base64url 字符: %c", c)
	}
}

func TestGeneratePKCE_Randomness(t *testing.T) {
	v1, _, _ := generatePKCE()
	v2, _, _ := generatePKCE()
	assert.NotEqual(t, v1, v2, "每次调用应产生不同 verifier")
}

func TestGeneratePKCE_SHA256Mapping(t *testing.T) {
	// 验证 challenge = base64url(SHA256(verifier))
	verifier, challenge, err := generatePKCE()
	require.NoError(t, err)

	// 自己重算一遍
	hash := sha256Sum([]byte(verifier))
	expected := base64.RawURLEncoding.EncodeToString(hash)

	assert.Equal(t, expected, challenge, "challenge 应等于 base64url(SHA256(verifier))")
}

func TestGenerateState_Format(t *testing.T) {
	state, err := generateState()
	require.NoError(t, err)
	assert.Len(t, state, 22, "state 应为 22 字符 (16 bytes base64url)")
}

func TestGenerateState_Randomness(t *testing.T) {
	s1, _ := generateState()
	s2, _ := generateState()
	assert.NotEqual(t, s1, s2, "每次 state 应不同")
}

// sha256Sum 辅助: 重算 SHA256 用于 PKCE 测试
func sha256Sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

// ════════════════════════════════════════════════════════════════════════════
// OAuth2Config / buildAuthURL 测试
// ════════════════════════════════════════════════════════════════════════════

func TestBuildAuthURL_AllParams(t *testing.T) {
	cfg := &OAuth2Config{
		ClientID:    "test-client",
		AuthURL:     "https://example.com/auth",
		Scopes:      []string{"openid", "email"},
		UsePKCE:     true,
	}
	got := buildAuthURL(cfg, "test-state", "test-challenge", "http://localhost:18888/callback")

	assert.Contains(t, got, "client_id=test-client")
	assert.Contains(t, got, "redirect_uri=http%3A%2F%2Flocalhost%3A18888%2Fcallback")
	assert.Contains(t, got, "response_type=code")
	assert.Contains(t, got, "scope=openid+email")
	assert.Contains(t, got, "state=test-state")
	assert.Contains(t, got, "code_challenge=test-challenge")
	assert.Contains(t, got, "code_challenge_method=S256")
}

func TestBuildAuthURL_NoPKCE(t *testing.T) {
	cfg := &OAuth2Config{
		ClientID: "test",
		AuthURL:  "https://example.com/auth",
		Scopes:   []string{"openid"},
		UsePKCE:  false,
	}
	got := buildAuthURL(cfg, "state1", "", "http://localhost:18888/callback")

	assert.NotContains(t, got, "code_challenge")
	assert.NotContains(t, got, "code_challenge_method")
}

// ════════════════════════════════════════════════════════════════════════════
// fetchUserInfo 测试 (用 httptest server mock OIDC userinfo endpoint)
// ════════════════════════════════════════════════════════════════════════════

func TestFetchUserInfo_StandardOIDC(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"sub": "user-123",
			"email": "alice@example.com",
			"name": "Alice",
			"groups": ["dev", "admin"]
		}`))
	}))
	defer server.Close()

	tok := &Token{AccessToken: "test-token", ExpiresAt: time.Now().Add(time.Hour)}
	info, err := fetchUserInfo(context.Background(), server.URL, tok)
	require.NoError(t, err)

	assert.Equal(t, "user-123", info.Subject)
	assert.Equal(t, "alice@example.com", info.Email)
	assert.Equal(t, "Alice", info.Name)
	assert.Equal(t, []string{"dev", "admin"}, info.Groups)
}

func TestFetchUserInfo_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	tok := &Token{AccessToken: "bad-token", ExpiresAt: time.Now().Add(time.Hour)}
	_, err := fetchUserInfo(context.Background(), server.URL, tok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
}

// ════════════════════════════════════════════════════════════════════════════
// OIDC Discovery 测试
// ════════════════════════════════════════════════════════════════════════════

func TestFetchOIDCDiscovery_Standard(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/.well-known/openid-configuration", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"issuer": "https://dex.example.com",
			"authorization_endpoint": "https://dex.example.com/auth",
			"token_endpoint": "https://dex.example.com/token",
			"userinfo_endpoint": "https://dex.example.com/userinfo"
		}`))
	}))
	defer server.Close()

	doc, err := fetchOIDCDiscovery(context.Background(), server.URL)
	require.NoError(t, err)

	assert.Equal(t, "https://dex.example.com/auth", doc.AuthorizationEndpoint)
	assert.Equal(t, "https://dex.example.com/token", doc.TokenEndpoint)
	assert.Equal(t, "https://dex.example.com/userinfo", doc.UserInfoEndpoint)
}

func TestFetchOIDCDiscovery_MissingFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer": "https://dex.example.com"}`))
	}))
	defer server.Close()

	_, err := fetchOIDCDiscovery(context.Background(), server.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "缺少必需字段")
}

func TestFetchOIDCDiscovery_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	_, err := fetchOIDCDiscovery(context.Background(), server.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestNewDexProvider_EmptyIssuer(t *testing.T) {
	_, err := NewDexProvider("", "client", "secret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "issuer")
}

func TestNewDexProvider_EmptyClientID(t *testing.T) {
	_, err := NewDexProvider("https://dex.example.com", "", "secret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client_id")
}

// ════════════════════════════════════════════════════════════════════════════
// Provider 构造测试
// ════════════════════════════════════════════════════════════════════════════

func TestNewGoogleProvider_Valid(t *testing.T) {
	p, err := NewGoogleProvider("test-client.apps.googleusercontent.com", "test-secret")
	require.NoError(t, err)
	assert.Equal(t, "google", p.Name())
}

func TestNewGoogleProvider_EmptyClientID(t *testing.T) {
	_, err := NewGoogleProvider("", "secret")
	require.Error(t, err)
}

func TestNewGitHubProvider_Valid(t *testing.T) {
	p, err := NewGitHubProvider("Iv1.test", "test-secret")
	require.NoError(t, err)
	assert.Equal(t, "github", p.Name())
}

func TestNewGitHubProvider_EmptyClientSecret(t *testing.T) {
	_, err := NewGitHubProvider("Iv1.test", "")
	require.Error(t, err, "GitHub 必填 client_secret (不支持 PKCE)")
}

// ════════════════════════════════════════════════════════════════════════════
// Credentials 持久化测试
// ════════════════════════════════════════════════════════════════════════════

// 测试 credentials 路径 (用 t.TempDir 隔离)
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestSaveAndLoadCredentials_RoundTrip(t *testing.T) {
	withTempHome(t)

	tok := &Token{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(time.Hour).UTC().Truncate(time.Second),
		IDToken:      "id-789",
	}
	info := &UserInfo{
		Subject: "user-123",
		Email:   "alice@example.com",
		Name:    "Alice",
		Groups:  []string{"dev"},
	}

	require.NoError(t, SaveCredentials("google", tok, info))

	gotTok, gotInfo, err := LoadCredentials("google")
	require.NoError(t, err)

	assert.Equal(t, tok.AccessToken, gotTok.AccessToken)
	assert.Equal(t, tok.IDToken, gotTok.IDToken)
	assert.True(t, tok.ExpiresAt.Equal(gotTok.ExpiresAt))

	assert.Equal(t, info.Subject, gotInfo.Subject)
	assert.Equal(t, info.Email, gotInfo.Email)
	assert.Equal(t, info.Groups, gotInfo.Groups)
}

func TestLoadCredentials_NotFound(t *testing.T) {
	withTempHome(t)

	_, _, err := LoadCredentials("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kp login --provider nonexistent",
		"应给出明确 hint")
}

func TestSetAndGetDefault_RoundTrip(t *testing.T) {
	withTempHome(t)

	require.NoError(t, SetDefault("google"))
	got, err := GetDefault()
	require.NoError(t, err)
	assert.Equal(t, "google", got)

	// 切换 default
	require.NoError(t, SetDefault("github"))
	got, err = GetDefault()
	require.NoError(t, err)
	assert.Equal(t, "github", got)
}

func TestGetDefault_NotSet(t *testing.T) {
	withTempHome(t)

	_, err := GetDefault()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "未登录")
	assert.Contains(t, err.Error(), "kp login")
}

func TestListCredentials_Empty(t *testing.T) {
	withTempHome(t)

	providers, err := ListCredentials()
	require.NoError(t, err)
	assert.Empty(t, providers)
}

func TestListCredentials_MultipleProviders(t *testing.T) {
	withTempHome(t)

	for _, name := range []string{"google", "github", "dex"} {
		tok := &Token{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)}
		info := &UserInfo{Subject: "s", Email: name + "@x.com"}
		require.NoError(t, SaveCredentials(name, tok, info))
	}
	require.NoError(t, SetDefault("google"))

	providers, err := ListCredentials()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"google", "github", "dex"}, providers,
		"不应包含 default.yaml 自身")
}

func TestDeleteCredentials_Idempotent(t *testing.T) {
	withTempHome(t)

	// 删除不存在的不应报错
	require.NoError(t, DeleteCredentials("nonexistent"))

	// 真删除
	tok := &Token{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)}
	info := &UserInfo{Subject: "s", Email: "e"}
	require.NoError(t, SaveCredentials("google", tok, info))
	require.NoError(t, DeleteCredentials("google"))

	_, _, err := LoadCredentials("google")
	require.Error(t, err)
}

func TestCredentialsPath_Format(t *testing.T) {
	withTempHome(t)
	path := CredentialsPath("google")
	assert.True(t, strings.HasSuffix(path, ".kp/credentials/google.yaml"),
		"路径应符合 ~/.kp/credentials/<provider>.yaml 格式, got: %s", path)
}

// ════════════════════════════════════════════════════════════════════════════
// 文件权限测试 (敏感数据)
// ════════════════════════════════════════════════════════════════════════════

func TestSaveCredentials_FilePermission0600(t *testing.T) {
	withTempHome(t)

	tok := &Token{AccessToken: "secret", ExpiresAt: time.Now().Add(time.Hour)}
	info := &UserInfo{Subject: "s", Email: "e"}
	require.NoError(t, SaveCredentials("google", tok, info))

	stat, err := os.Stat(CredentialsPath("google"))
	require.NoError(t, err)

	mode := stat.Mode().Perm()
	assert.Equal(t, os.FileMode(0o600), mode,
		"credentials 文件应为 0600 权限 (含敏感 token)")
}

// ════════════════════════════════════════════════════════════════════════════
// callback server 集成测试 (端口 18888 占用 + state 校验)
// ════════════════════════════════════════════════════════════════════════════

func TestStartCallbackServer_StateMismatch(t *testing.T) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	server, err := startCallbackServer("expected-state", codeCh, errCh)
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	// 模拟错误 state 的回调
	resp, err := http.Get("http://localhost:18888/callback?state=wrong-state&code=abc")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	select {
	case e := <-errCh:
		assert.Contains(t, e.Error(), "CSRF")
	case <-time.After(time.Second):
		t.Fatal("应收到 CSRF 错误")
	}
}

func TestStartCallbackServer_Success(t *testing.T) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	server, err := startCallbackServer("good-state", codeCh, errCh)
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	resp, err := http.Get("http://localhost:18888/callback?state=good-state&code=expected-code")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	select {
	case code := <-codeCh:
		assert.Equal(t, "expected-code", code)
	case <-time.After(time.Second):
		t.Fatal("应收到 code")
	}
}

func TestStartCallbackServer_OAuthError(t *testing.T) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	server, err := startCallbackServer("any", codeCh, errCh)
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	resp, _ := http.Get("http://localhost:18888/callback?state=any&error=access_denied&error_description=user_cancelled")
	if resp != nil {
		resp.Body.Close()
	}

	select {
	case e := <-errCh:
		assert.Contains(t, e.Error(), "access_denied")
	case <-time.After(time.Second):
		t.Fatal("应收到 OAuth provider 错误")
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Fake Provider 测试 - 完整 Login → Save → Load 闭环
// ════════════════════════════════════════════════════════════════════════════

// FakeProvider 用于测试完整 SaveCredentials / GetDefault / LoadCredentials 闭环.
// 不跑真 OAuth, 只验证管线衔接正确.
type FakeProvider struct {
	name     string
	loginErr error
	token    *Token
	info     *UserInfo
}

func (f *FakeProvider) Name() string { return f.name }
func (f *FakeProvider) Login(ctx context.Context) (*Token, *UserInfo, error) {
	if f.loginErr != nil {
		return nil, nil, f.loginErr
	}
	return f.token, f.info, nil
}
func (f *FakeProvider) VerifyToken(ctx context.Context, token *Token) (*UserInfo, error) {
	return f.info, nil
}

func TestEndToEndFlow_SaveLoadDefault(t *testing.T) {
	withTempHome(t)

	fake := &FakeProvider{
		name: "fake",
		token: &Token{
			AccessToken: "fake-token",
			ExpiresAt:   time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second),
		},
		info: &UserInfo{
			Subject: "fake-sub",
			Email:   "fake@example.com",
			Name:    "Fake User",
			Groups:  []string{"testers"},
		},
	}

	// 模拟 kp login 主流程
	ctx := context.Background()
	tok, info, err := fake.Login(ctx)
	require.NoError(t, err)

	require.NoError(t, SaveCredentials(fake.Name(), tok, info))
	require.NoError(t, SetDefault(fake.Name()))

	// 模拟 kp whoami 主流程
	defaultProvider, err := GetDefault()
	require.NoError(t, err)
	assert.Equal(t, "fake", defaultProvider)

	gotTok, gotInfo, err := LoadCredentials(defaultProvider)
	require.NoError(t, err)
	assert.Equal(t, "fake-token", gotTok.AccessToken)
	assert.Equal(t, "fake@example.com", gotInfo.Email)
	assert.False(t, gotTok.Expired())
}

func TestEndToEndFlow_LoginErrorDoesNotPersist(t *testing.T) {
	withTempHome(t)

	fake := &FakeProvider{
		name:     "fake",
		loginErr: errors.New("user cancelled"),
	}

	_, _, err := fake.Login(context.Background())
	require.Error(t, err)

	// credentials 应未被写入
	_, _, err = LoadCredentials("fake")
	require.Error(t, err)
}

// ════════════════════════════════════════════════════════════════════════════
// 测试用 home 验证
// ════════════════════════════════════════════════════════════════════════════

func TestCredentialsDir_Path(t *testing.T) {
	home := withTempHome(t)
	dir := CredentialsDir()
	assert.Equal(t, filepath.Join(home, ".kp", "credentials"), dir)
}
