package main

import (
	"fmt"
	"strings"
	"time"
)

type timingRow struct {
	name    string
	timing  deployTiming
	total   time.Duration
}

// printTimingTable 输出部署耗时统计表格
func printTimingTable(rows []timingRow) {
	if len(rows) == 0 {
		return
	}

	fmt.Println()
	fmt.Printf("  %s\n", colorize(colorCyan, "部署耗时统计"))
	fmt.Printf("  %-25s  %-8s  %-8s  %-8s  %-8s  %s\n",
		"服务", "build", "push", "helm", "rollout", "总计")
	fmt.Printf("  %s\n", strings.Repeat("─", 75))

	for _, r := range rows {
		fmt.Printf("  %-25s  %-8s  %-8s  %-8s  %-8s  %s\n",
			r.name,
			fmtDuration(r.timing.build),
			fmtDuration(r.timing.push),
			fmtDuration(r.timing.helm),
			fmtDuration(r.timing.rollout),
			colorize(colorGreen, fmtDuration(r.total)),
		)
	}
}

// fmtDuration 格式化耗时，0 显示为 -
func fmtDuration(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	if d < time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
