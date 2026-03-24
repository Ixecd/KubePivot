package main

import (
	"bytes"
	"fmt"
	"os/exec"
)

// runOutput 执行命令并返回 stdout，第一个元素是命令名，其余是参数
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
	args := []string{"kubectl"}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	if kubeContext != "" {
		args = append(args, "--context", kubeContext)
	}
	args = append(args, "get", "namespace", namespace)
	_, err := runOutput(args...)
	return err == nil
}

// deleteNamespace 删除 K8s namespace
func deleteNamespace(kubeconfig, kubeContext, namespace string) error {
	args := []string{"kubectl"}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	if kubeContext != "" {
		args = append(args, "--context", kubeContext)
	}
	args = append(args, "delete", "namespace", namespace, "--ignore-not-found")
	_, err := runOutput(args...)
	return err
}

// helmRollback 执行 helm rollback
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
