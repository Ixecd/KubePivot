package controller

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"

	"github.com/Ixecd/kubepivot/internal/config"
	"github.com/Ixecd/kubepivot/internal/etcdmanager"
	"github.com/Ixecd/kubepivot/internal/state"
)

// Start 启动 controller。
// 支持两种模式：
//   - per-project（默认，向后兼容 v2.2.0）：从 PROJECT_NAME/KUBE_NAMESPACE env 里拿单项目信息
//   - global（v2.3.0+，通过 --global flag 启用）：watch 所有带 kubepivot.io/managed=true 的 namespace
func Start(args ...string) {
	// 判断 --global flag
	global := false
	for _, a := range args {
		if a == "--global" {
			global = true
			break
		}
	}
	// 也支持环境变量（deployment.yaml 里用 KUBEPIVOT_MODE=global）
	if os.Getenv("KUBEPIVOT_MODE") == "global" {
		global = true
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		slog.Info("controller 正在优雅关闭...")
		cancel()
	}()

	if global {
		slog.Info("🌐 controller 启动（global 模式 v2.3.0）")

		// etcd compact/defrag 维护（v3.3: 接入 config 系统，替代硬编码）
		go startEtcdMaintenance(ctx)

		StartGlobal(ctx)
	} else {
		slog.Info("🚀 controller 启动（per-project 模式，向后兼容）")
		startPerProject(ctx)
	}

	slog.Info("controller 已退出")
}

// startPerProject v2.2.0 per-project 模式（保持不变）
func startPerProject(ctx context.Context) {
	project := getenv("PROJECT_NAME", "web3-blitz")
	namespace := getenv("KUBE_NAMESPACE", project)
	version := getenv("VERSION", "latest")
	kubeconfig := getenv("KUBE_CONFIG", "")
	etcdEPs := os.Getenv("ETCD_ENDPOINTS")

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
}

// startEtcdMaintenance 启动 etcd compact/defrag 维护循环。
//
// v3.3: 从 config 系统读 compact/defrag 间隔（替代硬编码 1h/24h）。
// 仅在 global 模式下运行（per-project 无 etcd 集群）。
// POD_INDEX 从 KUBEPIVOT_POD_INDEX 环境变量读取（StatefulSet 注入）。
func startEtcdMaintenance(ctx context.Context) {
	podIndex, _ := strconv.Atoi(os.Getenv("KUBEPIVOT_POD_INDEX"))
	cfg := config.Load()

	mgr := etcdmanager.NewEtcdManagerFromConfig(
		podIndex,
		cfg.Controller.Replicas,
		"kubepivot-system",
		cfg.Etcd.CompactInterval,
		cfg.Etcd.DefragInterval,
	)

	slog.Info("etcd maintenance 启动",
		"pod_index", podIndex,
		"compact_interval", cfg.Etcd.CompactInterval,
		"defrag_interval", cfg.Etcd.DefragInterval,
	)

	mgr.Run(ctx)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}