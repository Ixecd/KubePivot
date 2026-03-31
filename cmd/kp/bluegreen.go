package main

import (
	"fmt"

	"github.com/Ixecd/kubepivot/internal/bluegreen"
	"github.com/Ixecd/kubepivot/internal/planner"
)

// deployBlueGreen 蓝绿发布：部署新版本到非活跃 slot，等待 kp promote 切换
func deployBlueGreen(cfg *deployConfig, env map[string]string, plan planner.Plan, root, projectName, version, chartPath string) error {
	etcdEndpoints := env["ETCD_ENDPOINTS"]
	store := bluegreen.NewAutoStore(etcdEndpoints)

	// 读取当前蓝绿状态
	state, err := store.Load(projectName, cfg.namespace, plan.Name)
	if err != nil || state == nil {
		state = bluegreen.DefaultState(version)
	}

	// 新版本部署到非活跃 slot
	targetSlot := state.InactiveSlot()
	targetRelease := fmt.Sprintf("%s-%s-%s", projectName, plan.Name, targetSlot)
	P.Info("🔵", fmt.Sprintf("%s 蓝绿发布：部署到 %s slot（release: %s）", plan.Name, targetSlot, targetRelease))

	// build + push
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
	}

	// helm upgrade 到目标 slot release
	P.Start("⛵", fmt.Sprintf("helm upgrade %s（%s slot）", targetRelease, targetSlot))
	helmArgs := buildHelmArgs(cfg, targetRelease, chartPath, env, plan, version)
	// 加 slot label，方便 Service selector 切换
	helmArgs = append(helmArgs, "--set", fmt.Sprintf("bluegreen.slot=%s", targetSlot))
	helmArgs = append(helmArgs, "--set", "bluegreen.skipService=true")
	if _, err := runOutput(helmArgs...); err != nil {
		P.Fail(fmt.Sprintf("helm upgrade %s 失败", targetRelease))
		return fmt.Errorf("蓝绿部署失败: %w", err)
	}
	P.Done(fmt.Sprintf("helm upgrade %s 完成", targetRelease))

	// rollout status
	if plan.Image != "" {
		P.Start("🔍", fmt.Sprintf("等待 %s rollout", plan.Name))
		rolloutArgs := []string{
			"kubectl", "rollout", "status",
			fmt.Sprintf("deployment/%s", targetRelease),
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
			return fmt.Errorf("rollout 失败: %w", err)
		}
		P.Done(fmt.Sprintf("%s 就绪", plan.Name))
	}

	// 更新蓝绿状态
	if targetSlot == bluegreen.SlotBlue {
		state.BlueTag = version
	} else {
		state.GreenTag = version
	}
	if err := store.Save(projectName, cfg.namespace, plan.Name, state); err != nil {
		P.Info("⚠️ ", fmt.Sprintf("蓝绿状态保存失败: %v", err))
	}

	P.Info("✅", fmt.Sprintf("%s 已部署到 %s slot，运行 kp promote 切换流量", plan.Name, targetSlot))
	return nil
}

// switchServiceSelector 切换 Service selector 到目标 slot
func switchServiceSelector(cfg *deployConfig, serviceName, slot string) error {
	args := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	args = append(args,
		"patch", "service", serviceName,
		"--type=merge",
		"--patch", fmt.Sprintf(`{"spec":{"selector":{"bluegreen-slot":"%s"}}}`, slot),
	)
	_, err := runOutput(args...)
	return err
}

// getActiveSlot 从 Service selector 获取当前活跃 slot
func getActiveSlot(cfg *deployConfig, serviceName string) string {
	args := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	args = append(args,
		"get", "service", serviceName,
		"-o", "jsonpath={.spec.selector.bluegreen-slot}",
	)
	out, err := runOutput(args...)
	if err != nil {
		return bluegreen.SlotBlue // 默认 blue
	}
	return string(out)
}
