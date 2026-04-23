package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
)

// runOutput 兼容旧签名：第一个参数是 "kubectl" 或 "helm"，剥离后走 executor
func runOutput(cmdArgs ...string) ([]byte, error) {
	if len(cmdArgs) == 0 {
		return nil, fmt.Errorf("命令不能为空")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	bin := cmdArgs[0]
	rest := cmdArgs[1:]

	// 剥离 --kubeconfig（由 executor 处理）
	var kubeconfig string
	var cleaned []string
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--kubeconfig" && i+1 < len(rest) {
			kubeconfig = rest[i+1]
			i++
			continue
		}
		cleaned = append(cleaned, rest[i])
	}

	exec := executor.GetExecutor()
	switch bin {
	case "kubectl":
		return exec.Kubectl(ctx, kubeconfig, cleaned...)
	case "helm":
		return exec.Helm(ctx, kubeconfig, cleaned...)
	default:
		// 其他命令走 Generic（白名单外 fallback 到 bin 本身）
		return exec.Generic(ctx, bin, kubeconfig, cleaned...)
	}
}

// namespaceExists 检查 K8s namespace 是否已存在
func namespaceExists(kubeconfig, kubeContext, namespace string) bool {
	args := buildKubectlArgs(kubeconfig, kubeContext, "")
	args = append(args, "get", "namespace", namespace)
	_, err := runOutput(args...)
	return err == nil
}

// deleteNamespace 删除 K8s namespace
func deleteNamespace(kubeconfig, kubeContext, namespace string) error {
	args := buildKubectlArgs(kubeconfig, kubeContext, "")
	args = append(args, "delete", "namespace", namespace, "--ignore-not-found")
	_, err := runOutput(args...)
	return err
}

// helmRollback 执行 helm rollback，revision=0 表示回滚到上一个版本
func helmRollback(kubeconfig, kubeContext, namespace, release string) error {
	histArgs := []string{"helm", "history", release, "--namespace", namespace, "--output", "json"}
	if kubeconfig != "" {
		histArgs = append(histArgs, "--kubeconfig", kubeconfig)
	}
	if kubeContext != "" {
		histArgs = append(histArgs, "--kube-context", kubeContext)
	}
	out, err := runOutput(histArgs...)
	if err != nil {
		return fmt.Errorf("查询 helm history 失败: %w", err)
	}

	var history []struct {
		Revision int `json:"revision"`
	}
	if err := json.Unmarshal(out, &history); err != nil || len(history) == 0 {
		return fmt.Errorf("解析 helm history 失败")
	}

	latest := history[len(history)-1].Revision
	if latest <= 1 {
		return fmt.Errorf("当前是第一个版本（revision=%d），无法回滚", latest)
	}
	target := latest - 1

	args := []string{"helm", "rollback", release, fmt.Sprintf("%d", target)}
	if namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	if kubeContext != "" {
		args = append(args, "--kube-context", kubeContext)
	}
	args = append(args, "--wait")
	_, err = runOutput(args...)
	return err
}

// buildKubectlArgs 构建 kubectl 基础参数
// 注意：返回数组不包含 "kubectl" 前缀，由 runOutput 根据第一个元素判断走哪个 executor
func buildKubectlArgs(kubeconfig, kubeContext, namespace string) []string {
	var args []string
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	if kubeContext != "" {
		args = append(args, "--context", kubeContext)
	}
	if namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	return append([]string{"kubectl"}, args...)
}

// helmReleaseExists 检查 helm release 是否已有历史版本
func helmReleaseExists(kubeconfig, kubeContext, namespace, release string) bool {
	args := []string{"helm", "history", release, "--namespace", namespace, "--max", "1"}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	if kubeContext != "" {
		args = append(args, "--kube-context", kubeContext)
	}
	_, err := runOutput(args...)
	return err == nil
}
