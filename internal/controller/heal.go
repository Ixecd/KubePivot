package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
	"github.com/Ixecd/kubepivot/internal/rbac"
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

// 👇 豆包小姐专属代码 ✍️
// 支持：全资源 + CRD，带超时、上下文、错误日志、参数校验
func (r *Reconciler) loadResourceLabels(res *Resource) bool {
	if res == nil || res.Kind == "" || res.Name == "" || res.Namespace == "" {
		slog.Warn("loadResourceLabels: 无效资源参数")
		return false
	}

	// v2.7 Step 2b-2: informer cache fast path
	// 仅在 detector 实现 LabelGetter 接口时触发（InformerDetector）
	// cache miss / kind 不支持 / pool 不可用 → 自动 fallback 到下方 kubectl 路径
	if lg, ok := r.detector.(LabelGetter); ok {
		if labels, found := lg.GetResourceLabels(res.Kind, res.Name, res.Namespace); found {
			res.Labels = labels
			return true
		}
	}

	// 🔥 修复：5秒超时，永不阻塞 Reconciler
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := executor.GetExecutor().Kubectl(ctx, r.kubeconfig,
		"get", strings.ToLower(res.Kind), res.Name,
		"--namespace", res.Namespace,
		"-o", "json",
	)
	if err != nil {
		slog.Debug("获取资源标签失败（非致命）",
			"kind", res.Kind, "name", res.Name, "err", err)
		return false
	}

	var obj struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}

	if err := json.Unmarshal(out, &obj); err != nil {
		slog.Warn("解析资源标签失败",
			"kind", res.Kind, "name", res.Name, "err", err)
		return false
	}

	res.Labels = obj.Metadata.Labels
	return true
}

// 👇 豆包小姐专属代码 ✍️
// healCustomSafe 带防递归炸弹的 custom 安全版
func (r *Reconciler) healCustom(res Resource) error {
	if res.Fallback == "" {
		slog.Warn("custom 策略但 fallback 为空，跳过", "resource", res.Name)
		return nil
	}
	if res.MaxRetry <= 0 {
		slog.Error("custom 策略重试超限，终止", "resource", res.Name)
		return nil
	}

	// 安全降级，无死循环
	return r.checkAndHeal(Resource{
		Kind:      res.Kind,
		Name:      res.Name,
		Namespace: res.Namespace,
		OnMissing: res.Fallback,
		Fallback:  "",
		MaxRetry:  res.MaxRetry - 1,
		Labels:    res.Labels,
	})
}

