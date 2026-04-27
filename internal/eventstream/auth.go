package eventstream

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ─── K8s API Server Auth Resolution ───────────────────────────────
//
// v2.7 Step 2a-1：为 informer 提供真实 K8s 集群连接能力。
//
// 路径优先级（按 InformerOptions 字段决定，不做"猜测式 fallback"）：
//
//   1. opts.APIServerURL 非空 → 显式 URL，无 Bearer auth
//      - http://  → 零 TLS 配置（Day 3 测试 / fake server / kubectl proxy）
//      - https:// → Go 默认 root CA 校验（不显式配 TLS，不允许 InsecureSkipVerify）
//      - 自签证书场景必须走路径 2/3
//
//   2. opts.KubeConfig 非空 → 解析 kubeconfig 文件
//      - v2.7.0 暂不实施，返回 error（留 v2.7.x）
//      - 完整解析需支持 contexts / users / clusters 多 entry，~200 行+
//      - 当前生产场景被路径 3 覆盖，本地开发场景被路径 1 覆盖
//
//   3. 三者都空 → in-cluster ServiceAccount 自动检测
//      - URL: https://kubernetes.default.svc
//      - Token: /var/run/secrets/kubernetes.io/serviceaccount/token
//      - CA:    /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
//
// 安全约束（不可妥协）：
//   - InsecureSkipVerify 在任何路径都不允许
//   - Token 不存入 K8sConfig 字段（仅 transport 内部持有）
//   - error 信息不含 token 内容
//   - K8sConfig 字段全部不导出，外部无法修改

const (
	// serviceAccountTokenPath ServiceAccount token 路径（in-cluster 标准）
	serviceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

	// serviceAccountCAPath ServiceAccount CA 证书路径
	serviceAccountCAPath = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"

	// inClusterAPIServer in-cluster 默认 API server URL
	inClusterAPIServer = "https://kubernetes.default.svc"
)

// K8sConfig 解析后的 K8s 连接配置。
//
// 字段全部不导出，外部通过 APIServerURL() / HTTPClient() / Source() 访问。
// 这避免外部代码意外修改 token 或 TLS 配置。
type K8sConfig struct {
	apiServerURL string
	httpClient   *http.Client

	// source 标记配置来源（debug / log 用，不暴露 token）
	// 取值："explicit" / "kubeconfig" / "in-cluster"
	source string
}

// APIServerURL 返回 API server URL（含 scheme，无尾部斜杠）。
func (c *K8sConfig) APIServerURL() string {
	return c.apiServerURL
}

// HTTPClient 返回配置好的 HTTP client。
//
// 注意：watch 长连接不能用整体 timeout（client.Timeout=0），
// 调用方应通过 ctx 控制单次请求超时。
func (c *K8sConfig) HTTPClient() *http.Client {
	return c.httpClient
}

// Source 返回配置来源标识，用于 log / 调试。
// 不会泄露 token 等敏感信息。
func (c *K8sConfig) Source() string {
	return c.source
}

// ─── 入口：按优先级 resolve ─────────────────────────────────────

// resolveK8sConfig 按优先级 resolve K8s 连接配置。
//
// 不做"猜测式 fallback"——优先级由 InformerOptions 字段显式决定。
// 失败时返回 error，调用方决定 fail soft / 退出。
func resolveK8sConfig(opts InformerOptions) (*K8sConfig, error) {
	// 路径 1: 显式 APIServerURL
	if opts.APIServerURL != "" {
		return resolveExplicit(opts.APIServerURL)
	}

	// 路径 2: kubeconfig
	if opts.KubeConfig != "" {
		return resolveKubeconfig(opts.KubeConfig)
	}

	// 路径 3: in-cluster
	return resolveInCluster()
}

// ─── 路径 1: 显式 URL ────────────────────────────────────────────

