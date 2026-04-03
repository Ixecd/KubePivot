package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/planner"
)

// runPreviewGen 生成 Header-based Preview 路由模板
// 在 kp deploy --preview 时调用，不自动 apply
func runPreviewGen(cfg *deployConfig, plan planner.Plan, root, projectName, slot string) {
	outDir := filepath.Join(root, "deployments", projectName, "preview")
	os.MkdirAll(outDir, 0o755)

	// 检测流量管理层
	trafficLayer := detectTrafficLayer(cfg)

	switch trafficLayer {
	case "istio":
		generateIstioVirtualService(cfg, plan, root, projectName, slot, outDir)
	case "nginx":
		generateNginxIngressCanary(cfg, plan, root, projectName, slot, outDir)
	default:
		generatePreviewReadme(plan, slot, outDir)
	}
}

// detectTrafficLayer 检测当前集群使用的流量管理层
func detectTrafficLayer(cfg *deployConfig) string {
	args := kubectlBaseArgs(cfg.kubeconfig, cfg.context, "")

	// 检测 Istio
	istioArgs := append(args, "get", "crd",
		"virtualservices.networking.istio.io",
		"--ignore-not-found", "-o", "name")
	if out, err := runOutput(istioArgs...); err == nil &&
		strings.TrimSpace(string(out)) != "" {
		return "istio"
	}

	// 检测 Nginx Ingress
	nginxArgs := append(args, "get", "ingressclass",
		"--ignore-not-found", "-o", "name")
	if out, err := runOutput(nginxArgs...); err == nil &&
		strings.TrimSpace(string(out)) != "" {
		return "nginx"
	}

	return "none"
}

// generateIstioVirtualService 生成 Istio VirtualService Header 路由模板
func generateIstioVirtualService(cfg *deployConfig, plan planner.Plan,
	root, projectName, slot, outDir string) {

	inactiveSlot := slot
	activeSlot := "blue"
	if slot == "blue" {
		activeSlot = "green"
	}

	yaml := fmt.Sprintf(`# 自动生成 by kp deploy --preview
# ⚠️  请审查后手动 kubectl apply，kp 不会自动应用此文件
# 作用：x-kp-preview: %s 的请求路由到 %s slot
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: %s-preview
  namespace: %s
spec:
  hosts:
  - %s
  http:
  # Header 路由：Preview 流量 → inactive slot
  - match:
    - headers:
        x-kp-preview:
          exact: "%s"
    route:
    - destination:
        host: %s-%s-%s
        port:
          number: %d
  # 默认流量 → active slot
  - route:
    - destination:
        host: %s-%s-%s
        port:
          number: %d
      weight: 100
`, inactiveSlot, inactiveSlot,
		plan.Name, cfg.namespace, plan.Name,
		inactiveSlot,
		projectName, plan.Name, inactiveSlot, plan.Port,
		projectName, plan.Name, activeSlot, plan.Port)

	filename := fmt.Sprintf("virtualservice-%s-preview.yaml", plan.Name)
	outPath := filepath.Join(outDir, filename)
	P.Start("📝", fmt.Sprintf("生成 Istio VirtualService 模板：%s", filename))
	if err := os.WriteFile(outPath, []byte(yaml), 0o644); err != nil {
		P.Fail(fmt.Sprintf("写入失败: %v", err))
		return
	}
	P.Done(fmt.Sprintf("生成 %s", outPath))
	fmt.Printf("\n%s 审查后手动执行：\n", colorize(colorYellow, "💡"))
	fmt.Printf("  kubectl apply -f %s\n", outPath)
	fmt.Printf("  # 验证：curl -H 'x-kp-preview: %s' http://%s/healthz\n",
		inactiveSlot, plan.Name)
	fmt.Printf("  # 确认后：kp promote --service %s\n\n", plan.Name)
}

