package controller

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Ixecd/dev-toolkit/internal/state"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func (r *Reconciler) startEtcdWatcher(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	endpoints := os.Getenv("ETCD_ENDPOINTS")
	if endpoints == "" {
		slog.Warn("ETCD_ENDPOINTS 未配置，跳过 etcd Watch，只依赖定时对账")
		return
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   strings.Split(endpoints, ","),
		DialTimeout: 3 * time.Second,
	})
	if err != nil {
		slog.Warn("etcd Watch 启动失败，只依赖定时对账", "err", err)
		return
	}
	defer cli.Close()

	key := state.EtcdKey(
		getenv("PROJECT_NAME", "web3-blitz"),
		getenv("KUBE_NAMESPACE", "web3-blitz"),
	)

	slog.Info("etcd Watch 已启动", "key", key)
	watchChan := cli.Watch(ctx, key, clientv3.WithPrefix())

	for {
		select {
		case <-ctx.Done():
			return
		case watchResp, ok := <-watchChan:
			if !ok {
				slog.Warn("etcd Watch channel 已关闭")
				return
			}
			if watchResp.Err() != nil {
				slog.Error("etcd Watch 错误", "err", watchResp.Err())
				continue
			}
			slog.Debug("etcd 状态变更，立即触发 Reconcile")
			r.reconcile()
		}
	}
}
