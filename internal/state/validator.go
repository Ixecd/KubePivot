package state

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
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
// 条件：所有 pod Ready + healthz 返回 200
func ValidateDeployment(kubeconfig, kubeContext, namespace, deployment string, timeout time.Duration) error {
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
			// 所有 pod Ready，进一步检查 healthz
			ip, err := getPodIP(kubeconfig, kubeContext, namespace, deployment)
			if err != nil {
				slog.Debug("获取 pod IP 失败，稍后重试", "err", err)
				time.Sleep(tick)
				continue
			}
			if err := checkHealthz(ip); err != nil {
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

// getPodIP 获取 deployment 下第一个 Running pod 的 IP
func getPodIP(kubeconfig, kubeContext, namespace, deployment string) (string, error) {
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
			Status struct {
				PodIP string `json:"podIP"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return "", fmt.Errorf("解析 pod 列表失败: %w", err)
	}
	if len(result.Items) == 0 {
		return "", fmt.Errorf("没有 Running 的 pod")
	}
	return result.Items[0].Status.PodIP, nil
}

// checkHealthz 通过 pod IP 直接检查 healthz
func checkHealthz(podIP string) error {
	url := fmt.Sprintf("http://%s:8080/healthz", podIP)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("healthz 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz 返回非 200: %d", resp.StatusCode)
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
