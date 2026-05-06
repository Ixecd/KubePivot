package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
)

// ─── KubectlMetricsClient ─────────────────────────────────────────
//
// 基于 kubectl get --raw /apis/metrics.k8s.io/ 实现 MetricsClient。
//
// 优势：
//   - 几乎所有 K8s 集群都有 metrics-server（kubectl top 兜底）
//   - 0 额外配置（依赖 kubeconfig 已有）
//   - 0 额外依赖（不引入 prometheus client）
//
// 限制：
//   - 仅当前快照（无历史趋势）
//   - 采样窗口由 metrics-server 控制（默认 30s）
//   - 单次查询性能 ~100ms（kubectl fork 开销）
//
// v2.7.x 计划：
//   - PrometheusClient 提供历史 + 高分辨率
//   - 调用方按需选择（topology fallback）

// KubectlMetricsClient 通过 kubectl get --raw /apis/metrics.k8s.io/ 实现 MetricsClient。
//
// kubectl 字段是可注入函数，方便测试 mock。
// 生产时默认使用 executor.GetExecutor().Kubectl。
type KubectlMetricsClient struct {
	kubeconfig string

	// kubectl 是 kubectl 子进程调用的注入点。
	// 生产时默认 = executor.GetExecutor().Kubectl
	// 测试时替换为 mock 函数（与项目其他模块同模式：
	//   internal/eventstream/auth.go readTokenFile
	//   internal/controller/informer_pool.go newInformerFunc）
	kubectl func(ctx context.Context, kubeconfig string, args ...string) ([]byte, error)
}

// NewKubectlMetricsClient 创建 client 实例。
//
// kubeconfig 为空时使用默认环境（in-cluster 或 ~/.kube/config）。
// 内部 kubectl 调用走 executor.GetExecutor().Kubectl（共享 KubePivot 全局信号量限流）。
func NewKubectlMetricsClient(kubeconfig string) *KubectlMetricsClient {
	return &KubectlMetricsClient{
		kubeconfig: kubeconfig,
		kubectl:    executor.GetExecutor().Kubectl,
	}
}

// ─── metrics API JSON 结构 ──────────────────────────────────────

// kubectl get --raw /apis/metrics.k8s.io/v1beta1/... 输出格式：
//
//	{
//	  "kind": "PodMetricsList",
//	  "items": [
//	    {
//	      "metadata": {"name": "...", "namespace": "..."},
//	      "timestamp": "2026-04-27T19:00:00Z",
//	      "window": "30s",
//	      "containers": [
//	        {"name": "main", "usage": {"cpu": "10m", "memory": "128Mi"}}
//	      ]
//	    }
//	  ]
//	}

type podMetricsList struct {
	Kind  string         `json:"kind"`
	Items []podMetricsV1 `json:"items"`
}

type podMetricsV1 struct {
	Metadata struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	} `json:"metadata"`
	Timestamp  string                 `json:"timestamp"`
	Window     string                 `json:"window"`
	Containers []containerMetricsJSON `json:"containers"`
}

type containerMetricsJSON struct {
	Name  string `json:"name"`
	Usage struct {
		CPU    string `json:"cpu"`
		Memory string `json:"memory"`
	} `json:"usage"`
}

type nodeMetricsList struct {
	Kind  string          `json:"kind"`
	Items []nodeMetricsV1 `json:"items"`
}

type nodeMetricsV1 struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Timestamp string `json:"timestamp"`
	Window    string `json:"window"`
	Usage     struct {
		CPU    string `json:"cpu"`
		Memory string `json:"memory"`
	} `json:"usage"`
}

// ─── GetPodMetrics ───────────────────────────────────────────────

func (c *KubectlMetricsClient) GetPodMetrics(ctx context.Context, namespace, name string) (*PodMetrics, error) {
	if name == "" {
		return nil, fmt.Errorf("metrics: GetPodMetrics: name required")
	}
	if namespace == "" {
		return nil, fmt.Errorf("metrics: GetPodMetrics: namespace required")
	}

	out, err := c.kubectlTop(ctx, "pod", name, namespace)
	if err != nil {
		return nil, err
	}

	// kubectl top pod <name> -o json 返回单个 PodMetrics（不是 list）
	var pm podMetricsV1
	if err := json.Unmarshal(out, &pm); err != nil {
		return nil, fmt.Errorf("metrics: GetPodMetrics: parse JSON: %w", err)
	}

	return convertPodMetrics(pm)
}

// ─── ListPodMetrics ──────────────────────────────────────────────

func (c *KubectlMetricsClient) ListPodMetrics(ctx context.Context, namespace string) ([]*PodMetrics, error) {
	out, err := c.kubectlTop(ctx, "pod", "", namespace)
	if err != nil {
		return nil, err
	}

	var list podMetricsList
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("metrics: ListPodMetrics: parse JSON: %w", err)
	}

	results := make([]*PodMetrics, 0, len(list.Items))
	for _, pm := range list.Items {
		converted, err := convertPodMetrics(pm)
		if err != nil {
			return nil, fmt.Errorf("metrics: ListPodMetrics: %w", err)
		}
		results = append(results, converted)
	}
	return results, nil
}

// ─── GetNodeMetrics ──────────────────────────────────────────────

