package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func runCompat(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "用法: kp compat <子命令>")
		fmt.Fprintln(os.Stderr, "  kp compat check   检测 API 破坏性变更")
		os.Exit(1)
	}
	switch args[0] {
	case "check":
		runCompatCheck(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n", args[0])
		os.Exit(1)
	}
}

func runCompatCheck(args []string) {
	flags := flag.NewFlagSet("compat check", flag.ExitOnError)
	baseFile := flags.String("base", "", "基准 API 文件（旧版本）")
	revFile := flags.String("revision", "", "对比 API 文件（新版本）")
	outputJSON := flags.Bool("output-json", false, "输出 JSON（CI/CD 集成）")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	// 检查 oasdiff 是否安装
	if _, err := runOutput("oasdiff", "--version"); err != nil {
		fmt.Fprintln(os.Stderr, "oasdiff 未安装，请运行: brew install oasdiff")
		fmt.Fprintln(os.Stderr, "或参考: https://github.com/oasdiff/oasdiff")
		os.Exit(1)
	}

	root, err := Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	// 自动探测 swagger 文件
	if *baseFile == "" || *revFile == "" {
		detected := findSwaggerFile(root)
		if detected == "" {
			fmt.Fprintln(os.Stderr, "未找到 swagger/openapi 文件，请通过 --base 和 --revision 指定")
			fmt.Fprintln(os.Stderr, "支持的路径：docs/swagger.yaml, api/openapi.yaml, internal/api/swagger.yaml")
			os.Exit(1)
		}
		// 需要两个文件做对比，如果只有一个，提示用户
		if *baseFile == "" && *revFile == "" {
			fmt.Fprintf(os.Stderr, "找到 API 文件：%s\n", detected)
			fmt.Fprintln(os.Stderr, "需要提供两个版本进行对比：")
			fmt.Fprintln(os.Stderr, "  kp compat check --base old/swagger.yaml --revision docs/swagger.yaml")
			os.Exit(1)
		}
		if *baseFile == "" {
			*baseFile = detected
		}
		if *revFile == "" {
			*revFile = detected
		}
	}

	P.Info("🔍", "检测 API 兼容性变更")
	fmt.Printf("  基准版本: %s\n", *baseFile)
	fmt.Printf("  对比版本: %s\n", *revFile)
	fmt.Println()

	// 调用 oasdiff breaking
	var oasdiffArgs []string
	if *outputJSON {
		oasdiffArgs = []string{"oasdiff", "breaking", *baseFile, *revFile, "--format", "json"}
	} else {
		oasdiffArgs = []string{"oasdiff", "breaking", *baseFile, *revFile}
	}

	out, err := runOutput(oasdiffArgs...)
	output := strings.TrimSpace(string(out))

	if err != nil {
		// oasdiff 发现破坏性变更时返回非零退出码
		if output != "" {
			P.Fail("发现 API 破坏性变更，部署可能影响现有客户端")
			fmt.Println()
			// 格式化输出
			for _, line := range strings.Split(output, "\n") {
				if line == "" {
					continue
				}
				fmt.Printf("    %s %s\n", colorize(colorRed, "❌"), line)
			}
			fmt.Println()
			fmt.Printf("%s 请更新 API 版本号或保持向后兼容后再部署\n",
				colorize(colorYellow, "💡"))
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "oasdiff 执行失败:", err)
		os.Exit(1)
	}

	if output == "" {
		P.Info("✅", "未发现 API 破坏性变更，向后兼容")
	} else {
		// 有输出但退出码为 0（警告级别）
		P.Info("⚠️ ", "发现 API 变更（非破坏性）")
		for _, line := range strings.Split(output, "\n") {
			if line != "" {
				fmt.Printf("    %s %s\n", colorize(colorYellow, "⚠️ "), line)
			}
		}
	}
}

// findSwaggerFile 自动探测 swagger/openapi 文件
func findSwaggerFile(root string) string {
	candidates := []string{
		filepath.Join(root, "docs", "swagger.yaml"),
		filepath.Join(root, "docs", "swagger.json"),
		filepath.Join(root, "docs", "openapi.yaml"),
		filepath.Join(root, "api", "openapi.yaml"),
		filepath.Join(root, "api", "swagger.yaml"),
		filepath.Join(root, "internal", "api", "swagger.yaml"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}