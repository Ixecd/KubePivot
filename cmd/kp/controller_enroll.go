package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
)

// ── kp controller enroll ─────────────────────────────────────────────────────
//
// 把当前项目接入全局 KubePivot Controller。
//
// 三件事：
//   1. kubectl label ns <ns> kubepivot.io/managed=true
//      （ns 不存在则自动创建，和 kp deploy 行为一致）
//   2. 读取 configs/resources.yaml（可 --resources 覆盖）
//   3. 创建/更新 ConfigMap kubepivot-resources
//      - label: kubepivot.io/managed=true（让 controller watcher 能选中）
//      - annotation: kubepivot.io/sha256=<hex>（controller 热加载快速 skip）

func runControllerEnrollReal(args []string) {
	flags := flag.NewFlagSet("controller enroll", flag.ExitOnError)
	namespace := flags.String("namespace", "", "项目 namespace（默认读 configs/project.env 的 KUBE_NAMESPACE）")
	resourcesFile := flags.String("resources", "", "resources.yaml 路径（默认 configs/resources.yaml）")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	context_ := flags.String("context", "", "kube context")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	// 1. 定位项目根目录
	root, err := projectRoot()
	if err != nil {
		P.Fail(fmt.Sprintf("找不到项目根目录: %v", err))
		os.Exit(1)
	}

	// 2. 决定 namespace
	ns := *namespace
	if ns == "" {
		env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
		ns = envOrDefault(env, "KUBE_NAMESPACE", filepath.Base(root))
	}
	if ns == "" {
		P.Fail("无法确定 namespace，请指定 --namespace 或设置 configs/project.env")
		os.Exit(1)
	}

	// 3. 决定 resources.yaml 路径
	resourcesPath := *resourcesFile
	if resourcesPath == "" {
		resourcesPath = filepath.Join(root, "configs", "resources.yaml")
	}
	resourcesData, err := os.ReadFile(resourcesPath)
	if err != nil {
		P.Fail(fmt.Sprintf("读取 %s 失败: %v", resourcesPath, err))
		P.Info("💡", fmt.Sprintf("提示：kp init 会生成 configs/resources.yaml"))
		os.Exit(1)
	}
	if len(strings.TrimSpace(string(resourcesData))) == 0 {
		P.Fail(fmt.Sprintf("%s 为空", resourcesPath))
		os.Exit(1)
	}

	P.Info("🔗", fmt.Sprintf("接入项目 → namespace=%s, resources=%s",
		ns, relPath(root, resourcesPath)))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	kubeconfigExp := expandHome(*kubeconfig)

	// 4. 确保 namespace 存在（自动创建）
	if err := ensureNamespace(ctx, kubeconfigExp, *context_, ns); err != nil {
		P.Fail(fmt.Sprintf("确保 namespace 存在失败: %v", err))
		os.Exit(1)
	}

	// 5. 打 label
	P.Start("🏷 ", fmt.Sprintf("标记 namespace %s 为 managed", ns))
	if err := labelNamespace(ctx, kubeconfigExp, *context_, ns, "kubepivot.io/managed=true"); err != nil {
		P.Fail(fmt.Sprintf("label namespace 失败: %v", err))
		os.Exit(1)
	}
	P.Done("namespace label 已设置")

	// 6. 创建/更新 kubepivot-resources ConfigMap
	P.Start("📋", "同步 resources.yaml 到 ConfigMap")
	hash := sha256Hex(resourcesData)
	if err := syncResourcesConfigMap(ctx, kubeconfigExp, *context_, ns, string(resourcesData), hash); err != nil {
		P.Fail(fmt.Sprintf("同步 ConfigMap 失败: %v", err))
		os.Exit(1)
	}
	P.Done(fmt.Sprintf("ConfigMap kubepivot-resources 已更新（sha256=%s...）", hash[:8]))

	fmt.Println()
	fmt.Printf("%s 接入完成！\n", colorize(colorGreen, "✅"))
	fmt.Printf("  下一步验证：\n")
	fmt.Printf("    kp controller status               查看 controller 管理的项目数\n")
	fmt.Printf("    kp controller projects             列出所有被管理的项目\n")
	fmt.Printf("  以后修改 configs/resources.yaml 并重新 kp deploy 会自动同步\n")
}

