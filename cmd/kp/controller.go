package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Ixecd/kubepivot/internal/audit"
	"github.com/Ixecd/kubepivot/internal/rbac"
	"github.com/Ixecd/kubepivot/internal/controller_installer"
)

// ── 主入口 ────────────────────────────────────────────────────────────────────

func runController(args []string) {
	if len(args) == 0 {
		printControllerUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "install":
		runControllerInstall(args[1:])
	case "uninstall":
		runControllerUninstall(args[1:])
	case "status":
		runControllerStatus(args[1:])
	case "enroll":
		runControllerEnroll(args[1:])
	case "unenroll":
		runControllerUnenroll(args[1:])
	case "projects":
		runControllerProjects(args[1:])
	case "rotate-certs":
		runControllerRotateCerts(args[1:])
	case "update":
		runControllerUpdate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n", args[0])
		printControllerUsage()
		os.Exit(1)
	}
}

func printControllerUsage() {
	fmt.Println("用法: kp controller <子命令>")
	fmt.Println()
	fmt.Println("集群级管理：")
	fmt.Println("  kp controller install    [--namespace kubepivot-system] [--image xxx:tag] [--wait]")
	fmt.Println("  kp controller uninstall  [--force]")
	fmt.Println("  kp controller update     [--dry-run] [--apply] [--shards N] [--replicas N] [--force-downscale]")
	fmt.Println("  kp controller status")
	fmt.Println("  kp controller projects")
	fmt.Println()
	fmt.Println("项目接入：")
	fmt.Println("  kp controller enroll     [--resources configs/resources.yaml]")
	fmt.Println("  kp controller unenroll")
}

// ── kp controller install ────────────────────────────────────────────────────

