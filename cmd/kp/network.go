package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ixecd/kubepivot/internal/planner"
)

type crossDep struct {
	service string
	fromNs  string
	fromSvc string
}

func runNetwork(args []string) {
	if len(args) == 0 {
		fmt.Println("用法: kp network gen [--output <dir>]")
		os.Exit(1)
	}
	switch args[0] {
	case "gen":
		runNetworkGen(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n", args[0])
		os.Exit(1)
	}
}

func runNetworkGen(args []string) {
	flags := flag.NewFlagSet("network gen", flag.ExitOnError)
	output := flags.String("output", "", "输出目录（默认 deployments/<project>/network/）")
	flags.Parse(args)

	root, err := Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))

	components, err := planner.LoadComponents(
		filepath.Join(root, "configs", "components.yaml"),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "解析 components.yaml 失败:", err)
		os.Exit(1)
	}

	var deps []crossDep
	for _, c := range components {
		for _, d := range c.CrossNsDeps {
			parts := strings.SplitN(d, "/", 2)
			if len(parts) != 2 {
				continue
			}
			deps = append(deps, crossDep{
				service: c.Name,
				fromNs:  parts[0],
				fromSvc: parts[1],
			})
		}
	}

	if len(deps) == 0 {
		P.Info("✅", "未找到跨 namespace 依赖，无需生成 NetworkPolicy 模板")
		return
	}

	// 输出目录
	outDir := *output
	if outDir == "" {
		outDir = filepath.Join(root, "deployments", projectName, "network")
	}
	os.MkdirAll(outDir, 0o755)

	P.Info("🔧", fmt.Sprintf("生成跨 namespace NetworkPolicy 模板（共 %d 条依赖）", len(deps)))
	fmt.Println()

	// 按本服务分组生成
	grouped := make(map[string][]crossDep)
	for _, d := range deps {
		grouped[d.service] = append(grouped[d.service], d)
	}

	for svc, ds := range grouped {
		policy := buildNetworkPolicyYAML(svc, ds, projectName)
		filename := fmt.Sprintf("networkpolicy-%s-cross-ns.yaml", svc)
		outPath := filepath.Join(outDir, filename)

		P.Start("📝", fmt.Sprintf("生成 %s", filename))
		if err := os.WriteFile(outPath, []byte(policy), 0o644); err != nil {
			P.Fail(fmt.Sprintf("写入 %s 失败: %v", filename, err))
			continue
		}
		P.Done(fmt.Sprintf("生成 %s", outPath))
	}

	fmt.Println()
	fmt.Printf("%s 请检查以上文件后手动执行：\n", colorize(colorYellow, "💡"))
	fmt.Printf("  kubectl apply -f %s/\n", outDir)
	fmt.Printf("\n%s kp 不会自动 apply NetworkPolicy（只保护，不越权）\n",
		colorize(colorCyan, "ℹ️ "))
}

// buildNetworkPolicyYAML 生成允许跨 namespace 访问的 NetworkPolicy
func buildNetworkPolicyYAML(svc string, deps []crossDep, namespace string) string {
	// 按来源 namespace 去重
	nsSeen := make(map[string]bool)
	var ingressRules []string
	for _, d := range deps {
		if nsSeen[d.fromNs] {
			continue
		}
		nsSeen[d.fromNs] = true
		ingressRules = append(ingressRules, fmt.Sprintf(`  - from:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: %s
    # 允许来自 %s namespace 的入站流量（依赖: %s）`, d.fromNs, d.fromNs, d.fromSvc))
	}

	return fmt.Sprintf(`# 自动生成 by kp network gen
# ⚠️  请审查后手动 kubectl apply，kp 不会自动应用此文件
# 作用：允许 %s 被跨 namespace 服务访问
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: %s-allow-cross-ns
  namespace: %s
spec:
  podSelector:
    matchLabels:
      app: %s
  policyTypes:
    - Ingress
  ingress:
%s
`, svc, svc, namespace, svc, strings.Join(ingressRules, "\n"))
}