// generateNginxIngressCanary 生成 Nginx Ingress canary 注解模板
func generateNginxIngressCanary(cfg *deployConfig, plan planner.Plan,
	root, projectName, slot, outDir string) {

	yaml := fmt.Sprintf(`# 自动生成 by kp deploy --preview
# ⚠️  请审查后手动 kubectl apply，kp 不会自动应用此文件
# 作用：x-kp-preview: %s 的请求路由到 %s slot
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: %s-preview-canary
  namespace: %s
  annotations:
    nginx.ingress.kubernetes.io/canary: "true"
    nginx.ingress.kubernetes.io/canary-by-header: "x-kp-preview"
    nginx.ingress.kubernetes.io/canary-by-header-value: "%s"
spec:
  rules:
  - http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: %s-%s-%s
            port:
              number: %d
`, slot, slot,
		plan.Name, cfg.namespace, slot,
		projectName, plan.Name, slot, plan.Port)

	filename := fmt.Sprintf("ingress-%s-preview-canary.yaml", plan.Name)
	outPath := filepath.Join(outDir, filename)
	P.Start("📝", fmt.Sprintf("生成 Nginx Ingress Canary 模板：%s", filename))
	if err := os.WriteFile(outPath, []byte(yaml), 0o644); err != nil {
		P.Fail(fmt.Sprintf("写入失败: %v", err))
		return
	}
	P.Done(fmt.Sprintf("生成 %s", outPath))
	fmt.Printf("\n%s 审查后手动执行：\n", colorize(colorYellow, "💡"))
	fmt.Printf("  kubectl apply -f %s\n", outPath)
	fmt.Printf("  # 验证：curl -H 'x-kp-preview: %s' http://<ingress-ip>/healthz\n\n", slot)
}

// generatePreviewReadme 无流量管理层时生成使用指引
func generatePreviewReadme(plan planner.Plan, slot, outDir string) {
	content := fmt.Sprintf(`# Preview 路由指引 — %s (%s slot)

生成时间：%s

## 当前环境

未检测到 Istio 或 Nginx Ingress，无法自动生成 Header 路由模板。

## 手动配置选项

### 方案 A：安装 Nginx Ingress Controller

	kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-v1.10.0/deploy/static/provider/cloud/deploy.yaml
	kp deploy --preview   # 重新运行，将自动生成 Ingress canary 模板

### 方案 B：安装 Istio

	curl -L https://istio.io/downloadIstio | sh -
	istioctl install --set profile=minimal
	kp deploy --preview   # 重新运行，将自动生成 VirtualService 模板

### 方案 C：直接 port-forward 验证（本地开发用）

	kubectl port-forward -n <namespace> \
	  deployment/%s-%s-%s <local-port>:%d
	curl http://localhost:<local-port>/healthz

## 确认后切换流量

	kp promote --service %s
`, plan.Name, slot, time.Now().Format("2006-01-02 15:04:05"),
		"<project>", plan.Name, slot, plan.Port, plan.Name)

	outPath := filepath.Join(outDir, fmt.Sprintf("PREVIEW-%s.md", plan.Name))
	P.Info("📝", fmt.Sprintf("生成 Preview 指引：%s", outPath))
	os.WriteFile(outPath, []byte(content), 0o644)

	fmt.Printf("\n%s 未检测到 Istio / Nginx Ingress\n", colorize(colorYellow, "⚠️ "))
	fmt.Printf("  已生成使用指引：%s\n", outPath)
	fmt.Printf("  本地验证：kubectl port-forward deployment/%s-%s-%s <port>:%d\n\n",
		"<project>", plan.Name, slot, plan.Port)
}

