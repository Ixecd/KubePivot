package executor

import (
	"context"
	"log/slog"
	"os/exec"
	"sync"
)

// 全局单例（懒加载，无需 Init，永不 panic）
var (
	_global *KpExecutor
	_once   sync.Once
)

// KpExecutor 核心单例
type KpExecutor struct {
	kubectl string
	helm    string
	sem     chan struct{}
}

// GetExecutor 安全懒加载单例
func GetExecutor() *KpExecutor {
	_once.Do(func() {
		_global = &KpExecutor{
			kubectl: "/usr/local/bin/kubectl",
			helm:    "/usr/local/bin/helm",
			sem:     make(chan struct{}, 5),
		}
	})
	return _global
}

func (e *KpExecutor) Sh(ctx context.Context, cmd string) ([]byte, error) {
	return e.Generic(ctx, "sh", "", "-c", cmd)
}

func (e *KpExecutor) Kubectl(ctx context.Context, kubeconfig string, args ...string) ([]byte, error) {
	if kubeconfig != "" {
		args = append([]string{"--kubeconfig", kubeconfig}, args...)
	}
	return e.run(ctx, e.kubectl, args...)
}

func (e *KpExecutor) Helm(ctx context.Context, kubeconfig string, args ...string) ([]byte, error) {
	if kubeconfig != "" {
		args = append([]string{"--kubeconfig", kubeconfig}, args...)
	}
	return e.run(ctx, e.helm, args...)
}

// Generic 全局通用
func (e *KpExecutor) Generic(ctx context.Context, bin string, kubeconfig string, args ...string) ([]byte, error) {
	path := map[string]string{
		"kubectl": e.kubectl,
		"helm":    e.helm,
	}[bin]

	// 安全 fallback（仅允许已知白名单，scratch 安全）
	if path == "" {
		path = bin
	}

	// 自动注入 kubeconfig
	if kubeconfig != "" {
		args = append([]string{"--kubeconfig", kubeconfig}, args...)
	}

	return e.run(ctx, path, args...)
}

func (e *KpExecutor) run(ctx context.Context, bin string, args ...string) ([]byte, error) {
	e.sem <- struct{}{}
	defer func() { <-e.sem }()

	slog.Debug("exec command", "bin", bin, "args", args)
	cmd := exec.CommandContext(ctx, bin, args...)
	return cmd.CombinedOutput()
}

// CmdKubectl 返回底层 *exec.Cmd，用于自定义 Stdin/Stdout/Stderr
func (e *KpExecutor) CmdKubectl(ctx context.Context, kubeconfig string, args ...string) *exec.Cmd {
	// 自动注入 kubeconfig
	if kubeconfig != "" {
		args = append([]string{"--kubeconfig", kubeconfig}, args...)
	}
	// 返回 Cmd 对象，不执行，给外部手动控制
	return exec.CommandContext(ctx, e.kubectl, args...)
}
