package controller

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/Ixecd/dev-toolkit/internal/state"
)

// checkAndHeal 检查资源是否存在，缺失时执行自愈并同步状态机
// 这是 controller 和 CLI 共用的核心自愈逻辑
func (r *Reconciler) checkAndHeal(res Resource) error {
	exists, err := r.sm.DetectResourceExists(res.Kind, res.Name)
	if err != nil {
		return fmt.Errorf("检查资源状态失败: %w", err)
	}
	if exists {
		return nil // 资源正常，无需处理
	}

	slog.Warn("资源缺失，启动自愈", "kind", res.Kind, "name", res.Name, "namespace", res.Namespace)

	switch res.OnMissing {
	case "recreate":
		return r.healRecreate(res)
	case "alert":
		slog.Error("资源缺失告警（不自动处理）", "kind", res.Kind, "name", res.Name, "namespace", res.Namespace)
		return nil
	default:
		slog.Warn("未知 on_missing 策略，跳过", "strategy", res.OnMissing, "kind", res.Kind, "name", res.Name)
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

// healRecreate 改为用 --reuse-values 重新安装
func (r *Reconciler) healRecreate(res Resource) error {
	releaseName := getenv("PROJECT_NAME", res.Name)

	// 查最新 revision 号
	revision := getLatestRevision(releaseName, res.Namespace)
	if revision == 0 {
		slog.Warn("查不到 helm release，无法自愈", "release", releaseName)
		return nil
	}

	slog.Info("执行 helm rollback 恢复资源", "release", releaseName, "revision", revision)
	if err := runHelm("rollback", releaseName, fmt.Sprintf("%d", revision),
		"--namespace", res.Namespace, "--wait",
	); err != nil {
		slog.Error("rollback 执行失败", "release", releaseName, "err", err)
		return fmt.Errorf("自愈失败: %w", err)
	}
	slog.Info("自愈成功", "release", releaseName)
	r.sm.Transition(state.StateRunning, "controller: rollback 自愈成功") // 必须同步状态机
	return nil
}

func getLatestRevision(releaseName, namespace string) int {
	out, err := runHelmOutput("history", releaseName,
		"--namespace", namespace, "--max", "1", "--output", "json")
	if err != nil || len(out) == 0 {
		return 0
	}
	var history []struct {
		Revision int `json:"revision"`
	}
	if err := json.Unmarshal(out, &history); err != nil || len(history) == 0 {
		return 0
	}
	return history[0].Revision
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
