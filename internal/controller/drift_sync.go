package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
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
			r.scanAndSync(ctx)
		}
	}
}

// scanAndSync 扫描所有 force-sync 资源，发现漂移则强制对齐
func (r *Reconciler) scanAndSync(ctx context.Context) {
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
	// 找到对应的 helm release
	release := findReleaseForResource(kubeconfig, namespace, res)
	if release == "" {
		return nil, nil
	}

	args := []string{
		"helm", "diff", "upgrade", release,
		"--namespace", namespace,
		"--no-hooks",
		"--suppress-secrets",
		"--three-way-merge",
	}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}

	out, _ := exec.Command(args[0], args[1:]...).CombinedOutput()
	output := strings.TrimSpace(string(out))
	if output == "" {
		return nil, nil
	}

	// 简单解析：找出 replicas/image 等关键字段的变化
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
	release := findReleaseForResource(r.kubeconfig, namespace, res)
	if release == "" {
		return fmt.Errorf("找不到对应的 helm release")
	}

	args := []string{
		"helm", "upgrade", release,
		"--namespace", namespace,
		"--reuse-values",
		"--wait",
		"--timeout", "120s",
	}
	if r.kubeconfig != "" {
		args = append(args, "--kubeconfig", r.kubeconfig)
	}

	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

// findReleaseForResource 从资源名推断 helm release 名
func findReleaseForResource(kubeconfig, namespace string, res Resource) string {
	project := getenv("PROJECT_NAME", "")
	if project == "" {
		return ""
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

// writeDriftAuditLog 写漂移审计日志
func writeDriftAuditLog(resource, namespace string, diffs []string) {
	slog.Info("drift audit",
		"resource", resource,
		"namespace", namespace,
		"diffs", strings.Join(diffs, "; "),
		"ts", time.Now().Format(time.RFC3339),
	)
	// TODO(v1.7.0)：写入 etcd /kubepivot/<project>/drift/<timestamp>
}
