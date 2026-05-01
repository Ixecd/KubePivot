package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/audit"
	"github.com/Ixecd/kubepivot/internal/rbac"
)

// Chaos Mesh API 地址（默认 port-forward 到本地）
const defaultChaosMeshAddr = "http://127.0.0.1:2333"

func runChaos(args []string) {
	if len(args) == 0 {
		fmt.Println("用法: kp chaos <子命令>")
		fmt.Println("  inject   注入混沌（pod-kill / network-delay / cpu-stress / memory-stress）")
		fmt.Println("  list     列出当前混沌实验")
		fmt.Println("  stop     停止混沌实验")
		fmt.Println("  status   查看混沌实验状态")
		fmt.Println()
		fmt.Printf("%s 需要 Chaos Mesh 已安装：\n", colorize(colorYellow, "💡"))
		fmt.Println("  helm repo add chaos-mesh https://charts.chaos-mesh.org")
		fmt.Println("  helm install chaos-mesh chaos-mesh/chaos-mesh -n chaos-mesh --create-namespace")
		os.Exit(1)
	}
	switch args[0] {
	case "inject":
		runChaosInject(args[1:])
	case "list", "ls":
		runChaosList(args[1:])
	case "stop":
		runChaosStop(args[1:])
	case "status":
		runChaosStatus(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n", args[0])
		os.Exit(1)
	}
}

func runChaosInject(args []string) {
	flags := flag.NewFlagSet("chaos inject", flag.ExitOnError)
	service := flags.String("service", "", "目标服务名（必填）")
	kind := flags.String("kind", "pod-kill", "混沌类型：pod-kill | network-delay | cpu-stress | memory-stress")
	namespace := flags.String("namespace", "", "kubernetes namespace")
	duration := flags.String("duration", "30s", "持续时间（如 30s / 5m）")
	addr := flags.String("chaos-mesh", defaultChaosMeshAddr, "Chaos Mesh API 地址")
	dryRun := flags.Bool("dry-run", false, "只打印实验配置，不实际注入")
	// network-delay 参数
	latency := flags.String("latency", "100ms", "网络延迟（network-delay 类型）")
	// cpu/memory-stress 参数
	workers := flags.Int("workers", 1, "压测线程数（cpu-stress / memory-stress）")
	flags.Parse(args)

	if *service == "" {
		fmt.Fprintln(os.Stderr, "❌ --service 必填")
		os.Exit(1)
	}

	root, _ := Root()
	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	ns := *namespace
	if ns == "" {
		ns = envOrDefault(env, "KUBE_NAMESPACE", "default")
	}

	// RBAC 检查
	mustCheck(audit.ResolveActor(), ns, rbac.PermChaos)

	// 构建实验配置
	expName := fmt.Sprintf("kp-%s-%s-%d", *kind, *service,
		time.Now().Unix())

	var expConfig map[string]interface{}
	switch *kind {
	case "pod-kill":
		expConfig = buildPodKillConfig(expName, ns, *service, *duration)
	case "network-delay":
		expConfig = buildNetworkDelayConfig(expName, ns, *service, *duration, *latency)
	case "cpu-stress":
		expConfig = buildCPUStressConfig(expName, ns, *service, *duration, *workers)
	case "memory-stress":
		expConfig = buildMemoryStressConfig(expName, ns, *service, *duration, *workers)
	default:
		fmt.Fprintf(os.Stderr, "不支持的混沌类型: %s\n", *kind)
		os.Exit(1)
	}

	configJSON, _ := json.MarshalIndent(expConfig, "  ", "  ")

	if *dryRun {
		fmt.Printf("\n%s 混沌实验配置（dry-run）：\n\n  %s\n\n",
			colorize(colorYellow, "📋"), string(configJSON))
		fmt.Printf("实际注入：kp chaos inject --service %s --kind %s --duration %s\n\n",
			*service, *kind, *duration)
		return
	}

	P.Start("💥", fmt.Sprintf("注入混沌（%s → %s，持续 %s）", *kind, *service, *duration))

	// 调用 Chaos Mesh API
	uid, err := createChaosExperiment(*addr, expConfig)
	if err != nil {
		P.Fail(fmt.Sprintf("注入失败: %v", err))
		fmt.Printf("\n%s 确认 Chaos Mesh 是否运行：\n", colorize(colorYellow, "💡"))
		fmt.Printf("  kubectl port-forward -n chaos-mesh svc/chaos-dashboard 2333:2333\n")
		os.Exit(1)
	}

	P.Done(fmt.Sprintf("混沌已注入（uid: %s）", uid))
	fmt.Printf("\n  停止：kp chaos stop --uid %s\n", uid)
	fmt.Printf("  监控：kp chaos status --uid %s\n\n", uid)
}

func runChaosList(args []string) {
	flags := flag.NewFlagSet("chaos list", flag.ExitOnError)
	addr := flags.String("chaos-mesh", defaultChaosMeshAddr, "Chaos Mesh API 地址")
	namespace := flags.String("namespace", "", "kubernetes namespace")
	flags.Parse(args)

	root, _ := Root()
	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	ns := *namespace
	if ns == "" {
		ns = envOrDefault(env, "KUBE_NAMESPACE", "default")
	}

	url := fmt.Sprintf("%s/api/v1/experiments?namespace=%s", *addr, ns)
	out, err := runOutput("curl", "-sf", url)
	if err != nil {
		P.Fail("无法连接 Chaos Mesh，确认 port-forward 是否运行")
		os.Exit(1)
	}

	var exps []map[string]interface{}
	if err := json.Unmarshal(out, &exps); err != nil || len(exps) == 0 {
		P.Info("✅", "当前无混沌实验")
		return
	}

	fmt.Printf("  %-36s  %-15s  %-12s  %s\n", "UID", "类型", "状态", "名称")
	fmt.Printf("  %s\n", strings.Repeat("─", 80))
	for _, e := range exps {
		uid, _ := e["uid"].(string)
		kind, _ := e["kind"].(string)
		status, _ := e["status"].(string)
		name, _ := e["name"].(string)
		fmt.Printf("  %-36s  %-15s  %-12s  %s\n", uid, kind, status, name)
	}
}

func runChaosStop(args []string) {
	flags := flag.NewFlagSet("chaos stop", flag.ExitOnError)
	uid := flags.String("uid", "", "实验 UID（必填）")
	namespace := flags.String("namespace", "", "kubernetes namespace")
	addr := flags.String("chaos-mesh", defaultChaosMeshAddr, "Chaos Mesh API 地址")
	flags.Parse(args)

	if *uid == "" {
		fmt.Fprintln(os.Stderr, "❌ --uid 必填，运行 kp chaos list 查看")
		os.Exit(1)
	}

	// 解析 namespace（对齐 runChaosInject 第 64-69 行模式）
	root, _ := Root()
	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	ns := *namespace
	if ns == "" {
		ns = envOrDefault(env, "KUBE_NAMESPACE", "default")
	}

	// v3.0 H1: RBAC 检查
	mustCheck(audit.ResolveActor(), ns, rbac.PermChaos)

	P.Start("⏹ ", fmt.Sprintf("停止混沌实验（uid: %s）", *uid))
	url := fmt.Sprintf("%s/api/v1/experiments/%s", *addr, *uid)
	out, err := runOutput("curl", "-sf", "-X", "DELETE", url)
	if err != nil {
		P.Fail(fmt.Sprintf("停止失败: %v\n%s", err, string(out)))
		os.Exit(1)
	}
	P.Done("混沌实验已停止")
}

func runChaosStatus(args []string) {
	flags := flag.NewFlagSet("chaos status", flag.ExitOnError)
	uid := flags.String("uid", "", "实验 UID（必填）")
	addr := flags.String("chaos-mesh", defaultChaosMeshAddr, "Chaos Mesh API 地址")
	flags.Parse(args)

	if *uid == "" {
		fmt.Fprintln(os.Stderr, "❌ --uid 必填")
		os.Exit(1)
	}

	url := fmt.Sprintf("%s/api/v1/experiments/%s", *addr, *uid)
	out, err := runOutput("curl", "-sf", url)
	if err != nil {
		P.Fail("查询失败")
		os.Exit(1)
	}

	var exp map[string]interface{}
	if err := json.Unmarshal(out, &exp); err != nil {
		fmt.Println(string(out))
		return
	}

	data, _ := json.MarshalIndent(exp, "  ", "  ")
	fmt.Printf("  %s\n", string(data))
}

// ── 实验配置构建 ──────────────────────────────────────────────────────────────

func buildPodKillConfig(name, namespace, service, duration string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "chaos-mesh.org/v1alpha1",
		"kind":       "PodChaos",
		"metadata":   map[string]string{"name": name, "namespace": namespace},
		"spec": map[string]interface{}{
			"action":   "pod-kill",
			"mode":     "one",
			"duration": duration,
			"selector": map[string]interface{}{
				"namespaces":     []string{namespace},
				"labelSelectors": map[string]string{"app": service},
			},
		},
	}
}

