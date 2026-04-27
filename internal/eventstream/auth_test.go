package eventstream

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── 测试用：fake CA cert（PEM 格式，仅用于测试 PEM 解析路径）─────

// fakeCAPEM 是一个最小的有效 PEM 块（不需要可用证书，仅测试 AppendCertsFromPEM 路径）。
// 这是 Go x509 文档中的示例 CA 证书片段，仅用于测试解析能否成功。
//
// 用 cert：openssl req -x509 -newkey rsa:2048 -keyout /tmp/k.pem -out /tmp/c.pem \
//          -days 1 -nodes -subj "/CN=test"
const fakeCAPEM = `-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dNuaTAKBggqhkjOPQQDAjASMRAw
DgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow
EjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d
7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B
5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr
BgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1
NDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l
Wf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBmLiAFQOyTDT+/wQc
6MF9+Yw1Yy0t
-----END CERTIFICATE-----`

// ─── Helper: 替换 readTokenFile / readCAFile ─────────────────────

func withMockedFiles(t *testing.T, tokenContent string, caContent string, tokenErr, caErr error) func() {
	t.Helper()
	oldRead := readTokenFile
	oldCA := readCAFile

	readTokenFile = func() ([]byte, error) {
		if tokenErr != nil {
			return nil, tokenErr
		}
		return []byte(tokenContent), nil
	}
	readCAFile = func() ([]byte, error) {
		if caErr != nil {
			return nil, caErr
		}
		return []byte(caContent), nil
	}

	return func() {
		readTokenFile = oldRead
		readCAFile = oldCA
	}
}

// ─── 测试 K8sConfig 访问器 ────────────────────────────────────────

func TestK8sConfig_Accessors(t *testing.T) {
	cfg := &K8sConfig{
		apiServerURL: "https://example.com",
		httpClient:   &http.Client{},
		source:       "test",
	}

	if cfg.APIServerURL() != "https://example.com" {
		t.Errorf("APIServerURL() = %q, want %q", cfg.APIServerURL(), "https://example.com")
	}
	if cfg.HTTPClient() == nil {
		t.Error("HTTPClient() returned nil")
	}
	if cfg.Source() != "test" {
		t.Errorf("Source() = %q, want %q", cfg.Source(), "test")
	}
}

// ─── 测试 resolveK8sConfig 路径选择 ──────────────────────────────

func TestResolveK8sConfig_PrefersExplicitURL(t *testing.T) {
	cfg, err := resolveK8sConfig(InformerOptions{
		APIServerURL: "http://127.0.0.1:8080",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Source() != "explicit" {
		t.Errorf("source=%q, want explicit", cfg.Source())
	}
}

func TestResolveK8sConfig_FallbackToKubeconfig(t *testing.T) {
	// kubeconfig 路径暂未实施，应返回明确 error
	_, err := resolveK8sConfig(InformerOptions{
		KubeConfig: "/tmp/nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for kubeconfig path")
	}
	if !strings.Contains(err.Error(), "kubeconfig") {
		t.Errorf("error 应提示 kubeconfig 路径未实施，得到: %v", err)
	}
}

func TestResolveK8sConfig_FallbackToInCluster(t *testing.T) {
	restore := withMockedFiles(t, "fake-token-12345", fakeCAPEM, nil, nil)
	defer restore()

	cfg, err := resolveK8sConfig(InformerOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Source() != "in-cluster" {
		t.Errorf("source=%q, want in-cluster", cfg.Source())
	}
	if cfg.APIServerURL() != inClusterAPIServer {
		t.Errorf("APIServerURL=%q, want %q", cfg.APIServerURL(), inClusterAPIServer)
	}
}

// ─── 测试路径 1: 显式 URL ────────────────────────────────────────

func TestResolveExplicit_HTTPNoTLS(t *testing.T) {
	cfg, err := resolveExplicit("http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Source() != "explicit" {
		t.Errorf("source=%q, want explicit", cfg.Source())
	}
	// http:// 场景：不应配 TLSConfig
	transport := cfg.HTTPClient().Transport.(*http.Transport)
	if transport.TLSClientConfig != nil {
		t.Error("http:// 路径不应配 TLSConfig")
	}
}

func TestResolveExplicit_HTTPSDefaultRootCA(t *testing.T) {
	cfg, err := resolveExplicit("https://kubernetes.default.svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// https:// 显式路径：不显式配 TLSConfig，让 Go 默认 root CA 校验
	transport := cfg.HTTPClient().Transport.(*http.Transport)
	if transport.TLSClientConfig != nil {
		t.Error("https:// 显式路径不应显式配 TLSConfig（让 Go 默认 root CA 校验）")
	}
}

func TestResolveExplicit_TrimsTrailingSlash(t *testing.T) {
	cfg, err := resolveExplicit("http://example.com/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.APIServerURL() != "http://example.com" {
		t.Errorf("APIServerURL=%q, want trimmed http://example.com", cfg.APIServerURL())
	}
}

func TestResolveExplicit_RejectsInvalidURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"missing scheme", "example.com"},
		{"unsupported scheme", "ftp://example.com"},
		{"missing host", "https://"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveExplicit(tt.url)
			if err == nil {
				t.Errorf("expected error for url=%q", tt.url)
			}
		})
	}
}

