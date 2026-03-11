package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Ixecd/dev-toolkit/internal/ai"
	"github.com/Ixecd/dev-toolkit/internal/scaffold"
)

func expandHome(path string) string {
	if path == "" {
		return path
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return home + path[1:]
		}
	}

	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	return path
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	if strings.HasPrefix(os.Args[1], "-") {
		runInit(os.Args[1:])
		return
	}

	switch os.Args[1] {
	case "init":
		runInit(os.Args[2:])
	case "deploy":
		runDeploy(os.Args[2:])
	default:
		printUsage()
		os.Exit(1)
	}
}

func runInit(args []string) {
	flags := flag.NewFlagSet("init", flag.ExitOnError)
	name := flags.String("name", "", "project name (lowercase, e.g. demo-svc)")
	module := flags.String("module", "", "go module path (e.g. github.com/you/demo-svc)")
	output := flags.String("output", "", "output directory (default: ./<name>)")
	template := flags.String("template", "", "template root (default: repo root or DTK_TEMPLATE_ROOT)")
	force := flags.Bool("force", false, "allow non-empty output directory")

	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "解析参数失败:", err)
		os.Exit(1)
	}

	*output = expandHome(*output)
	*template = expandHome(*template)

	if *name == "" && *output != "" {
		*name = filepathBase(*output)
	}
	if *name == "" {
		fmt.Fprintln(os.Stderr, "缺少 --name")
		flags.Usage()
		os.Exit(1)
	}

	if err := scaffold.InitProject(scaffold.InitOptions{
		Name:         *name,
		Module:       *module,
		OutputDir:    *output,
		TemplateRoot: *template,
		Force:        *force,
		Stdout:       os.Stdout,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "初始化失败:", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `dtk - dev-toolkit 脚手架

用法:
  dtk init --name <project> --module <module> [--output <dir>] [--template <dir>] [--force]
  dtk deploy [--components <path>] [--namespace <ns>] [--context <ctx>] [--dry-run]

示例:
  dtk init --name demo-svc --module github.com/you/demo-svc
  dtk init --name demo-svc --module github.com/you/demo-svc --output ./demo-svc
  dtk deploy
`)
}

func filepathBase(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

func runDeploy(args []string) {
	flags := flag.NewFlagSet("deploy", flag.ExitOnError)
	components := flags.String("components", "configs/components.yaml", "components config path")
	namespace := flags.String("namespace", "", "kubernetes namespace (default from configs/project.env)")
	context := flags.String("context", "", "kubernetes context (default from configs/project.env)")
	dryRun := flags.Bool("dry-run", false, "print plan only, do not deploy")

	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "解析参数失败:", err)
		os.Exit(1)
	}

	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	plan, err := ai.BuildPlan(filepath.Join(root, *components))
	if err != nil {
		fmt.Fprintln(os.Stderr, "解析组件失败:", err)
		os.Exit(1)
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	projectName := env["PROJECT_NAME"]
	if projectName == "" {
		projectName = filepath.Base(root)
	}
	if *namespace == "" {
		*namespace = env["KUBE_NAMESPACE"]
		if *namespace == "" {
			*namespace = projectName
		}
	}
	if *context == "" {
		*context = env["KUBE_CONTEXT"]
	}

	printPlan(plan)
	if *dryRun {
		return
	}

	makeArgs := []string{"deploy.full"}
	makeEnv := os.Environ()
	if *namespace != "" {
		makeEnv = append(makeEnv, "KUBE_NAMESPACE="+*namespace)
	}
	if *context != "" {
		makeEnv = append(makeEnv, "KUBE_CONTEXT="+*context)
	}

	for k, v := range env {
		makeEnv = append(makeEnv, k+"="+v)
	}
	makeEnv = append(makeEnv, "VERSION=v0.1.0", "ARCH=amd64", "REGISTRY_PREFIX=local")
	if err := runCmd(root, makeEnv, "make", makeArgs...); err != nil {
		fmt.Fprintln(os.Stderr, "部署失败:", err)
		os.Exit(1)
	}

	for _, item := range plan {
		if item.Name == "" {
			continue
		}
		if item.Image == "" {
			continue
		}
		if err := scaleDeployment(*context, *namespace, item.Name, item.Replicas); err != nil {
			fmt.Fprintln(os.Stderr, "设置副本数失败:", err)
			os.Exit(1)
		}
		if err := setDeploymentResources(*context, *namespace, item.Name, item); err != nil {
			fmt.Fprintln(os.Stderr, "设置资源失败:", err)
			os.Exit(1)
		}
	}
}

func projectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		if fileExists(filepath.Join(dir, "Makefile")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("当前目录不是项目根目录")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func readEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	env := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		env[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return env, nil
}

func runCmd(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func printPlan(plan []ai.Plan) {
	fmt.Println("AI 规划结果:")
	for _, item := range plan {
		if item.Name == "" {
			continue
		}
		if item.Image == "" {
			fmt.Printf("- %s: skip (no image)\n", item.Name)
			continue
		}
		fmt.Printf("- %s: replicas=%d cpu=%s memory=%s storage=%s\n", item.Name, item.Replicas, item.CPU, item.Memory, item.Storage)
	}
}

func scaleDeployment(context, namespace, name string, replicas int) error {
	if replicas <= 0 {
		return nil
	}
	args := []string{}
	if context != "" {
		args = append(args, "--context", context)
	}
	if namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	args = append(args, "scale", "deployment/"+name, fmt.Sprintf("--replicas=%d", replicas))
	return runCmd("", nil, "kubectl", args...)
}

func setDeploymentResources(context, namespace, name string, item ai.Plan) error {
	limits := buildResourceArgs(item.CPU, item.Memory, item.Storage)
	if limits == "" {
		return nil
	}
	args := []string{}
	if context != "" {
		args = append(args, "--context", context)
	}
	if namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	args = append(args, "set", "resources", "deployment/"+name, "--limits="+limits, "--requests="+limits)
	return runCmd("", nil, "kubectl", args...)
}

func buildResourceArgs(cpu, memory, storage string) string {
	parts := []string{}
	if cpu != "" {
		parts = append(parts, "cpu="+cpu)
	}
	if memory != "" {
		parts = append(parts, "memory="+memory)
	}
	if storage != "" {
		parts = append(parts, "ephemeral-storage="+storage)
	}
	return strings.Join(parts, ",")
}