func runControllerInstall(args []string) {
	flags := flag.NewFlagSet("controller install", flag.ExitOnError)
	namespace := flags.String("namespace", "kubepivot-system", "controller 部署 namespace")
	image := flags.String("image", "", "controller 镜像（默认 qingchun22/kubepivot-controller:<kpVersion>）")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	kubeContext := flags.String("context", "", "kube context")
	wait := flags.Bool("wait", true, "等待 Deployment ready")
	waitTimeout := flags.Duration("wait-timeout", 120*time.Second, "等待超时")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	// v2.8 B.7.2 + B.3 + B.7.3: controller install (PermControllerInstall)
	// Q-B7.12=A: namespace 用 *namespace 诚实反映实际操作 ns
	mustCheck(audit.ResolveActor(), *namespace, rbac.PermControllerInstall)

	// 自动从 kpVersion 推导镜像
	if *image == "" {
		*image = fmt.Sprintf("qingchun22/kubepivot-controller:%s", kpVersion)
	}

	P.Info("🔧", fmt.Sprintf("安装 KubePivot Controller → namespace=%s, image=%s", *namespace, *image))

	inst := controller_installer.New(controller_installer.Config{
		Namespace:  *namespace,
		Image:      *image,
		Kubeconfig: expandHome(*kubeconfig),
		Context:    *kubeContext,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := inst.Install(ctx); err != nil {
		P.Fail(fmt.Sprintf("安装失败: %v", err))
		os.Exit(1)
	}
	P.Done("Manifests apply 完成")

	if *wait {
		P.Start("⏳", "等待 Deployment ready")
		waitCtx, waitCancel := context.WithTimeout(context.Background(), *waitTimeout)
		defer waitCancel()
		if err := inst.WaitReady(waitCtx, *waitTimeout); err != nil {
			P.Fail(fmt.Sprintf("Deployment 未就绪: %v", err))
			os.Exit(1)
		}
		P.Done("Controller ready")
	}

	fmt.Println()
	fmt.Printf("%s 下一步：\n", colorize(colorCyan, "💡"))
	fmt.Printf("  1. 进入项目目录：cd myproject\n")
	fmt.Printf("  2. 接入到 controller：%s\n", colorize(colorGreen, "kp controller enroll"))
	fmt.Printf("  3. 查看状态：%s\n", colorize(colorGreen, "kp controller status"))
}

// ── kp controller uninstall ──────────────────────────────────────────────────

func runControllerUninstall(args []string) {
	flags := flag.NewFlagSet("controller uninstall", flag.ExitOnError)
	namespace := flags.String("namespace", "kubepivot-system", "controller 部署 namespace")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	force := flags.Bool("force", false, "跳过确认")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	// v2.8 B.7.2 + B.3 + B.7.3: controller uninstall (PermControllerUninstall)
	mustCheck(audit.ResolveActor(), *namespace, rbac.PermControllerUninstall)

	if !*force {
		fmt.Printf("%s 即将卸载 KubePivot Controller（namespace=%s）\n",
			colorize(colorYellow, "⚠️ "), *namespace)
		fmt.Printf("此操作会：\n")
		fmt.Printf("  1. 删除 namespace %s 及其下所有资源\n", *namespace)
		fmt.Printf("  2. 删除 ClusterRole / ClusterRoleBinding\n")
		fmt.Printf("  3. 不会动被管理项目的 namespace label（需手工 unenroll）\n")
		fmt.Printf("\n确认继续？[y/N]: ")
		var answer string
		fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" {
			fmt.Println("已取消")
			return
		}
	}

	inst := controller_installer.New(controller_installer.Config{
		Namespace:  *namespace,
		Kubeconfig: expandHome(*kubeconfig),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	P.Start("🗑", fmt.Sprintf("卸载 controller（namespace=%s）", *namespace))
	if err := inst.Uninstall(ctx); err != nil {
		P.Fail(fmt.Sprintf("卸载失败: %v", err))
		os.Exit(1)
	}
	P.Done("卸载完成")
}

// ── kp controller status ─────────────────────────────────────────────────────

func runControllerStatus(args []string) {
	flags := flag.NewFlagSet("controller status", flag.ExitOnError)
	namespace := flags.String("namespace", "kubepivot-system", "controller 部署 namespace")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	inst := controller_installer.New(controller_installer.Config{
		Namespace:  *namespace,
		Kubeconfig: expandHome(*kubeconfig),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	st, err := inst.Status(ctx)
	if err != nil {
		P.Fail(fmt.Sprintf("查询状态失败: %v", err))
		os.Exit(1)
	}

	fmt.Println()
	fmt.Printf("%s KubePivot Controller 状态\n", colorize(colorCyan, "🔱"))
	fmt.Println()
	if !st.Installed {
		fmt.Printf("  %s 未安装（运行 kp controller install）\n", colorize(colorRed, "✗"))
		return
	}
	fmt.Printf("  %s 已安装\n", colorize(colorGreen, "✓"))
	fmt.Printf("  %-20s %s\n", "Namespace:", st.Namespace)
	fmt.Printf("  %-20s %s\n", "Deployment Ready:", st.DeploymentReady)
	fmt.Printf("  %-20s %d 个\n", "Managed Projects:", st.ManagedNamespaces)
	fmt.Println()
}

func runControllerProjects(args []string) { runControllerProjectsReal(args) }

func runControllerEnroll(args []string)   { runControllerEnrollReal(args) }

func runControllerUnenroll(args []string) { runControllerUnenrollReal(args) }

// ── kp controller update ──────────────────────────────────────────────────────

func runControllerUpdate(args []string) {
	flags := flag.NewFlagSet("controller update", flag.ExitOnError)
	dryRun := flags.Bool("dry-run", true, "预览建议，不实际修改")
	apply := flags.Bool("apply", false, "实际应用推荐配置")
	shardsFlag := flags.Int("shards", 0, "手动指定分片数（跳过自动推导）")
	replicasFlag := flags.Int("replicas", 0, "手动指定副本数（跳过自动推导）")
	forceDownscale := flags.Bool("force-downscale", false, "允许推荐值低于当前值")
	namespace := flags.String("namespace", "kubepivot-system", "controller 部署 namespace")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	if *apply {
		*dryRun = false
	}

	// RBAC: controller update (PermControllerUpdate)
	mustCheck(audit.ResolveActor(), *namespace, rbac.PermControllerUpdate)

	inst := controller_installer.New(controller_installer.Config{
		Namespace:  *namespace,
		Kubeconfig: expandHome(*kubeconfig),
	})

	// 父 ctx 超时：apply 模式给 600s（WaitReady 自适应最长 10min），dry-run 给 60s
	parentTimeout := 60 * time.Second
	if *apply {
		parentTimeout = 600 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), parentTimeout)
	defer cancel()

	// 采集当前状态
	info, err := inst.GetSizingInfo(ctx)
	if err != nil {
		P.Fail(fmt.Sprintf("读取集群状态失败: %v", err))
		os.Exit(1)
	}

	// 手动指定时跳过算法
	var shards, replicas int
	var reason string
	var warnings []string

	if *shardsFlag > 0 && *replicasFlag > 0 {
		shards = *shardsFlag
		replicas = *replicasFlag
		reason = "手动指定分片数和副本数"
	} else if *shardsFlag > 0 {
		shards = *shardsFlag
		replicas, _, reason, warnings = controller_installer.Recommend(
			info.ProjectCount, info.CurrentShards, info.CurrentReplicas, *forceDownscale)
		// 手动指定 shards 时不替换
		reason = fmt.Sprintf("手动指定分片数 S=%d, C=%d 由算法推导", shards, replicas)
	} else if *replicasFlag > 0 {
		shards, _, reason, warnings = controller_installer.Recommend(
			info.ProjectCount, info.CurrentShards, info.CurrentReplicas, *forceDownscale)
		replicas = *replicasFlag
		reason = fmt.Sprintf("C=%d 手动指定, S=%d 由算法推导", replicas, shards)
	} else {
		shards, replicas, reason, warnings = controller_installer.Recommend(
			info.ProjectCount, info.CurrentShards, info.CurrentReplicas, *forceDownscale)
	}

	// dry-run 模式 — 展示 diff
	if *dryRun {
		printSizingDiff(info, shards, replicas, reason, warnings)
		return
	}

	// apply 模式
	P.Info("🔧", "应用推荐配置...")

	if err := inst.ApplySizing(ctx, info, shards, replicas); err != nil {
		P.Fail(fmt.Sprintf("应用失败: %v", err))
		os.Exit(1)
	}
	P.Done("Controller 规模调整完成")
}

func printSizingDiff(info *controller_installer.SizingInfo, shards, replicas int, reason string, warnings []string) {
	sep := fmt.Sprintf("%s", colorize(colorCyan, "─────────────────────────────────────────"))

	fmt.Println()
	fmt.Printf("%s KubePivot Controller 规模分析\n", colorize(colorCyan, "📊"))
	fmt.Println()

	// 当前状态
	fmt.Printf("  %s:\n", colorize(colorGray, "当前状态"))
	fmt.Printf("    %-22s %d\n", "Managed Projects:", info.ProjectCount)
	fmt.Printf("    %-22s %d\n", "Shards:", info.CurrentShards)
	fmt.Printf("    %-22s %d\n", "Controller Pods:", info.CurrentReplicas)
	fmt.Printf("    %-22s %.1f\n", "Projects/Shard:", float64(info.ProjectCount)/float64(max(info.CurrentShards, 1)))
	currQuota := float64(info.CurrentShards) / float64(max(info.CurrentReplicas, 1))
	fmt.Printf("    %-22s %.0f\n", "Shards/Pod (quota):", currQuota)
	fmt.Println()

	// 推荐配置
	fmt.Printf("  %s:\n", colorize(colorGreen, "推荐配置"))
	if shards != info.CurrentShards {
		fmt.Printf("    %-22s %s\n", "Shards:", fmt.Sprintf("%d → %d", info.CurrentShards, shards))
	} else {
		fmt.Printf("    %-22s %d (不变)\n", "Shards:", shards)
	}
	if replicas != info.CurrentReplicas {
		fmt.Printf("    %-22s %s\n", "Controller Pods:", fmt.Sprintf("%d → %d", info.CurrentReplicas, replicas))
	} else {
		fmt.Printf("    %-22s %d (不变)\n", "Controller Pods:", replicas)
	}
	newPS := float64(info.ProjectCount) / float64(max(shards, 1))
	fmt.Printf("    %-22s %.1f → %.1f\n", "Projects/Shard:", float64(info.ProjectCount)/float64(max(info.CurrentShards, 1)), newPS)
	newSC := float64(shards) / float64(max(replicas, 1))
	fmt.Printf("    %-22s %.0f → %.0f\n", "Shards/Pod (quota):", currQuota, newSC)

	// 压力变化趋势表
	fmt.Println()
	fmt.Printf("  %s:\n", colorize(colorGray, "压力变化趋势"))
	fmt.Println(sep)
	fmt.Printf("  %-22s %-10s %-10s %-8s\n", "维度", "当前值", "推荐值", "变化")
	fmt.Println(sep)
	fmt.Printf("  %-22s %-10.1f %-10.1f %-8s\n",
		"单分片承载 (P/S)",
		float64(info.ProjectCount)/float64(max(info.CurrentShards, 1)),
		newPS,
		trendArrow(info.CurrentShards, shards),
	)
	fmt.Printf("  %-22s %-10.1f %-10.1f %-8s\n",
		"单 Pod 承载 (S/C)",
		currQuota,
		newSC,
		trendArrow(info.CurrentReplicas, replicas),
	)
	fmt.Printf("  %-22s %-10d %-10d %-8s\n",
		"Lease 总数",
		info.CurrentShards,
		shards,
		pctChange(info.CurrentShards, shards),
	)
	fmt.Printf("  %-22s %-10d %-10d %-8s\n",
		"Controller Pod 开销",
		info.CurrentReplicas,
		replicas,
		pctChange(info.CurrentReplicas, replicas),
	)
	fmt.Println(sep)

	// 理由
	if reason != "" {
		fmt.Println()
		fmt.Printf("  %s\n", colorize(colorGray, "理由:"))
		fmt.Printf("  %s\n", reason)
	}

	// 警告
	if len(warnings) > 0 {
		fmt.Println()
		for _, w := range warnings {
			fmt.Printf("  %s\n", colorize(colorYellow, w))
		}
	}

	fmt.Println()
	fmt.Printf("%s 运行 %s 应用此推荐\n",
		colorize(colorGreen, "💡"),
		colorize(colorGreen, "kp controller update --apply"))
	fmt.Println()
}

func trendArrow(curr, rec int) string {
	if rec > curr {
		return "↑"
	} else if rec < curr {
		return "↓"
	}
	return "—"
}

func pctChange(curr, new int) string {
	if curr == 0 {
		return "—"
	}
	delta := float64(new-curr) / float64(curr) * 100
	if delta > 0 {
		return fmt.Sprintf("↑ %.0f%%", delta)
	} else if delta < 0 {
		return fmt.Sprintf("↓ %.0f%%", -delta)
	}
	return "—"
}
