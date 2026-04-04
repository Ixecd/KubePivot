package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Ixecd/kubepivot/internal/planner"
)

// kpPoliciesDir 策略文件目录
func kpPoliciesDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kp", "policies")
}

func runPolicy(args []string) {
	if len(args) == 0 {
		fmt.Println("用法: kp policy <子命令>")
		fmt.Println("  add     添加策略文件")
		fmt.Println("  list    列出所有策略")
		fmt.Println("  remove  删除策略")
		fmt.Println("  check   手动运行策略检查（不部署）")
		os.Exit(1)
	}
	switch args[0] {
	case "add":
		runPolicyAdd(args[1:])
	case "list", "ls":
		runPolicyList()
	case "remove", "rm":
		runPolicyRemove(args[1:])
	case "check":
		runPolicyCheck(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n", args[0])
		os.Exit(1)
	}
}

func runPolicyAdd(args []string) {
	flags := flag.NewFlagSet("policy add", flag.ExitOnError)
	name := flags.String("name", "", "策略名称（必填）")
	file := flags.String("file", "", "Rego 策略文件路径（必填）")
	flags.Parse(args)

	if *name == "" || *file == "" {
		fmt.Fprintln(os.Stderr, "❌ --name 和 --file 必填")
		os.Exit(1)
	}

	data, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取策略文件失败:", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(kpPoliciesDir(), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "创建策略目录失败:", err)
		os.Exit(1)
	}

	dst := filepath.Join(kpPoliciesDir(), *name+".rego")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "写入失败:", err)
		os.Exit(1)
	}

	P.Done(fmt.Sprintf("策略 %q 已添加（%s）", *name, dst))
	fmt.Printf("\n策略检查将在 kp deploy 前自动运行\n")
}

func runPolicyList() {
	entries, err := os.ReadDir(kpPoliciesDir())
	if err != nil {
		fmt.Println("暂无已配置的策略，运行 kp policy add 添加")
		return
	}
	fmt.Printf("  已配置策略：\n\n")
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".rego") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".rego")
		path := filepath.Join(kpPoliciesDir(), e.Name())
		data, _ := os.ReadFile(path)
		lines := strings.Count(string(data), "\n")
		fmt.Printf("  %-20s  %s  (%d 行)\n", name, path, lines)
	}
}

func runPolicyRemove(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "用法: kp policy remove <name>")
		os.Exit(1)
	}
	path := filepath.Join(kpPoliciesDir(), args[0]+".rego")
	if err := os.Remove(path); err != nil {
		fmt.Fprintf(os.Stderr, "删除失败: %v\n", err)
		os.Exit(1)
	}
	P.Done(fmt.Sprintf("策略 %q 已删除", args[0]))
}

func runPolicyCheck(args []string) {
	flags := flag.NewFlagSet("policy check", flag.ExitOnError)
	namespace := flags.String("namespace", "", "kubernetes namespace")
	flags.Parse(args)

	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}
	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	resolveDeployConfig(&deployConfig{namespace: *namespace}, env, root)

	blocked, warns := checkOPAPolicies(root, env)
	if len(blocked) == 0 && len(warns) == 0 {
		P.Info("✅", "所有策略检查通过")
		return
	}
	for _, w := range warns {
		fmt.Printf("  %s %s\n", colorize(colorYellow, "⚠️ "), w)
	}
	for _, b := range blocked {
		fmt.Printf("  %s %s\n", colorize(colorRed, "❌"), b)
	}
	if len(blocked) > 0 {
		os.Exit(1)
	}
}

// checkOPAPolicies 执行所有策略检查，返回阻断项和警告项
// 有 opa 命令才跑，没有静默跳过（不阻断部署）
func checkOPAPolicies(root string, env map[string]string) (blocked []string, warns []string) {
	// 检查 opa 是否可用
	if _, err := runOutput("opa", "version"); err != nil {
		return nil, nil // opa 未安装，静默跳过
	}

	entries, err := os.ReadDir(kpPoliciesDir())
	if err != nil {
		return nil, nil // 无策略目录，跳过
	}

	// 构建输入 context
	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))
	namespace := envOrDefault(env, "KUBE_NAMESPACE", projectName)
	version := envOrDefault(env, "VERSION", "v0.1.0")

	plans, _ := planner.BuildPlan(filepath.Join(root, "configs", "components.yaml"))
	var services []string
	for _, p := range plans {
		if p.Image != "" {
			services = append(services, p.Name)
		}
	}

	input := map[string]interface{}{
		"project":   projectName,
		"namespace": namespace,
		"version":   version,
		"services":  services,
		"env":       env,
	}
	inputJSON, _ := json.Marshal(input)

	// 对每个策略文件执行 opa eval
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".rego") {
			continue
		}
		policyPath := filepath.Join(kpPoliciesDir(), e.Name())
		policyName := strings.TrimSuffix(e.Name(), ".rego")

		// opa eval --data <policy> --stdin-input 'data.kp.deny'
		cmd := exec.Command("opa", "eval",
			"--data", policyPath,
			"--stdin-input",
			"--format", "raw",
			"data.kp.deny",
		)
		cmd.Stdin = bytes.NewReader(inputJSON)
		out, err := cmd.Output()

		if err != nil {
			warns = append(warns, fmt.Sprintf("[%s] opa 执行失败: %v", policyName, err))
			continue
		}

		result := strings.TrimSpace(string(out))
		if result == "" || result == "[]" || result == "undefined" {
			continue // 无违规
		}

		// 解析 deny 结果
		var denies []string
		if json.Unmarshal([]byte(result), &denies) == nil {
			for _, d := range denies {
				blocked = append(blocked, fmt.Sprintf("[%s] %s", policyName, d))
			}
		}
	}
	return blocked, warns
}
