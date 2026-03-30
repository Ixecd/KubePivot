package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Ixecd/kubepivot/internal/planner"
	"github.com/Ixecd/kubepivot/internal/state"
)

func runStatus(args []string) {
	flags := flag.NewFlagSet("status", flag.ExitOnError)
	namespace := flags.String("namespace", "", "kubernetes namespace")
	context := flags.String("context", "", "kubernetes context")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 文件路径")
	showHistory := flags.Bool("history", false, "显示状态转换历史")
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
	version := envOrDefault(env, "VERSION", "v0.1.0")

	// ── 头部信息 ──────────────────────────────────────────────
	fmt.Printf("项目: %-20s 命名空间: %-20s 版本: %s\n\n",
		projectName, cfg.namespace, version)

	// ── 状态机状态 ────────────────────────────────────────────
	store := state.NewAutoStore(env["ETCD_ENDPOINTS"])
	sm, err := state.New(store, projectName, cfg.namespace, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载状态失败:", err)
		os.Exit(1)
	}

	record := sm.Record()
	stateIcon := stateEmoji(record.State)
	fmt.Printf("部署状态: %s %s\n", stateIcon, record.State)
	if !record.UpdatedAt.IsZero() {
		fmt.Printf("  最后更新: %s\n", record.UpdatedAt.Format("2006-01-02 15:04:05"))
	}
	if record.Reason != "" {
		fmt.Printf("  原因:     %s\n", record.Reason)
	}
	fmt.Println()

	// ── K8s Pod 状态 ──────────────────────────────────────────
	fmt.Println("K8s 实际状态:")
	printPodStatus(cfg)
	fmt.Println()

	// ── Helm Release 信息 ─────────────────────────────────────────
	fmt.Println("Helm:")
	componentsPath := filepath.Join(root, "configs", "components.yaml")
	components, err := planner.LoadComponents(componentsPath)
	if err != nil || len(components) == 0 {
		// 降级：单 release 模式
		printHelmStatus(cfg, projectName)
	} else {
		printHelmStatusMulti(cfg, projectName, components)
	}

	// ── 历史记录（--history）─────────────────────────────────
	if *showHistory {
		fmt.Println()
		printHistory(record)
	}
}

// stateEmoji 根据状态返回对应图标
func stateEmoji(s state.State) string {
	switch s {
	case state.StateRunning:
		return "✅"
	case state.StateDeploying, state.StateInitializing, state.StateValidating:
		return "🔄"
	case state.StateRollingBack:
		return "⏪"
	case state.StateCleaning:
		return "🧹"
	case state.StateTerminated:
		return "🔴"
	default:
		return "⬜"
	}
}

// printPodStatus 列出 namespace 下所有 pod
func printPodStatus(cfg *deployConfig) {
	args := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	args = append(args, "get", "pods",
		"--no-headers",
		"-o", "custom-columns=NAME:.metadata.name,READY:.status.containerStatuses[0].ready,STATUS:.status.phase,RESTARTS:.status.containerStatuses[0].restartCount,AGE:.metadata.creationTimestamp",
	)

	out, err := runOutput(args...)
	if err != nil || strings.TrimSpace(string(out)) == "" {
		fmt.Println("  （namespace 下没有 pod，或集群不可达）")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		podName := fields[0]
		status := fields[2]

		icon := "✓"
		if status != "Running" {
			icon = "✗"
		}
		fmt.Fprintf(w, "  %s %s\t%s\n", icon, podName, status)
	}
	w.Flush()
}

