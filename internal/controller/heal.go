package controller

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
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

// checkAndHeal 检查资源是否存在，缺失时执行自愈
func (r *Reconciler) checkAndHeal(res Resource) error {
	exists, err := r.detector.ResourceExists(res.Kind, res.Name, res.Namespace)
	if err != nil {
		return fmt.Errorf("检查资源状态失败: %w", err)
	}

	// 资源存在时，检查异常状态
	if exists {
		return r.checkAbnormalState(res)
	}

	slog.Warn("资源缺失，启动自愈",
		"kind", res.Kind, "name", res.Name,
		"namespace", res.Namespace, "strategy", res.OnMissing)

	switch res.OnMissing {
	case "auto-heal", "recreate":
		return r.healRecreate(res)
	case "rollback":
		return r.healRollback(res)
	case "scale-down":
		return r.healScaleDown(res)
	case "alert":
		slog.Error("❌ 资源缺失告警（不自动处理）",
			"kind", res.Kind, "name", res.Name, "namespace", res.Namespace)
		return nil
	case "custom":
		return r.healCustom(res)
	default:
		slog.Warn("未知 on-missing 策略，跳过",
			"strategy", res.OnMissing, "kind", res.Kind, "name", res.Name)
		return nil
	}
}

// checkAbnormalState 资源存在时检查异常状态（OOMKilled / CrashLoopBackOff）
func (r *Reconciler) checkAbnormalState(res Resource) error {
	if strings.ToLower(res.Kind) != "deployment" {
		return nil
	}

	pods, err := getPodsForResource(r.kubeconfig, res.Namespace, res.Name)
	if err != nil || len(pods) == 0 {
		return nil
	}

	for _, pod := range pods {
		// OOMKilled 检测
		if pod.OOMKilled {
			slog.Warn("⚠️  检测到 OOMKilled",
				"pod", pod.Name, "resource", res.Name,
				"current_memory", pod.MemoryLimit)
			if err := r.handleOOMKilled(res, pod); err != nil {
				slog.Error("OOMKilled 处理失败", "err", err)
			}
			continue
		}

		// CrashLoopBackOff 检测
		if pod.CrashLoopBackOff {
			crashType := analyzeCrashType(r.kubeconfig, res.Namespace, pod.Name)
			slog.Warn("⚠️  检测到 CrashLoopBackOff",
				"pod", pod.Name, "resource", res.Name,
				"crash_type", crashType, "restarts", pod.RestartCount)
			r.handleCrashLoop(res, pod, crashType)
		}
	}
	return nil
}

// ── on-missing 策略实现 ───────────────────────────────────────────────────────

// healRecreate helm upgrade --reuse-values 重新安装
func (r *Reconciler) healRecreate(res Resource) error {
	releaseName := getenv("PROJECT_NAME", "") + "-" + res.Name

	history, err := r.helm.History(releaseName, res.Namespace)
	if err != nil || len(history) == 0 {
		slog.Warn("查不到 helm release，无法自愈", "release", releaseName)
		return nil
	}

	latest := history[len(history)-1].Revision
	target := latest - 1
	if target < 1 {
		target = 1
	}
	slog.Info("执行 helm rollback（重新应用当前版本）",
		"release", releaseName, "revision", target)
	if err := r.helm.Rollback(releaseName, res.Namespace, target); err != nil {
		if isSSAConflict(err.Error()) {
			slog.Warn("SSA 冲突，清除 managedFields 后重试", "release", releaseName)
			clearNamespaceManagedFields(res.Namespace)
			if err = r.helm.Rollback(releaseName, res.Namespace, target); err != nil {
				return fmt.Errorf("自愈失败: %w", err)
			}
		} else {
			return fmt.Errorf("自愈失败: %w", err)
		}
	}

	slog.Info("✅ 自愈成功（recreate）", "release", releaseName)
	r.syncStateRunning("controller: recreate 自愈成功")
	return nil
}

// healRollback helm rollback 到上一个 revision
func (r *Reconciler) healRollback(res Resource) error {
	releaseName := getenv("PROJECT_NAME", "") + "-" + res.Name

	history, err := r.helm.History(releaseName, res.Namespace)
	if err != nil || len(history) == 0 {
		slog.Warn("查不到 helm release，降级为 recreate", "release", releaseName)
		return r.healRecreate(res)
	}

	latest := history[len(history)-1].Revision
	target := latest - 1
	if target < 1 {
		target = 1
	}

	slog.Info("执行 helm rollback", "release", releaseName, "from", latest, "to", target)
	if err := r.helm.Rollback(releaseName, res.Namespace, target); err != nil {
		return fmt.Errorf("healRollback 失败: %w", err)
	}

	slog.Info("✅ 自愈成功（rollback）", "release", releaseName)
	r.syncStateRunning("controller: rollback 自愈成功")
	return nil
}

