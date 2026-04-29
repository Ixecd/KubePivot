// internal/scheduler/kubectl_adapter.go
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Ixecd/kubepivot/internal/executor"
)

// kubectlAdapter 基于 kubectl 命令同时实现 PodLister 和 NodeLister。
// Phase 1：使用 exec.CommandContext 直接调用 kubectl。
// Phase 2：Node 列表可切换为缓存 + 事件驱动刷新，Pod 列表可切换为 Informer.ListAll，
//
//	接口签名不变，仅替换构造函数返回的实现。
type kubectlAdapter struct {
	kubeconfig string
}

func NewKubectlAdapter(kubeconfig string) *kubectlAdapter {
	return &kubectlAdapter{kubeconfig: kubeconfig}
}

func (a *kubectlAdapter) kubectl(ctx context.Context, args ...string) ([]byte, error) {
	// 复用 executor 的全局单例，确保 concurrency limit（sem=5）和一致的二进制路径
	return executor.GetExecutor().Kubectl(ctx, a.kubeconfig, args...)
}

// ─── NodeLister 实现 ───────────────────────────────────────

type nodeJSON struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Status struct {
		Allocatable struct {
			CPU    string `json:"cpu"`
			Memory string `json:"memory"`
		} `json:"allocatable"`
	} `json:"status"`
}

func (a *kubectlAdapter) ListAllNodes(ctx context.Context) ([]*NodeInfo, error) {
	out, err := a.kubectl(ctx, "get", "nodes", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("kubectl get nodes: %w\n%s", err, string(out))
	}

	var list struct {
		Items []nodeJSON `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parse nodes json: %w", err)
	}

	nodes := make([]*NodeInfo, 0, len(list.Items))
	for _, item := range list.Items {
		cpu, err := parseCPU(item.Status.Allocatable.CPU)
		if err != nil {
			return nil, fmt.Errorf("node %s: cpu %q: %w", item.Metadata.Name, item.Status.Allocatable.CPU, err)
		}
		mem, err := parseMemory(item.Status.Allocatable.Memory)
		if err != nil {
			return nil, fmt.Errorf("node %s: memory %q: %w", item.Metadata.Name, item.Status.Allocatable.Memory, err)
		}
		nodes = append(nodes, &NodeInfo{
			Name:              item.Metadata.Name,
			AllocatableCPU:    cpu,
			AllocatableMemory: mem,
		})
	}
	return nodes, nil
}

// ─── PodLister 实现 ───────────────────────────────────────

type podJSON struct {
	Metadata struct {
		Name      string            `json:"name"`
		Namespace string            `json:"namespace"`
		Labels    map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		NodeName   string `json:"nodeName"`
		Containers []struct {
			Resources struct {
				Requests struct {
					CPU    string `json:"cpu"`
					Memory string `json:"memory"`
				} `json:"requests"`
			} `json:"resources"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

func (a *kubectlAdapter) ListAllPods(ctx context.Context) ([]*PodInfo, error) {
	out, err := a.kubectl(ctx, "get", "pods", "-A", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("kubectl get pods -A: %w\n%s", err, string(out))
	}

	var list struct {
		Items []podJSON `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parse pods json: %w", err)
	}

	pods := make([]*PodInfo, 0, len(list.Items))
	for _, item := range list.Items {
		// 汇总所有容器的资源请求
		var totalCPU, totalMem int64
		for _, c := range item.Spec.Containers {
			cpu, _ := parseCPU(c.Resources.Requests.CPU)
			mem, _ := parseMemory(c.Resources.Requests.Memory)
			totalCPU += cpu
			totalMem += mem
		}

		pods = append(pods, &PodInfo{
			Namespace: item.Metadata.Namespace,
			Name:      item.Metadata.Name,
			NodeName:  item.Spec.NodeName,
			Phase:     item.Status.Phase,
			Labels:    item.Metadata.Labels,
			Requests: ResourceRequest{
				CPU:    totalCPU,
				Memory: totalMem,
			},
		})
	}
	return pods, nil
}

// ─── 临时资源解析（Phase 1） ──────────────────────────────
// 后续可直接引用 internal/metrics/quantity.go 的公开函数
// 消除此处的重复实现。当前先用简单 Scanf 覆盖核心格式。

func parseCPU(raw string) (int64, error) {
	if raw == "" {
		return 0, fmt.Errorf("empty cpu value")
	}
	// "10" → 10 cores → 10000m
	if raw[len(raw)-1] == 'm' {
		var v int64
		_, err := fmt.Sscanf(raw, "%dm", &v)
		return v, err
	}
	var v float64
	_, err := fmt.Sscanf(raw, "%f", &v)
	return int64(v * 1000), err
}

func parseMemory(raw string) (int64, error) {
	if raw == "" {
		return 0, fmt.Errorf("empty memory value")
	}

	// 二进制单位 (IEC)
	if len(raw) >= 2 && raw[len(raw)-2:] == "Ki" {
		var v int64
		_, err := fmt.Sscanf(raw, "%dKi", &v)
		return v * 1024, err
	}
	if len(raw) >= 2 && raw[len(raw)-2:] == "Mi" {
		var v int64
		_, err := fmt.Sscanf(raw, "%dMi", &v)
		return v * 1024 * 1024, err
	}
	if len(raw) >= 2 && raw[len(raw)-2:] == "Gi" {
		var v int64
		_, err := fmt.Sscanf(raw, "%dGi", &v)
		return v * 1024 * 1024 * 1024, err
	}
	if len(raw) >= 2 && raw[len(raw)-2:] == "Ti" {
		var v int64
		_, err := fmt.Sscanf(raw, "%dTi", &v)
		return v * 1024 * 1024 * 1024 * 1024, err
	}

	// 十进制单位 (SI) — K8s 中不常用但合法
	if raw[len(raw)-1] == 'k' || raw[len(raw)-1] == 'K' {
		var v float64
		_, err := fmt.Sscanf(raw, "%fK", &v)
		return int64(v * 1000), err
	}
	if raw[len(raw)-1] == 'M' && (len(raw) < 2 || raw[len(raw)-2] != 'i') {
		var v float64
		_, err := fmt.Sscanf(raw, "%fM", &v)
		return int64(v * 1000 * 1000), err
	}
	if raw[len(raw)-1] == 'G' && (len(raw) < 2 || raw[len(raw)-2] != 'i') {
		var v float64
		_, err := fmt.Sscanf(raw, "%fG", &v)
		return int64(v * 1000 * 1000 * 1000), err
	}

	// 纯数字，默认单位为字节
	var v int64
	_, err := fmt.Sscanf(raw, "%d", &v)
	return v, err
}
