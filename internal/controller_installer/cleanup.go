package controller_installer

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Ixecd/kubepivot/internal/executor"
)

// cleanupOrphanedLeases 删除 newShards ≤ idx < oldShards 范围的 Lease 对象。
//
// Lease 命名规则: {prefix}{idx}，0-indexed。
// 例：S 从 10 减到 5 时，删除 kubepivot-controller-shard-5 ~ shard-9。
//
// 清理失败不返回 error（Lease 会在 TTL 后自动过期，清理是加速手段）。
func (i *Installer) cleanupOrphanedLeases(ctx context.Context, newShards, oldShards int) error {
	leasePrefix := "kubepivot-controller-shard-"
	exec := executor.GetExecutor()
	var deleted int

	for idx := newShards; idx < oldShards; idx++ {
		leaseName := fmt.Sprintf("%s%d", leasePrefix, idx)
		_, err := exec.Kubectl(ctx, i.cfg.Kubeconfig,
			"delete", "lease", leaseName,
			"-n", i.cfg.Namespace,
			"--ignore-not-found",
			"--wait=false") // 异步删除，不阻塞主流程
		if err != nil {
			slog.Warn("删除孤儿 Lease 失败", "lease", leaseName, "error", err)
			continue
		}
		deleted++
	}

	if deleted > 0 {
		slog.Info("孤儿 Lease 已清理", "range", fmt.Sprintf("shard-%d~shard-%d", newShards, oldShards-1), "count", deleted)
	}

	return nil
}
