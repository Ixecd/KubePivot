package state

import (
	"context"
	"fmt"
	"log/slog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// DetectActualState 真正去 K8s 查询指定 deployment 是否存在
func (m *Machine) DetectActualState(kubeConfig, deploymentName string) (State, error) {
	// 如果当前状态已经是终态，直接返回
	if m.record.State == StateRunning || m.record.State == StateTerminated {
		return m.record.State, nil
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeConfig)
	if err != nil {
		return m.record.State, fmt.Errorf("加载 kubeconfig 失败: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return m.record.State, fmt.Errorf("创建 k8s client 失败: %w", err)
	}

	// 查询 deployment 是否存在
	_, err = clientset.AppsV1().Deployments(m.record.Namespace).Get(
		context.Background(),
		deploymentName, // ← 关键：使用传入的 deploymentName，而不是 record.Project
		metav1.GetOptions{},
	)
	if err != nil {
		// deployment 不存在 → 可能是首次部署失败或已被清理
		if m.IsFirstDeploy() {
			return StateIdle, nil
		}
		return StateCleaning, nil
	}

	// deployment 存在，但状态机还在 DEPLOYING/VALIDATING → 可能是正在部署
	return StateDeploying, nil
}

// ResumeFromValidating 从 Validating 状态恢复（带合法性检查）
func (m *Machine) ResumeFromValidating(reason string) error {
	if m.record.State != StateValidating {
		return fmt.Errorf("只能从 VALIDATING 状态 resume，当前状态: %s", m.record.State)
	}
	return m.Transition(StateRunning, reason)
}

// ClearSSAConflicts 清理 SSA 冲突（managedFields）—— 待实现
func (m *Machine) ClearSSAConflicts(kubeConfig string) error {
	slog.Warn("SSA 冲突清理功能待实现（可通过 kubectl patch --type=merge 清理 managedFields）")
	return nil
}
