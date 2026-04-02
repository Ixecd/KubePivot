package controller

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/Ixecd/kubepivot/internal/state"
)

func Start() {
	slog.Info("🚀 controller 已启动（Reconciliation Loop）")

	project    := getenv("PROJECT_NAME", "web3-blitz")
	namespace  := getenv("KUBE_NAMESPACE", project)
	version    := getenv("VERSION", "latest")
	kubeconfig := getenv("KUBE_CONFIG", "")
	etcdEPs    := os.Getenv("ETCD_ENDPOINTS")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		slog.Info("controller 正在优雅关闭...")
		cancel()
	}()

	// Leader Election：有 etcd 则多副本 HA，无 etcd 则单机运行
	RunWithLeaderElection(ctx, etcdEPs, project, namespace, func(leaderCtx context.Context) {
		store := state.NewAutoStore(etcdEPs)
		sm, err := state.New(store, project, namespace, version)
		if err != nil {
			slog.Error("状态机初始化失败", "err", err)
			return
		}

		var wg sync.WaitGroup
		wg.Add(1)
		reconciler := NewReconciler(sm, kubeconfig)
		reconciler.Start(leaderCtx, &wg)
		wg.Wait()
	})

	slog.Info("controller 已退出")
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
