package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Ixecd/dev-toolkit/internal/logger"
	"github.com/Ixecd/dev-toolkit/internal/planner"
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
	logger.Init()

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
	case "resume":
		runResume(os.Args[2:])
	case "rollback":
		runRollback(os.Args[2:])
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
	withFrontend := flags.Bool("with-frontend", false, "generate React + Vite + Tailwind frontend skeleton")

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
		WithFrontend: *withFrontend,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "初始化失败:", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `dtk - dev-toolkit 脚手架

用法:
  dtk init     --name <project> --module <module> [--output <dir>] [--template <dir>] [--force]
  dtk deploy   [--components <path>] [--namespace <ns>] [--context <ctx>] [--kubeconfig <path>] [--dry-run]
  dtk resume   [--namespace <ns>] [--context <ctx>] [--kubeconfig <path>]
  dtk rollback [--namespace <ns>] [--context <ctx>] [--kubeconfig <path>]

示例:
  dtk init --name demo-svc --module github.com/you/demo-svc
  dtk deploy
  dtk deploy --kubeconfig ~/.kube/prod.yaml --context prod-cluster
  dtk resume
  dtk rollback
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
		env[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return env, scanner.Err()
}

func runCmd(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func printPlan(plan []planner.Plan) {
	fmt.Println("AI 规划结果:")
	for _, item := range plan {
		if item.Name == "" {
			continue
		}
		if item.Image == "" {
			fmt.Printf("- %s: skip (no image)\n", item.Name)
			continue
		}
		fmt.Printf("- %s: replicas=%d cpu=%s memory=%s storage=%s\n",
			item.Name, item.Replicas, item.CPU, item.Memory, item.Storage)
	}
}

func scaleDeployment(kubeconfig, context, namespace, name string, replicas int) error {
    if replicas <= 0 {
        return nil
    }
    args := append([]string{"kubectl"}, kubectlBaseArgs(kubeconfig, context, namespace)...)
    args = append(args, "scale", "deployment/"+name, fmt.Sprintf("--replicas=%d", replicas))
    _, err := runOutput(args...)
    return err
}

func setDeploymentResources(kubeconfig, context, namespace, name string, item planner.Plan) error {
    limits := buildResourceArgs(item.CPU, item.Memory, item.Storage)
    if limits == "" {
        return nil
    }
    args := append([]string{"kubectl"}, kubectlBaseArgs(kubeconfig, context, namespace)...)
    args = append(args, "set", "resources", "deployment/"+name,
        "--limits="+limits, "--requests="+limits)
    _, err := runOutput(args...)
    return err
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
