// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.

// Package sealed 是 KubePivot v2.8 G 模块的 sealed-secrets 集成层.
//
// 设计核心 (跟 v2.5/v2.6/v2.8 工程纪律一致):
//   - 不引入 sealed-secrets Go SDK (体量过大, 维护成本高)
//   - 复用既有 kubectl/helm wrapper 模式 (exec.Command 调 kubeseal CLI)
//   - 跟 ROADMAP §2.4 H 哲学一致: "cosign-cli wrapper, 不引入 cosign Go SDK"
//
// 工程意义:
//
//	K8s Secret 默认只是 base64 编码 (不是加密!), 任何 git 仓库不能放这种 YAML.
//	sealed-secrets (Bitnami 出品) 用集群公钥加密 Secret → 输出 SealedSecret YAML.
//	SealedSecret 即使泄露也无法解密 (私钥永远不出集群).
//	apply 后 sealed-secrets-controller 自动解密成普通 Secret.
//
//	跟 GitOps 完全一致: kp secret seal → SealedSecret YAML → git commit → kp deploy.
//
// 6 个 Q 拍板:
//
//	Q-G.1=A   G-Level1 only sealed-secrets (~270 行)
//	Q-G.2=A   doctor 检测 + 提示安装 kubeseal
//	Q-G.3=A   --from-literal + --from-file 双模式 (跟 kubectl 一致)
//	Q-G.4=C   默认 stdout + --output 覆盖
//	Q-G.5=B   默认 scope=namespace-wide
//	Q-G.6=A   单元测试 + skip 真集成
//	Q-G.7=A+C 默认实时 fetch-cert + --cert 显式覆盖
package sealed

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Scope sealed-secrets 的解密 scope.
//
// 跟 sealed-secrets 原生 scope 一一对应:
//   - ScopeStrict        只能特定 ns + 特定 name 解密 (最安全)
//   - ScopeNamespace     特定 ns 内任何 name 都能解密 (默认, 跟 K8s 习惯一致)
//   - ScopeClusterWide   整个集群任何 ns 都能解密 (最宽松)
type Scope string

const (
	ScopeStrict      Scope = "strict"
	ScopeNamespace   Scope = "namespace-wide"
	ScopeClusterWide Scope = "cluster-wide"

	// DefaultScope 默认 scope (Q-G.5=B namespace-wide).
	DefaultScope = ScopeNamespace

	// kubesealBinary kubeseal CLI 二进制名 (PATH 中查找).
	kubesealBinary = "kubeseal"

	// kubectlBinary kubectl CLI 二进制名 (PATH 中查找).
	kubectlBinary = "kubectl"
)

// IsValid 判断 scope 是否合法.
func (s Scope) IsValid() bool {
	return s == ScopeStrict || s == ScopeNamespace || s == ScopeClusterWide
}

// SealOptions kubeseal 加密参数集.
//
// SecretName / Namespace 必填, FromLiterals 和 FromFiles 至少 1 个非空.
// CertPath 可选 (空时 kubeseal 实时 --fetch-cert 从集群获取).
type SealOptions struct {
	SecretName   string            // K8s Secret name (必填)
	Namespace    string            // K8s namespace (必填)
	Scope        Scope             // 解密 scope, 空时默认 DefaultScope
	FromLiterals map[string]string // --from-literal=KEY=VALUE 列表
	FromFiles    map[string]string // --from-file=KEY=PATH 列表
	CertPath     string            // 公钥文件路径 (空时 kubeseal --fetch-cert 实时获取)
}

// Validate 验证参数完整性.
func (o *SealOptions) Validate() error {
	if o.SecretName == "" {
		return errors.New("SealOptions: SecretName 必填")
	}
	if o.Namespace == "" {
		return errors.New("SealOptions: Namespace 必填")
	}
	if len(o.FromLiterals) == 0 && len(o.FromFiles) == 0 {
		return errors.New("SealOptions: 必须至少 1 个 --from-literal 或 --from-file")
	}
	if o.Scope != "" && !o.Scope.IsValid() {
		return fmt.Errorf("SealOptions: scope %q 无效, 必须是 strict/namespace-wide/cluster-wide",
			o.Scope)
	}
	for k, v := range o.FromFiles {
		if v == "" {
			return fmt.Errorf("SealOptions: --from-file=%s 路径为空", k)
		}
		if _, err := os.Stat(v); err != nil {
			return fmt.Errorf("SealOptions: --from-file=%s 路径无法访问: %w", k, err)
		}
	}
	return nil
}

// DetectKubeseal 检测 kubeseal 是否在 PATH 中.
//
// 不存在时返回 (false, nil), 调用方按需提示安装.
// PATH 查找失败也是 (false, nil), 不算错误.
//
// 跟 KubePivot 既有 doctor 检测模式一致 (kubectl/helm/docker).
func DetectKubeseal() (bool, error) {
	_, err := exec.LookPath(kubesealBinary)
	if err != nil {
		return false, nil
	}
	return true, nil
}