// healScaleDown 缩容到 0（保留资源但停止服务）
func (r *Reconciler) healScaleDown(res Resource) error {
	slog.Info("执行 scale-down", "resource", res.Name, "namespace", res.Namespace)
	args := []string{
		"kubectl", "scale",
		strings.ToLower(res.Kind) + "/" + res.Name,
		"--namespace", res.Namespace,
		"--replicas=0",
	}
	if r.kubeconfig != "" {
		args = append(args, "--kubeconfig", r.kubeconfig)
	}
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("healScaleDown 失败: %w\n%s", err, string(out))
	}
	slog.Info("✅ 已缩容到 0", "resource", res.Name)
	return nil
}

// healCustom 执行自定义命令（resources.yaml 里的 fallback 字段）
func (r *Reconciler) healCustom(res Resource) error {
	if res.Fallback == "" {
		slog.Warn("custom 策略但 fallback 为空，跳过", "resource", res.Name)
		return nil
	}
	slog.Info("执行自定义自愈", "command", res.Fallback, "resource", res.Name)
	out, err := exec.Command("sh", "-c", res.Fallback).CombinedOutput()
	if err != nil {
		return fmt.Errorf("healCustom 失败: %w\n%s", err, string(out))
	}
	slog.Info("✅ 自定义自愈完成", "resource", res.Name)
	return nil
}

// ── OOMKilled 处理 ────────────────────────────────────────────────────────────

type podStatus struct {
	Name             string
	OOMKilled        bool
	CrashLoopBackOff bool
	RestartCount     int
	MemoryLimit      string
}

// getPodsForResource 获取 deployment 下所有 pod 的状态
func getPodsForResource(kubeconfig, namespace, name string) ([]podStatus, error) {
	args := []string{
		"kubectl", "get", "pods",
		"--namespace", namespace,
		"-l", fmt.Sprintf("app=%s", name),
		"-o", "json",
	}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	out, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		return nil, err
	}

	var podList struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				ContainerStatuses []struct {
					RestartCount int `json:"restartCount"`
					State        struct {
						Waiting *struct {
							Reason string `json:"reason"`
						} `json:"waiting"`
					} `json:"state"`
					LastState struct {
						Terminated *struct {
							Reason string `json:"reason"`
						} `json:"terminated"`
					} `json:"lastState"`
				} `json:"containerStatuses"`
			} `json:"status"`
			Spec struct {
				Containers []struct {
					Resources struct {
						Limits map[string]string `json:"limits"`
					} `json:"resources"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"items"`
	}

	if err := json.Unmarshal(out, &podList); err != nil {
		return nil, err
	}

	var result []podStatus
	for _, item := range podList.Items {
		ps := podStatus{Name: item.Metadata.Name}
		for _, cs := range item.Status.ContainerStatuses {
			ps.RestartCount = cs.RestartCount
			if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
				ps.CrashLoopBackOff = true
			}
			if cs.LastState.Terminated != nil && cs.LastState.Terminated.Reason == "OOMKilled" {
				ps.OOMKilled = true
			}
		}
		if len(item.Spec.Containers) > 0 {
			ps.MemoryLimit = item.Spec.Containers[0].Resources.Limits["memory"]
		}
		result = append(result, ps)
	}
	return result, nil
}

// handleOOMKilled 自动调整 memory limits（上调 25%）
func (r *Reconciler) handleOOMKilled(res Resource, pod podStatus) error {
	newLimit := bumpMemory(pod.MemoryLimit, 25)
	if newLimit == "" {
		slog.Warn("无法解析 memory limit，跳过自动调整", "current", pod.MemoryLimit)
		return nil
	}

	slog.Info("自动调整 memory limit",
		"resource", res.Name, "from", pod.MemoryLimit, "to", newLimit)

	// patch deployment memory limit
	patch := fmt.Sprintf(
		`{"spec":{"template":{"spec":{"containers":[{"name":"%s","resources":{"limits":{"memory":"%s"}}}]}}}}`,
		res.Name, newLimit,
	)
	args := []string{
		"kubectl", "patch", "deployment", res.Name,
		"--namespace", res.Namespace,
		"--type=merge",
		fmt.Sprintf("--patch=%s", patch),
	}
	if r.kubeconfig != "" {
		args = append(args, "--kubeconfig", r.kubeconfig)
	}
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("调整 memory limit 失败: %w\n%s", err, string(out))
	}

	slog.Info("✅ memory limit 已自动调整", "resource", res.Name, "new_limit", newLimit)
	return nil
}

