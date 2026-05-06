// internal/scheduler/kubectl_adapter.go
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Ixecd/kubepivot/internal/executor"
	"github.com/Ixecd/kubepivot/internal/metrics"
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
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Status struct {
		Allocatable struct {
			CPU    string `json:"cpu"`
			Memory string `json:"memory"`
			GPU    string `json:"nvidia.com/gpu,omitempty"`
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
		node := &NodeInfo{
			Name:              item.Metadata.Name,
			AllocatableCPU:    cpu,
			AllocatableMemory: mem,
		}
		// v3.1: 解析 GPU 资源
		if gpuStr := item.Status.Allocatable.GPU; gpuStr != "" {
			gpuCount, _ := parseGPUCount(gpuStr)
			node.GPU = parseGPUNodeLabels(item.Metadata.Labels, gpuCount)
		}
		nodes = append(nodes, node)
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
					GPU    string `json:"nvidia.com/gpu,omitempty"`
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
		var totalCPU, totalMem, totalGPU int64
		for _, c := range item.Spec.Containers {
			cpu, _ := parseCPU(c.Resources.Requests.CPU)
			mem, _ := parseMemory(c.Resources.Requests.Memory)
			gpu, _ := parseGPUCount(c.Resources.Requests.GPU)
			totalCPU += cpu
			totalMem += mem
			totalGPU += gpu
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
				GPU:    totalGPU,
			},
		})
	}
	return pods, nil
}

// ─── 资源解析适配层（v3.1） ─────────────────────────────────────
// kubectlAdapter 的 parseCPU / parseMemory 原为 Phase 1 临时实现。
// v3.1 统一到 internal/metrics/quantity.go 的公开函数 ParseCPU / ParseMemory / ParseGPUCount，
// 此处保留薄封装以保持 (int64, error) 返回签名，避免修改所有调用方。

func parseCPU(raw string) (int64, error) {
	q, err := metrics.ParseCPU(raw)
	if err != nil {
		return 0, err
	}
	return q.Value, nil
}

func parseMemory(raw string) (int64, error) {
	q, err := metrics.ParseMemory(raw)
	if err != nil {
		return 0, err
	}
	return q.Value, nil
}

func parseGPUCount(raw string) (int64, error) {
	q, err := metrics.ParseGPUCount(raw)
	if err != nil {
		return 0, err
	}
	return q.Value, nil
}

// parseGPUNodeLabels 从 Node Labels 提取 GPU 设备信息。
// NVIDIA GPU Operator 在节点上打的标签：
//
//	nvidia.com/gpu.product       → "NVIDIA-A100-SXM4-40GB"
//	nvidia.com/gpu.count         → "8"
//	nvidia.com/gpu.memory        → "40960" (MiB)
//	nvidia.com/gpu.nvswitch      → "true" (有 NVSwitch)
func parseGPUNodeLabels(labels map[string]string, gpuCount int64) []GPUInfo {
	if gpuCount <= 0 {
		return nil
	}
	product := labels["nvidia.com/gpu.product"]
	memStr := labels["nvidia.com/gpu.memory"]
	hasNVSwitch := labels["nvidia.com/gpu.nvswitch"] == "true"

	var memTotal int64
	if memStr != "" {
		if m, err := fmt.Sscanf(memStr, "%d", &memTotal); m == 1 && err == nil {
			memTotal *= 1024 * 1024 // MiB → bytes
		}
	}

	gpus := make([]GPUInfo, 0, gpuCount)
	for i := int64(0); i < gpuCount; i++ {
		domain := 0
		if hasNVSwitch {
			// 简化：每 4 个 GPU 一个 NVSwitch domain（A100 典型配置）
			domain = int(i / 4)
		}
		gpus = append(gpus, GPUInfo{
			Product:      product,
			Index:        int(i),
			MemTotal:     memTotal,
			Health:       "Healthy",
			NVLinkDomain: domain,
		})
	}
	return gpus
}
