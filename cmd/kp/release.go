package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var semverPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

func runRelease(args []string) {
	flags := flag.NewFlagSet("release", flag.ExitOnError)
	version := flags.String("version", "", "版本号，格式：v{major}.{minor}.{patch}，例如 v1.2.3")
	deploy := flags.Bool("deploy", false, "打完 tag 后自动触发 kp deploy")

	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "解析参数失败:", err)
		os.Exit(1)
	}

	if *version == "" {
		fmt.Fprintln(os.Stderr, "缺少 --version，例如：kp release --version v1.0.0")
		os.Exit(1)
	}

	if !semverPattern.MatchString(*version) {
		fmt.Fprintf(os.Stderr, "版本号格式错误：%q\n必须符合 v{major}.{minor}.{patch}，例如 v1.2.3\n", *version)
		os.Exit(1)
	}

	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	// 1. 检查工作区是否干净
	if err := checkCleanWorkspace(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 2. 检查 tag 是否已存在
	if tagExists(root, *version) {
		fmt.Fprintf(os.Stderr, "tag %s 已存在，请使用其他版本号\n", *version)
		os.Exit(1)
	}

	// 3. 更新 project.env
	envPath := filepath.Join(root, "configs", "project.env")
	if err := updateVersion(envPath, *version); err != nil {
		fmt.Fprintln(os.Stderr, "更新 project.env 失败:", err)
		os.Exit(1)
	}
	fmt.Printf("✅ 已更新 configs/project.env → VERSION=%s\n", *version)

	// 4. git add + commit
	if err := runInProject(root, "git", "add", "configs/project.env"); err != nil {
		fmt.Fprintln(os.Stderr, "git add 失败:", err)
		os.Exit(1)
	}
	commitMsg := fmt.Sprintf("chore: release %s", *version)
	if err := runInProject(root, "git", "commit", "-m", commitMsg); err != nil {
		fmt.Fprintln(os.Stderr, "git commit 失败:", err)
		os.Exit(1)
	}
	fmt.Printf("✅ 已提交：%s\n", commitMsg)

	// 5. git tag
	tagMsg := fmt.Sprintf("release %s", *version)
	if err := runInProject(root, "git", "tag", "-a", *version, "-m", tagMsg); err != nil {
		fmt.Fprintln(os.Stderr, "git tag 失败:", err)
		os.Exit(1)
	}
	fmt.Printf("✅ 已打 tag：%s\n", *version)

	// 6. git push

	if err := runInProject(root, "git", "push"); err != nil {
		fmt.Fprintln(os.Stderr, "git push 失败:", err)
		os.Exit(1)
	}
	if err := runInProject(root, "git", "push", "--tags"); err != nil {
		fmt.Fprintln(os.Stderr, "git push --tags 失败:", err)
		os.Exit(1)
	}
	fmt.Println("✅ 已推送 commit 和 tag 到远端")

	fmt.Printf("\n🎉 发布完成：%s\n", *version)

	// 7. 可选：触发部署
	if *deploy {
		fmt.Println("\n🚀 触发部署...")
		runDeploy([]string{})
	}
}

// checkCleanWorkspace 检查工作区是否干净（无未提交改动）
func checkCleanWorkspace(root string) error {
	out, err := runOutputInDir(root, "git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("检查工作区状态失败: %w", err)
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		return fmt.Errorf("工作区有未提交的改动，请先 commit 或 stash：\n%s", string(out))
	}
	return nil
}

// tagExists 检查 tag 是否已存在
func tagExists(root, version string) bool {
	out, err := runOutputInDir(root, "git", "tag", "-l", version)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == version
}

// updateVersion 更新 project.env 里的 VERSION 字段
func updateVersion(path, version string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取 project.env 失败: %w", err)
	}

	var lines []string
	updated := false
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "VERSION=") {
			lines = append(lines, "VERSION="+version)
			updated = true
		} else {
			lines = append(lines, line)
		}
	}

	if !updated {
		// 没有 VERSION 行，追加一行
		lines = append(lines, "VERSION="+version)
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// runInProject 在项目根目录执行命令，输出到终端
func runInProject(root string, name string, args ...string) error {
	return runCmd(root, nil, name, args...)
}

// runOutputInDir 在指定目录执行命令并返回输出
func runOutputInDir(dir string, cmdArgs ...string) ([]byte, error) {
	if len(cmdArgs) == 0 {
		return nil, fmt.Errorf("命令不能为空")
	}
	// 临时切换目录
	original, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if err := os.Chdir(dir); err != nil {
		return nil, err
	}
	defer os.Chdir(original)

	return runOutput(cmdArgs...)
}
