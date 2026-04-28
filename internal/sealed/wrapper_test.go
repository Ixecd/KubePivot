// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package sealed

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ════════════════════════════════════════════════════════════════════════════
// Scope.IsValid
// ════════════════════════════════════════════════════════════════════════════

func TestScope_IsValid(t *testing.T) {
	tests := []struct {
		scope Scope
		want  bool
	}{
		{ScopeStrict, true},
		{ScopeNamespace, true},
		{ScopeClusterWide, true},
		{Scope(""), false},
		{Scope("invalid"), false},
		{Scope("Strict"), false}, // 大小写敏感
	}

	for _, tt := range tests {
		t.Run(string(tt.scope), func(t *testing.T) {
			assert.Equal(t, tt.want, tt.scope.IsValid())
		})
	}
}

// ════════════════════════════════════════════════════════════════════════════
// SealOptions.Validate
// ════════════════════════════════════════════════════════════════════════════

func TestSealOptions_Validate_MissingSecretName(t *testing.T) {
	opts := SealOptions{
		Namespace:    "kp-prod",
		FromLiterals: map[string]string{"password": "xxx"},
	}
	err := opts.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SecretName 必填")
}

func TestSealOptions_Validate_MissingNamespace(t *testing.T) {
	opts := SealOptions{
		SecretName:   "db-creds",
		FromLiterals: map[string]string{"password": "xxx"},
	}
	err := opts.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Namespace 必填")
}

func TestSealOptions_Validate_NoLiteralsNoFiles(t *testing.T) {
	opts := SealOptions{
		SecretName: "db-creds",
		Namespace:  "kp-prod",
	}
	err := opts.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "至少 1 个")
}

func TestSealOptions_Validate_InvalidScope(t *testing.T) {
	opts := SealOptions{
		SecretName:   "db-creds",
		Namespace:    "kp-prod",
		Scope:        Scope("clusterwide"), // 应该是 cluster-wide
		FromLiterals: map[string]string{"password": "xxx"},
	}
	err := opts.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope")
	assert.Contains(t, err.Error(), "无效")
}

func TestSealOptions_Validate_FromFilePathMissing(t *testing.T) {
	opts := SealOptions{
		SecretName: "db-creds",
		Namespace:  "kp-prod",
		FromFiles:  map[string]string{"cert": "/nonexistent/path"},
	}
	err := opts.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "无法访问")
}

func TestSealOptions_Validate_HappyPath(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "fake-cert.pem")
	require.NoError(t, os.WriteFile(certPath, []byte("fake"), 0o644))

	opts := SealOptions{
		SecretName:   "db-creds",
		Namespace:    "kp-prod",
		Scope:        ScopeNamespace,
		FromLiterals: map[string]string{"password": "xxx"},
		FromFiles:    map[string]string{"cert": certPath},
	}
	assert.NoError(t, opts.Validate())
}

func TestSealOptions_Validate_EmptyScope_OK(t *testing.T) {
	// Scope 空时不校验, 由 SealSecret 应用 DefaultScope
	opts := SealOptions{
		SecretName:   "db-creds",
		Namespace:    "kp-prod",
		Scope:        "",
		FromLiterals: map[string]string{"password": "xxx"},
	}
	assert.NoError(t, opts.Validate())
}

// ════════════════════════════════════════════════════════════════════════════
// DetectKubeseal
// ════════════════════════════════════════════════════════════════════════════

func TestDetectKubeseal(t *testing.T) {
	// 仅验证函数不 panic, 不 assert true/false (CI 环境不一定有 kubeseal)
	got, err := DetectKubeseal()
	require.NoError(t, err)
	t.Logf("DetectKubeseal = %v (CI 环境可能无 kubeseal)", got)
}

// ════════════════════════════════════════════════════════════════════════════
// InstallHint
// ════════════════════════════════════════════════════════════════════════════

func TestInstallHint(t *testing.T) {
	hint := InstallHint()
	assert.Contains(t, hint, "kubeseal")
	assert.Contains(t, hint, "brew install kubeseal")
	assert.Contains(t, hint, "github.com/bitnami-labs/sealed-secrets")
}

