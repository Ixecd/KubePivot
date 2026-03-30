// cmd/dtk/history.go
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"strings"

	"github.com/Ixecd/kubepivot/internal/state"
)

func runHistory(args []string) {
	flags := flag.NewFlagSet("history", flag.ExitOnError)
	namespace := flags.String("namespace", "", "kubernetes namespace")
	context := flags.String("context", "", "kubernetes context")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 文件路径")
	limit := flags.Int("n", 20, "显示最近 N 条，0 = 全部")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "解析参数失败:", err)
		os.Exit(1)
	}
	*kubeconfig = expandHome(*kubeconfig)

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

	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))
	version := envOrDefault(env, "VERSION", "v0.1.0")

	store := state.NewAutoStore(env["ETCD_ENDPOINTS"])
	sm, err := state.New(store, projectName, cfg.namespace, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载状态失败:", err)
		os.Exit(1)
	}

	history := sm.Record().History
	if len(history) == 0 {
		fmt.Println("暂无部署历史")
		return
	}

	// 取最近 N 条
	start := 0
	if *limit > 0 && len(history) > *limit {
		start = len(history) - *limit
	}
	history = history[start:]

	fmt.Printf("项目: %s  命名空间: %s\n\n", projectName, cfg.namespace)

	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintf(w, "  #\t时间\t从\t→\t到\t版本\t原因\n")
	fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\n",
		strings.Repeat("-", 3),
		strings.Repeat("-", 19),
		strings.Repeat("-", 14),
		strings.Repeat("-", 1),
		strings.Repeat("-", 14),
		strings.Repeat("-", 8),
		strings.Repeat("-", 20),
	)

	for i, h := range history {
		fmt.Fprintf(w, "  %d\t%s\t%s\t→\t%s\t%s\t%s\n",
			start+i+1,
			h.Timestamp.Format("2006-01-02 15:04:05"),
			h.From,
			h.To,
			h.Version,
			h.Reason,
		)
	}
	w.Flush()

	if *limit > 0 && start > 0 {
		fmt.Printf("\n（共 %d 条，显示最近 %d 条，用 -n 0 查看全部）\n",
			len(sm.Record().History), *limit)
	} else {
		fmt.Printf("\n共 %d 条\n", len(history))
	}
}