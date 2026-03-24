package main

import (
	"bytes"
	"fmt"
	"os/exec"
)

// runOutput 执行命令并返回 stdout
func runOutput(cmdArgs ...string) ([]byte, error) {
	if len(cmdArgs) == 0 {
		return nil, fmt.Errorf("命令不能为空")
	}
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("命令执行失败 %v: %w\nstderr: %s",
			cmdArgs, err, stderr.String())
	}
	return stdout.Bytes(), nil
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
func helmRollback(kubeconfig, kubeContext, namespace, release string, revision int) error {
	args := []string{"helm", "rollback", release}
	if revision > 0 {
		args = append(args, fmt.Sprintf("%d", revision))
	}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	if kubeContext != "" {
		args = append(args, "--kube-context", kubeContext)
	}
	args = append(args, "--namespace", namespace, "--wait")
	_, err := runOutput(args...)
	return err
}

// buildKubectlArgs 构建 kubectl 基础参数
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
// 比 namespaceExists 更准确：helm --create-namespace 会自动建 namespace，
// 导致首次部署失败后 namespaceExists 返回 true，误判为更新。
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
