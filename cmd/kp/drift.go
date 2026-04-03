package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Ixecd/kubepivot/internal/controller"
	"github.com/Ixecd/kubepivot/internal/planner"
)

// DriftLevel 漂移级别
type DriftLevel int

const (
	DriftHard     DriftLevel = iota // ❌ kp 拥有所有权，将强制同步
	DriftManaged                    // ⚠️ 豁免字段，透明展示
	DriftExternal                   // ℹ️ 外部注入，完全忽略
)

// kp 声明所有权的字段前缀
var kubepivotOwnedFields = []string{
	"spec.template.spec.containers",
	"spec.replicas",
}

// 豁免字段（不强制同步，但透明展示）
var exemptedFields = []string{
	"spec.replicas", // 可能由 HPA 管理
}

// classifyDriftField 判断漂移字段的级别
func classifyDriftField(field string, managers []string) DriftLevel {
	// 是否由外部 manager 管理（Istio、HPA 等）
	for _, m := range managers {
		if m != "kubepivot" && m != "helm" {
			// 外部 manager 持有的字段
			return DriftExternal
		}
	}
	// 是否豁免字段
	for _, f := range exemptedFields {
		if strings.Contains(field, f) {
			return DriftManaged
		}
	}
	// kp 拥有所有权
	for _, f := range kubepivotOwnedFields {
		if strings.Contains(field, f) {
			return DriftHard
		}
	}
	return DriftExternal
}

// driftResult 单个服务的漂移检测结果
type driftResult struct {
	service  string
	hard     []string // 硬冲突：kp 拥有所有权
	managed  []string // 受控偏离：豁免字段
	clean    bool     // 无漂移
	exempted []string // 豁免字段（no-sync-fields）
	err      error
}

// runDriftCheck 检测所有服务的配置漂移
func runDriftCheck(cfg *deployConfig, root string, env map[string]string, serviceFilter string) {
	// 检查 helm-diff 插件
	if _, err := exec.Command("helm", "plugin", "list").Output(); err != nil {
		P.Fail("helm 不可用")
		return
	}
	out, _ := exec.Command("helm", "plugin", "list").Output()
	if !strings.Contains(string(out), "diff") {
		P.Fail("helm-diff 插件未安装，运行：helm plugin install https://github.com/databus23/helm-diff")
		return
	}

	// 加载 resources.yaml 获取 no-sync-fields 配置
	resCfg, _ := controller.LoadResources(
		filepath.Join(root, "configs", "resources.yaml"),
	)
	noSyncFields := func(svcName string) []string {
		if resCfg == nil {
			return nil
		}
		for _, res := range resCfg.Resources {
			if res.Name == svcName || strings.HasSuffix(res.Name, "-"+svcName) {
				return res.NoSyncFields
			}
		}
		return nil
	}

	plans, err := planner.BuildPlan(filepath.Join(root, "configs", "components.yaml"))
	if err != nil {
		P.Fail(fmt.Sprintf("解析 components.yaml 失败: %v", err))
		return
	}

	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))

	P.Info("🔍", "检测配置漂移（--field-manager=kubepivot）")
	fmt.Println()

	var results []driftResult
	for _, plan := range plans {
		if plan.Image == "" {
			continue
		}
		if serviceFilter != "" && plan.Name != serviceFilter {
			continue
		}
		release := releaseName(projectName, plan.Name)
		chartPath := filepath.Join(root, "deployments", projectName, plan.Name)

		// 蓝绿服务：检测两个 slot 的实际活跃 release
		if plan.Strategy == "blue-green" {
			for _, slot := range []string{"blue", "green"} {
				slotRelease := release + "-" + slot
				if helmReleaseExists(cfg.kubeconfig, cfg.context, cfg.namespace, slotRelease) {
					r := detectServiceDrift(cfg, slotRelease, chartPath, env, plan, noSyncFields(plan.Name))
					r.service = fmt.Sprintf("%s (%s slot)", plan.Name, slot)
					results = append(results, r)
				}
			}
			continue
		}

		r := detectServiceDrift(cfg, release, chartPath, env, plan, noSyncFields(plan.Name))
		r.service = plan.Name
		results = append(results, r)
	}

	// 输出结果
	hasHard := false
	for _, r := range results {
		if r.err != nil {
			fmt.Printf("  ⏭  %-25s 跳过（%v）\n", r.service, r.err)
			continue
		}
		if r.clean && len(r.hard) == 0 && len(r.managed) == 0 && len(r.exempted) == 0 {
			fmt.Printf("  %s %-25s 无漂移\n", colorize(colorGreen, "✓"), r.service)
			continue
		}

		fmt.Printf("  %s %s\n", colorize(colorCyan, "▶"), r.service)
		for _, h := range r.hard {
			fmt.Printf("    %s %s\n", colorize(colorRed, "❌ 硬冲突："), h)
			hasHard = true
		}
		for _, m := range r.managed {
			fmt.Printf("    %s %s\n", colorize(colorYellow, "⚠️  受控偏离："), m)
		}
		for _, e := range r.exempted {
			fmt.Printf("    %s %s\n", colorize(colorGray, "ℹ️  已豁免："), e)
		}
	}

	fmt.Println()
	if hasHard {
		P.Info("💡", "发现硬冲突，运行 kp deploy 或 kp deploy --force-sync 强制对齐")
	} else {
		P.Info("✅", "无硬冲突")
	}
}