// resolveExplicit 处理显式 APIServerURL 路径。
//
// http://  → 零 TLS 配置
// https:// → Go 默认 root CA 校验（不显式配 TLSConfig）
//
// 不允许自签证书或 InsecureSkipVerify。
// 自签场景请使用路径 2 (kubeconfig) 或路径 3 (in-cluster)。
func resolveExplicit(apiServerURL string) (*K8sConfig, error) {
	u, err := url.Parse(apiServerURL)
	if err != nil {
		return nil, fmt.Errorf("eventstream(auth): 无效 APIServerURL: %w", err)
	}
	if u.Scheme == "" {
		return nil, fmt.Errorf("eventstream(auth): APIServerURL 缺 scheme: %s", apiServerURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("eventstream(auth): APIServerURL 仅支持 http/https，得到: %s", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("eventstream(auth): APIServerURL 缺 host: %s", apiServerURL)
	}

	// 不显式配 TLSConfig，让 Go 默认行为生效：
	// - http:// 不走 TLS
	// - https:// 用系统 root CA 校验
	return &K8sConfig{
		apiServerURL: strings.TrimRight(apiServerURL, "/"),
		httpClient:   defaultHTTPClient(nil),
		source:       "explicit",
	}, nil
}

// ─── 路径 2: kubeconfig（v2.7.0 暂不实施） ───────────────────────

// resolveKubeconfig 解析 kubeconfig 文件。
//
// v2.7.0 暂不实施，返回 error。
// 留 v2.7.x：完整解析需支持 contexts / users / clusters 多 entry。
//
// 当前生产场景被路径 3 (in-cluster) 覆盖，
// 本地开发场景被路径 1 (显式 URL + kubectl proxy) 覆盖。
func resolveKubeconfig(path string) (*K8sConfig, error) {
	return nil, fmt.Errorf("eventstream(auth): kubeconfig 解析尚未实施 (path=%s)，请使用 APIServerURL 或 in-cluster", path)
}

// ─── 路径 3: in-cluster ServiceAccount ───────────────────────────

// readTokenFile / readCAFile 是可注入的文件读取函数。
//
// 生产时读 /var/run/secrets/... 标准路径。
// 测试时替换为 fake，避免依赖真实 in-cluster 环境。
//
// 注意：用 var 而非 const，便于测试时通过函数变量替换 mock。
var (
	readTokenFile = func() ([]byte, error) {
		return os.ReadFile(serviceAccountTokenPath)
	}
	readCAFile = func() ([]byte, error) {
		return os.ReadFile(serviceAccountCAPath)
	}
)

// resolveInCluster 检测 in-cluster 环境并构造配置。
//
// 必需文件：
//   - serviceAccountTokenPath (token)
//   - serviceAccountCAPath    (CA cert)
//
// 任一缺失或无效都返回 error。
// error 信息绝不包含 token 内容。
func resolveInCluster() (*K8sConfig, error) {
	// ── 读 token ──
	tokenBytes, err := readTokenFile()
	if err != nil {
		return nil, fmt.Errorf("eventstream(auth): 读 in-cluster token 失败: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return nil, fmt.Errorf("eventstream(auth): in-cluster token 为空")
	}

	// ── 读 CA ──
	caBytes, err := readCAFile()
	if err != nil {
		return nil, fmt.Errorf("eventstream(auth): 读 in-cluster CA 失败: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caBytes) {
		// 注意：err 不含 caBytes 内容，只说"解析失败"
		return nil, fmt.Errorf("eventstream(auth): in-cluster CA 证书 PEM 解析失败")
	}

	// ── 构造 TLS config（强制校验 CA，不允许 InsecureSkipVerify）──
	tlsConfig := &tls.Config{
		RootCAs:    caPool,
		MinVersion: tls.VersionTLS12,
		// InsecureSkipVerify: 默认 false，且永不显式设 true
	}

	// ── 构造 HTTP client + Bearer transport ──
	httpClient := defaultHTTPClient(tlsConfig)
	httpClient.Transport = &bearerAuthTransport{
		token: token,
		base:  httpClient.Transport,
	}

	return &K8sConfig{
		apiServerURL: inClusterAPIServer,
		httpClient:   httpClient,
		source:       "in-cluster",
	}, nil
}

// ─── HTTP client 工厂 ────────────────────────────────────────────

// defaultHTTPClient 创建默认 HTTP client。
//
// 配置说明：
//   - Timeout: 0 — watch 是长连接，不能设整体 timeout
//     调用方应通过 ctx 控制单次请求超时（list 用 30s ctx，watch 用 ctx.Done() 控制生命周期）
//   - MaxIdleConns: 10 — 复用连接，K8s API server 高频访问场景够用
//   - IdleConnTimeout: 90s — K8s 长连接保持时间
//   - ResponseHeaderTimeout: httpRequestTimeout — list 阶段头部超时
//
// tlsConfig 可为 nil（http:// 场景不需要 TLS）。
func defaultHTTPClient(tlsConfig *tls.Config) *http.Client {
	transport := &http.Transport{
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		DisableCompression:    false,
		ResponseHeaderTimeout: httpRequestTimeout,
	}
	if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig
	}
	return &http.Client{
		// Timeout: 0  watch 用 ctx 控制
		Transport: transport,
	}
}

// ─── Bearer Auth Transport ───────────────────────────────────────

// bearerAuthTransport 包装底层 RoundTripper，自动给每个请求添加 Bearer auth header。
//
// 安全性：
//   - 通过 req.Clone() 避免修改原 request（K8s 客户端可能复用 request）
//   - token 仅在 transport 内部持有，不暴露给 K8sConfig 字段
//   - RoundTrip 错误不包含 token
type bearerAuthTransport struct {
	token string
	base  http.RoundTripper
}

// RoundTrip 实现 http.RoundTripper。
//
// 不直接修改原 req（防止上游复用 request 时污染）。
// header 写入 cloned request。
func (t *bearerAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	newReq := req.Clone(req.Context())
	newReq.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(newReq)
}
