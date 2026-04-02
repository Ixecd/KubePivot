package controller

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Ixecd/kubepivot/internal/state"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func (r *Reconciler) startEtcdWatcher(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	endpoints := os.Getenv("ETCD_ENDPOINTS")
	if endpoints == "" {
		slog.Warn("ETCD_ENDPOINTS 未配置，跳过 etcd Watch，只依赖定时对账")
		return
	}

	key := state.EtcdKey(
		getenv("PROJECT_NAME", "web3-blitz"),
		getenv("KUBE_NAMESPACE", "web3-blitz"),
	)

	const (
		initialDelay = 1 * time.Second
		maxDelay     = 30 * time.Second
	)

	delay := initialDelay

	for {
		// 检查 ctx 是否已取消
		select {
		case <-ctx.Done():
			slog.Info("etcd Watcher 已退出")
			return
		default:
		}

		cli, err := clientv3.New(clientv3.Config{
			Endpoints:   strings.Split(endpoints, ","),
			DialTimeout: 3 * time.Second,
		})
		if err != nil {
			slog.Warn("etcd 连接失败，等待重试",
				"delay", delay,
				"err", err,
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			delay = min(delay*2, maxDelay)
			continue
		}

		slog.Info("etcd Watch 已启动", "key", key)
		delay = initialDelay // 连接成功，重置退避时间

		disconnected := r.watch(ctx, cli, key)
		cli.Close()

		if !disconnected {
			// ctx 被取消，正常退出
			return
		}

		// etcd 断线，等待重连
		slog.Warn("etcd Watch 断线，等待重连", "delay", delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, maxDelay)
	}
}

// watch 启动 etcd Watch 循环，返回 true 表示因断线退出，返回 false 表示 ctx 取消正常退出
func (r *Reconciler) watch(ctx context.Context, cli *clientv3.Client, key string) bool {
	watchChan := cli.Watch(ctx, key, clientv3.WithPrefix())

	for {
		select {
		case <-ctx.Done():
			return false
		case watchResp, ok := <-watchChan:
			if !ok {
				slog.Warn("etcd Watch channel 已关闭，准备重连")
				return true
			}
			if watchResp.Err() != nil {
				slog.Error("etcd Watch 错误，准备重连", "err", watchResp.Err())
				return true
			}
			slog.Debug("etcd 状态变更，立即触发 Reconcile")
			r.queue.Add("reconcile")
		}
	}
}
