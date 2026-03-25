package controller

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Ixecd/dev-toolkit/internal/state"
)

func Start() {
	log.Println("🚀 web3-blitz-controller 已启动（Reconciliation Loop）")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 加载状态机
	store := state.NewAutoStore(os.Getenv("ETCD_ENDPOINTS"))
	sm, err := state.New(store, "web3-blitz", "web3-blitz", os.Getenv("VERSION"))
	if err != nil {
		log.Fatal("状态机初始化失败:", err)
	}

	// 启动 Reconciliation Loop
	reconciler := NewReconciler(sm)
	go reconciler.Start(ctx)

	// 优雅退出
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("⛔ controller 正在优雅关闭...")
	cancel()
	time.Sleep(2 * time.Second)
}
