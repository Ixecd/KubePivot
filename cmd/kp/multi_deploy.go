package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Ixecd/kubepivot/internal/planner"
	"github.com/Ixecd/kubepivot/internal/state"
)

// deployLayers 按拓扑层级部署所有服务
// 同层并行，层间串行
// 失败时：重试 3 次 → 级联 rollback → 整组 rollback → kp down
func deployLayers(sm *state.Machine, cfg *deployConfig, env map[string]string, layers []planner.Layer, root string) error {
	// secret 存在性检查（只警告，不阻断）
	checkRequiredSecrets(cfg, root)

	// 镜像安全扫描（有 trivy 才跑，没有静默跳过）
	if _, err := runOutput("trivy", "--version"); err == nil {
		runScan([]string{})
	}

	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))
	version := envOrDefault(env, "VERSION", "v0.1.0")

	// 记录已成功部署的 release（用于整组 rollback）
	var deployed []string
	var deployedMu sync.Mutex

	for layerIdx, layer := range layers {
		P.Info("📦", fmt.Sprintf("部署第 %d 层（共 %d 层，%d 个服务）",
			layerIdx+1, len(layers), len(layer)))

		type result struct {
			name string
			err  error
		}
		results := make(chan result, len(layer))
		var wg sync.WaitGroup

		for _, plan := range layer {
			wg.Add(1)
			go func(p planner.Plan) {
				defer wg.Done()
				err := deployService(cfg, env, p, root, projectName, version)
				results <- result{name: p.Name, err: err}
			}(plan)
		}

		wg.Wait()
		close(results)

		// 收集结果
		var failed []string
		for r := range results {
			if r.err != nil {
				P.Fail(fmt.Sprintf("%s 部署失败: %v", r.name, r.err))
				failed = append(failed, r.name)
			} else {
				deployedMu.Lock()
				deployed = append(deployed, releaseName(projectName, r.name))
				deployedMu.Unlock()
			}
		}

		if len(failed) == 0 {
			P.Info("✓ ", fmt.Sprintf("第 %d 层全部就绪", layerIdx+1))
			continue
		}

		// 有失败 → 级联 rollback
		P.Info("⏪", fmt.Sprintf("检测到 %d 个服务失败，开始级联回滚", len(failed)))
		affected := collectAffected(failed, layers)
		P.Info("⏪", fmt.Sprintf("受影响服务（逆序）: %v", affected))

		cascadeOK := true
		for _, svcName := range affected {
			release := releaseName(projectName, svcName)
			if !helmReleaseExists(cfg.kubeconfig, cfg.context, cfg.namespace, release) {
				P.Info("⏭ ", fmt.Sprintf("跳过 rollback %s（未安装）", release))
				continue
			}
			P.Start("⏪", fmt.Sprintf("rollback %s", release))
			if err := helmRollback(cfg.kubeconfig, cfg.context, cfg.namespace, release); err != nil {
				P.Fail(fmt.Sprintf("rollback %s 失败", release))
				cascadeOK = false
			} else {
				P.Done(fmt.Sprintf("rollback %s 完成", release))
			}
		}

		if cascadeOK {
			sm.Transition(state.StateRunning, fmt.Sprintf("级联回滚完成，失败服务：%v", failed))
			return fmt.Errorf("部署失败，已级联回滚：%v", failed)
		}

		// 级联 rollback 也失败 → 整组 rollback
		P.Info("⏪", "级联回滚失败，尝试整组回滚")
		sm.Transition(state.StateRollingBack, "整组回滚")

		fullOK := true
		for i := len(deployed) - 1; i >= 0; i-- {
			release := deployed[i]
			if !helmReleaseExists(cfg.kubeconfig, cfg.context, cfg.namespace, release) {
				P.Info("⏭ ", fmt.Sprintf("跳过 rollback %s（未安装）", release))
				continue
			}
			P.Start("⏪", fmt.Sprintf("整组 rollback %s", release))
			if err := helmRollback(cfg.kubeconfig, cfg.context, cfg.namespace, release); err != nil {
				P.Fail(fmt.Sprintf("rollback %s 失败", release))
				fullOK = false
			} else {
				P.Done(fmt.Sprintf("rollback %s 完成", release))
			}
		}

		if fullOK {
			sm.Transition(state.StateRunning, "整组回滚完成")
			return fmt.Errorf("部署失败，已整组回滚")
		}

		// 整组 rollback 也失败 → kp down
		P.Info("🧹", "整组回滚失败，执行 kp down")
		sm.Transition(state.StateCleaning, "整组回滚失败，执行 down")
		deleteNamespace(cfg.kubeconfig, cfg.context, cfg.namespace)
		sm.Transition(state.StateIdle, "已下线")
		return fmt.Errorf("部署失败且回滚失败，已执行 kp down")
	}

	return nil
}

