package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/audit"
	"github.com/Ixecd/kubepivot/internal/rbac"
	"github.com/Ixecd/kubepivot/internal/executor"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const driftSyncInterval = 30 * time.Second

// StartDriftSyncLoop 启动 force-sync 扫描循环（独立于 8s 自愈周期）
// 只对 resources.yaml 里标记了 force-sync: true 的资源生效
func (r *Reconciler) StartDriftSyncLoop(ctx context.Context) {
	ticker := time.NewTicker(driftSyncInterval)
	defer ticker.Stop()

	slog.Info("🔄 Drift Sync Loop 已启动", "interval", driftSyncInterval)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Drift Sync Loop 已退出")
			return
		case <-ticker.C:
			r.scanAndSync()
		}
	}
}

// scanAndSync 扫描所有 force-sync 资源，发现漂移则强制对齐
func (r *Reconciler) scanAndSync() {
	for _, res := range r.resources.Resources {
		if !res.ForceSync {
			continue
		}

		ns := res.Namespace
		if ns == "" {
			ns = getenv("KUBE_NAMESPACE", "web3-blitz")
		}

		diffs, err := detectDrift(r.kubeconfig, ns, res)
		if err != nil {
			slog.Warn("drift 检测失败", "resource", res.Name, "err", err)
			continue
		}
		if len(diffs) == 0 {
			continue
		}

		// 过滤豁免字段
		var hardDiffs []string
		for _, d := range diffs {
			if isNoSyncField(d, res.NoSyncFields) {
				slog.Info("drift 豁免字段，跳过", "field", d, "resource", res.Name)
				continue
			}
			hardDiffs = append(hardDiffs, d)
		}
		if len(hardDiffs) == 0 {
			continue
		}

		slog.Warn("⚠️  检测到配置漂移，触发 force-sync",
			"resource", res.Name,
			"diffs", hardDiffs,
		)

		// 触发 helm upgrade 强制对齐
		if err := r.forceSync(ns, res); err != nil {
			slog.Error("force-sync 失败", "resource", res.Name, "err", err)
		} else {
			slog.Info("✅ force-sync 完成", "resource", res.Name)
			// 写漂移审计日志
			writeDriftAuditLog(res.Name, ns, hardDiffs)
		}
	}
}

// detectDrift 用 helm diff 检测单个资源的漂移
func detectDrift(kubeconfig, namespace string, res Resource) ([]string, error) {
	release := findReleaseForResource(namespace, res)
	if release == "" {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := executor.GetExecutor().Helm(ctx, kubeconfig,
		"diff", "upgrade", release,
		"--namespace", namespace,
		"--no-hooks",
		"--suppress-secrets",
		"--three-way-merge",
	)
	if err != nil {
		slog.Warn("helm diff 失败，跳过此资源", "release", release, "namespace", namespace, "err", err)
		return nil, nil
	}
	output := strings.TrimSpace(string(out))
	if output == "" {
		return nil, nil
	}

	var diffs []string
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "+") {
			continue
		}
		entry := strings.TrimSpace(trimmed[1:])
		if entry == "" || strings.HasSuffix(entry, ":") {
			continue
		}
		parts := strings.SplitN(entry, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			if isKubepivotOwnedKey(key) {
				diffs = append(diffs, entry)
			}
		}
	}
	return diffs, nil
}

// forceSync 触发 helm upgrade 强制对齐
func (r *Reconciler) forceSync(namespace string, res Resource) error {
	// v2.8 B.5 (D-Level1): RBAC + audit denied 接入
	if !mustCheckController(context.Background(), namespace, rbac.PermDriftSync, "drift.force-sync") {
		return nil // ENFORCE 模式拒绝, 跳过本次 force-sync
	}

	release := findReleaseForResource(namespace, res)
	if release == "" {
		return fmt.Errorf("找不到对应的 helm release")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	out, err := executor.GetExecutor().Helm(ctx, r.kubeconfig,
		"upgrade", release,
		"--namespace", namespace,
		"--reuse-values",
		"--wait",
		"--timeout", "120s",
		"--history-max", "10",
	)
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

// findReleaseForResource 推断或返回 helm release 名
//
// v2.4.0 改造：
//   - 优先返回 res.HelmRelease（resources.yaml 显式声明，蓝绿/金丝雀场景必需）
//   - 否则按默认推断：<PROJECT_NAME>-<Resource.Name>
//   - PROJECT_NAME 为空时回退到 namespace
//
// 死参数 kubeconfig 已移除（v2.3.0 起从未使用）
func findReleaseForResource(namespace string, res Resource) string {
	// v2.4.0：显式声明优先（蓝绿场景，比如 web3-blitz-blue / web3-blitz-green）
	if res.HelmRelease != "" {
		return res.HelmRelease
	}

	project := getenv("PROJECT_NAME", "")
	if project == "" {
		project = namespace
	}
	return project + "-" + res.Name
}

// isNoSyncField 检查是否是豁免字段
func isNoSyncField(entry string, noSyncFields []string) bool {
	for _, f := range noSyncFields {
		if strings.Contains(entry, f) {
			return true
		}
	}
	return false
}

// isKubepivotOwnedKey 判断是否是 kp 声明所有权的字段
func isKubepivotOwnedKey(key string) bool {
	owned := []string{"image", "replicas", "limits", "requests", "cpu", "memory"}
	lower := strings.ToLower(key)
	for _, o := range owned {
		if lower == o {
			return true
		}
	}
	return false
}

// writeDriftAuditLog 写漂移审计日志到 etcd（降级到 slog）
func writeDriftAuditLog(resource, namespace string, diffs []string) {
	// v2.8 B.7.1: 加 actor 字段 (controller 端固定 kubepivot-controller@<pod>)
	entry := fmt.Sprintf(`{"resource":"%s","namespace":"%s","diffs":%q,"actor":"%s","ts":"%s"}`,
		resource, namespace,
		strings.Join(diffs, "; "),
		audit.ResolveControllerActor(),
		time.Now().Format(time.RFC3339),
	)

	slog.Info("drift audit", "resource", resource, "namespace", namespace,
		"diffs", strings.Join(diffs, "; "))

	// 写入 etcd
	endpoints := os.Getenv("ETCD_ENDPOINTS")
	if endpoints == "" {
		return
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   strings.Split(endpoints, ","),
		DialTimeout: 3 * time.Second,
	})
	if err != nil {
		slog.Warn("drift audit: etcd 连接失败", "err", err)
		return
	}
	defer cli.Close()

	key := fmt.Sprintf("/kubepivot/%s/%s/drift/%d",
		getenv("PROJECT_NAME", namespace),
		namespace,
		time.Now().UnixNano(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := cli.Put(ctx, key, entry); err != nil {
		slog.Warn("drift audit: 写入 etcd 失败", "err", err)
	} else {
		slog.Debug("drift audit: 已写入 etcd", "key", key)
	}
}
