package metrics

import (
	"context"
	"time"
)

// ─── MetricsClient 接口 ───────────────────────────────────────────
//
// MetricsClient 抽象 K8s 资源使用率数据源。
//
// 实现类型：
//   - KubectlMetricsClient   通过 kubectl top（v2.7.0 唯一实现）
//   - PrometheusClient       通过 Prometheus API（v2.7.x 计划）
//
// 错误约定：
//   - 资源不存在 → 返回 ErrNotFound
//   - kubectl/network 错误 → 包装原 error 返回
//   - 解析错误 → 包装原 error 返回
//
// 不做缓存：调用方需要自行管理（metrics 数据时间敏感）

// MetricsClient 拉取 K8s 资源使用率。
type MetricsClient interface {
	// GetPodMetrics 拉取单个 Pod 的资源使用率。
	//
	// 返回数据含每个 container 的 CPU / Memory + Pod 级别聚合。
	// 资源不存在时返回 ErrNotFound。
	GetPodMetrics(ctx context.Context, namespace, name string) (*PodMetrics, error)

	// GetNodeMetrics 拉取单个 Node 的资源使用率 + 可分配上限。
	//
	// 资源不存在时返回 ErrNotFound。
	GetNodeMetrics(ctx context.Context, name string) (*NodeMetrics, error)

	// ListPodMetrics 拉取 namespace 下所有 Pod 的指标。
	//
	// namespace 为空表示所有 namespace（需 RBAC）。
	// 空 namespace + 空结果不算 error，返回 []。
	ListPodMetrics(ctx context.Context, namespace string) ([]*PodMetrics, error)

	// ListNodeMetrics 拉取所有 Node 的指标。
	ListNodeMetrics(ctx context.Context) ([]*NodeMetrics, error)
}

// ─── 数据结构 ─────────────────────────────────────────────────────

// PodMetrics 单个 Pod 的资源使用率快照。
//
// 包含每个 container 详细数据 + Pod 级别聚合。
// v2.9 sizing engine 可按需消费 Containers 或 TotalCPU/TotalMemory。
type PodMetrics struct {
	Namespace  string
	Name       string
	Containers []ContainerMetrics

	// TotalCPU / TotalMemory 是所有 container 的聚合
	// 计算方式：sum(container.CPU.Value)、sum(container.Memory.Value)
	// Raw 字段为空（聚合值无原始字符串）
	TotalCPU    Quantity
	TotalMemory Quantity

	// Timestamp 数据采集时刻
	Timestamp time.Time

	// Window 采样窗口（kubectl top 默认 30s）
	// 可能为 0 表示数据源未提供
	Window time.Duration
}

// ContainerMetrics 单个 container 的指标。
type ContainerMetrics struct {
	Name   string
	CPU    Quantity // milli-cores 单位
	Memory Quantity // bytes 单位
}

// NodeMetrics 单个 Node 的资源使用率 + 容量。
//
// CPU / Memory 是当前使用量
// AllocatableCPU / AllocatableMemory 是 Node 上可分配上限
// （v2.7.0 仅 GetNodeMetrics 调 kubectl top node 不含 allocatable，
//   allocatable 字段保留为 0；v2.7.x 补 kubectl get node 调用填充）
type NodeMetrics struct {
	Name string

	CPU    Quantity
	Memory Quantity

	AllocatableCPU    Quantity
	AllocatableMemory Quantity

	Timestamp time.Time
	Window    time.Duration
}