func buildNetworkDelayConfig(name, namespace, service, duration, latency string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "chaos-mesh.org/v1alpha1",
		"kind":       "NetworkChaos",
		"metadata":   map[string]string{"name": name, "namespace": namespace},
		"spec": map[string]interface{}{
			"action":   "delay",
			"mode":     "all",
			"duration": duration,
			"selector": map[string]interface{}{
				"namespaces":     []string{namespace},
				"labelSelectors": map[string]string{"app": service},
			},
			"delay": map[string]string{
				"latency":     latency,
				"correlation": "100",
				"jitter":      "0ms",
			},
		},
	}
}

func buildCPUStressConfig(name, namespace, service, duration string, workers int) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "chaos-mesh.org/v1alpha1",
		"kind":       "StressChaos",
		"metadata":   map[string]string{"name": name, "namespace": namespace},
		"spec": map[string]interface{}{
			"mode":     "all",
			"duration": duration,
			"selector": map[string]interface{}{
				"namespaces":     []string{namespace},
				"labelSelectors": map[string]string{"app": service},
			},
			"stressors": map[string]interface{}{
				"cpu": map[string]interface{}{
					"workers": workers,
					"load":    80,
				},
			},
		},
	}
}

func buildMemoryStressConfig(name, namespace, service, duration string, workers int) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "chaos-mesh.org/v1alpha1",
		"kind":       "StressChaos",
		"metadata":   map[string]string{"name": name, "namespace": namespace},
		"spec": map[string]interface{}{
			"mode":     "all",
			"duration": duration,
			"selector": map[string]interface{}{
				"namespaces":     []string{namespace},
				"labelSelectors": map[string]string{"app": service},
			},
			"stressors": map[string]interface{}{
				"memory": map[string]interface{}{
					"workers": workers,
					"size":    "256MB",
				},
			},
		},
	}
}

// createChaosExperiment 调用 Chaos Mesh API 创建实验
func createChaosExperiment(addr string, config map[string]interface{}) (string, error) {
	body, _ := json.Marshal(config)
	url := fmt.Sprintf("%s/api/v1/experiments", addr)

	out, err := runOutput("curl", "-sf",
		"-X", "POST",
		"-H", "Content-Type: application/json",
		"-d", string(body),
		url)
	if err != nil {
		return "", fmt.Errorf("API 请求失败: %w\n%s", err, string(out))
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}

	uid, _ := resp["uid"].(string)
	if uid == "" {
		return "", fmt.Errorf("响应中未找到 uid: %s", string(out))
	}
	return uid, nil
}

// 引用 bytes 包避免 unused import
var _ = bytes.NewBuffer