// ════════════════════════════════════════════════════════════════════════════
// buildKubectlCreateArgs (Q-G.6=A 单测覆盖参数构造)
// ════════════════════════════════════════════════════════════════════════════

func TestBuildKubectlCreateArgs_BasicLiteral(t *testing.T) {
	opts := SealOptions{
		SecretName:   "db-creds",
		Namespace:    "kp-prod",
		FromLiterals: map[string]string{"password": "xxx"},
	}
	args := buildKubectlCreateArgs(opts)

	// 关键参数全部出现
	assert.Equal(t, []string{"create", "secret", "generic", "db-creds"}, args[:4])
	assert.Contains(t, args, "--namespace")
	assert.Contains(t, args, "kp-prod")
	assert.Contains(t, args, "--dry-run=client")
	assert.Contains(t, args, "-o")
	assert.Contains(t, args, "yaml")
	assert.Contains(t, args, "--from-literal=password=xxx")
}

func TestBuildKubectlCreateArgs_MultipleLiterals(t *testing.T) {
	opts := SealOptions{
		SecretName: "db-creds",
		Namespace:  "kp-prod",
		FromLiterals: map[string]string{
			"password": "xxx",
			"user":     "admin",
		},
	}
	args := buildKubectlCreateArgs(opts)

	assert.Contains(t, args, "--from-literal=password=xxx")
	assert.Contains(t, args, "--from-literal=user=admin")
}

func TestBuildKubectlCreateArgs_FromFile(t *testing.T) {
	opts := SealOptions{
		SecretName: "db-creds",
		Namespace:  "kp-prod",
		FromFiles:  map[string]string{"tls.crt": "/path/to/cert"},
	}
	args := buildKubectlCreateArgs(opts)

	assert.Contains(t, args, "--from-file=tls.crt=/path/to/cert")
}

// ════════════════════════════════════════════════════════════════════════════
// buildKubesealArgs (Q-G.7=A+C 默认/显式 cert)
// ════════════════════════════════════════════════════════════════════════════

func TestBuildKubesealArgs_NoCert_FetchesAtRuntime(t *testing.T) {
	args := buildKubesealArgs(ScopeNamespace, "")
	assert.Equal(t, []string{"--scope", "namespace-wide", "-o", "yaml"}, args)
	// 没 --cert → kubeseal 默认 --fetch-cert (Q-G.7=A)
	assert.NotContains(t, args, "--cert")
}

func TestBuildKubesealArgs_WithCert_Explicit(t *testing.T) {
	args := buildKubesealArgs(ScopeStrict, "/path/to/cert.pem")
	assert.Equal(t, []string{"--scope", "strict", "-o", "yaml",
		"--cert", "/path/to/cert.pem"}, args)
}

func TestBuildKubesealArgs_ClusterWide(t *testing.T) {
	args := buildKubesealArgs(ScopeClusterWide, "")
	assert.Contains(t, args, "cluster-wide")
}

// ════════════════════════════════════════════════════════════════════════════
// SealSecret 集成 (用 mockRunner 验证, 不真跑 kubeseal)
// ════════════════════════════════════════════════════════════════════════════

// mockRunner 实现 CommandRunner 接口, 记录调用 + 返回预设结果.
type mockRunner struct {
	// Run 调用记录
	runCmds  []string
	runArgs  [][]string
	runRet   []byte
	runErr   error

	// RunWithStdin 调用记录
	stdinCmds  []string
	stdinArgs  [][]string
	stdinIn    []byte
	stdinRet   []byte
	stdinErr   error
}

func (m *mockRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	m.runCmds = append(m.runCmds, name)
	m.runArgs = append(m.runArgs, args)
	return m.runRet, m.runErr
}

func (m *mockRunner) RunWithStdin(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	m.stdinCmds = append(m.stdinCmds, name)
	m.stdinArgs = append(m.stdinArgs, args)
	m.stdinIn = stdin
	return m.stdinRet, m.stdinErr
}