// ─── 测试路径 2: kubeconfig（未实施） ────────────────────────────

func TestResolveKubeconfig_NotImplemented(t *testing.T) {
	_, err := resolveKubeconfig("/tmp/some-kubeconfig")
	if err == nil {
		t.Fatal("expected error for unimplemented kubeconfig path")
	}
	if !strings.Contains(err.Error(), "kubeconfig") {
		t.Errorf("error 应提示 kubeconfig，得到: %v", err)
	}
	if !strings.Contains(err.Error(), "尚未实施") {
		t.Errorf("error 应明确未实施信息，得到: %v", err)
	}
}

// ─── 测试路径 3: in-cluster ─────────────────────────────────────

func TestResolveInCluster_Success(t *testing.T) {
	restore := withMockedFiles(t, "valid-token", fakeCAPEM, nil, nil)
	defer restore()

	cfg, err := resolveInCluster()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Source() != "in-cluster" {
		t.Errorf("source=%q, want in-cluster", cfg.Source())
	}
	if cfg.APIServerURL() != inClusterAPIServer {
		t.Errorf("APIServerURL=%q, want %q", cfg.APIServerURL(), inClusterAPIServer)
	}

	// 验证 TLS 配置不允许 InsecureSkipVerify
	transport := cfg.HTTPClient().Transport
	bat, ok := transport.(*bearerAuthTransport)
	if !ok {
		t.Fatalf("transport 类型 = %T, want *bearerAuthTransport", transport)
	}
	baseTransport, ok := bat.base.(*http.Transport)
	if !ok {
		t.Fatalf("base transport 类型 = %T, want *http.Transport", bat.base)
	}
	if baseTransport.TLSClientConfig == nil {
		t.Fatal("in-cluster 路径必须配 TLSConfig")
	}
	if baseTransport.TLSClientConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify 必须为 false")
	}
	if baseTransport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion=%v, want TLS 1.2", baseTransport.TLSClientConfig.MinVersion)
	}
	if baseTransport.TLSClientConfig.RootCAs == nil {
		t.Error("RootCAs 必须非 nil（CA 校验必需）")
	}
}

func TestResolveInCluster_TokenReadError(t *testing.T) {
	restore := withMockedFiles(t, "", fakeCAPEM, errors.New("file not found"), nil)
	defer restore()

	_, err := resolveInCluster()
	if err == nil {
		t.Fatal("expected error for token read failure")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error 应提示 token 失败，得到: %v", err)
	}
}

func TestResolveInCluster_EmptyToken(t *testing.T) {
	restore := withMockedFiles(t, "   \n  \n", fakeCAPEM, nil, nil)
	defer restore()

	_, err := resolveInCluster()
	if err == nil {
		t.Fatal("expected error for empty token")
	}
	if !strings.Contains(err.Error(), "为空") {
		t.Errorf("error 应提示 token 为空，得到: %v", err)
	}
}

