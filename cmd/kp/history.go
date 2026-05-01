// cmd/kp/history.go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Ixecd/kubepivot/internal/state"
)

func runHistory(args []string) {
	flags := flag.NewFlagSet("history", flag.ExitOnError)
	namespace := flags.String("namespace", "", "kubernetes namespace")
	context := flags.String("context", "", "kubernetes context")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 文件路径")
	limit := flags.Int("n", 20, "显示最近 N 条，0 = 全部")
	export := flags.String("export", "", "导出格式：json | csv，留空只打印")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "解析参数失败:", err)
		os.Exit(1)
	}
	*kubeconfig = expandHome(*kubeconfig)

	root, err := Root()
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

	// --export 导出
	if *export != "" {
		exportHistory(history, *export, projectName, cfg.namespace)
	}

	if *limit > 0 && start > 0 {
		fmt.Printf("\n（共 %d 条，显示最近 %d 条，用 -n 0 查看全部）\n",
			len(sm.Record().History), *limit)
	} else {
		fmt.Printf("\n共 %d 条\n", len(history))
	}
}

// exportHistory 导出部署历史
func exportHistory(history []state.Transition, format, projectName, namespace string) {
	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("kp-history-%s-%s.%s", projectName, timestamp, format)

	switch format {
	case "json":
		data, err := json.MarshalIndent(map[string]interface{}{
			"project":   projectName,
			"namespace": namespace,
			"exported":  time.Now().Format(time.RFC3339),
			"history":   history,
		}, "", "  ")
		if err != nil {
			P.Fail(fmt.Sprintf("JSON 序列化失败: %v", err))
			return
		}
		if err := os.WriteFile(filename, data, 0o644); err != nil {
			P.Fail(fmt.Sprintf("写入失败: %v", err))
			return
		}

	case "csv":
		var b strings.Builder
		b.WriteString("序号,时间,从,到,版本,原因\n")
		for i, h := range history {
			b.WriteString(fmt.Sprintf("%d,%s,%s,%s,%s,%s\n",
				i+1,
				h.Timestamp.Format("2006-01-02 15:04:05"),
				h.From,
				h.To,
				h.Version,
				strings.ReplaceAll(h.Reason, ",", "，"), // 转义中文逗号
			))
		}
		if err := os.WriteFile(filename, []byte(b.String()), 0o644); err != nil {
			P.Fail(fmt.Sprintf("写入失败: %v", err))
			return
		}

	default:
		P.Fail(fmt.Sprintf("不支持的格式：%s（支持 json / csv）", format))
		return
	}

	P.Done(fmt.Sprintf("已导出 %d 条记录到 %s", len(history), filename))
}
