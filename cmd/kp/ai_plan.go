package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/ai"
)

func runAIPlan(args []string) {
	flags := flag.NewFlagSet("ai-plan", flag.ExitOnError)
	suggestOnly := flags.Bool("suggest-only", false, "只打印建议，不写入 components.yaml")
	desc := flags.String("desc", "", "补充描述（可选），如：高并发交易系统，峰值 QPS 5000")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "解析参数失败:", err)
		os.Exit(1)
	}

	root, err := Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	// 初始化 LLM 客户端
	client, err := ai.NewLLMClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, "初始化 LLM 客户端失败:", err)
		fmt.Fprintln(os.Stderr, "\n配置方式：")
		fmt.Fprintln(os.Stderr, "  export KP_LLM_API_KEY=your-api-key")
		fmt.Fprintln(os.Stderr, "  export KP_LLM_PROVIDER=grok  # grok / claude / openai / doubao")
		os.Exit(1)
	}

	// 扫描仓库
	P.Start("🔍", "扫描项目仓库")
	repoCtx, err := ai.ScanRepo(root, *desc)
	if err != nil {
		P.Fail("扫描仓库失败")
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	names := make([]string, len(repoCtx.Services))
	for i, s := range repoCtx.Services {
		names[i] = s.Name
	}
	P.Done(fmt.Sprintf("发现 %d 个服务：%s", len(repoCtx.Services), strings.Join(names, ", ")))

	// 调用 LLM
	P.Start("🧠", "LLM 分析中")
	prompt := ai.BuildPrompt(repoCtx)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	raw, err := client.Complete(ctx, prompt)
	if err != nil {
		P.Fail("LLM 调用失败")
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	P.Done("分析完成")

	// 解析输出
	plan, err := ai.ParsePlan(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "解析 LLM 输出失败:", err)
		fmt.Fprintln(os.Stderr, "原始输出:")
		fmt.Fprintln(os.Stderr, raw)
		os.Exit(1)
	}

	// 打印规划结果
	fmt.Println()
	fmt.Println("📋 AI 规划结果：")
	fmt.Println()
	for _, c := range plan.Components {
		fmt.Printf("  %-24s replicas=%-2d cpu=%-8s memory=%-8s",
			c.Name, c.Replicas, c.CPU, c.Memory)
		if c.Image == "" {
			fmt.Print("  (CLI 工具，跳过 build/push)")
		}
		fmt.Println()
	}
	fmt.Println()
	fmt.Printf("💡 分析依据：%s\n", plan.Reasoning)
	fmt.Println()

	if *suggestOnly {
		P.Info("ℹ️ ", "--suggest-only 模式，不写入文件")
		return
	}

	// 生成 YAML 内容
	yamlContent := ai.RenderComponentsYAML(plan)
	componentsPath := filepath.Join(root, "configs", "components.yaml")

	fmt.Println("生成的 components.yaml：")
	fmt.Println()
	fmt.Println(yamlContent)
	fmt.Println()

	if _, err := os.Stat(componentsPath); err == nil {
		fmt.Print("⚠️  configs/components.yaml 已存在，是否覆盖？(y/N): ")
	} else {
		fmt.Print("是否写入 configs/components.yaml？(y/N): ")
	}

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	if input != "y" {
		P.Info("ℹ️ ", "已取消，未写入文件")
		return
	}

	if err := os.WriteFile(componentsPath, []byte(yamlContent+"\n"), 0644); err != nil {
		fmt.Fprintln(os.Stderr, "写入 components.yaml 失败:", err)
		os.Exit(1)
	}

	P.Info("✅", "已写入 configs/components.yaml")
	fmt.Println()
	P.Info("💡", "下一步：")
	fmt.Println("  kp deploy            # 直接用 AI 规划部署")
	fmt.Println("  kp deploy --dry-run  # 先预览规划再部署")
}
