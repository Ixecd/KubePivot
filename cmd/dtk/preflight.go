package main

import (
	"fmt"
	"os"
	"os/exec"
)

type dep struct {
	bin        string
	installURL string
}

// deployDeps 是 dtk deploy 依赖的外部工具。
// 按实际调用顺序排列，方便用户一次性看完缺什么。
var deployDeps = []dep{
	{"docker", "https://docs.docker.com/engine/install/"},
	{"kubectl", "https://kubernetes.io/docs/tasks/tools/"},
	{"helm", "https://helm.sh/docs/intro/install/"},
}

// checkDeps 检查所有依赖工具是否可用。
// 全部缺失时一次性列出，避免用户装一个再发现下一个也缺。
func checkDeps(deps []dep) error {
	var missing []dep
	for _, d := range deps {
		if _, err := exec.LookPath(d.bin); err != nil {
			missing = append(missing, d)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	fmt.Fprintln(os.Stderr, "❌ 以下工具未安装或不在 PATH 中：")
	fmt.Fprintln(os.Stderr)
	for _, d := range missing {
		fmt.Fprintf(os.Stderr, "  %-10s  %s\n", d.bin, d.installURL)
	}
	fmt.Fprintln(os.Stderr)
	return fmt.Errorf("缺少必要工具，请安装后重试")
}
