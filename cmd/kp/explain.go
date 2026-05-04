package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// runExplain 调度决策溯源 (decision-stack.md §4.3)
//
//	kp explain                    — 项目级概览
//	kp explain --pod <name>       — 单 Pod 决策溯源
//	kp explain --list-pods        — 输出 managed pod 列表（供 shell completion 使用）
func runExplain(args []string) {
	flags := flag.NewFlagSet("explain", flag.ExitOnError)
	podName := flags.String("pod", "", "Pod 名称")
	namespace := flags.String("namespace", "", "namespace（默认从 project.env 读取）")
	listPods := flags.Bool("list-pods", false, "输出 managed pod 列表（一行一个，供 shell completion）")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	if *listPods {
		listManagedPods(*namespace)
		return
	}

	componentsPath := filepath.Join("configs", "components.yaml")
	sizingMeta := parseSizingComments(componentsPath)

	if *podName != "" {
		printPodExplain(*podName, *namespace, sizingMeta)
	} else {
		printProjectExplain(sizingMeta)
	}
}

// listManagedPods 输出所有 managed pod 名称（一行一个，shell completion 用）。
func listManagedPods(namespace string) {
	// 获取 managed namespace 列表
	nsOut, err := execKubectl("get", "namespace",
		"-l", "kubepivot.io/managed=true",
		"-o", "jsonpath={.items[*].metadata.name}")
	if err != nil || len(nsOut) == 0 {
		return
	}
	namespaces := strings.Fields(strings.TrimSpace(string(nsOut)))

	for _, ns := range namespaces {
		out, err := execKubectl("get", "pods",
			"-n", ns,
			"-o", "jsonpath={.items[*].metadata.name}")
		if err != nil {
			continue
		}
		for _, name := range strings.Fields(strings.TrimSpace(string(out))) {
			fmt.Println(name)
		}
	}
}

// execKubectl 执行 kubectl 命令（无 kubeconfig，用当前 context）。
func execKubectl(args ...string) (string, error) {
	cmd := exec.Command("kubectl", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// sizingEntry 从 components.yaml 注释中解析出的单个组件决策元数据。
type sizingEntry struct {
	Name       string
	Profile    string
	Confidence float64
	Samples    int
	CurrentCPU string
	RecCPU     string
	SavingsCPU string
	CurrentMem string
	RecMem     string
	SavingsMem string
	AppliedAt  string
}

// parseSizingComments 读取 components.yaml，提取 # Reason: sizing: 注释中的决策元数据。
func parseSizingComments(path string) []sizingEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil
	}

	// 导航到 components 序列
	var compSeq *yaml.Node
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		compSeq = findComponentsNode(root.Content[0])
	} else {
		compSeq = findComponentsNode(&root)
	}
	if compSeq == nil || compSeq.Kind != yaml.SequenceNode {
		return nil
	}

	var entries []sizingEntry
	for _, compMap := range compSeq.Content {
		if compMap.Kind != yaml.MappingNode {
			continue
		}

		var name string
		var lineComment string

		for i := 0; i < len(compMap.Content); i += 2 {
			key := compMap.Content[i]
			val := compMap.Content[i+1]
			if key.Value == "name" {
				name = val.Value
			}
			if key.Value == "cpu" && val.LineComment != "" {
				lineComment = val.LineComment
			}
			if key.Value == "memory" && val.LineComment != "" && lineComment == "" {
				lineComment = val.LineComment
			}
		}

		if name == "" || lineComment == "" || !strings.HasPrefix(lineComment, "# Reason: sizing:") {
			continue
		}

		entry := parseSizingComment(name, lineComment)
		if entry != nil {
			entries = append(entries, *entry)
		}
	}
	return entries
}

// parseSizingComment 解析单行 sizing 注释。
// 格式: "# Reason: sizing: profile=web confidence=0.85 samples=672 cpu=500m→320m(-36%) mem=512Mi→384Mi(-25%) at=2026-05-04T11:00:00Z"
func parseSizingComment(name, comment string) *sizingEntry {
	// 去掉前缀
	content := strings.TrimPrefix(comment, "# Reason: sizing:")
	content = strings.TrimSpace(content)

	entry := &sizingEntry{Name: name}
	parts := strings.Fields(content)

	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k, v := kv[0], kv[1]

		switch k {
		case "profile":
			entry.Profile = v
		case "confidence":
			entry.Confidence, _ = strconv.ParseFloat(v, 64)
		case "samples":
			entry.Samples, _ = strconv.Atoi(v)
		case "cpu":
			// "500m→320m(-36%)"
			parseCPUChange(v, entry)
		case "mem":
			// "512Mi→384Mi(-25%)"
			parseMemChange(v, entry)
		case "at":
			entry.AppliedAt = v
		}
	}
	return entry
}

