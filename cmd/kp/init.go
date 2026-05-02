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

	"github.com/Ixecd/kubepivot/internal/scaffold"
)

func Root() (string, error) {
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
  kp release  --version <v1.2.3> [--deploy]
  kp scan     [--severity CRITICAL,HIGH] [--image img:tag]   扫描镜像 CVE
  kp promote  [--service <name>] [--namespace <ns>]   切换蓝绿流量到新版本
  kp migrate  status [--database-url] [--service] [--migration-tool]  查看 DB 迁移状态
  kp compat   check [--base] [--revision] [--output-json]  检测 API 破坏性变更
  kp migrate  run   [--dry-run] [--full-sql] [--target N]   执行数据库迁移
  kp pvc      backup/restore/list --service <name>   PVC 快照备份和恢复（需要 CSI） 
  kp controller install    安装全局 KubePivot Controller 到集群（kubepivot-system）
  kp controller status     查看 controller 状态 + 被管理的项目数量
  kp controller enroll     当前项目接入全局 controller（在项目根目录运行）
  kp controller uninstall  卸载 controller（危险操作，会清理整个 kubepivot-system）
  kp controller start      （pod 内部使用）启动 Reconciliation Loop

示例:
  kp init --name demo-svc --module github.com/you/demo-svc
  kp deploy
  kp deploy --kubeconfig ~/.kube/prod.yaml --context prod-cluster
  kp resume
  kp rollback
`)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func runInit(args []string) {
	flags := flag.NewFlagSet("init", flag.ExitOnError)
	name := flags.String("name", "", "project name (lowercase, e.g. demo-svc)")
	module := flags.String("module", "", "go module path (e.g. github.com/you/demo-svc)")
	output := flags.String("output", "", "output directory (default: ./<name>)")
	template := flags.String("template", "", "template root directory (inline templates are used by default and can be overridden by advanced users)")
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
