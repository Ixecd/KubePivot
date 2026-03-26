package controller

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/Ixecd/dev-toolkit/internal/state"
)

// checkAndHeal 检查资源是否存在，缺失时执行自愈并同步状态机
func (r *Reconciler) checkAndHeal(res Resource) error {
	exists, err := DetectResourceExists(r.kubeconfig, res.Namespace, res.Kind, res.Name)
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

// healRecreate 改为用 --reuse-values 重新安装
func (r *Reconciler) healRecreate(res Resource) error {
	releaseName := getenv("PROJECT_NAME", res.Name)

	// 查最新 revision 号
	revision, err := getLatestRevision(releaseName, res.Namespace)
	if err != nil || revision == 0 {
		slog.Warn("查不到 helm release，无法自愈", "release", releaseName, "err", err)
		return nil
	}

	// revision=1 时 target=0，helm rollback to 0 等价于重装，可接受
	target := revision - 1
	slog.Info("执行 helm rollback 恢复资源", "release", releaseName, "from", revision, "to", target)
	if err := runHelm("rollback", releaseName, fmt.Sprintf("%d", target),
		"--namespace", res.Namespace, "--wait",
	); err != nil {
		slog.Error("rollback 执行失败", "release", releaseName, "err", err)
		return fmt.Errorf("自愈失败: %w", err)
	}

	slog.Info("自愈成功，同步状态机", "release", releaseName)
	if err := r.sm.Transition(state.StateRunning, "controller: rollback 自愈成功"); err != nil {
		// 状态机同步失败不阻塞自愈结果，但必须记录
		slog.Error("状态机同步失败", "err", err)
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
