package metrics

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ─── Mock kubectl 函数 ───────────────────────────────────────────
//
// 不 mock executor 全局单例（KubePivot 没有 SetExecutor）
// 改为注入 kubectl 函数到 KubectlMetricsClient.kubectl 字段
// 与 readTokenFile / newInformerFunc 同模式

// mockKubectl 创建一个 mock kubectl 函数 + 调用记录器。
type mockKubectl struct {
	gotArgs []string
	output  []byte
	err     error
}

// fn 返回符合 kubectl 函数签名的闭包。
func (m *mockKubectl) fn() func(ctx context.Context, kubeconfig string, args ...string) ([]byte, error) {
	return func(ctx context.Context, kubeconfig string, args ...string) ([]byte, error) {
		m.gotArgs = args
		return m.output, m.err
	}
}

// newMockClient 创建一个用 mock kubectl 替换默认 executor 的 client。
func newMockClient(mock *mockKubectl) *KubectlMetricsClient {
	c := NewKubectlMetricsClient("")
	c.kubectl = mock.fn() // 替换注入点
	return c
}

// ─── Sample JSON Outputs ─────────────────────────────────────────

const samplePodMetricsJSON = `{
  "kind": "PodMetrics",
  "apiVersion": "metrics.k8s.io/v1beta1",
  "metadata": {
    "name": "myapp-abc",
    "namespace": "default"
  },
  "timestamp": "2026-04-27T19:00:00Z",
  "window": "30s",
  "containers": [
    {"name": "main", "usage": {"cpu": "100m", "memory": "128Mi"}},
    {"name": "sidecar", "usage": {"cpu": "50m", "memory": "64Mi"}}
  ]
}`

const samplePodMetricsListJSON = `{
  "kind": "PodMetricsList",
  "items": [
    {
      "metadata": {"name": "pod-1", "namespace": "default"},
      "timestamp": "2026-04-27T19:00:00Z",
      "window": "30s",
      "containers": [{"name": "main", "usage": {"cpu": "10m", "memory": "32Mi"}}]
    },
    {
      "metadata": {"name": "pod-2", "namespace": "default"},
      "timestamp": "2026-04-27T19:00:00Z",
      "window": "30s",
      "containers": [{"name": "main", "usage": {"cpu": "20m", "memory": "64Mi"}}]
    }
  ]
}`

const sampleNodeMetricsJSON = `{
  "kind": "NodeMetrics",
  "metadata": {"name": "node-1"},
  "timestamp": "2026-04-27T19:00:00Z",
  "window": "30s",
  "usage": {"cpu": "1500m", "memory": "4Gi"}
}`

// ─── GetPodMetrics ───────────────────────────────────────────────

func TestKubectlMetricsClient_GetPodMetrics_Success(t *testing.T) {
	mock := &mockKubectl{output: []byte(samplePodMetricsJSON)}
	client := newMockClient(mock)

	pm, err := client.GetPodMetrics(context.Background(), "default", "myapp-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pm.Namespace != "default" {
		t.Errorf("Namespace=%q", pm.Namespace)
	}
	if pm.Name != "myapp-abc" {
		t.Errorf("Name=%q", pm.Name)
	}
	if len(pm.Containers) != 2 {
		t.Errorf("Containers len=%d, want 2", len(pm.Containers))
	}

	// 聚合验证：100m + 50m = 150m
	if pm.TotalCPU.Value != 150 {
		t.Errorf("TotalCPU=%d, want 150", pm.TotalCPU.Value)
	}
	// 128Mi + 64Mi = 192Mi
	expectedMem := int64(192) * 1024 * 1024
	if pm.TotalMemory.Value != expectedMem {
		t.Errorf("TotalMemory=%d, want %d", pm.TotalMemory.Value, expectedMem)
	}

	// 验证调用参数：走 raw API，不再用 kubectl top
	lastArg := mock.gotArgs[len(mock.gotArgs)-1]
	if !strings.Contains(lastArg, "/apis/metrics.k8s.io/") || !strings.Contains(lastArg, "myapp-abc") {
		t.Errorf("kubectl args 应走 raw API 且含 pod 名: %v", mock.gotArgs)
	}
}

func TestKubectlMetricsClient_GetPodMetrics_NotFound(t *testing.T) {
	mock := &mockKubectl{
		err:    errors.New(`Error from server (NotFound): pods "missing" not found`),
		output: []byte(""),
	}
	client := newMockClient(mock)

	_, err := client.GetPodMetrics(context.Background(), "default", "missing")

	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestKubectlMetricsClient_GetPodMetrics_ParamValidation(t *testing.T) {
	client := NewKubectlMetricsClient("")

	if _, err := client.GetPodMetrics(context.Background(), "", "name"); err == nil {
		t.Error("空 namespace 应报错")
	}
	if _, err := client.GetPodMetrics(context.Background(), "ns", ""); err == nil {
		t.Error("空 name 应报错")
	}
}

// ─── ListPodMetrics ──────────────────────────────────────────────

func TestKubectlMetricsClient_ListPodMetrics_Success(t *testing.T) {
	mock := &mockKubectl{output: []byte(samplePodMetricsListJSON)}
	client := newMockClient(mock)

	results, err := client.ListPodMetrics(context.Background(), "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("results len=%d, want 2", len(results))
	}
	if results[0].Name != "pod-1" || results[1].Name != "pod-2" {
		t.Errorf("names 错误: %q, %q", results[0].Name, results[1].Name)
	}
}

// ─── GetNodeMetrics ──────────────────────────────────────────────

func TestKubectlMetricsClient_GetNodeMetrics_Success(t *testing.T) {
	mock := &mockKubectl{output: []byte(sampleNodeMetricsJSON)}
	client := newMockClient(mock)

	nm, err := client.GetNodeMetrics(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if nm.Name != "node-1" {
		t.Errorf("Name=%q", nm.Name)
	}
	// 1500m
	if nm.CPU.Value != 1500 {
		t.Errorf("CPU=%d, want 1500", nm.CPU.Value)
	}
	// 4Gi
	expectedMem := int64(4) * 1024 * 1024 * 1024
	if nm.Memory.Value != expectedMem {
		t.Errorf("Memory=%d, want %d", nm.Memory.Value, expectedMem)
	}
}

// ─── 接口契约 ────────────────────────────────────────────────────

func TestKubectlMetricsClient_ImplementsInterface(t *testing.T) {
	var _ MetricsClient = (*KubectlMetricsClient)(nil)
}

// ─── 默认 client 行为：kubectl 字段非 nil ────────────────────────

func TestNewKubectlMetricsClient_DefaultKubectl(t *testing.T) {
	client := NewKubectlMetricsClient("")
	if client.kubectl == nil {
		t.Error("默认 kubectl 字段不应为 nil（应绑定到 executor.GetExecutor().Kubectl）")
	}
}

// ─── 工具函数 ────────────────────────────────────────────────────

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 100); got != "hello" {
		t.Errorf("短字符串不应截断, got %q", got)
	}
	if got := truncate("hello world", 5); !strings.HasSuffix(got, "...(truncated)") {
		t.Errorf("长字符串应截断, got %q", got)
	}
}

func contains(slice []string, s string) bool {
	for _, x := range slice {
		if x == s {
			return true
		}
	}
	return false
}
