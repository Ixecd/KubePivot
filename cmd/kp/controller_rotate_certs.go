package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Ixecd/kubepivot/internal/audit"
	"github.com/Ixecd/kubepivot/internal/controller_installer"
	"github.com/Ixecd/kubepivot/internal/etcdmanager"
	"github.com/Ixecd/kubepivot/internal/executor"
	"github.com/Ixecd/kubepivot/internal/rbac"
)

func runControllerRotateCerts(args []string) {
	flags := flag.NewFlagSet("controller rotate-certs", flag.ExitOnError)
	namespace := flags.String("namespace", "kubepivot-system", "controller namespace")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	flags.Parse(args)

	mustCheck(audit.ResolveActor(), *namespace, rbac.PermControllerInstall)

	P.Info("🔄", "强制轮转 etcd TLS CA 证书")
	fmt.Println()
	fmt.Println("⚠️  这将导致以下影响：")
	fmt.Println("  1. 生成新的 CA 证书并覆盖 Secret kubepivot-etcd-certs")
	fmt.Println("  2. 滚动重启所有 Controller Pod（OrderedReady，逐个进行）")
	fmt.Println("  3. 重启期间可能出现短暂的集群不可用")
	fmt.Println()
	fmt.Print("确认轮转？(y/N): ")

	var input string
	fmt.Scanln(&input)
	if input != "y" && input != "Y" {
		P.Info("ℹ️ ", "已取消")
		return
	}

	ctx := context.Background()

	// Step 1: 强制更新 CA Secret
	cfg := etcdmanager.DefaultCertConfig()
	if err := etcdmanager.CreateCertsSecret(ctx, cfg, *namespace, true); err != nil {
		P.Fail(fmt.Sprintf("更新 CA 证书失败: %v", err))
		os.Exit(1)
	}
	P.Done("CA 证书已更新 → Secret kubepivot-etcd-certs")

	// Step 2: 滚动重启 StatefulSet
	P.Start("🔄", "滚动重启 Controller Pod")
	inst := controller_installer.New(controller_installer.Config{
		Namespace:  *namespace,
		Kubeconfig: *kubeconfig,
	})
	// kubectl rollout restart statefulset
	exec := executor.GetExecutor()
	if _, err := exec.Kubectl(ctx, *kubeconfig,
		"rollout", "restart", "statefulset/kubepivot-controller",
		"-n", *namespace,
	); err != nil {
		P.Fail(fmt.Sprintf("重启失败: %v", err))
		os.Exit(1)
	}

	// Step 3: 等待就绪
	_ = inst.WaitReady(ctx, 300*time.Second)
	P.Done("Controller Pod 已就绪，使用新 CA 证书")

	fmt.Println()
	P.Info("✅", "证书轮转完成")
}