// ── kp controller unenroll ───────────────────────────────────────────────────
//
// 取消接入：删除 ConfigMap + 移除 namespace label
// 不会删 namespace，不会卸载项目资源

func runControllerUnenrollReal(args []string) {
	flags := flag.NewFlagSet("controller unenroll", flag.ExitOnError)
	namespace := flags.String("namespace", "", "项目 namespace（默认读 configs/project.env 的 KUBE_NAMESPACE）")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	context_ := flags.String("context", "", "kube context")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	ns := *namespace
	if ns == "" {
		root, err := projectRoot()
		if err != nil {
			P.Fail(fmt.Sprintf("找不到项目根目录: %v", err))
			os.Exit(1)
		}
		env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
		ns = envOrDefault(env, "KUBE_NAMESPACE", filepath.Base(root))
	}
	if ns == "" {
		P.Fail("无法确定 namespace，请指定 --namespace")
		os.Exit(1)
	}

	P.Info("🔌", fmt.Sprintf("解除接入 → namespace=%s", ns))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	kubeconfigExp := expandHome(*kubeconfig)
	exec := executor.GetExecutor()

	// 1. 删除 ConfigMap
	P.Start("🗑 ", "删除 kubepivot-resources ConfigMap")
	_, err := exec.Kubectl(ctx, kubeconfigExp,
		"delete", "configmap", "kubepivot-resources",
		"-n", ns,
		"--ignore-not-found",
	)
	if err != nil {
		P.Fail(fmt.Sprintf("删除 ConfigMap 失败: %v", err))
		os.Exit(1)
	}
	P.Done("ConfigMap 已删除")

	// 2. 移除 namespace label
	P.Start("🏷 ", "移除 namespace managed label")
	if err := labelNamespace(ctx, kubeconfigExp, *context_, ns, "kubepivot.io/managed-"); err != nil {
		P.Fail(fmt.Sprintf("移除 label 失败: %v", err))
		os.Exit(1)
	}
	P.Done("namespace label 已移除")

	fmt.Println()
	fmt.Printf("%s 已解除接入\n", colorize(colorYellow, "⚠️ "))
	fmt.Printf("  注意：项目资源（Deployment/Service 等）未被卸载\n")
	fmt.Printf("  如需完全清理：kubectl delete namespace %s\n", ns)
}

// ── kp controller projects ───────────────────────────────────────────────────

func runControllerProjectsReal(args []string) {
	flags := flag.NewFlagSet("controller projects", flag.ExitOnError)
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	kubeconfigExp := expandHome(*kubeconfig)
	exec := executor.GetExecutor()

	out, err := exec.Kubectl(ctx, kubeconfigExp,
		"get", "namespace",
		"-l", "kubepivot.io/managed=true",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}",
	)
	if err != nil {
		P.Fail(fmt.Sprintf("查询失败: %v", err))
		os.Exit(1)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var projects []string
	for _, l := range lines {
		if l != "" {
			projects = append(projects, l)
		}
	}

	fmt.Println()
	if len(projects) == 0 {
		fmt.Printf("%s 当前没有任何项目被 controller 管理\n",
			colorize(colorYellow, "ℹ"))
		fmt.Printf("  进入项目目录运行 %s 接入\n",
			colorize(colorCyan, "kp controller enroll"))
		return
	}

	fmt.Printf("%s Controller 管理的项目（%d 个）：\n",
		colorize(colorCyan, "🔱"), len(projects))
	fmt.Println()
	for _, ns := range projects {
		// 检查是否有 kubepivot-resources ConfigMap
		cmOut, _ := exec.Kubectl(ctx, kubeconfigExp,
			"get", "configmap", "kubepivot-resources",
			"-n", ns,
			"-o", "jsonpath={.metadata.annotations.kubepivot\\.io/sha256}",
			"--ignore-not-found",
		)
		sha := strings.TrimSpace(string(cmOut))
		if sha != "" && len(sha) > 8 {
			sha = sha[:8] + "..."
		} else if sha == "" {
			sha = colorize(colorYellow, "(ConfigMap 缺失)")
		}
		fmt.Printf("  %s %-30s %s\n", colorize(colorGreen, "✓"), ns, sha)
	}
}