func TestResolveInCluster_CAReadError(t *testing.T) {
	restore := withMockedFiles(t, "valid-token", "", nil, errors.New("ca file missing"))
	defer restore()

	_, err := resolveInCluster()
	if err == nil {
		t.Fatal("expected error for CA read failure")
	}
	if !strings.Contains(err.Error(), "CA") {
		t.Errorf("error 应提示 CA 失败，得到: %v", err)
	}
}

func TestResolveInCluster_InvalidCA(t *testing.T) {
	restore := withMockedFiles(t, "valid-token", "not a valid PEM", nil, nil)
	defer restore()

	_, err := resolveInCluster()
	if err == nil {
		t.Fatal("expected error for invalid CA PEM")
	}
	if !strings.Contains(err.Error(), "CA") || !strings.Contains(err.Error(), "PEM") {
		t.Errorf("error 应提示 CA PEM 解析失败，得到: %v", err)
	}
}

// ─── Token 安全：error 信息不泄露 token ──────────────────────────

func TestResolveInCluster_ErrorDoesNotLeakToken(t *testing.T) {
	const sensitiveToken = "super-secret-bearer-token-do-not-leak-12345"

	// CA 失败场景：token 已读取成功但 CA 失败
	restore := withMockedFiles(t, sensitiveToken, "invalid-pem", nil, nil)
	defer restore()

	_, err := resolveInCluster()
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), sensitiveToken) {
		t.Errorf("error 信息泄露了 token: %v", err)
	}
}

// ─── 测试 defaultHTTPClient ────────────────────────────────────

func TestDefaultHTTPClient_NoTLS(t *testing.T) {
	client := defaultHTTPClient(nil)
	if client.Timeout != 0 {
		t.Errorf("Timeout=%v, want 0 (watch 用 ctx 控制)", client.Timeout)
	}
	transport := client.Transport.(*http.Transport)
	if transport.TLSClientConfig != nil {
		t.Error("nil tlsConfig 时不应设 TLSClientConfig")
	}
}

func TestDefaultHTTPClient_WithTLS(t *testing.T) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	client := defaultHTTPClient(tlsConfig)
	transport := client.Transport.(*http.Transport)
	if transport.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig 应被设置")
	}
	if transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Error("TLS 配置传递错误")
	}
}

// ─── 测试 bearerAuthTransport ──────────────────────────────────

func TestBearerAuthTransport_AddsHeader(t *testing.T) {
	const testToken = "test-token-xyz"

	// 用 httptest 验证 Authorization header
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transport := &bearerAuthTransport{
		token: testToken,
		base:  http.DefaultTransport,
	}
	client := &http.Client{Transport: transport}

	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	defer resp.Body.Close()

	expected := "Bearer " + testToken
	if receivedAuth != expected {
		t.Errorf("Authorization=%q, want %q", receivedAuth, expected)
	}
}

func TestBearerAuthTransport_DoesNotMutateOriginalRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transport := &bearerAuthTransport{
		token: "some-token",
		base:  http.DefaultTransport,
	}

	req, _ := http.NewRequest("GET", server.URL, nil)
	// 验证原 req 没有 Authorization header
	if req.Header.Get("Authorization") != "" {
		t.Fatal("原 req 不应有 Authorization header")
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip failed: %v", err)
	}
	defer resp.Body.Close()

	// 验证原 req **仍然没有** Authorization header（被 Clone 保护）
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("原 req 被污染了，Authorization=%q", got)
	}
}

// ─── 集成测试：bearerAuthTransport + httptest TLS ─────────────

func TestBearerAuthTransport_WithTLS(t *testing.T) {
	const testToken = "integration-token"
	var receivedAuth string

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// 使用 server 的 client（含正确的 TLS 配置）作为 base
	transport := &bearerAuthTransport{
		token: testToken,
		base:  server.Client().Transport,
	}
	client := &http.Client{Transport: transport}

	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	defer resp.Body.Close()

	if receivedAuth != "Bearer "+testToken {
		t.Errorf("Authorization=%q, want %q", receivedAuth, "Bearer "+testToken)
	}
}