// checkAndHeal 检查资源是否存在，缺失时执行自愈
func (r *Reconciler) checkAndHeal(res Resource) error {
	// v2.8 B.5 (D-Level1): RBAC + audit denied 接入
	if !mustCheckController(context.Background(), res.Namespace, rbac.PermHeal, "heal.check") {
		return nil // ENFORCE 模式拒绝, 跳过本次 healing
	}

	exists, err := r.detector.ResourceExists(res.Kind, res.Name, res.Namespace)
	if err != nil {
		return fmt.Errorf("检查资源状态失败: %w", err)
	}

	// 👇 豆包小姐专属注入 ✍️
	// 资源存在 → 加载 Labels
	if exists {
		loaded := r.loadResourceLabels(&res)
		// 这里仅 Deployment 做异常状态检测（OOM / CrashLoop）
		if loaded && strings.ToLower(res.Kind) == "deployment" {
			_ = r.checkAbnormalState(res)
		}
		return nil // 🔥 最关键：防穿透
	}

	slog.Warn("资源缺失，启动自愈",
		"kind", res.Kind, "name", res.Name,
		"namespace", res.Namespace, "strategy", res.OnMissing)

	// 加载标签用于识别 Helm Release
	r.loadResourceLabels(&res)

	// 👇 保持清爽！只做策略分发，不堆臃肿代码
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
	// CRD 资源检测：非标准 K8s 资源走 kubectl apply 而不是 helm rollback
	if isCRDKind(res.Kind) {
		return r.healCRDApply(res)
	}

	releaseName := r.project + "-" + res.Name

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

// 👇 豆包小姐专属实现
// 👑 Author: 豆包小姐
// 🛠  功能：统一命名 + 带 --wait + SSA 冲突重试 + 日志纯净
// healRollback 执行 Helm 回滚至上一个版本，用于控制器自愈
func (r *Reconciler) healRollback(res Resource) error {
	// DryRun 👇
	// if res.DryRun {
	// 	slog.Info("[dry-run] 跳过实际执行", "resource", res.Name)
	// 	return nil
	// }

	// 多标签兜底，彻底稳到底
	releaseName := res.Labels["meta.helm.sh/release-name"]
	if releaseName == "" {
		releaseName = res.Labels["app.kubernetes.io/name"]
	}
	if releaseName == "" {
		releaseName = r.project + "-" + res.Name
	}

	// 查询 Helm 历史
	history, err := r.helm.History(releaseName, res.Namespace)
	if err != nil || len(history) == 0 {
		slog.Info("自愈策略：Helm Release 不存在，尝试重建恢复",
			"release", releaseName,
			"namespace", res.Namespace,
		)
		return r.healRecreate(res)
	}

	latestRevision := history[len(history)-1].Revision

	// 只有一个版本，无法回滚 → 正常降级重建
	if latestRevision <= 1 {
		slog.Info("自愈策略：仅存在初始版本，尝试重建恢复", "release", releaseName, "revision", latestRevision)
		return r.healRecreate(res)
	}

	// 执行回滚
	targetRevision := latestRevision - 1
	slog.Info("执行自愈：Helm 回滚", "release", releaseName, "from", latestRevision, "to", targetRevision)

	// 🔥 修复：Rollback 带 --wait --timeout
	err = r.helm.Rollback(releaseName, res.Namespace, targetRevision)

	// 🔥 修复：SSA 冲突自动重试（和 recreate 对齐）
	if err != nil && isSSAConflict(err.Error()) {
		slog.Warn("SSA 冲突，清理后重试回滚", "release", releaseName)
		clearNamespaceManagedFields(res.Namespace)
		err = r.helm.Rollback(releaseName, res.Namespace, targetRevision)
	}

	if err != nil {
		return fmt.Errorf("rollback 失败: %w", err)
	}

	slog.Info("📊 自愈指标", "resource", res.Name, "strategy", "rollback", "status", "success")

	// 🔥 修复：真实等成功，不乐观同步状态
	r.syncStateRunning("controller: rollback 自愈成功")
	slog.Info("✅ 自愈成功", "strategy", "rollback", "release", releaseName)
	return nil
}

// healScaleDown 缩容到 0（保留资源但停止服务）
func (r *Reconciler) healScaleDown(res Resource) error {
	slog.Info("执行 scale-down", "resource", res.Name, "namespace", res.Namespace)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := executor.GetExecutor().Kubectl(ctx, r.kubeconfig,
		"scale",
		strings.ToLower(res.Kind)+"/"+res.Name,
		"--namespace", res.Namespace,
		"--replicas=0",
	)
	if err != nil {
		return fmt.Errorf("healScaleDown 失败: %w\n%s", err, string(out))
	}
	slog.Info("✅ 已缩容到 0", "resource", res.Name)
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	args := []string{
		"get", "pods",
		"--namespace", namespace,
		"-l", fmt.Sprintf("app=%s", name),
		"-o", "json",
	}

	out, err := executor.GetExecutor().Kubectl(ctx, kubeconfig, args...)
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := executor.GetExecutor().Kubectl(ctx, r.kubeconfig,
		"patch", "deployment", res.Name,
		"--namespace", res.Namespace,
		"--type=merge",
		fmt.Sprintf("--patch=%s", patch),
	)
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := executor.GetExecutor().Kubectl(ctx, kubeconfig,
		"logs", podName,
		"--namespace", namespace,
		"--previous",
		"--tail=50",
	)
	if err != nil {
		return crashUnknown
	}
	return classifyCrashLogs(strings.ToLower(string(out)))
}

// ── rollback 自循环保护 ──────────────────────────────────────────────────────

// rollbackEntry 单 ns 的 rollback 追踪状态。
type rollbackEntry struct {
	cnt    int
	lastAt time.Time
}

// rollbackTracker 按 namespace 追踪 rollback 频率，防止自愈死循环。
//
// 设计：
//   - 同一 ns 连续 rollback >= 3 次后启动指数退避（1min, 2min, 4min…）
//   - 冷却期内到达的 reconcile 直接跳过（不 block worker）
//   - 读路径 (shouldBlock) 用 RLock：20 worker 并发无争用
//   - 写路径 (record / cleanup) 用 Lock
//   - 每小时清理 2h 无活动的 entry，防止 map 无限增长
//   - 全局模式（handleTask 每次 new Reconciler）和 standalone 模式共享同一 tracker
type rollbackTracker struct {
	mu      sync.RWMutex
	entries map[string]rollbackEntry
}

// shouldBlock 判定 ns 是否在 rollback 冷却期内。
// blocked=true 时 remaining 为剩余冷却时间。
func (t *rollbackTracker) shouldBlock(ns string) (blocked bool, remaining time.Duration) {
	t.mu.RLock()
	e, ok := t.entries[ns]
	t.mu.RUnlock()

	if !ok || e.cnt < 3 {
		return false, 0
	}

	// 指数退避：cnt=3 → 1min, cnt=4 → 2min, cnt=5 → 4min …
	backoff := time.Duration(1<<uint(e.cnt-3)) * time.Minute
	if since := time.Since(e.lastAt); since < backoff {
		return true, backoff - since
	}
	return false, 0
}

// record 记录一次成功的 rollback。
func (t *rollbackTracker) record(ns string) {
	t.mu.Lock()
	e := t.entries[ns]
	e.cnt++
	e.lastAt = time.Now()
	t.entries[ns] = e
	t.mu.Unlock()
}

// startCleanup 启动周期性清理 goroutine（仅首次调用生效）。
func (t *rollbackTracker) startCleanup() {
	go func() {
		for {
			time.Sleep(1 * time.Hour)
			t.mu.Lock()
			cutoff := time.Now().Add(-2 * time.Hour)
			for ns, e := range t.entries {
				if e.lastAt.Before(cutoff) {
					delete(t.entries, ns)
				}
			}
			t.mu.Unlock()
		}
	}()
}

// 包级单例，standalone + global 两种模式共享。
var rollbackTk = &rollbackTracker{
	entries: make(map[string]rollbackEntry),
}

func init() {
	rollbackTk.startCleanup()
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
			// rollback 自循环保护：同 ns 连续 3 次 rollback 后指数退避
			if blocked, remaining := rollbackTk.shouldBlock(res.Namespace); blocked {
				slog.Warn("rollback 自循环保护触发，跳过本次 rollback",
					"namespace", res.Namespace, "resource", res.Name,
					"remaining_sec", int(remaining.Seconds()))
				return
			}
			slog.Warn("重启次数 >= 5，触发自动 rollback", "resource", res.Name)
			if err := r.healRollback(res); err != nil {
				slog.Error("自动 rollback 失败", "resource", res.Name, "err", err)
			} else {
				rollbackTk.record(res.Namespace)
			}
		}
	default:
		slog.Warn("CrashLoopBackOff（原因不明）",
			"resource", res.Name, "pod", pod.Name)
	}
}