// deployService 部署单个服务
// - chart 不存在：直接报错，不重试
// - build/push 只做一次
// - helm upgrade + rollout：最多重试 3 次
func deployService(cfg *deployConfig, env map[string]string, plan planner.Plan, root, projectName, version string) error {
	release := releaseName(projectName, plan.Name)
	chartPath := filepath.Join(root, "deployments", projectName, plan.Name)

	// image 为空且 chart 不存在 → CLI 工具，跳过
	if plan.Image == "" {
		if _, err := os.Stat(chartPath); err != nil {
			P.Info("⏭ ", fmt.Sprintf("跳过 %s（CLI 工具，无 chart）", plan.Name))
			return nil
		}
	}

	// chart 必须存在才能继续
	if _, err := os.Stat(chartPath); err != nil {
		return fmt.Errorf("chart 目录不存在：%s\n请运行 kp init 重新生成项目结构，或手动创建 %s", chartPath, chartPath)
	}

	// ── build + push 只做一次 ─────────────────────────────────────────────────
	if plan.Image != "" {
		makeEnv := buildMakeEnvForService(env, cfg, plan)

		P.Start("🏗 ", fmt.Sprintf("构建 %s:%s", plan.Image, version))
		if err := runCmd(root, makeEnv, "make", "deploy.build"); err != nil {
			P.Fail(fmt.Sprintf("构建 %s 失败", plan.Image))
			return fmt.Errorf("构建失败: %w", err)
		}
		P.Done(fmt.Sprintf("构建 %s 完成", plan.Image))

		P.Start("📤", fmt.Sprintf("推送 %s:%s", plan.Image, version))
		if err := runCmd(root, makeEnv, "make", "deploy.push"); err != nil {
			P.Fail(fmt.Sprintf("推送 %s 失败", plan.Image))
			return fmt.Errorf("推送失败: %w", err)
		}

		P.Done(fmt.Sprintf("推送 %s 完成", plan.Image))

		// cosign 签名
		if cfg.sign {
			registryPrefix := envOrDefault(env, "REGISTRY_PREFIX", "")
			arch := envOrDefault(env, "ARCH", "amd64")
			fullImage := buildImageName(registryPrefix, plan.Image, arch, version)
			signImage(fullImage)
		}
	}

	// ── helm upgrade + rollout：最多重试 3 次 ────────────────────────────────
	const maxRetry = 3
	var lastErr error

	for attempt := 1; attempt <= maxRetry; attempt++ {
		if attempt > 1 {
			P.Info("🔄", fmt.Sprintf("%s 重试第 %d/%d 次", plan.Name, attempt, maxRetry))
		}

		P.Start("⛵", fmt.Sprintf("helm upgrade %s", release))
		helmArgs := buildHelmArgs(cfg, release, chartPath, env, plan, version)
		if _, err := runOutput(helmArgs...); err != nil {
			P.Fail(fmt.Sprintf("helm upgrade %s 失败", release))
			lastErr = err
			continue
		}
		P.Done(fmt.Sprintf("helm upgrade %s 完成", release))

		// rollout status（只有 image 不为空的服务需要等待）
		if plan.Image != "" {
			P.Start("🔍", fmt.Sprintf("等待 %s rollout", plan.Name))
			rolloutArgs := []string{
				"kubectl", "rollout", "status",
				fmt.Sprintf("deployment/%s", plan.Name),
				"--namespace", cfg.namespace,
				"--timeout=120s",
			}
			if cfg.kubeconfig != "" {
				rolloutArgs = append(rolloutArgs, "--kubeconfig", cfg.kubeconfig)
			}
			if cfg.context != "" {
				rolloutArgs = append(rolloutArgs, "--context", cfg.context)
			}
			if _, err := runOutput(rolloutArgs...); err != nil {
				P.Fail(fmt.Sprintf("%s rollout 超时", plan.Name))
				// 打印实际错误
				fmt.Fprintf(os.Stderr, "  错误详情: %v\n", err)
				lastErr = err
				continue
			}
			P.Done(fmt.Sprintf("%s 就绪", plan.Name))
		}

		return nil // 成功
	}

	return fmt.Errorf("%s 部署失败（重试 %d 次）: %w", plan.Name, maxRetry, lastErr)
}

