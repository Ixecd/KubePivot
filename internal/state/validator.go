package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"time"
)

func runOutput(cmdArgs ...string) ([]byte, error) {
	if len(cmdArgs) == 0 {
		return nil, fmt.Errorf("命令不能为空")
	}
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("命令执行失败: %w\nstderr: %s", err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// ValidateDeployment 验证部署是否成功
// 条件：所有 pod Ready + kubectl exec healthz 返回 200
func ValidateDeployment(kubeconfig, kubeContext, namespace, deployment string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	tick := 5 * time.Second

	slog.Info("开始验证部署", "deployment", deployment, "namespace", namespace, "timeout", timeout)

	for time.Now().Before(deadline) {
		ready, total, err := getPodReadiness(kubeconfig, kubeContext, namespace, deployment)
		if err != nil {
			slog.Debug("获取 pod 状态失败，稍后重试", "err", err)
			time.Sleep(tick)
			continue
		}

		slog.Debug("Pod 就绪状态", "ready", ready, "total", total)
		if ready > 0 && ready == total {
			podName, err := getPodName(kubeconfig, kubeContext, namespace, deployment)
			if err != nil {
				slog.Debug("获取 pod 名称失败，稍后重试", "err", err)
				time.Sleep(tick)
				continue
			}
			if err := checkHealthz(kubeconfig, kubeContext, namespace, podName, port); err != nil {
				slog.Debug("healthz 检查失败，稍后重试", "err", err)
				time.Sleep(tick)
				continue
			}
			slog.Info("部署验证通过", "deployment", deployment, "ready", ready, "total", total)
			return nil
		}

		slog.Info("等待 pod 就绪", "ready", ready, "total", total,
			"remaining", time.Until(deadline).Round(time.Second))
		time.Sleep(tick)
	}

	return fmt.Errorf("验证超时：deployment %s 在 %s 内未就绪", deployment, timeout)
}

// getPodReadiness 获取 deployment 的 pod 就绪数量
func getPodReadiness(kubeconfig, kubeContext, namespace, deployment string) (ready, total int, err error) {
	args := kubectlBaseArgs(kubeconfig, kubeContext, namespace)
	args = append(args, "get", "deployment", deployment,
		"-o", `jsonpath={.status.readyReplicas}/{.status.replicas}`)

	out, err := runOutput(args...)
	if err != nil {
		return 0, 0, err
	}

	var r, t int
	fmt.Sscanf(string(out), "%d/%d", &r, &t)
	return r, t, nil
}

// getPodName 获取 deployment 下第一个 Running pod 的名称
func getPodName(kubeconfig, kubeContext, namespace, deployment string) (string, error) {
	args := kubectlBaseArgs(kubeconfig, kubeContext, namespace)
	args = append(args, "get", "pods",
		"-l", "app="+deployment,
		"--field-selector=status.phase=Running",
		"-o", "json")

	out, err := runOutput(args...)
	if err != nil {
		return "", err
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return "", fmt.Errorf("解析 pod 列表失败: %w", err)
	}
	if len(result.Items) == 0 {
		return "", fmt.Errorf("没有 Running 的 pod")
	}
	return result.Items[0].Metadata.Name, nil
}

// checkHealthz 通过 kubectl exec 在 pod 内部检查 healthz
func checkHealthz(kubeconfig, kubeContext, namespace, podName string, port int) error {
	args := kubectlBaseArgs(kubeconfig, kubeContext, namespace)
	args = append(args, "exec", podName, "--",
		"wget", "-qO-", fmt.Sprintf("http://localhost:%d/healthz", port))
	_, err := runOutput(args...)
	if err != nil {
		return fmt.Errorf("healthz 检查失败: %w", err)
	}
	return nil
}

// kubectlBaseArgs 构建 kubectl 基础参数
func kubectlBaseArgs(kubeconfig, kubeContext, namespace string) []string {
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
