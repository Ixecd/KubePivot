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
