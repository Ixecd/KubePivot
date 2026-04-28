package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ixecd/kubepivot/internal/audit"
	"github.com/Ixecd/kubepivot/internal/rbac"
)

func runDown(args []string) {
	flags := flag.NewFlagSet("down", flag.ExitOnError)
	namespace := flags.String("namespace", "", "kubernetes namespace")
	context := flags.String("context", "", "kubernetes context")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 文件路径")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "解析参数失败:", err)
		os.Exit(1)
	}
	*kubeconfig = expandHome(*kubeconfig)

	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	cfg := &deployConfig{
		namespace:  *namespace,
		context:    *context,
		kubeconfig: *kubeconfig,
	}
	resolveDeployConfig(cfg, env, root)

	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))

	// v2.8 B.7.2 + B.3 + B.7.3: down 销毁性操作 (跟 rollback 同权限 PermRollback)
	mustCheck(audit.ResolveActor(), cfg.namespace, rbac.PermRollback)

	stateFile := expandHome(fmt.Sprintf("~/.kp/state/%s/%s.json", projectName, cfg.namespace))

	// 二次确认
	fmt.Printf("⚠️  即将删除以下资源：\n")
	fmt.Printf("  namespace          : %s\n", cfg.namespace)
	fmt.Printf("  ClusterRole        : %s-controller\n", projectName)
	fmt.Printf("  ClusterRoleBinding : %s-controller\n", projectName)
	fmt.Printf("  本地状态文件       : %s\n", stateFile)
	fmt.Printf("\n确认删除？(y/N): ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	if input != "y" {
		fmt.Println("已取消")
		return
	}

	hasError := false

	// 1. 删除 ClusterRole
	if err := runKubectl(cfg, "delete", "clusterrole", projectName+"-controller", "--ignore-not-found"); err != nil {
		fmt.Fprintf(os.Stderr, "删除 ClusterRole 失败: %v\n", err)
		hasError = true
	} else {
		fmt.Printf("✓ ClusterRole %s-controller 已删除\n", projectName)
	}

	// 2. 删除 ClusterRoleBinding
	if err := runKubectl(cfg, "delete", "clusterrolebinding", projectName+"-controller", "--ignore-not-found"); err != nil {
		fmt.Fprintf(os.Stderr, "删除 ClusterRoleBinding 失败: %v\n", err)
		hasError = true
	} else {
		fmt.Printf("✓ ClusterRoleBinding %s-controller 已删除\n", projectName)
	}

	// 3. 删除 namespace
	if err := runKubectl(cfg, "delete", "namespace", cfg.namespace, "--ignore-not-found"); err != nil {
		fmt.Fprintf(os.Stderr, "删除 namespace 失败: %v\n", err)
		hasError = true
	} else {
		fmt.Printf("✓ namespace %s 已删除\n", cfg.namespace)
	}

	// 4. 删除本地状态文件
	if err := os.Remove(stateFile); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "删除状态文件失败: %v\n", err)
		hasError = true
	} else {
		fmt.Printf("✓ 本地状态文件已删除\n")
	}

	if hasError {
		fmt.Fprintln(os.Stderr, "\n⚠️  部分资源删除失败，请手动检查")
		os.Exit(1)
	}
	fmt.Printf("\n✅ %s 已完全下线\n", projectName)
}

// runKubectl 执行 kubectl 命令，复用 cfg 里的 kubeconfig/context
func runKubectl(cfg *deployConfig, args ...string) error {
	base := kubectlBaseArgs(cfg.kubeconfig, cfg.context, "")
	base = append(base, args...)
	_, err := runOutput(base...)
	return err
}