// ── 辅助函数 ──────────────────────────────────────────────────────────────────

// ensureNamespace 幂等创建 namespace
func ensureNamespace(ctx context.Context, kubeconfig, kubeContext, ns string) error {
	exec := executor.GetExecutor()
	// kubectl create namespace <ns> --dry-run=client -o yaml | kubectl apply -f -
	createArgs := []string{"create", "namespace", ns,
		"--dry-run=client", "-o", "yaml"}
	yaml, err := exec.Kubectl(ctx, kubeconfig, createArgs...)
	if err != nil {
		return fmt.Errorf("生成 namespace yaml: %w", err)
	}
	applyCmd := exec.CmdKubectl(ctx, kubeconfig, "apply", "-f", "-")
	applyCmd.Stdin = strings.NewReader(string(yaml))
	if out, err := applyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apply namespace: %w\n%s", err, string(out))
	}
	return nil
}

// labelNamespace 给 namespace 打（或移除）label
// label="kubepivot.io/managed=true" → 打
// label="kubepivot.io/managed-"    → 移除
func labelNamespace(ctx context.Context, kubeconfig, kubeContext, ns, label string) error {
	exec := executor.GetExecutor()
	args := []string{"label", "namespace", ns, label, "--overwrite"}
	out, err := exec.Kubectl(ctx, kubeconfig, args...)
	if err != nil {
		return fmt.Errorf("%w\n%s", err, string(out))
	}
	return nil
}

// syncResourcesConfigMap 幂等创建/更新 kubepivot-resources ConfigMap
//
// ConfigMap 结构：
//
//	metadata:
//	  name: kubepivot-resources
//	  namespace: <ns>
//	  labels:
//	    kubepivot.io/managed: "true"       ← controller watcher 用这个 label 选中
//	  annotations:
//	    kubepivot.io/sha256: <hex>          ← controller 快速 skip 用
//	data:
//	  resources.yaml: |
//	    resources: [...]
func syncResourcesConfigMap(ctx context.Context, kubeconfig, kubeContext, ns, content, hash string) error {
	exec := executor.GetExecutor()

	// 1. 用 kubectl create --dry-run 生成基础 CM
	createArgs := []string{
		"create", "configmap", "kubepivot-resources",
		"-n", ns,
		"--from-literal", "resources.yaml=__PLACEHOLDER__", // 临时占位，下面会替换
		"--dry-run=client", "-o", "yaml",
	}
	_, err := exec.Kubectl(ctx, kubeconfig, createArgs...)
	if err != nil {
		return fmt.Errorf("生成基础 CM: %w", err)
	}

	// 2. 直接手写完整 YAML（比 kubectl create --from-literal 更可控）
	yaml := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: kubepivot-resources
  namespace: %s
  labels:
    kubepivot.io/managed: "true"
    app.kubernetes.io/managed-by: kp
  annotations:
    kubepivot.io/sha256: %q
data:
  resources.yaml: |
%s
`, ns, hash, indent(content, "    "))

	applyCmd := exec.CmdKubectl(ctx, kubeconfig, "apply", "-f", "-")
	applyCmd.Stdin = strings.NewReader(yaml)
	if out, err := applyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apply CM: %w\n%s", err, string(out))
	}
	return nil
}

// indent 给每一行添加缩进前缀
func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// sha256Hex 计算内容的 sha256 十六进制
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// relPath 项目相对路径，方便日志打印
func relPath(root, absPath string) string {
	if rel, err := filepath.Rel(root, absPath); err == nil {
		return rel
	}
	return absPath
}
