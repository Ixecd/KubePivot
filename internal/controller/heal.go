package controller

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// checkAndHeal 检查资源是否存在，缺失时按配置执行自愈
func (r *Reconciler) checkAndHeal(res Resource) error {
	exists, err := resourceExists(res.Kind, res.Name, res.Namespace)
	if err != nil {
		return fmt.Errorf("检查资源状态失败: %w", err)
	}
	if exists {
		return nil
	}

	slog.Warn("资源缺失，启动自愈", "kind", res.Kind, "name", res.Name, "namespace", res.Namespace)

	switch res.OnMissing {
	case "recreate":
		return r.healRecreate(res)
	case "alert":
		slog.Error("资源缺失告警（不自动处理）", "kind", res.Kind, "name", res.Name)
		return nil
	default:
		slog.Warn("未知 on_missing 策略，跳过", "strategy", res.OnMissing)
		return nil
	}
}

// resourceExists 通过 kubectl 检查资源是否存在
// 用命令行而非 client-go，保持和项目其他地方一致，不引入额外依赖
func resourceExists(kind, name, namespace string) (bool, error) {
	args := []string{"kubectl", "get", strings.ToLower(kind), name}
	if namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	cmd := exec.Command(args[0], args[1:]...)
	err := cmd.Run()
	if err != nil {
		// exit code != 0 通常是资源不存在
		return false, nil
	}
	return true, nil
}

// healRecreate 通过 helm upgrade 重建资源，失败后 rollback
func (r *Reconciler) healRecreate(res Resource) error {
	chartDir := os.Getenv("CHART_DIR")
	releaseName := getenv("PROJECT_NAME", res.Name)

	for attempt := 1; attempt <= res.MaxRetry; attempt++ {
		slog.Info("自愈尝试", "attempt", attempt, "max", res.MaxRetry, "release", releaseName)

		if chartDir == "" {
			// pod 内没有 chart 目录，直接跳到 rollback
			slog.Warn("CHART_DIR 未配置，跳过 upgrade，直接 rollback")
			break
		}

		err := runHelm("upgrade", "--install", "--wait", "--force-conflicts",
			releaseName, chartDir,
			"--namespace", res.Namespace,
			"--set", "image.tag="+os.Getenv("VERSION"),
		)
		if err == nil {
			slog.Info("自愈成功", "release", releaseName)
			return nil
		}

		slog.Warn("自愈失败，稍后重试", "attempt", attempt, "err", err)
		time.Sleep(3 * time.Second)
	}

	if res.Fallback == "rollback" {
		return r.fallbackRollback(res)
	}
	return fmt.Errorf("自愈失败，已达最大重试次数 %d", res.MaxRetry)
}

// fallbackRollback 执行 helm rollback 回退到上一个稳定版本
func (r *Reconciler) fallbackRollback(res Resource) error {
	releaseName := getenv("PROJECT_NAME", res.Name)
	slog.Warn("执行 fallback rollback", "release", releaseName)

	if err := runHelm("rollback", releaseName, "--namespace", res.Namespace, "--wait"); err != nil {
		slog.Error("rollback 失败", "release", releaseName, "err", err)
		return fmt.Errorf("rollback 失败: %w", err)
	}

	slog.Info("已回滚到上一个版本", "release", releaseName)
	return nil
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
