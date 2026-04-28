package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
	"github.com/Ixecd/kubepivot/internal/rbac"
)

// runSweeperLoop 周期性清理任务，仅 leader pod 跑
//
// v2.5.0 第一版职责：
//   - 清理孤儿 shard lease（lease.id >= totalShards 时表示用户调小了 N）
//
// v2.5.0 后续 commit 会扩展（孤儿 machine 由每 pod 自扫，不在这里）
//
// 调用频率：每 60 秒一次（孤儿 lease 不紧急，过频反而浪费 kubectl exec）
func runSweeperLoop(ctx context.Context, kubeconfig string, totalShards int) {
	interval := 60 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("🧹 Sweeper Loop 启动（leader-only）",
		"interval", interval,
		"total_shards", totalShards,
	)

	// 立刻先跑一轮（避免 leader 切换后 60 秒内 shard 缩减不被清理）
	cleanupOrphanShardLeases(ctx, kubeconfig, totalShards)

	for {
		select {
		case <-ctx.Done():
			slog.Info("🧹 Sweeper Loop 退出")
			return
		case <-ticker.C:
			cleanupOrphanShardLeases(ctx, kubeconfig, totalShards)
		}
	}
}

// cleanupOrphanShardLeases 清理"index >= totalShards"的孤儿 shard lease
//
// 触发场景：用户把 KUBEPIVOT_SHARDS 从 20 调小到 10，
// 旧的 shard-10 ~ shard-19 lease 不会被任何 pod 续约，
// 但也不会自动删除——sweeper 主动清理。
func cleanupOrphanShardLeases(ctx context.Context, kubeconfig string, totalShards int) {
	const namespace = "kubepivot-system"
	const leasePrefix = "kubepivot-controller-shard-"

	// v2.8 B.5 (D-Level1): RBAC + audit denied 接入
	// system namespace 操作, 严格检查权限
	if !mustCheckController(ctx, namespace, rbac.PermSweeperLease, "sweeper.cleanup-lease") {
		return // ENFORCE 模式拒绝, 跳过本轮清理
	}

	// 列举所有 shard lease
	out, err := executor.GetExecutor().Kubectl(ctx, kubeconfig,
		"get", "lease",
		"-n", namespace,
		"-o", "json",
	)
	if err != nil {
		slog.Warn("Sweeper: 列举 lease 失败", "err", err)
		return
	}

	var listing struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &listing); err != nil {
		slog.Warn("Sweeper: 解析 lease 列表失败", "err", err)
		return
	}

	cleaned := 0
	for _, item := range listing.Items {
		name := item.Metadata.Name
		if !strings.HasPrefix(name, leasePrefix) {
			continue
		}
		idxStr := strings.TrimPrefix(name, leasePrefix)
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			continue
		}
		if idx < totalShards {
			continue // 仍有效，跳过
		}

		// 孤儿 lease：删除
		if err := deleteLease(ctx, kubeconfig, namespace, name); err != nil {
			slog.Warn("Sweeper: 删除孤儿 lease 失败", "lease", name, "err", err)
			continue
		}
		slog.Info("🗑 Sweeper 删除孤儿 shard lease",
			"lease", name,
			"index", idx,
			"current_total_shards", totalShards)
		cleaned++
	}

	if cleaned > 0 {
		slog.Info("🧹 Sweeper 本轮清理完成", "orphan_leases_deleted", cleaned)
	}
}

func deleteLease(ctx context.Context, kubeconfig, namespace, name string) error {
	_, err := executor.GetExecutor().Kubectl(ctx, kubeconfig,
		"delete", "lease", name,
		"-n", namespace,
		"--ignore-not-found",
	)
	if err != nil {
		return fmt.Errorf("kubectl delete lease %s: %w", name, err)
	}
	return nil
}
