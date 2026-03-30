package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type checkResult struct {
	name    string
	ok      bool
	isError bool
	detail  string
	fix     string
}

func runDoctor(_ []string) {
	P.Info("🩺", "检查环境依赖...")
	fmt.Println()

	var results []checkResult
	results = append(results, checkGo())
	results = append(results, checkDocker())
	results = append(results, checkKubectl())
	results = append(results, checkHelm())
	results = append(results, checkK8sCluster())

	root, err := projectRoot()
	if err == nil {
		results = append(results, checkProjectEnv(root))
		results = append(results, checkRegistryPrefix(root))
	}

	fmt.Println("环境依赖：")
	for _, r := range results[:min(5, len(results))] {
		printResult(r)
	}
	if len(results) > 5 {
		fmt.Println("\n项目配置：")
		for _, r := range results[5:] {
			printResult(r)
		}
	}

	// 安全检查（在项目根目录下才跑）—— 必须在统计之前
	if err == nil {
		secResults := []checkResult{
			checkPlaintextSecrets(root),
			checkPodSecurityContext(root),
			checkRBACWildcard(root),
			checkNetworkPolicy(root),
		}
		fmt.Println("\n安全检查：")
		for _, r := range secResults {
			printResult(r)
		}
		results = append(results, secResults...)
	}

	// 统计
	var errors, warns int
	for _, r := range results {
		if !r.ok {
			if r.isError {
				errors++
			} else {
				warns++
			}
		}
	}

	fmt.Println()
	if errors == 0 && warns == 0 {
		P.Info("✅", "环境检查通过，可以开始使用 dtk")
		return
	}

	parts := []string{}
	if errors > 0 {
		parts = append(parts, fmt.Sprintf("%d 个错误", errors))
	}
	if warns > 0 {
		parts = append(parts, fmt.Sprintf("%d 个警告", warns))
	}
	fmt.Println(strings.Join(parts, "，"))
	if errors > 0 {
		os.Exit(1)
	}
}

func checkGo() checkResult {
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		return checkResult{
			name: "Go", ok: false, isError: true,
			detail: "未找到 go 命令",
			fix:    "安装 Go 1.21+：https://go.dev/dl/",
		}
	}
	version := parseGoVersion(strings.TrimSpace(string(out)))
	if !goVersionOK(version) {
		return checkResult{
			name: "Go", ok: false, isError: true,
			detail: fmt.Sprintf("版本过低：%s，需要 1.21+", version),
			fix:    "升级 Go：https://go.dev/dl/",
		}
	}
	return checkResult{name: "Go", ok: true, detail: version}
}

func checkDocker() checkResult {
	out, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Output()
	if err != nil {
		return checkResult{
			name: "Docker", ok: false, isError: true,
			detail: "Docker 未运行或未安装",
			fix:    "启动 Docker Desktop，或安装 Docker：https://docs.docker.com/get-docker/",
		}
	}
	version := strings.TrimSpace(string(out))
	if version == "" {
		return checkResult{
			name: "Docker", ok: false, isError: true,
			detail: "Docker daemon 未运行",
			fix:    "启动 Docker Desktop 或运行 sudo systemctl start docker",
		}
	}
	return checkResult{name: "Docker", ok: true, detail: version + " (running)"}
}

func checkKubectl() checkResult {
	out, err := exec.Command("kubectl", "version", "--client", "--output=yaml").Output()
	if err != nil {
		return checkResult{
			name: "kubectl", ok: false, isError: true,
			detail: "未找到 kubectl",
			fix:    "安装 kubectl：https://kubernetes.io/docs/tasks/tools/",
		}
	}
	version := extractYAMLField(string(out), "gitVersion")
	if version == "" {
		version = "已安装"
	}
	return checkResult{name: "kubectl", ok: true, detail: version}
}

func checkHelm() checkResult {
	out, err := exec.Command("helm", "version", "--short").Output()
	if err != nil {
		return checkResult{
			name: "helm", ok: false, isError: true,
			detail: "未找到 helm",
			fix:    "安装 helm：https://helm.sh/docs/intro/install/",
		}
	}
	version := strings.TrimSpace(string(out))
	if idx := strings.Index(version, "+"); idx != -1 {
		version = version[:idx]
	}
	return checkResult{name: "helm", ok: true, detail: version}
}

func checkK8sCluster() checkResult {
	out, err := exec.Command("kubectl", "cluster-info", "--request-timeout=3s").Output()
	if err != nil {
		return checkResult{
			name: "K8s 集群", ok: false, isError: false,
			detail: "无法连接 K8s 集群",
			fix:    "检查 kubeconfig 配置，或确认集群是否运行：kubectl cluster-info",
		}
	}
	line := strings.SplitN(string(out), "\n", 2)[0]
	line = regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(line, "")
	line = strings.TrimSpace(strings.TrimPrefix(line, "Kubernetes control plane is running at "))
	return checkResult{name: "K8s 集群", ok: true, detail: line + " (reachable)"}
}

func checkProjectEnv(root string) checkResult {
	path := filepath.Join(root, "configs", "project.env")
	if _, err := os.Stat(path); err != nil {
		return checkResult{
			name: "project.env", ok: false, isError: false,
			detail: "configs/project.env 不存在",
			fix:    "运行 dtk init 生成项目，或手动创建 configs/project.env",
		}
	}
	return checkResult{name: "project.env", ok: true, detail: "存在"}
}

func checkRegistryPrefix(root string) checkResult {
	env, err := readEnvFile(filepath.Join(root, "configs", "project.env"))
	if err != nil {
		return checkResult{name: "REGISTRY_PREFIX", ok: true, detail: "跳过（读取 project.env 失败）"}
	}
	prefix := env["REGISTRY_PREFIX"]
	if prefix == "" {
		return checkResult{
			name: "REGISTRY_PREFIX", ok: false, isError: true,
			detail: "未填写，dtk deploy 时 push 镜像会失败",
			fix:    "编辑 configs/project.env，填写 REGISTRY_PREFIX=your-dockerhub-username",
		}
	}
	return checkResult{name: "REGISTRY_PREFIX", ok: true, detail: prefix}
}

func printResult(r checkResult) {
	if r.ok {
		fmt.Printf("  ✓ %-16s %s\n", r.name, r.detail)
		return
	}
	symbol := "⚠"
	if r.isError {
		symbol = "✗"
	}
	fmt.Printf("  %s %-16s %s\n", symbol, r.name, r.detail)
	if r.fix != "" {
		fmt.Printf("    %s%s\n", strings.Repeat(" ", 18), r.fix)
	}
}

func parseGoVersion(s string) string {
	re := regexp.MustCompile(`go(\d+\.\d+(?:\.\d+)?)`)
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return s
	}
	return m[1]
}

func goVersionOK(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	return major > 1 || (major == 1 && minor >= 21)
}

func extractYAMLField(yaml, key string) string {
	for _, line := range strings.Split(yaml, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key+":") {
			return strings.TrimSpace(strings.TrimPrefix(line, key+":"))
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