// bumpMemory 将 memory 字符串上调指定百分比
// 支持 Mi/Gi 单位
func bumpMemory(current string, pct int) string {
	current = strings.TrimSpace(current)
	if current == "" {
		return ""
	}
	var val int
	var unit string
	if strings.HasSuffix(current, "Gi") {
		fmt.Sscanf(strings.TrimSuffix(current, "Gi"), "%d", &val)
		unit = "Gi"
	} else if strings.HasSuffix(current, "Mi") {
		fmt.Sscanf(strings.TrimSuffix(current, "Mi"), "%d", &val)
		unit = "Mi"
	} else {
		return ""
	}
	newVal := val * (100 + pct) / 100
	if newVal <= val {
		newVal = val + 1
	}
	return strconv.Itoa(newVal) + unit
}

// ── CrashLoopBackOff 分析 ─────────────────────────────────────────────────────

type crashType string

const (
	crashStartup crashType = "startup" // 启动失败（配置错误、依赖不就绪）
	crashRuntime crashType = "runtime" // 运行时崩溃（业务逻辑错误）
	crashUnknown crashType = "unknown"
)

// analyzeCrashType 分析 CrashLoopBackOff 是启动错误还是运行时错误
func analyzeCrashType(kubeconfig, namespace, podName string) crashType {
	args := []string{
		"kubectl", "logs", podName,
		"--namespace", namespace,
		"--previous",
		"--tail=50",
	}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	out, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		return crashUnknown
	}
	logs := strings.ToLower(string(out))

	return classifyCrashLogs(logs)
}

// handleCrashLoop CrashLoopBackOff 处理逻辑
func (r *Reconciler) handleCrashLoop(res Resource, pod podStatus, ct crashType) {
	switch ct {
	case crashStartup:
		slog.Warn("CrashLoopBackOff（启动错误）：可能是配置/依赖问题，等待人工介入",
			"resource", res.Name, "pod", pod.Name,
			"advice", "检查 Secret/ConfigMap/依赖服务是否就绪")
	case crashRuntime:
		slog.Warn("CrashLoopBackOff（运行时错误）：业务代码崩溃",
			"resource", res.Name, "pod", pod.Name,
			"advice", "检查应用日志，考虑回滚到上一版本：kp rollback")
		// 运行时崩溃且重启次数过多，自动触发 rollback
		if pod.RestartCount >= 5 {
			slog.Warn("重启次数 >= 5，触发自动 rollback", "resource", res.Name)
			r.healRollback(res)
		}
	default:
		slog.Warn("CrashLoopBackOff（原因不明）",
			"resource", res.Name, "pod", pod.Name)
	}
}

// ── 辅助函数 ──────────────────────────────────────────────────────────────────

func (r *Reconciler) syncStateRunning(reason string) {
	if r.sm.State() != state.StateRunning {
		if err := r.sm.Transition(state.StateRunning, reason); err != nil {
			slog.Error("状态机同步失败", "err", err)
		}
	}
}

func isSSAConflict(errMsg string) bool {
	keywords := []string{
		"Apply failed", "conflict:", "another manager",
		"field manager", "UPGRADE FAILED: rendered manifests contain a new resource",
	}
	for _, kw := range keywords {
		if strings.Contains(errMsg, kw) {
			return true
		}
	}
	return false
}

func clearNamespaceManagedFields(namespace string) error {
	kinds := []string{
		"deployment", "statefulset", "service",
		"configmap", "serviceaccount",
	}
	for _, kind := range kinds {
		clearManagedFieldsByKind(namespace, kind)
	}
	return nil
}

func clearManagedFieldsByKind(namespace, kind string) error {
	out, err := exec.Command("kubectl", "get", kind,
		"--namespace", namespace, "--no-headers",
		"-o", "custom-columns=NAME:.metadata.name",
	).Output()
	if err != nil {
		return nil
	}
	for _, name := range strings.Fields(strings.TrimSpace(string(out))) {
		exec.Command("kubectl", "patch", kind, name,
			"--namespace", namespace, "--type=merge",
			"--patch", `{"metadata":{"managedFields":null}}`,
		).Run()
	}
	return nil
}

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
	return history[len(history)-1].Revision, nil
}

func runHelmOutput(args ...string) ([]byte, error) {
	return exec.Command("helm", args...).Output()
}

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

// classifyCrashLogs 从日志文本判断崩溃类型（纯函数，可测试）
func classifyCrashLogs(logs string) crashType {
	lower := strings.ToLower(logs)
	startupKeywords := []string{
		"connection refused", "dial tcp", "no such host",
		"failed to connect", "timeout", "database", "etcd",
		"config", "env", "secret", "permission denied",
	}
	for _, kw := range startupKeywords {
		if strings.Contains(lower, kw) {
			return crashStartup
		}
	}
	runtimeKeywords := []string{
		"panic:", "runtime error", "segmentation fault",
		"nil pointer", "index out of range",
	}
	for _, kw := range runtimeKeywords {
		if strings.Contains(lower, kw) {
			return crashRuntime
		}
	}
	return crashUnknown
}
