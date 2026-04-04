package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

// kpPluginsDir 插件安装目录
func kpPluginsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kp", "plugins")
}

func runPlugin(args []string) {
	if len(args) == 0 {
		fmt.Println("用法: kp plugin <子命令>")
		fmt.Println("  install <name>[@version]  安装插件")
		fmt.Println("  list                      列出已安装插件")
		fmt.Println("  remove  <name>            删除插件")
		fmt.Println("  run     <name> [args...]  运行插件")
		os.Exit(1)
	}
	switch args[0] {
	case "install":
		runPluginInstall(args[1:])
	case "list", "ls":
		runPluginList()
	case "remove", "rm":
		runPluginRemove(args[1:])
	case "run":
		runPluginExec(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n", args[0])
		os.Exit(1)
	}
}

func runPluginInstall(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "用法: kp plugin install <name>[@version]")
		fmt.Fprintln(os.Stderr, "示例: kp plugin install kp-metrics")
		fmt.Fprintln(os.Stderr, "      kp plugin install kp-metrics@v1.0.0")
		os.Exit(1)
	}

	nameVer := args[0]
	name, version := parsePluginNameVersion(nameVer)

	if err := os.MkdirAll(kpPluginsDir(), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "创建插件目录失败:", err)
		os.Exit(1)
	}

	// 插件命名约定：GitHub repo = github.com/Ixecd/<name>
	// 或用户自定义：github.com/<owner>/<name>
	repo := fmt.Sprintf("github.com/Ixecd/%s", name)
	if strings.Contains(name, "/") {
		repo = "github.com/" + name
		parts := strings.Split(name, "/")
		name = parts[len(parts)-1]
	}

	tag := "latest"
	if version != "" {
		tag = version
	}

	P.Start("⬇️ ", fmt.Sprintf("安装插件 %s@%s", name, tag))

	// go install 安装插件到 GOPATH/bin
	installPath := fmt.Sprintf("%s/cmd/%s@%s", repo, name, tag)
	cmd := exec.Command("go", "install", installPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// 降级：尝试直接 install repo
		installPath = fmt.Sprintf("%s@%s", repo, tag)
		cmd = exec.Command("go", "install", installPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			P.Fail(fmt.Sprintf("安装失败: %v", err))
			os.Exit(1)
		}
	}

	// 在 ~/.kp/plugins/ 创建符号链接或包装脚本
	pluginBin := filepath.Join(kpPluginsDir(), name)
	goBin, _ := exec.LookPath(name)
	if goBin == "" {
		// 找不到，在 GOPATH/bin 里找
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			home, _ := os.UserHomeDir()
			gopath = filepath.Join(home, "go")
		}
		goBin = filepath.Join(gopath, "bin", name)
	}

	// 创建包装脚本
	wrapper := fmt.Sprintf("#!/bin/sh\nexec %s \"$@\"\n", goBin)
	if err := os.WriteFile(pluginBin, []byte(wrapper), 0o755); err != nil {
		P.Info("⚠️ ", fmt.Sprintf("创建插件入口失败（插件仍已安装在 GOPATH/bin）: %v", err))
	}

	P.Done(fmt.Sprintf("插件 %s 已安装，运行：kp %s", name, name))
}

func runPluginList() {
	entries, err := os.ReadDir(kpPluginsDir())
	if err != nil {
		fmt.Println("暂无已安装的插件，运行 kp plugin install 安装")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintf(w, "  %-20s  %s\n", "插件", "路径")
	fmt.Fprintf(w, "  %s\n", strings.Repeat("─", 50))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fmt.Fprintf(w, "  %-20s  %s\n", e.Name(),
			filepath.Join(kpPluginsDir(), e.Name()))
	}
	w.Flush()
}

func runPluginRemove(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "用法: kp plugin remove <name>")
		os.Exit(1)
	}
	path := filepath.Join(kpPluginsDir(), args[0])
	if err := os.Remove(path); err != nil {
		fmt.Fprintf(os.Stderr, "删除失败: %v\n", err)
		os.Exit(1)
	}
	P.Done(fmt.Sprintf("插件 %q 已删除", args[0]))
}

// runPluginExec 直接运行插件
func runPluginExec(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "用法: kp plugin run <name> [args...]")
		os.Exit(1)
	}
	execPlugin(args[0], args[1:])
}

// execPlugin 查找并执行插件，未找到时报错
func execPlugin(name string, args []string) {
	// 1. 在 ~/.kp/plugins/ 找
	pluginPath := filepath.Join(kpPluginsDir(), name)
	if _, err := os.Stat(pluginPath); err == nil {
		cmd := exec.Command(pluginPath, args...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			os.Exit(1)
		}
		return
	}

	// 2. 在 PATH 里找 kp-<name>
	if bin, err := exec.LookPath("kp-" + name); err == nil {
		cmd := exec.Command(bin, args...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			os.Exit(1)
		}
		return
	}

	fmt.Fprintf(os.Stderr, "未知命令: %s\n运行 kp plugin install %s 安装，或 kp --help 查看内置命令\n",
		name, name)
	os.Exit(1)
}

// parsePluginNameVersion 解析 name@version 格式
func parsePluginNameVersion(s string) (name, version string) {
	parts := strings.SplitN(s, "@", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return s, ""
}
