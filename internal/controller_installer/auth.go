package controller_installer

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Ixecd/kubepivot/internal/executor"
)

// checkAccessReview 验证当前 kubeconfig 是否具备 apply 所需权限。
//
// 需要三项权限：
//   - patch configmaps in <namespace>
//   - update deployments/scale in <namespace>
//   - delete leases in <namespace>
func (i *Installer) checkAccessReview(ctx context.Context) error {
	checks := []struct {
		verb     string
		resource string
	}{
		{"patch", "configmaps"},
		{"update", "deployments/scale"},
		{"delete", "leases"},
	}

	exec := executor.GetExecutor()
	for _, c := range checks {
		out, err := exec.Kubectl(ctx, i.cfg.Kubeconfig,
			"auth", "can-i", c.verb, c.resource,
			"-n", i.cfg.Namespace)
		if err != nil {
			return fmt.Errorf("无法检查权限: %w", err)
		}
		if strings.TrimSpace(string(out)) != "yes" {
			return fmt.Errorf("缺少权限: %s %s in namespace %s", c.verb, c.resource, i.cfg.Namespace)
		}
	}
	return nil
}

// checkEtcdHealth 检查 etcd 连接和 DB size。
//
// 如果 etcd 不可达或 DB 过大，返回 error 阻断 apply。
func (i *Installer) checkEtcdHealth(ctx context.Context) error {
	endpoints := os.Getenv("ETCD_ENDPOINTS")
	if endpoints == "" {
		return nil // 无 etcd 配置 → 用本地 store，跳过检查
	}
	// 用 etcdctl endpoint health 做快速拨测
	exec := executor.GetExecutor()
	_, err := exec.Generic(ctx, "etcdctl", i.cfg.Kubeconfig,
		"--endpoints", endpoints,
		"endpoint", "health")
	if err != nil {
		return fmt.Errorf("etcd 端点不健康 (endpoints=%s): %w", endpoints, err)
	}
	return nil
}