// buildHelmArgs 构建 helm upgrade 参数
func buildHelmArgs(cfg *deployConfig, release, chartPath string, env map[string]string, plan planner.Plan, version string) []string {
	registryPrefix := envOrDefault(env, "REGISTRY_PREFIX", "")
	arch := envOrDefault(env, "ARCH", "amd64")

	args := []string{
		"helm", "upgrade", "--install", release, chartPath,
		"--namespace", cfg.namespace,
		"--create-namespace",
		"--wait",
		"--timeout", "120s",
	}
	if cfg.kubeconfig != "" {
		args = append(args, "--kubeconfig", cfg.kubeconfig)
	}
	if cfg.context != "" {
		args = append(args, "--kube-context", cfg.context)
	}
	if plan.Image != "" {
		args = append(args,
			"--set", fmt.Sprintf("image.repository=%s", imageRepo(registryPrefix, plan.Image, arch)),
			"--set", fmt.Sprintf("image.tag=%s", version),
			"--set", fmt.Sprintf("replicaCount=%d", plan.Replicas),
			"--set", fmt.Sprintf("service.port=%d", plan.Port),
		)
	}

	// controller chart 注入 resources.yaml
	resourcesPath := filepath.Join(root(chartPath), "configs", "resources.yaml")
	if _, err := os.Stat(resourcesPath); err == nil {
		args = append(args, "--set-file", fmt.Sprintf("resourcesConfig=%s", resourcesPath))
	}
	return args
}

// root 从 chart 路径推断项目根目录（deployments/{project}/{service} → 上两级）
func root(chartPath string) string {
	return filepath.Dir(filepath.Dir(chartPath))
}

// buildMakeEnvForService 构建单个服务的 make 环境变量
func buildMakeEnvForService(env map[string]string, cfg *deployConfig, plan planner.Plan) []string {
	makeEnv := os.Environ()
	if cfg.namespace != "" {
		makeEnv = append(makeEnv, "KUBE_NAMESPACE="+cfg.namespace)
	}
	if cfg.context != "" {
		makeEnv = append(makeEnv, "KUBE_CONTEXT="+cfg.context)
	}
	if cfg.kubeconfig != "" {
		makeEnv = append(makeEnv, "KUBE_CONFIG="+cfg.kubeconfig)
	}
	makeEnv = append(makeEnv,
		"VERSION="+envOrDefault(env, "VERSION", "v0.1.0"),
		"ARCH="+envOrDefault(env, "ARCH", "amd64"),
		"REGISTRY_PREFIX="+envOrDefault(env, "REGISTRY_PREFIX", ""),
		"IMAGES="+plan.Image,
	)
	return makeEnv
}

// releaseName 生成 helm release 名称：{project}-{service}
func releaseName(project, service string) string {
	return project + "-" + service
}

// collectAffected 收集失败服务和所有下游，返回逆拓扑顺序（用于级联 rollback）
func collectAffected(failed []string, layers []planner.Layer) []string {
	seen := make(map[string]bool)
	var result []string
	for _, f := range failed {
		downstream := planner.Downstream(layers, f)
		for _, d := range downstream {
			if !seen[d] {
				seen[d] = true
				result = append(result, d)
			}
		}
	}
	return result
}

// buildImageName 根据 registry 类型构建镜像名
// ACR 格式：registry.cn-*.aliyuncs.com/ns/image:tag（不拼 arch）
// 其他格式：prefix/image-arch:tag
func buildImageName(registryPrefix, image, arch, version string) string {
	if strings.Contains(registryPrefix, ".aliyuncs.com") {
		return fmt.Sprintf("%s/%s:%s", registryPrefix, image, version)
	}
	return fmt.Sprintf("%s/%s-%s:%s", registryPrefix, image, arch, version)
}

func imageRepo(registryPrefix, image, arch string) string {
	if strings.Contains(registryPrefix, ".aliyuncs.com") {
		return fmt.Sprintf("%s/%s", registryPrefix, image)
	}
	return fmt.Sprintf("%s/%s-%s", registryPrefix, image, arch)
}
