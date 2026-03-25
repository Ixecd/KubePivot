package state

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// ResourceType 定义要检查的 K8s 资源类型
type ResourceType struct {
	Kind      string
	Group     string
	Name      string // deployment / statefulset / service 等
	Namespace string
}

// coreResources 定义核心需要检查的资源（可扩展）
var coreResources = []ResourceType{
	{Kind: "Deployment", Group: "apps", Name: "wallet-service"}, // 你的主服务
	{Kind: "Service", Group: "", Name: "wallet-service"},
	{Kind: "StatefulSet", Group: "apps", Name: "postgres"}, // 如果有 postgres
	// 未来加新资源只需在这里加一行
	// {Kind: "Ingress", Group: "networking.k8s.io", Name: "wallet-ingress"},
}

// DetectResourcesState 检查多个关键资源是否存在
func (m *Machine) DetectResourcesState(kubeConfig string) (State, error) {
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

	for _, res := range coreResources {
		exists, err := checkResourceExists(clientset, res)
		if err != nil || !exists {
			// 任意一个核心资源不存在 → 视为异常状态
			if m.IsFirstDeploy() {
				return StateIdle, nil
			}
			return StateCleaning, nil
		}
	}

	// 所有核心资源都存在
	return StateDeploying, nil
}

// checkResourceExists 检查单个资源是否存在
func checkResourceExists(clientset *kubernetes.Clientset, res ResourceType) (bool, error) {
	switch res.Kind {
	case "Deployment":
		_, err := clientset.AppsV1().Deployments(res.Namespace).Get(context.Background(), res.Name, metav1.GetOptions{})
		return err == nil, nil
	case "StatefulSet":
		_, err := clientset.AppsV1().StatefulSets(res.Namespace).Get(context.Background(), res.Name, metav1.GetOptions{})
		return err == nil, nil
	case "Service":
		_, err := clientset.CoreV1().Services(res.Namespace).Get(context.Background(), res.Name, metav1.GetOptions{})
		return err == nil, nil
	// 以后加新类型在这里扩展
	default:
		return false, fmt.Errorf("不支持的资源类型: %s", res.Kind)
	}
}