func parseCPUChange(v string, e *sizingEntry) {
	arrow := strings.Index(v, "→")
	if arrow < 0 {
		return
	}
	e.CurrentCPU = v[:arrow]
	rest := v[arrow+len("→"):]
	paren := strings.Index(rest, "(")
	if paren >= 0 {
		e.RecCPU = rest[:paren]
		e.SavingsCPU = strings.TrimSuffix(rest[paren+1:], ")")
	} else {
		e.RecCPU = rest
	}
}

func parseMemChange(v string, e *sizingEntry) {
	arrow := strings.Index(v, "→")
	if arrow < 0 {
		return
	}
	e.CurrentMem = v[:arrow]
	rest := v[arrow+len("→"):]
	paren := strings.Index(rest, "(")
	if paren >= 0 {
		e.RecMem = rest[:paren]
		e.SavingsMem = strings.TrimSuffix(rest[paren+1:], ")")
	} else {
		e.RecMem = rest
	}
}

func findComponentsNode(node *yaml.Node) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == "components" {
			return node.Content[i+1]
		}
	}
	return nil
}

// ── 输出 ────────────────────────────────────────────────────────────────────────

func printPodExplain(podName, namespace string, entries []sizingEntry) {
	sep := fmt.Sprintf("%s", colorize(colorCyan, "─────────────────────────────────────────"))

	fmt.Println()
	fmt.Printf("%s Pod 决策溯源\n", colorize(colorCyan, formatPodRef(podName, namespace)))
	fmt.Println()

	// 匹配组件
	var entry *sizingEntry
	for i := range entries {
		if strings.Contains(podName, entries[i].Name) || strings.HasPrefix(podName, entries[i].Name+"-") {
			entry = &entries[i]
			break
		}
	}

	// ── Sizing (Layer 2) ──
	fmt.Printf("  %s Sizing (Layer 2)\n", colorize(colorGray, "──"))
	if entry != nil {
		fmt.Printf("  %-16s %s\n", "Source:", "DP sizing")
		fmt.Printf("  %-16s %s\n", "Profile:", entry.Profile)
		fmt.Printf("  %-16s %s\n", "Confidence:", colorize(colorGreen, fmt.Sprintf("%.0f%%", entry.Confidence*100)))
		fmt.Printf("  %-16s %d (7d window)\n", "Samples:", entry.Samples)
		fmt.Println(sep)
		fmt.Printf("  %-16s %s → %s (%s)\n", "CPU:", entry.CurrentCPU, colorize(colorGreen, entry.RecCPU), entry.SavingsCPU)
		fmt.Printf("  %-16s %s → %s (%s)\n", "Memory:", entry.CurrentMem, colorize(colorGreen, entry.RecMem), entry.SavingsMem)
		if entry.AppliedAt != "" {
			fmt.Printf("  %-16s %s\n", "Applied:", entry.AppliedAt)
		}
	} else {
		fmt.Printf("  %s\n", colorize(colorGray, "暂无 sizing 决策记录"))
		fmt.Printf("  %s\n", colorize(colorGray, "运行 kp deploy --sizing auto 开始数据驱动的资源优化"))
	}

	// ── Scheduling (Layer 3) ──
	fmt.Println()
	fmt.Printf("  %s Scheduling (Layer 3)\n", colorize(colorGray, "──"))
	fmt.Printf("  %s\n", colorize(colorGray, "Status: not yet available (planned v3.0)"))
	fmt.Printf("  %s\n", colorize(colorGray, "Layer 3 将在 v3.0 实现节点级 bin packing 调度优化"))
	fmt.Println()
}

func printProjectExplain(entries []sizingEntry) {
	fmt.Println()
	fmt.Printf("%s 项目决策概览\n", colorize(colorCyan, "📊"))
	fmt.Println()

	if len(entries) == 0 {
		fmt.Printf("  %s\n", colorize(colorGray, "暂无 sizing 决策记录"))
		fmt.Printf("  %s\n", colorize(colorGray, "运行 kp deploy --sizing auto 开始数据驱动的资源优化"))
		fmt.Println()
		return
	}

	fmt.Printf("  %d 个组件有 sizing 决策记录:\n\n", len(entries))
	for _, e := range entries {
		fmt.Printf("  %s %s\n", colorize(colorGreen, "•"), colorize(colorGray, e.Name))
		fmt.Printf("    profile=%-7s confidence=%.0f%% samples=%-5d cpu=%s→%s(%s) mem=%s→%s(%s)\n",
			e.Profile, e.Confidence*100, e.Samples,
			e.CurrentCPU, e.RecCPU, e.SavingsCPU,
			e.CurrentMem, e.RecMem, e.SavingsMem,
		)
	}

	fmt.Println()
	fmt.Printf("  %s 运行 %s 查看单 Pod 详情\n",
		colorize(colorGray, "💡"),
		colorize(colorGreen, "kp explain --pod <name>"))
	fmt.Println()
}

func formatPodRef(name, ns string) string {
	if ns != "" {
		return fmt.Sprintf("%s/%s", ns, name)
	}
	return name
}
