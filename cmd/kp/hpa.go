package main

import (
	"fmt"
	"os"

	"github.com/Ixecd/kubepivot/internal/planner"
)

// applyHPA 为服务创建或更新 HPA
func applyHPA(cfg *deployConfig, plan planner.Plan, namespace string) error {
	minReplicas := plan.MinReplicas
	if minReplicas <= 0 {
		minReplicas = 1
	}
	maxReplicas := plan.MaxReplicas
	if maxReplicas < minReplicas {
		maxReplicas = minReplicas * 2
	}

	// deployment 名：蓝绿用 release 名，rolling 用 plan.Name
	deployName := plan.Name

	yaml := fmt.Sprintf(`apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: %s-hpa
  namespace: %s
  labels:
    app: %s
    managed-by: kubepivot
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: %s
  minReplicas: %d
  maxReplicas: %d
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: %d
`, plan.Name, namespace, plan.Name, deployName,
		minReplicas, maxReplicas, plan.TargetCPU)

	// 写临时文件 apply
	f, err := os.CreateTemp("", "kp-hpa-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(yaml); err != nil {
		return err
	}
	f.Close()

	args := kubectlBaseArgs(cfg.kubeconfig, cfg.context, namespace)
	args = append(args, "apply", "-f", f.Name())
	_, err = runOutput(args...)
	return err
}
