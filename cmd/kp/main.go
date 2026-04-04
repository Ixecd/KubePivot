package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Ixecd/kubepivot/internal/controller"
	"github.com/Ixecd/kubepivot/internal/logger"
	"github.com/Ixecd/kubepivot/internal/planner"
	"github.com/Ixecd/kubepivot/internal/scaffold"
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
	case "down":
		runDown(os.Args[2:])
	case "ai-plan":
		runAIPlan(os.Args[2:])
	case "doctor":
		runDoctor(os.Args[2:])
	case "upgrade":
		runUpgrade(os.Args[2:])
	case "history":
		runHistory(os.Args[2:])
	case "diff":
		runDiff(os.Args[2:])
	case "resume":
		runResume(os.Args[2:])
	case "status":
		runStatus(os.Args[2:])
	case "rollback":
		runRollback(os.Args[2:])
	case "release":
		runRelease(os.Args[2:])
	case "scan":
		runScan(os.Args[2:])
	case "warmup":
		runWarmup(os.Args[2:])
	case "promote":
		runPromote(os.Args[2:])
	case "migrate":
		runMigrate(os.Args[2:])
	case "compat":
		runCompat(os.Args[2:])
	case "pvc":
		runPVC(os.Args[2:])
	case "network":
		runNetwork(os.Args[2:])
	case "secret":
		runSecret(os.Args[2:])
	case "context":
		runContext(os.Args[2:])
	case "sandbox":
		runSandbox(os.Args[2:])
	case "controller":
		if len(os.Args) > 2 && os.Args[2] == "start" {
			controller.Start()
		} else {
			fmt.Fprintln(os.Stderr, "用法: kp controller start")
			os.Exit(1)
		}
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
	template := flags.String("template", "", "template root (default: repo root or KP_TEMPLATE_ROOT)")
	force := flags.Bool("force", false, "allow non-empty output directory")
	withFrontend := flags.Bool("with-frontend", false, "generate React + Vite + Tailwind frontend skeleton")
	dryRun := flags.Bool("dry-run", false, "print what would be generated, do not execute")

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
		DryRun:       *dryRun,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "初始化失败:", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `kp - kubepivot 脚手架

用法:
  kp init     --name <project> --module <module> [--output <dir>] [--template <dir>] [--force]
  kp ai-plan  [--suggest-only] [--desc "描述"]   AI 扫描仓库，自动规划组件配置
  kp doctor   检查环境依赖
  kp deploy   [--components <path>] [--namespace <ns>] [--context <ctx>] [--kubeconfig <path>] [--dry-run]
  kp upgrade  [--target v1.4.0] [--service <name>] [--dry-run] [--force]  跨版本全链路升级
  kp status   [--namespace] [--context] [--kubeconfig] [--history]  查看部署状态
  kp history  [-n 20] [--namespace] [--context]   查看部署历史
  kp diff     [--from N] [--to M] [--namespace] [--context]   对比两个版本的配置差异
  kp down     [--namespace] [--context] [--kubeconfig]   彻底下线服务
  kp resume   [--namespace <ns>] [--context <ctx>] [--kubeconfig <path>]
  kp rollback [--namespace <ns>] [--context <ctx>] [--kubeconfig <path>]
  kp release  --version <v1.2.3> [--deploy] [--no-push] 
  kp scan     [--severity CRITICAL,HIGH] [--image img:tag]   扫描镜像 CVE
  kp promote  [--service <name>] [--namespace <ns>]   切换蓝绿流量到新版本
  kp migrate  status [--database-url] [--service] [--migration-tool]  查看 DB 迁移状态
  kp compat   check [--base] [--revision] [--output-json]  检测 API 破坏性变更
  kp migrate  run   [--dry-run] [--full-sql] [--target N]   执行数据库迁移
  kp pvc      backup/restore/list --service <name>   PVC 快照备份和恢复（需要 CSI） 
  kp controller start   （在 controller pod 内部运行，启动 Reconciliation Loop）

示例:
  kp init --name demo-svc --module github.com/you/demo-svc
  kp deploy
  kp deploy --kubeconfig ~/.kube/prod.yaml --context prod-cluster
  kp resume
  kp rollback
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

	// 用 TeeWriter 同时输出到终端和捕获 stderr 内容
	var stderrBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)

	if err := cmd.Run(); err != nil {
		// 把 stderr 内容带进 error，供上层检测 SSA 冲突等关键词
		return fmt.Errorf("%w\n%s", err, stderrBuf.String())
	}
	return nil
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