// printHelmStatus 显示 helm release 信息
func printHelmStatus(cfg *deployConfig, releaseName string) {
	helmArgs := []string{"helm", "status", releaseName,
		"--namespace", cfg.namespace,
		"--output", "json",
	}
	if cfg.kubeconfig != "" {
		helmArgs = append(helmArgs, "--kubeconfig", cfg.kubeconfig)
	}
	if cfg.context != "" {
		helmArgs = append(helmArgs, "--kube-context", cfg.context)
	}

	out, err := runOutput(helmArgs...)
	if err != nil {
		fmt.Println("  （helm release 不存在或集群不可达）")
		return
	}

	var result struct {
		Info struct {
			Status       string `json:"status"`
			LastDeployed string `json:"last_deployed"`
		} `json:"info"`
		Version int `json:"version"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		fmt.Println("  （解析 helm status 失败）")
		return
	}

	// 时间格式化
	updated := result.Info.LastDeployed
	if t, err := time.Parse(time.RFC3339Nano, updated); err == nil {
		updated = t.Local().Format("2006-01-02 15:04:05")
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintf(w, "  Release:\t%s\n", releaseName)
	fmt.Fprintf(w, "  Revision:\t%d\n", result.Version)
	fmt.Fprintf(w, "  Status:\t%s\n", result.Info.Status)
	fmt.Fprintf(w, "  Updated:\t%s\n", updated)
	w.Flush()
}

// printHistory 打印最近 10 条状态转换历史
func printHistory(record *state.DeployRecord) {
	history := record.History
	if len(history) == 0 {
		fmt.Println("历史记录: （暂无）")
		return
	}

	// 取最近 10 条
	start := 0
	if len(history) > 10 {
		start = len(history) - 10
		fmt.Printf("历史记录（最近 10 条，共 %d 条）:\n", len(history))
	} else {
		fmt.Printf("历史记录（共 %d 条）:\n", len(history))
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n", "时间", "从", "→", "到", "原因")
	fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n",
		strings.Repeat("-", 19), strings.Repeat("-", 14), "-",
		strings.Repeat("-", 14), strings.Repeat("-", 20))

	for _, h := range history[start:] {
		fmt.Fprintf(w, "  %s\t%s\t→\t%s\t%s\n",
			h.Timestamp.Format("2006-01-02 15:04:05"),
			h.From, h.To, h.Reason,
		)
	}
	w.Flush()

	_ = time.Now() // 保留 time import
}

func printHelmStatusMulti(cfg *deployConfig, projectName string, components []planner.Component) {
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintf(w, "  %-40s\t%-10s\t%-12s\t%s\n", "Release", "Revision", "Status", "Updated")
	fmt.Fprintf(w, "  %-40s\t%-10s\t%-12s\t%s\n",
		strings.Repeat("-", 40), strings.Repeat("-", 8),
		strings.Repeat("-", 12), strings.Repeat("-", 19))

	for _, c := range components {
		releaseName := projectName + "-" + c.Name
		info := helmReleaseInfo(cfg, releaseName)
		fmt.Fprintf(w, "  %-40s\t%-10s\t%-12s\t%s\n",
			releaseName, info.revision, info.status, info.updated)
	}
	w.Flush()
}

type releaseInfo struct {
	revision string
	status   string
	updated  string
}

func helmReleaseInfo(cfg *deployConfig, releaseName string) releaseInfo {
	helmArgs := []string{"helm", "status", releaseName,
		"--namespace", cfg.namespace,
		"--output", "json",
	}
	if cfg.kubeconfig != "" {
		helmArgs = append(helmArgs, "--kubeconfig", cfg.kubeconfig)
	}
	if cfg.context != "" {
		helmArgs = append(helmArgs, "--kube-context", cfg.context)
	}

	out, err := runOutput(helmArgs...)
	if err != nil {
		return releaseInfo{revision: "-", status: "not found", updated: "-"}
	}

	var result struct {
		Info struct {
			Status       string `json:"status"`
			LastDeployed string `json:"last_deployed"`
		} `json:"info"`
		Version int `json:"version"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return releaseInfo{revision: "-", status: "parse error", updated: "-"}
	}

	updated := result.Info.LastDeployed
	if t, err := time.Parse(time.RFC3339Nano, updated); err == nil {
		updated = t.Local().Format("2006-01-02 15:04:05")
	}

	return releaseInfo{
		revision: fmt.Sprintf("%d", result.Version),
		status:   result.Info.Status,
		updated:  updated,
	}
}
