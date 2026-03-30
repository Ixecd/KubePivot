package controller

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/Ixecd/kubepivot/internal/state"
)

// HelmClient helm 操作接口
type HelmClient interface {
	History(release, namespace string) ([]HelmRelease, error)
	Rollback(release, namespace string, revision int) error
}

type HelmRelease struct {
	Revision int `json:"revision"`
}

// RealHelmClient 真实实现
type RealHelmClient struct{}

// checkAndHeal 检查资源是否存在，缺失时执行自愈并同步状态机
func (r *Reconciler) checkAndHeal(res Resource) error {
	exists, err := r.detector.ResourceExists(res.Kind, res.Name, res.Namespace)
	if err != nil {
		return fmt.Errorf("检查资源状态失败: %w", err)
	}
	if exists {
		return nil // 资源正常，无需处理
	}

	slog.Warn("资源缺失，启动自愈", "kind", res.Kind, "name", res.Name, "namespace", res.Namespace)

	switch res.OnMissing {
	case "auto-heal":
		return r.healRecreate(res)
	case "alert":
		slog.Error("资源缺失告警（不自动处理）", "kind", res.Kind, "name", res.Name, "namespace", res.Namespace)
		return nil
	default:
		slog.Warn("未知 on_missing 策略，跳过", "strategy", res.OnMissing, "kind", res.Kind, "name", res.Name)
		return nil
	}
}

// healRecreate 改为用 --reuse-values 重新安装
func (r *Reconciler) healRecreate(res Resource) error {
	releaseName := getenv("PROJECT_NAME", "") + "-" + res.Name

	history, err := r.helm.History(releaseName, res.Namespace)
	if err != nil || len(history) == 0 {
		slog.Warn("查不到 helm release，无法自愈", "release", releaseName)
		return nil
	}

	latest := history[len(history)-1].Revision
	target := latest - 1

	slog.Info("执行 helm rollback", "release", releaseName, "from", latest, "to", target)
	if err := r.helm.Rollback(releaseName, res.Namespace, target); err != nil {
		// SSA 冲突：清除 managedFields 后重试一次
		if isSSAConflict(err.Error()) {
			slog.Warn("检测到 SSA 冲突，清除 managedFields 后重试", "release", releaseName)
			if clearErr := clearNamespaceManagedFields(res.Namespace); clearErr != nil {
				slog.Error("清除 managedFields 失败", "err", clearErr)
				return fmt.Errorf("SSA 修复失败: %w", clearErr)
			}
			if retryErr := r.helm.Rollback(releaseName, res.Namespace, target); retryErr != nil {
				slog.Error("SSA 修复后重试 rollback 失败", "err", retryErr)
				return fmt.Errorf("自愈失败: %w", retryErr)
			}
		} else {
			slog.Error("rollback 执行失败", "err", err)
			return fmt.Errorf("自愈失败: %w", err)
		}
	}

	slog.Info("自愈成功", "release", releaseName)

	// 只有不在 RUNNING 时才需要同步
	if r.sm.State() != state.StateRunning {
		if err := r.sm.Transition(state.StateRunning, "controller: rollback 自愈成功"); err != nil {
			slog.Error("状态机同步失败", "err", err)
		}
	}
	return nil
}

// isSSAConflict 检测是否是 SSA managedFields 冲突
func isSSAConflict(errMsg string) bool {
	keywords := []string{
		"Apply failed",
		"conflict:",
		"another manager",
		"field manager",
		"UPGRADE FAILED: rendered manifests contain a new resource",
	}
	for _, kw := range keywords {
		if strings.Contains(errMsg, kw) {
			return true
		}
	}
	return false
}

// clearNamespaceManagedFields 清除 namespace 下所有 helm 管理资源的 managedFields
func clearNamespaceManagedFields(namespace string) error {
	kinds := []string{
		"deployment", "statefulset", "service",
		"configmap", "serviceaccount",
		"role", "rolebinding", "ingress",
	}
	for _, kind := range kinds {
		if err := clearManagedFieldsByKind(namespace, kind); err != nil {
			slog.Warn("清除 managedFields 失败，跳过", "kind", kind, "err", err)
		}
	}
	return nil
}

// clearManagedFieldsByKind 清除某种资源类型下所有资源的 managedFields
func clearManagedFieldsByKind(namespace, kind string) error {
	out, err := exec.Command("kubectl", "get", kind,
		"--namespace", namespace,
		"--no-headers",
		"-o", "custom-columns=NAME:.metadata.name",
	).Output()
	if err != nil {
		return nil // 该类型不存在，跳过
	}

	names := strings.Fields(strings.TrimSpace(string(out)))
	for _, name := range names {
		args := []string{
			"patch", kind, name,
			"--namespace", namespace,
			"--type=merge",
			"--patch", `{"metadata":{"managedFields":null}}`,
		}
		if out, err := exec.Command("kubectl", args...).CombinedOutput(); err != nil {
			slog.Warn("清除 managedFields 失败", "kind", kind, "name", name, "err", string(out))
		} else {
			slog.Info("已清除 managedFields", "kind", kind, "name", name)
		}
	}
	return nil
}

// getLatestRevision 返回当前 helm release 的最新 revision 号
func getLatestRevision(releaseName, namespace string) (int, error) {
	out, err := runHelmOutput("history", releaseName, "--namespace", namespace, "--output", "json")
	if err != nil || len(out) == 0 {
		return 0, fmt.Errorf("helm history 失败: %w", err)
	}
	var history []struct {
		Revision int `json:"revision"`
	}
	if err := json.Unmarshal(out, &history); err != nil || len(history) == 0 {
		return 0, fmt.Errorf("解析 helm history 失败: %w", err)
	}
	// helm history 按 revision 升序，取最后一个
	return history[len(history)-1].Revision, nil
}

func runHelmOutput(args ...string) ([]byte, error) {
	return exec.Command("helm", args...).Output()
}

// runHelm 执行 helm 命令，返回错误（含 stderr 输出）
func runHelm(args ...string) error {
	cmd := exec.Command("helm", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, string(out))
	}
	return nil
}

func (h *RealHelmClient) History(release, namespace string) ([]HelmRelease, error) {
	out, err := runHelmOutput("history", release, "--namespace", namespace, "--output", "json")
	if err != nil {
		return nil, fmt.Errorf("helm history 失败: %w", err)
	}
	var history []HelmRelease
	if err := json.Unmarshal(out, &history); err != nil {
		return nil, fmt.Errorf("解析 helm history 失败: %w", err)
	}
	return history, nil
}

func (h *RealHelmClient) Rollback(release, namespace string, revision int) error {
	return runHelm("rollback", release, fmt.Sprintf("%d", revision),
		"--namespace", namespace, "--wait",
	)
}
