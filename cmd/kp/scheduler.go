package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Ixecd/kubepivot/internal/audit"
	"github.com/Ixecd/kubepivot/internal/rbac"
	"github.com/Ixecd/kubepivot/internal/scheduler"
)

// ── 主入口 ────────────────────────────────────────────────────────────────────

func runSchedulerCmd(args []string) {
	if len(args) == 0 {
		printSchedulerUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "status":
		runSchedulerStatus(args[1:])
	case "reschedule":
		runSchedulerReschedule(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n", args[0])
		printSchedulerUsage()
		os.Exit(1)
	}
}

func printSchedulerUsage() {
	fmt.Println("用法: kp scheduler <子命令>")
	fmt.Println()
	fmt.Println("调度器管理：")
	fmt.Println("  kp scheduler status       查看调度器状态与指标")
	fmt.Println("  kp scheduler reschedule   手动触发一次重调度")
}

// clusterSummary 集群资源统计摘要（从 Node/Pod 列表计算，可独立测试）
type clusterSummary struct {
	NodeCount    int
	PodCount     int
	RunningCount int
	TotalCPU     int64 // 毫核
	TotalMemory  int64 // 字节
}

// computeClusterSummary 从 Node 和 Pod 列表计算集群摘要。
// 抽取为独立函数，便于单测覆盖统计逻辑。
func computeClusterSummary(nodes []*scheduler.NodeInfo, pods []*scheduler.PodInfo) clusterSummary {
	s := clusterSummary{NodeCount: len(nodes)}
	for _, p := range pods {
		s.PodCount++
		if p.Phase == "Running" {
			s.RunningCount++
		}
	}
	for _, n := range nodes {
		s.TotalCPU += n.AllocatableCPU
		s.TotalMemory += n.AllocatableMemory
	}
	return s
}

// ── kp scheduler status ──────────────────────────────────────────────────────

func runSchedulerStatus(args []string) {
	flags := flag.NewFlagSet("scheduler status", flag.ExitOnError)
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	mustCheck(audit.ResolveActor(), "", rbac.PermSizing)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	kc := expandHome(*kubeconfig)
	adapter := scheduler.NewKubectlAdapter(kc)

	nodes, err := adapter.ListAllNodes(ctx)
	if err != nil {
		P.Fail(fmt.Sprintf("读取节点失败: %v", err))
		os.Exit(1)
	}

	pods, err := adapter.ListAllPods(ctx)
	if err != nil {
		P.Fail(fmt.Sprintf("读取 Pod 失败: %v", err))
		os.Exit(1)
	}

	s := computeClusterSummary(nodes, pods)

	fmt.Println()
	fmt.Printf("%s 乾枢调度器状态\n", colorize(colorCyan, "⚙️"))
	fmt.Println()
	fmt.Printf("  %-20s %d 个\n", "Nodes:", s.NodeCount)
	fmt.Printf("  %-20s %d 个\n", "Pods (Total):", s.PodCount)
	fmt.Printf("  %-20s %d 个\n", "Pods (Running):", s.RunningCount)
	fmt.Printf("  %-20s %.1f cores\n", "集群总 CPU:", float64(s.TotalCPU)/1000)
	fmt.Printf("  %-20s %.1f GiB\n", "集群总 Memory:", float64(s.TotalMemory)/(1024*1024*1024))

	fmt.Println()
}

// ── kp scheduler reschedule ──────────────────────────────────────────────────

func runSchedulerReschedule(args []string) {
	flags := flag.NewFlagSet("scheduler reschedule", flag.ExitOnError)
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	maxMigrations := flags.Int("max-migrations", 5, "单次最大迁移数（0=默认 5%）")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	mustCheck(audit.ResolveActor(), "", rbac.PermSizing)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	kc := expandHome(*kubeconfig)
	adapter := scheduler.NewKubectlAdapter(kc)

	P.Start("🔍", "扫描集群利用率...")
	nodes, err := adapter.ListAllNodes(ctx)
	if err != nil {
		P.Fail(fmt.Sprintf("读取节点失败: %v", err))
		os.Exit(1)
	}
	pods, err := adapter.ListAllPods(ctx)
	if err != nil {
		P.Fail(fmt.Sprintf("读取 Pod 失败: %v", err))
		os.Exit(1)
	}

	P.Info("📊", fmt.Sprintf("读取到 %d 节点 + %d Pod", len(nodes), len(pods)))

	// 构造 Scheduler 运行 bin packing
	sizing := scheduler.NewSizingAdapter(kc, "")
	planWriter := scheduler.NewFilePlanWriter(".") // 写入当前目录
	sched := scheduler.NewScheduler(adapter, adapter, sizing, sizing, planWriter)

	P.Start("🧮", "运行 BinPack 调度...")
	plan, err := sched.Schedule(ctx)
	if err != nil {
		P.Fail(fmt.Sprintf("调度失败: %v", err))
		os.Exit(1)
	}

	fmt.Println()
	fmt.Printf("%s 调度结果\n", colorize(colorCyan, "📋"))
	fmt.Printf("  Pod 分配数: %d 个\n", len(plan.PodAssignments))
	if plan.Converged {
		fmt.Printf("  %s\n", colorize(colorGreen, "✓ 收敛"))
	} else {
		fmt.Printf("  %s\n", colorize(colorYellow, "⚠ 未收敛（可能需要更多迭代）"))
	}

	// 构造 Rescheduler 执行实际迁移（sched 实现 PodAssigner 接口）
	reschedCfg := scheduler.ReschedulerConfig{
		Interval:      5 * time.Minute,
		MaxMigrations: *maxMigrations,
	}
	resched := scheduler.NewRescheduler(sched, adapter, adapter, reschedCfg)

	P.Start("🔄", "执行重调度迁移...")
	resched.RunOnce(ctx)
	P.Done(fmt.Sprintf("重调度完成（最多 %d 次迁移）", *maxMigrations))

	fmt.Println()
}
