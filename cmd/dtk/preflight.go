package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Ixecd/dev-toolkit/internal/state"
)

type dep struct {
	bin        string
	installURL string
}

// deployDeps 是 dtk deploy 依赖的外部工具。
// 按实际调用顺序排列，方便用户一次性看完缺什么。
var deployDeps = []dep{
	{"docker", "https://docs.docker.com/engine/install/"},
	{"kubectl", "https://kubernetes.io/docs/tasks/tools/"},
	{"helm", "https://helm.sh/docs/intro/install/"},
}

// checkDeps 检查所有依赖工具是否可用。
// 全部缺失时一次性列出，避免用户装一个再发现下一个也缺。
func checkDeps(deps []dep) error {
	var missing []dep
	for _, d := range deps {
		if _, err := exec.LookPath(d.bin); err != nil {
			missing = append(missing, d)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	fmt.Fprintln(os.Stderr, "❌ 以下工具未安装或不在 PATH 中：")
	fmt.Fprintln(os.Stderr)
	for _, d := range missing {
		fmt.Fprintf(os.Stderr, "  %-10s  %s\n", d.bin, d.installURL)
	}
	fmt.Fprintln(os.Stderr)
	return fmt.Errorf("缺少必要工具，请安装后重试")
}

// checkPendingRollback 检查 helm release 是否卡在 pending-rollback
// 如果是，询问用户是否自动清理
func checkPendingRollback(cfg *deployConfig, env map[string]string, sm *state.Machine) error {
	releaseName := envOrDefault(env, "PROJECT_NAME", "")
	if releaseName == "" {
		return nil
	}

	// 查 helm release 状态
	args := []string{"helm", "status", releaseName,
		"--namespace", cfg.namespace,
		"--output", "json",
	}
	if cfg.kubeconfig != "" {
		args = append(args, "--kubeconfig", cfg.kubeconfig)
	}
	if cfg.context != "" {
		args = append(args, "--kube-context", cfg.context)
	}

	out, err := runOutput(args...)
	if err != nil {
		// release 不存在，正常情况，跳过
		return nil
	}

	var result struct {
		Info struct {
			Status string `json:"status"`
		} `json:"info"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil
	}

	if result.Info.Status != "pending-rollback" {
		return nil
	}

	// 发现 pending-rollback，询问用户
	fmt.Println("⚠️  检测到 helm release 卡在 pending-rollback 状态")
	fmt.Println("   这通常是 controller 和 dtk deploy 并发操作导致的。")
	fmt.Println()
	fmt.Println("   自动清理将执行：")
	fmt.Println("   1. 删除 pending-rollback secret")
	fmt.Println("   2. 重置状态机为 RUNNING")
	fmt.Println()
	fmt.Print("是否自动清理并继续部署？(y/N): ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	if input != "y" {
		return fmt.Errorf("已取消。可参考文档手动处理：docs/guide/zh-CN/gotchas.md")
	}

	// 删除 pending-rollback secret
	if err := deletePendingRollbackSecret(cfg, releaseName); err != nil {
		return fmt.Errorf("清理 pending-rollback secret 失败: %w", err)
	}

	// 重置状态机为 RUNNING
	if err := sm.ForceState(state.StateRunning, "自动清理 pending-rollback"); err != nil {
		return fmt.Errorf("重置状态机失败: %w", err)
	}

	fmt.Println("✓ 清理完成，继续部署...")
	fmt.Println()
	return nil
}

// deletePendingRollbackSecret 删除 helm pending-rollback 状态的 secret
func deletePendingRollbackSecret(cfg *deployConfig, releaseName string) error {
	// 找到 pending-rollback 的 secret 名字
	args := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	args = append(args,
		"get", "secret",
		"-l", fmt.Sprintf("owner=helm,name=%s", releaseName),
		"-o", `jsonpath={.items[?(@.metadata.labels.status=="pending-rollback")].metadata.name}`,
	)

	out, err := runOutput(args...)
	if err != nil {
		return fmt.Errorf("查询 secret 失败: %w", err)
	}

	secretName := strings.TrimSpace(string(out))
	if secretName == "" {
		// 没找到 secret，可能已经被清理了
		return nil
	}

	// 删除
	delArgs := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	delArgs = append(delArgs, "delete", "secret", secretName)
	if _, err := runOutput(delArgs...); err != nil {
		return fmt.Errorf("删除 secret 失败: %w", err)
	}

	fmt.Printf("✓ 已删除 pending-rollback secret: %s\n", secretName)
	return nil
}