// runWarmup kp warmup --steps 10,50,100 --interval 2m,5m
func runWarmup(args []string) {
	flags := flag.NewFlagSet("warmup", flag.ExitOnError)
	namespace := flags.String("namespace", "", "kubernetes namespace")
	context := flags.String("context", "", "kubernetes context")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	service := flags.String("service", "", "服务名（必填）")
	steps := flags.String("steps", "10,50,100", "流量权重步骤（逗号分隔，如 10,50,100）")
	intervals := flags.String("interval", "2m,5m", "每步等待时间（逗号分隔，如 2m,5m）")
	errThreshold := flags.Float64("err-threshold", 0.01, "错误率阈值（超过则自动回滚，默认 1%）")
	dryRun := flags.Bool("dry-run", false, "只打印计划，不执行")
	flags.Parse(args)

	if *service == "" {
		fmt.Fprintln(os.Stderr, "❌ --service 必填")
		os.Exit(1)
	}

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

	// 解析 steps 和 intervals
	stepList := parseIntList(*steps)
	intervalList := parseDurationList(*intervals)

	if *dryRun {
		fmt.Printf("📋 Warmup 计划（dry-run）\n\n")
		fmt.Printf("  服务:         %s\n", *service)
		fmt.Printf("  错误率阈值:   %.1f%%\n\n", *errThreshold*100)
		for i, step := range stepList {
			wait := time.Duration(0)
			if i < len(intervalList) {
				wait = intervalList[i]
			}
			fmt.Printf("  步骤 %d:  inactive slot %d%%  →  等待 %s  →  检查 error rate\n",
				i+1, step, wait)
		}
		fmt.Printf("  步骤 %d:  kp promote（全量切换）\n\n", len(stepList)+1)
		fmt.Printf("  %s 任意步骤 error rate > %.1f%% 自动回滚流量\n",
			colorize(colorYellow, "⚠️ "), *errThreshold*100)
		return
	}

	// 检测流量管理层
	layer := detectTrafficLayer(cfg)
	if layer == "none" {
		P.Fail("未检测到 Istio 或 Nginx Ingress，warmup 需要流量管理层支持")
		fmt.Printf("  %s 可以用 kp promote 直接切换，或先安装 Istio/Nginx\n",
			colorize(colorYellow, "💡"))
		os.Exit(1)
	}

	P.Info("🌡️ ", fmt.Sprintf("开始 warmup（%s，%d 步）", layer, len(stepList)))

	for i, step := range stepList {
		wait := time.Duration(0)
		if i < len(intervalList) {
			wait = intervalList[i]
		}

		P.Start("⚖️ ", fmt.Sprintf("步骤 %d/%d：设置 inactive slot 权重 %d%%",
			i+1, len(stepList), step))

		if err := patchTrafficWeight(cfg, *service, step, layer); err != nil {
			P.Fail(fmt.Sprintf("设置权重失败: %v", err))
			os.Exit(1)
		}
		P.Done(fmt.Sprintf("权重已设置 %d%%", step))

		if wait > 0 {
			P.Info("⏳", fmt.Sprintf("等待 %s 后检查 error rate...", wait))
			time.Sleep(wait)
		}

		// 采样 error rate（有 Prometheus 才做）
		rate := sampleErrorRate(cfg, *service)
		if rate < 0 {
			P.Info("⏭ ", "无法获取 error rate（Prometheus 不可达），跳过检查")
			continue
		}

		if rate > *errThreshold {
			P.Fail(fmt.Sprintf("error rate %.2f%% > 阈值 %.2f%%，自动回滚流量",
				rate*100, *errThreshold*100))
			patchTrafficWeight(cfg, *service, 0, layer)
			os.Exit(1)
		}
		P.Info("✅", fmt.Sprintf("error rate %.2f%%，继续", rate*100))
	}

	P.Info("🎉", fmt.Sprintf("warmup 完成，运行 kp promote --service %s 全量切换", *service))
}

// patchTrafficWeight 调整 inactive slot 的流量权重
func patchTrafficWeight(cfg *deployConfig, service string, weight int, layer string) error {
	// TODO(v1.8.0)：实现 Istio VirtualService / Nginx Ingress weight patch
	// 当前版本：打印提示，实际 patch 留给用户
	P.Info("💡", fmt.Sprintf("请手动更新 %s 流量权重到 %d%%（%s）",
		service, weight, layer))
	return nil
}

// sampleErrorRate 从 Prometheus 采样错误率
func sampleErrorRate(cfg *deployConfig, service string) float64 {
	// TODO(v1.8.0)：实现 Prometheus 查询
	// 当前版本：返回 -1 表示不可用
	return -1
}

// parseIntList 解析逗号分隔的整数列表
func parseIntList(s string) []int {
	var result []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		var v int
		fmt.Sscanf(p, "%d", &v)
		if v > 0 {
			result = append(result, v)
		}
	}
	return result
}

// parseDurationList 解析逗号分隔的时间列表
func parseDurationList(s string) []time.Duration {
	var result []time.Duration
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if d, err := time.ParseDuration(p); err == nil {
			result = append(result, d)
		}
	}
	return result
}
