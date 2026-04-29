// internal/scheduler/sizing_adapter.go
package scheduler

import (
	"context"
	"time"

	"github.com/Ixecd/kubepivot/internal/metrics"
	"github.com/Ixecd/kubepivot/internal/sizing"
)

// sizingAdapter 同时实现 MetricsProvider 和 SizingProvider。
// Phase 1：仅使用 kubectl top 瞬时采样。
// Phase 2：注入 PrometheusClient 后启用历史查询。
type sizingAdapter struct {
	kubectlClient *metrics.KubectlMetricsClient
	promClient    *metrics.PrometheusClient // Phase 2
}

func NewSizingAdapter(kubeconfig string, promBaseURL string) *sizingAdapter {
	adapter := &sizingAdapter{
		kubectlClient: metrics.NewKubectlMetricsClient(kubeconfig),
	}
	if promBaseURL != "" {
		adapter.promClient = metrics.NewPrometheusClient(promBaseURL)
	}
	return adapter
}

func (a *sizingAdapter) GetPodMetrics(ctx context.Context, namespace, pod string) (*metrics.PodMetrics, error) {
	return a.kubectlClient.GetPodMetrics(ctx, namespace, pod)
}

func (a *sizingAdapter) QueryRange(ctx context.Context, cpuQuery, memQuery string, start time.Time, step time.Duration) ([]*metrics.PodMetrics, error) {
	if a.promClient == nil {
		// Prometheus 不可用 → 调度器降级到瞬时采样
		return nil, nil
	}
	// Phase 2：调用 a.promClient.QueryRange(...)
	// 当前先返回 nil，调度器自动 fallback 到 GetPodMetrics
	return nil, nil
}

func (a *sizingAdapter) Compute(ctx context.Context, samples []*metrics.PodMetrics, profile sizing.Profile) (*sizing.Suggestion, error) {
	return sizing.Compute(ctx, samples, profile)
}