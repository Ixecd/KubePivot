package controller_installer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
)

// ApplySizing 将推荐配置写入集群。
//
// 流程：
//  0. 权限预检
//  0.5. etcd 健康度预检
//  0.6. 快照旧状态（回滚用）
//  1. Patch ConfigMap
//  2. Scale StatefulSet
//  3. WaitReady
//  4. 清理孤儿 Lease（若 S 减少，WaitReady 成功后安全清理）
//
// Scale 失败时自动回滚（ConfigMap + replicas 恢复到 apply 前值）。
// 孤儿 Lease 在 WaitReady 之后清理，避免回滚时 Lease 已删无法恢复。
func (i *Installer) ApplySizing(ctx context.Context, info *SizingInfo, shards, replicas int) error {
	// 0. 权限预检 — 别等 apply 到一半才发现没权限
	if err := i.checkAccessReview(ctx); err != nil {
		return fmt.Errorf("权限不足: %w\n\n请联系集群管理员授予以下权限：\n"+
			"  - patch configmaps in namespace %s\n"+
			"  - update statefulsets/scale in namespace %s\n"+
			"  - delete leases in namespace %s\n"+
			"  或使用具有 cluster-admin 权限的 kubeconfig。", err, i.cfg.Namespace, i.cfg.Namespace, i.cfg.Namespace)
	}

	// 0.5. etcd 健康度预检
	if err := i.checkEtcdHealth(ctx); err != nil {
		return fmt.Errorf("etcd health check failed, aborting: %w", err)
	}

	// 0.6. 快照旧状态 — 失败时回滚用
	oldShards := info.CurrentShards
	oldReplicas := info.CurrentReplicas

	// 1. 更新 ConfigMap
	patch := fmt.Sprintf(`{"data":{"shards":"%d"}}`, shards)
	exec := executor.GetExecutor()
	_, err := exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"patch", "configmap", "kubepivot-controller-config",
		"-n", i.cfg.Namespace, "--type=merge", "-p", patch)
	if err != nil {
		return fmt.Errorf("update ConfigMap: %w", err)
	}

	// 2. Scale StatefulSet
	_, err = exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"scale", "statefulset", "kubepivot-controller",
		"-n", i.cfg.Namespace, fmt.Sprintf("--replicas=%d", replicas))
	if err != nil {
		// Scale 失败 → 进入回滚流程
		slog.Error("scale statefulset 失败，开始回滚", "error", err)
		return rollbackSizing(ctx, i, oldShards, oldReplicas, err)
	}

	// 3. 等待 rollout
	projectCount := info.ProjectCount
	if projectCount == 0 {
		projectCount = 3 // 兜底
	}
	adaptiveTimeout := time.Duration(120+projectCount*2) * time.Second
	if adaptiveTimeout > 600*time.Second {
		adaptiveTimeout = 600 * time.Second
	}
	if err := i.WaitReady(ctx, adaptiveTimeout); err != nil {
		// WaitReady 超时 → 提示用户（不回滚已生效的变更）
		slog.Error("WaitReady 超时，Pod 可能未完全就绪", "error", err)
		return fmt.Errorf(
			"WaitReady 超时 (%v): %w\n\n"+
				"ConfigMap 和 Scale 已应用，但 Pod 未在预期时间内就绪。\n"+
				"你可以：\n"+
				"  1. 运行 'kp controller update --apply' 再次尝试（恢复旧值）\n"+
				"  2. 手动排查 Pod 状态: kubectl -n %s describe pods\n"+
				"  3. 运行回滚: kp controller update --shards %d --replicas %d --apply",
			adaptiveTimeout, err, i.cfg.Namespace, oldShards, oldReplicas)
	}

	// 4. 清理孤儿 Lease（WaitReady 成功后，新 Pod 已接管新分片范围）
	if shards < oldShards {
		if err := i.cleanupOrphanedLeases(ctx, shards, oldShards); err != nil {
			// 清理失败不阻断主流程，但记录警告
			slog.Warn("孤儿 Lease 清理失败（不影响主流程，Lease 将在 TTL 后自动过期）", "error", err)
		}
	}

	return nil
}

// rollbackSizing 将 ConfigMap 和 replicas 恢复为 apply 前的值。
func rollbackSizing(ctx context.Context, i *Installer, oldShards, oldReplicas int, originalErr error) error {
	slog.Warn("正在回滚 sizing 变更...", "shards", oldShards, "replicas", oldReplicas)
	exec := executor.GetExecutor()

	// 回滚 ConfigMap
	patch := fmt.Sprintf(`{"data":{"shards":"%d"}}`, oldShards)
	_, err1 := exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"patch", "configmap", "kubepivot-controller-config",
		"-n", i.cfg.Namespace, "--type=merge", "-p", patch)

	// 回滚 replicas
	_, err2 := exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"scale", "statefulset", "kubepivot-controller",
		"-n", i.cfg.Namespace, fmt.Sprintf("--replicas=%d", oldReplicas))

	if err1 != nil || err2 != nil {
		return fmt.Errorf(
			"回滚失败！请手动恢复:\n"+
				"  kubectl patch configmap kubepivot-controller-config -n %s --type=merge -p '{\"data\":{\"shards\":\"%d\"}}'\n"+
				"  kubectl scale statefulset kubepivot-controller -n %s --replicas=%d\n"+
				"原始错误: %v\n回滚错误: ConfigMap=%v, Scale=%v",
			i.cfg.Namespace, oldShards, i.cfg.Namespace, oldReplicas, originalErr, err1, err2)
	}

	return fmt.Errorf("变更已回滚（ConfigMap=%d, Replicas=%d）。原始错误: %w", oldShards, oldReplicas, originalErr)
}
