package executor

import (
	"context"
	"log/slog"
	"os/exec"
	"sync"
)

// KpExecutor 统一执行器
type KpExecutor struct {
	kubectlPath string
	sem         chan struct{} // 并发控制信号量
}

var (
	instance *KpExecutor
	once     sync.Once
)

// GetExecutor 获取单例，限制最大并发为 5，适合 scratch 环境
func GetExecutor() *KpExecutor {
	once.Do(func() {
		instance = &KpExecutor{
			kubectlPath: "/usr/local/bin/kubectl",
			sem:         make(chan struct{}, 5),
		}
	})
	return instance
}

// Kubectl 执行一条 kubectl 命令并返回结果
func (e *KpExecutor) Kubectl(ctx context.Context, args ...string) ([]byte, error) {
	e.sem <- struct{}{}        // 获取令牌
	defer func() { <-e.sem }() // 释放令牌

	slog.Debug("Executing kubectl", "args", args)

	cmd := exec.CommandContext(ctx, e.kubectlPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, err
	}
	return out, nil
}