// detectServiceDrift 检测单个服务的漂移
func detectServiceDrift(cfg *deployConfig, release, chartPath string, env map[string]string, plan planner.Plan, noSyncFields []string) driftResult {
	var r driftResult

	// 用 helm diff upgrade 对比当前集群状态和 chart 期望状态
	args := []string{
		"helm", "diff", "upgrade", release, chartPath,
		"--namespace", cfg.namespace,

		"--no-hooks",
		"--suppress-secrets",
		"--three-way-merge",
	}
	if cfg.kubeconfig != "" {
		args = append(args, "--kubeconfig", cfg.kubeconfig)
	}
	if cfg.context != "" {
		args = append(args, "--kube-context", cfg.context)
	}

	// 注入 image 等参数（和 deploy 保持一致）
	registryPrefix := envOrDefault(env, "REGISTRY_PREFIX", "")
	arch := envOrDefault(env, "ARCH", "amd64")
	version := envOrDefault(env, "VERSION", "v0.1.0")
	if plan.Image != "" {
		args = append(args,
			"--set", fmt.Sprintf("image.repository=%s", imageRepo(registryPrefix, plan.Image, arch)),
			"--set", fmt.Sprintf("image.tag=%s", version),
		)
	}

	// 蓝绿 slot 参数
	if strings.HasSuffix(release, "-blue") || strings.HasSuffix(release, "-green") {
		slot := "blue"
		if strings.HasSuffix(release, "-green") {
			slot = "green"
		}
		args = append(args,
			"--set", fmt.Sprintf("bluegreen.slot=%s", slot),
			"--set", "bluegreen.skipService=true",
		)
	}

	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil && strings.TrimSpace(string(out)) == "" {
		r.err = fmt.Errorf("helm diff 失败: %v", err)
		return r
	}

	diffOutput := strings.TrimSpace(string(out))
	if diffOutput == "" {
		r.clean = true
		return r
	}

	// 解析 diff 输出，分类漂移级别
	// 解析 diff 输出，分类漂移级别
	// 用 map 收集 from/to，合并显示（如 replicas: 3 → 2）
	fromMap := make(map[string]string) // key → old value
	toMap := make(map[string]string)   // key → new value

	for _, line := range strings.Split(diffOutput, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "+") {
			continue
		}
		isRemove := strings.HasPrefix(trimmed, "-")
		entry := strings.TrimSpace(trimmed[1:])
		if entry == "" {
			continue
		}
		// 过滤纯 key 行（如 "env:" "containers:" 等）
		if strings.HasSuffix(entry, ":") {
			continue
		}
		// 解析 key: value 格式
		parts := strings.SplitN(entry, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if isRemove {
			fromMap[key] = val
		} else {
			toMap[key] = val
		}
	}

	// 合并 from/to，生成漂移条目
	seen := make(map[string]bool)
	for key, fromVal := range fromMap {
		seen[key] = true
		toVal := toMap[key]
		entry := fmt.Sprintf("%s: %s → %s", key, fromVal, toVal)
		
		// 检查 no-sync-fields 豁免
		if isNoSyncEntry(entry, noSyncFields) {
			r.exempted = append(r.exempted, entry)
			continue
		}

		level := classifyDriftKey(key)
		switch level {
		case DriftHard:
			r.hard = append(r.hard, entry)
		case DriftManaged:
			r.managed = append(r.managed, entry)
		}
	}
	for key, toVal := range toMap {
		if seen[key] {
			continue
		}
		entry := fmt.Sprintf("%s: (new) → %s", key, toVal)
		level := classifyDriftKey(key)
		switch level {
		case DriftHard:
			r.hard = append(r.hard, entry)
		case DriftManaged:
			r.managed = append(r.managed, entry)
		}
	}
	return r
}

func classifyDriftKey(key string) DriftLevel {
	lower := strings.ToLower(key)
	if lower == "image" || lower == "env" ||
		lower == "limits" || lower == "requests" ||
		lower == "resources" || lower == "cpu" || lower == "memory" {
		return DriftHard
	}
	if lower == "replicas" {
		return DriftManaged
	}
	return DriftExternal
}

// isNoSyncEntry 检查条目是否在豁免列表里
func isNoSyncEntry(entry string, noSyncFields []string) bool {
	for _, f := range noSyncFields {
		if strings.Contains(strings.ToLower(entry), strings.ToLower(f)) {
			return true
		}
	}
	return false
}