// ── 辅助函数 ──────────────────────────────────────────────────────────────────

func (r *Reconciler) syncStateRunning(reason string) {
	cur := r.sm.State()
	if cur == state.StateRunning {
		return // 已经是 RUNNING，无需转换
	}

	// Controller 的自愈只负责资源层，不强行推动状态机
	// 状态机应该由 kp deploy 驱动，或由 controller 启动时从 etcd 恢复
	slog.Debug("自愈成功，当前状态机状态非 RUNNING，不强行推动",
		"current", cur, "reason", reason)
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
	exec := executor.GetExecutor()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. 获取资源列表
	out, err := exec.Kubectl(ctx, "get", kind, "-n", namespace, "-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		return fmt.Errorf("list %s failed: %w", kind, err)
	}

	names := strings.Fields(strings.TrimSpace(string(out)))
	if len(names) == 0 {
		return nil
	}

	// 2. 并发执行 Patch (通过 executor 的信号量自动限流)
	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			pCtx, pCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer pCancel()

			_, pErr := exec.Kubectl(pCtx, "patch", kind, n, "-n", namespace,
				"--type=merge", "--patch", `{"metadata":{"managedFields":null}}`)

			if pErr != nil {
				slog.Warn("ManagedFields 清理失败", "kind", kind, "name", n, "err", pErr)
			}
		}(name)
	}
	wg.Wait()
	return nil
}

func runHelmOutput(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return executor.GetExecutor().Helm(ctx, "", args...)
}

func runHelm(args ...string) error {
	out, err := runHelmOutput(args...)
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
	// 👇 豆包小姐专属代码 ✍️
	// --history-max=10: 限制 revision 历史，防止自愈累积导致 helm release secret 撑爆
	return runHelm("rollback", release, fmt.Sprintf("%d", revision),
		"--namespace", namespace,
		"--wait", "--timeout=60s",
		"--history-max", "10",
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

// isCRDKind 判断是否为自定义资源（非标准 K8s 内置资源）
func isCRDKind(kind string) bool {
	builtinKinds := map[string]bool{
		"deployment":  true,
		"statefulset": true,
		"daemonset":   true,
		"replicaset":  true,
		"job":         true,
		"cronjob":     true,
		"pod":         true,
		"service":     true,
		"configmap":   true,
		"secret":      true,
	}
	if kind == "" {
		return false
	}
	return !builtinKinds[strings.ToLower(kind)]
}

// healCRDApply CRD 资源缺失时，尝试从 helm manifest 提取并重新 apply
func (r *Reconciler) healCRDApply(res Resource) error {
	slog.Info("CRD 资源缺失，尝试重新 apply",
		"kind", res.Kind, "name", res.Name, "namespace", res.Namespace)

	projectName := r.project
	releaseName := projectName + "-" + res.Name

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exec := executor.GetExecutor()

	// helm get manifest
	out, err := exec.Helm(ctx, r.kubeconfig,
		"get", "manifest", releaseName,
		"--namespace", res.Namespace,
	)
	if err != nil || len(out) == 0 {
		slog.Warn("无法获取 helm manifest，跳过 CRD 自愈",
			"release", releaseName, "err", err)
		return nil
	}

	manifest := string(out)
	if !strings.Contains(manifest, "kind: "+res.Kind) {
		slog.Warn("manifest 中未找到该 CRD，告警等待人工",
			"kind", res.Kind, "name", res.Name)
		return nil
	}

	// kubectl apply -f -（需要 stdin，用 CmdKubectl）
	cmd := exec.CmdKubectl(ctx, r.kubeconfig,
		"apply", "-f", "-",
		"--namespace", res.Namespace,
	)
	cmd.Stdin = strings.NewReader(manifest)
	applyOut, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("CRD 自愈 apply 失败: %w\n%s", err, applyOut)
	}

	slog.Info("✅ CRD 自愈成功（kubectl apply）",
		"kind", res.Kind, "name", res.Name)
	return nil
}
