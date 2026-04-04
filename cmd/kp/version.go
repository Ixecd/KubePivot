package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// kpVersion 当前版本，由 kp release 自动更新
const kpVersion = "v1.9.0"

func runVersion() {
	fmt.Printf("kp version %s\n", kpVersion)
	fmt.Printf("  module:  github.com/Ixecd/kubepivot\n")
	fmt.Printf("  go:      %s\n", goVersion())
}

func goVersion() string {
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		return "unknown"
	}
	fields := strings.Fields(string(out))
	if len(fields) >= 3 {
		return fields[2]
	}
	return strings.TrimSpace(string(out))
}

func runSelfUpdate(args []string) {
	P.Info("🔍", fmt.Sprintf("当前版本: %s", kpVersion))

	// 从 GitHub releases API 获取最新版本
	P.Start("📡", "检查最新版本...")
	latest, err := fetchLatestVersion()
	if err != nil {
		P.Fail(fmt.Sprintf("检查失败: %v", err))
		fmt.Printf("\n%s 手动更新：go install github.com/Ixecd/kubepivot/cmd/kp@latest\n",
			colorize(colorYellow, "💡"))
		os.Exit(1)
	}
	P.Done(fmt.Sprintf("最新版本: %s", latest))

	if latest == kpVersion {
		P.Info("✅", "已是最新版本，无需更新")
		return
	}

	fmt.Printf("\n  %s → %s\n\n", colorize(colorGray, kpVersion), colorize(colorGreen, latest))
	P.Start("⬇️ ", fmt.Sprintf("更新到 %s", latest))

	// go install 安装最新版本
	cmd := exec.Command("go", "install",
		fmt.Sprintf("github.com/Ixecd/kubepivot/cmd/kp@%s", latest))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		P.Fail(fmt.Sprintf("更新失败: %v", err))
		fmt.Printf("\n%s 手动更新：go install github.com/Ixecd/kubepivot/cmd/kp@%s\n",
			colorize(colorYellow, "💡"), latest)
		os.Exit(1)
	}

	P.Done(fmt.Sprintf("已更新到 %s，重新运行 kp 生效", latest))
}

// fetchLatestVersion 从 GitHub releases API 获取最新 tag
func fetchLatestVersion() (string, error) {
	url := "https://api.github.com/repos/Ixecd/KubePivot/releases/latest"
	out, err := exec.Command("curl", "-sf",
		"-H", "Accept: application/vnd.github.v3+json",
		url).Output()
	if err != nil {
		return "", fmt.Errorf("curl 请求失败: %w", err)
	}

	var resp struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}
	if resp.TagName == "" {
		return "", fmt.Errorf("未找到 release tag")
	}
	return resp.TagName, nil
}
