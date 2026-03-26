package controller

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/Ixecd/dev-toolkit/internal/state"
)

func Start() {
	slog.Info("🚀 controller 已启动（Reconciliation Loop）")

	project := getenv("PROJECT_NAME", "web3-blitz")
	namespace := getenv("KUBE_NAMESPACE", project)
	version := getenv("VERSION", "latest")
	kubeconfig := getenv("KUBE_CONFIG", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := state.NewAutoStore(os.Getenv("ETCD_ENDPOINTS"))
	sm, err := state.New(store, project, namespace, version)
	if err != nil {
		slog.Error("状态机初始化失败", "err", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup
	wg.Add(1)

	reconciler := NewReconciler(sm, kubeconfig)
	go reconciler.Start(ctx, &wg)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("controller 正在优雅关闭...")
	cancel()
	wg.Wait()
	slog.Info("controller 已退出")
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