// InstallHint 返回 kubeseal 安装提示信息.
//
// 跟 ROADMAP §3 G 节"由 kp doctor 引导用户安装"一致.
func InstallHint() string {
	return strings.Join([]string{
		"kubeseal CLI 未安装. 安装方法:",
		"  macOS:  brew install kubeseal",
		"  Linux:  https://github.com/bitnami-labs/sealed-secrets/releases",
		"  详细:    https://github.com/bitnami-labs/sealed-secrets#installation",
	}, "\n")
}

// CommandRunner 抽象 exec.Command 行为, 用于测试 mock (Q-G.6=A).
//
// 生产用 RealCommandRunner (调真实 kubeseal/kubectl).
// 测试用 MockCommandRunner (验证命令参数构造正确, 不真跑).
type CommandRunner interface {
	// Run 不带 stdin 执行命令.
	Run(ctx context.Context, name string, args ...string) ([]byte, error)

	// RunWithStdin 带 stdin 执行命令 (kubeseal 接收 Secret YAML 必需).
	RunWithStdin(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)
}

// RealCommandRunner 调真实 exec.Command.
type RealCommandRunner struct{}

// Run 执行命令, 返回 stdout (stderr 通过 error 携带).
func (r *RealCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("%s: %w (stderr: %s)",
			name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// RunWithStdin 带 stdin pipe 执行命令.
func (r *RealCommandRunner) RunWithStdin(ctx context.Context, stdin []byte, name string,
	args ...string) ([]byte, error) {

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("%s: %w (stderr: %s)",
			name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// NewRunner 默认 runner 工厂 (生产代码用).
func NewRunner() CommandRunner {
	return &RealCommandRunner{}
}

// SealSecret 用 kubeseal 加密 Secret 数据, 返回 SealedSecret YAML.
//
// 流程:
//  1. 校验 opts (SecretName / Namespace / scope 等)
//  2. 用 kubectl create secret generic --dry-run=client -o yaml 生成 Secret YAML
//  3. pipe 给 kubeseal --scope <scope> -o yaml 输出 SealedSecret YAML
//
// 实现策略 (Q-G.7=A+C):
//   - opts.CertPath 为空 → kubeseal 默认 --fetch-cert 实时从集群获取公钥
//   - opts.CertPath 非空 → kubeseal --cert <path> 用显式公钥
//
// 这是 sealed-secrets 官方推荐用法:
//
//	kubectl create secret generic db-creds --from-literal=password=xxx \
//	  --namespace kp-prod --dry-run=client -o yaml \
//	| kubeseal --scope namespace-wide -o yaml
func SealSecret(ctx context.Context, opts SealOptions, runner CommandRunner) ([]byte, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	scope := opts.Scope
	if scope == "" {
		scope = DefaultScope
	}

	// Step 1: kubectl create secret generic --dry-run=client -o yaml
	kubectlArgs := buildKubectlCreateArgs(opts)
	secretYAML, err := runner.Run(ctx, kubectlBinary, kubectlArgs...)
	if err != nil {
		return nil, fmt.Errorf("kubectl create secret 生成 YAML 失败: %w", err)
	}

	// Step 2: pipe 给 kubeseal
	kubesealArgs := buildKubesealArgs(scope, opts.CertPath)
	sealedYAML, err := runner.RunWithStdin(ctx, secretYAML, kubesealBinary, kubesealArgs...)
	if err != nil {
		return nil, fmt.Errorf("kubeseal 加密失败: %w", err)
	}

	return sealedYAML, nil
}

// buildKubectlCreateArgs 构造 kubectl create secret generic 参数.
//
// 提取为独立函数便于单测 (Q-G.6=A).
func buildKubectlCreateArgs(opts SealOptions) []string {
	args := []string{
		"create", "secret", "generic", opts.SecretName,
		"--namespace", opts.Namespace,
		"--dry-run=client",
		"-o", "yaml",
	}
	// 注: map iteration 顺序不固定, 但对 kubectl 行为无影响
	// (Secret data 字段最终都被 kubectl 转成 sorted map)
	for k, v := range opts.FromLiterals {
		args = append(args, "--from-literal="+k+"="+v)
	}
	for k, v := range opts.FromFiles {
		args = append(args, "--from-file="+k+"="+v)
	}
	return args
}

// buildKubesealArgs 构造 kubeseal 参数.
//
// 提取为独立函数便于单测 (Q-G.6=A).
func buildKubesealArgs(scope Scope, certPath string) []string {
	args := []string{"--scope", string(scope), "-o", "yaml"}
	if certPath != "" {
		args = append(args, "--cert", certPath)
	}
	// 没 --cert 时 kubeseal 默认 --fetch-cert (Q-G.7=A 实时获取)
	return args
}