func TestSealSecret_HappyPath(t *testing.T) {
	mock := &mockRunner{
		runRet:   []byte("apiVersion: v1\nkind: Secret\n..."),
		stdinRet: []byte("apiVersion: bitnami.com/v1alpha1\nkind: SealedSecret\n..."),
	}

	opts := SealOptions{
		SecretName:   "db-creds",
		Namespace:    "kp-prod",
		Scope:        ScopeNamespace,
		FromLiterals: map[string]string{"password": "mypass"},
	}

	out, err := SealSecret(context.Background(), opts, mock)
	require.NoError(t, err)
	assert.Contains(t, string(out), "SealedSecret")

	// 验证 kubectl 被调用
	require.Len(t, mock.runCmds, 1)
	assert.Equal(t, "kubectl", mock.runCmds[0])

	// 验证 kubeseal 被调用 (with stdin)
	require.Len(t, mock.stdinCmds, 1)
	assert.Equal(t, "kubeseal", mock.stdinCmds[0])

	// 验证 kubeseal stdin 是 kubectl 输出
	assert.Equal(t, "apiVersion: v1\nkind: Secret\n...", string(mock.stdinIn))
}

func TestSealSecret_DefaultScope(t *testing.T) {
	mock := &mockRunner{
		runRet:   []byte("secret-yaml"),
		stdinRet: []byte("sealed-yaml"),
	}

	// 不指定 Scope, 应该用 DefaultScope (namespace-wide)
	opts := SealOptions{
		SecretName:   "db-creds",
		Namespace:    "kp-prod",
		FromLiterals: map[string]string{"k": "v"},
	}

	_, err := SealSecret(context.Background(), opts, mock)
	require.NoError(t, err)

	// 验证 kubeseal 用了默认 scope
	require.Len(t, mock.stdinArgs, 1)
	args := mock.stdinArgs[0]
	scopeIdx := -1
	for i, a := range args {
		if a == "--scope" {
			scopeIdx = i
			break
		}
	}
	require.GreaterOrEqual(t, scopeIdx, 0, "应有 --scope 参数")
	assert.Equal(t, "namespace-wide", args[scopeIdx+1],
		"未指定 Scope 应该用 DefaultScope (namespace-wide)")
}

func TestSealSecret_KubectlFails(t *testing.T) {
	mock := &mockRunner{
		runErr: errors.New("kubectl: connection refused"),
	}

	opts := SealOptions{
		SecretName:   "db-creds",
		Namespace:    "kp-prod",
		FromLiterals: map[string]string{"k": "v"},
	}

	_, err := SealSecret(context.Background(), opts, mock)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kubectl create secret")
	assert.Contains(t, err.Error(), "connection refused")

	// kubeseal 没被调用
	assert.Empty(t, mock.stdinCmds)
}

func TestSealSecret_KubesealFails(t *testing.T) {
	mock := &mockRunner{
		runRet:   []byte("secret-yaml"),
		stdinErr: errors.New("kubeseal: no controller found"),
	}

	opts := SealOptions{
		SecretName:   "db-creds",
		Namespace:    "kp-prod",
		FromLiterals: map[string]string{"k": "v"},
	}

	_, err := SealSecret(context.Background(), opts, mock)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kubeseal 加密失败")
}

func TestSealSecret_ValidationFails(t *testing.T) {
	mock := &mockRunner{}

	opts := SealOptions{
		// 缺 SecretName
		Namespace:    "kp-prod",
		FromLiterals: map[string]string{"k": "v"},
	}

	_, err := SealSecret(context.Background(), opts, mock)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SecretName 必填")

	// 校验失败时, 任何 runner 都不被调用
	assert.Empty(t, mock.runCmds)
	assert.Empty(t, mock.stdinCmds)
}

func TestSealSecret_WithExplicitCert(t *testing.T) {
	mock := &mockRunner{
		runRet:   []byte("secret-yaml"),
		stdinRet: []byte("sealed-yaml"),
	}

	opts := SealOptions{
		SecretName:   "db-creds",
		Namespace:    "kp-prod",
		FromLiterals: map[string]string{"k": "v"},
		CertPath:     "/path/to/cert.pem",
	}

	_, err := SealSecret(context.Background(), opts, mock)
	require.NoError(t, err)

	// 验证 kubeseal 收到 --cert 参数
	require.Len(t, mock.stdinArgs, 1)
	args := strings.Join(mock.stdinArgs[0], " ")
	assert.Contains(t, args, "--cert /path/to/cert.pem")
}