func (c *KubectlMetricsClient) GetNodeMetrics(ctx context.Context, name string) (*NodeMetrics, error) {
	if name == "" {
		return nil, fmt.Errorf("metrics: GetNodeMetrics: name required")
	}

	out, err := c.kubectlTop(ctx, "node", name, "")
	if err != nil {
		return nil, err
	}

	var nm nodeMetricsV1
	if err := json.Unmarshal(out, &nm); err != nil {
		return nil, fmt.Errorf("metrics: GetNodeMetrics: parse JSON: %w", err)
	}

	return convertNodeMetrics(nm)
}

// ─── ListNodeMetrics ─────────────────────────────────────────────

func (c *KubectlMetricsClient) ListNodeMetrics(ctx context.Context) ([]*NodeMetrics, error) {
	out, err := c.kubectlTop(ctx, "node", "", "")
	if err != nil {
		return nil, err
	}

	var list nodeMetricsList
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("metrics: ListNodeMetrics: parse JSON: %w", err)
	}

	results := make([]*NodeMetrics, 0, len(list.Items))
	for _, nm := range list.Items {
		converted, err := convertNodeMetrics(nm)
		if err != nil {
			return nil, fmt.Errorf("metrics: ListNodeMetrics: %w", err)
		}
		results = append(results, converted)
	}
	return results, nil
}

// ─── 工具函数 ────────────────────────────────────────────────────

// kubectlTop 通过 kubectl get --raw 调 metrics API 获取指标 JSON。
//
// resource: "pod" / "node"
// name:     具体资源名，空表示 list 所有
// namespace: 仅 pod 有效
//
// 错误处理：
//   - "not found" 错误 → 包装为 ErrNotFound
//   - 其他错误 → 透传
func (c *KubectlMetricsClient) kubectlTop(ctx context.Context, resource, name, namespace string) ([]byte, error) {
	// kubectl top 不支持 -o json（v1.33+），改用 raw metrics API
	var path string
	switch resource {
	case "pod":
		if name != "" {
			path = fmt.Sprintf("/apis/metrics.k8s.io/v1beta1/namespaces/%s/pods/%s", namespace, name)
		} else {
			path = fmt.Sprintf("/apis/metrics.k8s.io/v1beta1/namespaces/%s/pods", namespace)
		}
	case "node":
		if name != "" {
			path = fmt.Sprintf("/apis/metrics.k8s.io/v1beta1/nodes/%s", name)
		} else {
			path = "/apis/metrics.k8s.io/v1beta1/nodes"
		}
	default:
		return nil, fmt.Errorf("metrics: unsupported resource: %s", resource)
	}

	args := []string{"get", "--raw", path}
	out, err := c.kubectl(ctx, c.kubeconfig, args...)
	if err != nil {
		// kubectl 把 "not found" 写到 stderr，error 含 "NotFound" 关键字
		errMsg := err.Error() + " " + string(out)
		if strings.Contains(errMsg, "NotFound") || strings.Contains(errMsg, "not found") {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("metrics: kubectl get --raw %s: %w (output: %s)",
			path, err, truncate(string(out), 200))
	}
	return out, nil
}

// convertPodMetrics 转换 K8s JSON 为内部 PodMetrics。
func convertPodMetrics(raw podMetricsV1) (*PodMetrics, error) {
	containers := make([]ContainerMetrics, 0, len(raw.Containers))
	cpuList := make([]Quantity, 0, len(raw.Containers))
	memList := make([]Quantity, 0, len(raw.Containers))

	for _, ct := range raw.Containers {
		cpu, err := ParseCPU(ct.Usage.CPU)
		if err != nil {
			return nil, fmt.Errorf("container %q CPU: %w", ct.Name, err)
		}
		mem, err := ParseMemory(ct.Usage.Memory)
		if err != nil {
			return nil, fmt.Errorf("container %q Memory: %w", ct.Name, err)
		}
		containers = append(containers, ContainerMetrics{
			Name:   ct.Name,
			CPU:    cpu,
			Memory: mem,
		})
		cpuList = append(cpuList, cpu)
		memList = append(memList, mem)
	}

	timestamp, _ := time.Parse(time.RFC3339, raw.Timestamp)
	window, _ := time.ParseDuration(raw.Window)

	return &PodMetrics{
		Namespace:   raw.Metadata.Namespace,
		Name:        raw.Metadata.Name,
		Containers:  containers,
		TotalCPU:    sumQuantities(cpuList...),
		TotalMemory: sumQuantities(memList...),
		Timestamp:   timestamp,
		Window:      window,
	}, nil
}

// convertNodeMetrics 转换 K8s JSON 为内部 NodeMetrics。
//
// 注意：v2.7.0 不调用 kubectl get node 取 allocatable
// AllocatableCPU / AllocatableMemory 留 v2.7.x 补全。
func convertNodeMetrics(raw nodeMetricsV1) (*NodeMetrics, error) {
	cpu, err := ParseCPU(raw.Usage.CPU)
	if err != nil {
		return nil, fmt.Errorf("node %q CPU: %w", raw.Metadata.Name, err)
	}
	mem, err := ParseMemory(raw.Usage.Memory)
	if err != nil {
		return nil, fmt.Errorf("node %q Memory: %w", raw.Metadata.Name, err)
	}

	timestamp, _ := time.Parse(time.RFC3339, raw.Timestamp)
	window, _ := time.ParseDuration(raw.Window)

	return &NodeMetrics{
		Name:      raw.Metadata.Name,
		CPU:       cpu,
		Memory:    mem,
		Timestamp: timestamp,
		Window:    window,
	}, nil
}

// truncate 截断字符串避免 error 信息过长。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// 编译期接口契约
var _ MetricsClient = (*KubectlMetricsClient)(nil)
