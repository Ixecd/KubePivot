package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	leaderTTL      = 15 // etcd lease TTL 秒
	leaderRenew    = 5  // 续约间隔秒
	leaderRetry    = 3  // 抢锁失败后重试间隔秒
)

// leaderKey 分布式锁的 etcd key
func leaderKey(project, namespace string) string {
	return fmt.Sprintf("/kubepivot/%s/%s/leader", project, namespace)
}

// identity 当前实例唯一标识
func identity() string {
	hostname, _ := os.Hostname()
	return fmt.Sprintf("%s/%d", hostname, os.Getpid())
}

// RunWithLeaderElection 基于 etcd 的 Leader Election
// 只有 Leader 运行 fn，Follower 等待
// Leader 退出后 TTL 到期，其他实例自动抢锁
func RunWithLeaderElection(ctx context.Context, etcdEndpoints, project, namespace string, fn func(ctx context.Context)) {
	if etcdEndpoints == "" {
		// 无 etcd → 单机模式，直接运行
		slog.Info("⚠️  ETCD_ENDPOINTS 未配置，以单机模式运行（无 HA）")
		fn(ctx)
		return
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{etcdEndpoints},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		slog.Error("etcd 连接失败，以单机模式运行", "err", err)
		fn(ctx)
		return
	}
	defer cli.Close()

	id := identity()
	key := leaderKey(project, namespace)
	slog.Info("🗳  参与 Leader Election", "id", id, "key", key)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := tryBecomeLeader(ctx, cli, key, id, fn); err != nil {
			slog.Warn("Leader Election 失败，等待重试", "err", err, "retry_after", leaderRetry)
			select {
			case <-ctx.Done():
				return
			case <-time.After(leaderRetry * time.Second):
			}
		}
	}
}

// tryBecomeLeader 尝试抢锁，成功则运行 fn，fn 退出后释放锁
func tryBecomeLeader(ctx context.Context, cli *clientv3.Client, key, id string, fn func(ctx context.Context)) error {
	// 创建 lease
	lease, err := cli.Grant(ctx, leaderTTL)
	if err != nil {
		return fmt.Errorf("grant lease 失败: %w", err)
	}

	// 原子写：只有 key 不存在时才成功（抢锁）
	txn := cli.Txn(ctx).
		If(clientv3.Compare(clientv3.CreateRevision(key), "=", 0)).
		Then(clientv3.OpPut(key, id, clientv3.WithLease(lease.ID))).
		Else()

	resp, err := txn.Commit()
	if err != nil {
		cli.Revoke(ctx, lease.ID)
		return fmt.Errorf("txn 失败: %w", err)
	}
	if !resp.Succeeded {
		cli.Revoke(ctx, lease.ID)
		// 没抢到锁，Watch 等待 key 释放
		slog.Info("👁  成为 Follower，等待 Leader 释放锁", "key", key)
		watchLeaderRelease(ctx, cli, key)
		return nil
	}

	// 抢到锁
	slog.Info("👑 成为 Leader", "id", id)

	// 启动续约
	keepAlive, err := cli.KeepAlive(ctx, lease.ID)
	if err != nil {
		cli.Revoke(ctx, lease.ID)
		return fmt.Errorf("keepalive 失败: %w", err)
	}

	// 启动 fn（主循环）
	leaderCtx, leaderCancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(leaderCtx)
	}()

	// 监控续约和主循环
	for {
		select {
		case _, ok := <-keepAlive:
			if !ok {
				// 续约失败，失去 Leader 身份
				slog.Warn("⚠️  Leader 续约失败，主动退出")
				leaderCancel()
				<-done
				return fmt.Errorf("keepalive 中断")
			}
		case <-done:
			// fn 正常退出
			leaderCancel()
			cli.Revoke(ctx, lease.ID)
			slog.Info("Leader fn 已退出，释放锁")
			return nil
		case <-ctx.Done():
			leaderCancel()
			<-done
			cli.Revoke(ctx, lease.ID)
			return nil
		}
	}
}

// watchLeaderRelease Watch etcd key，等待 Leader 释放锁
func watchLeaderRelease(ctx context.Context, cli *clientv3.Client, key string) {
	wch := cli.Watch(ctx, key)
	for {
		select {
		case <-ctx.Done():
			return
		case resp, ok := <-wch:
			if !ok {
				return
			}
			for _, ev := range resp.Events {
				if ev.Type == clientv3.EventTypeDelete {
					slog.Info("🔓 Leader 锁已释放，重新参与竞选")
					return
				}
			}
		}
	}
}
