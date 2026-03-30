package main

import (
	"fmt"
	"strings"
)

// isSSAConflict 检查错误信息是否是 SSA managedFields 冲突
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

// clearAllManagedFields 清除 namespace 下所有 helm 管理资源的 managedFields
// 幂等操作，清多了没有副作用
func clearAllManagedFields(cfg *deployConfig) error {
	// helm 管理的常见资源类型
	kinds := []string{
		"deployment",
		"statefulset",
		"service",
		"configmap",
		"serviceaccount",
		"clusterrole",
		"clusterrolebinding",
		"ingress",
	}

	for _, kind := range kinds {
		if err := clearManagedFieldsByKind(cfg, kind); err != nil {
			// 单个 kind 失败不阻断，继续处理其他
			fmt.Printf("  ⚠ 清除 %s managedFields 失败（跳过）: %v\n", kind, err)
		}
	}
	return nil
}

// clearManagedFieldsByKind 清除某种资源类型下所有资源的 managedFields
func clearManagedFieldsByKind(cfg *deployConfig, kind string) error {
	// 先列出该类型的所有资源名
	listArgs := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	listArgs = append(listArgs,
		"get", kind,
		"--no-headers",
		"-o", "custom-columns=NAME:.metadata.name",
	)

	out, err := runOutput(listArgs...)
	if err != nil {
		// 资源类型不存在或 namespace 下没有，直接跳过
		return nil
	}

	names := strings.Fields(strings.TrimSpace(string(out)))
	if len(names) == 0 {
		return nil
	}

	for _, name := range names {
		if err := clearManagedFields(cfg, kind, name); err != nil {
			fmt.Printf("    ⚠ %s/%s: %v\n", kind, name, err)
		} else {
			fmt.Printf("    ✓ 已清除 %s/%s managedFields\n", kind, name)
		}
	}
	return nil
}

// clearManagedFields 清除单个资源的 managedFields
func clearManagedFields(cfg *deployConfig, kind, name string) error {
	args := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	args = append(args,
		"patch", kind, name,
		"--type=merge",
		"--patch", `{"metadata":{"managedFields":null}}`,
	)
	_, err := runOutput(args...)
	return err
}

// retryDeployWithSSAFix 检测到 SSA 冲突时，清除 managedFields 后重试
func retryDeployWithSSAFix(cfg *deployConfig, makeEnv []string, root string) error {
	fmt.Println("⚠️  检测到 SSA managedFields 冲突，正在自动清除...")
	fmt.Println()

	if err := clearAllManagedFields(cfg); err != nil {
		return fmt.Errorf("清除 managedFields 失败: %w", err)
	}

	fmt.Println()
	fmt.Println("🔄 managedFields 已清除，重试部署...")
	fmt.Println()

	if err := runCmd(root, makeEnv, "make", "deploy.full"); err != nil {
		return fmt.Errorf("重试部署失败（非 SSA 冲突问题，请手动排查）: %w", err)
	}
	return nil
}

// isImagePullError 检测是否是镜像拉取失败
func isImagePullError(errMsg string) bool {
	keywords := []string{
		"ImagePullBackOff",
		"ErrImagePull",
		"image pull",
		"does not exist",
		"manifest unknown",
	}
	for _, kw := range keywords {
		if strings.Contains(errMsg, kw) {
			return true
		}
	}
	return false
}
