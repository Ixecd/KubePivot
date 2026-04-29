// Package scheduler 实现乾枢 (v3.0) 智能调度系统。
//
// 本包通过 adapter.go 中定义的接口与 KubePivot 现有基础设施交互，
// 内核（bin packing、双 DP 协同、重调度）仅依赖本包自己的类型，
// 不直接引用 internal/eventstream、internal/metrics 等具体实现。
//
// 架构分层：
//
//	cmd/kp/deploy.go  ──→  Scheduler 接口
//	cmd/kp/webhook.go ──→  Scheduler 接口
//	controller/...     ──→  (未来) Rescheduler 接口
//
// 数据来源（由 adapter 实现桥接）：
//   - Pod 列表：eventstream.Informer.List / ListAll
//   - Node 列表：kubectl get nodes（当前）→ 未来可由 Node Informer 替换
//   - Pod 指标：metrics.MetricsClient.GetPodMetrics / QueryRange
//   - Sizing 引擎：sizing.Compute
//   - 调度结果写入：deploy_sizing.go 同款的原子写入模式
//
// 每个接口只暴露乾枢需要的最小方法集，其余方法由具体实现保留。
package scheduler

import (
	"context"
	"time"

	"github.com/Ixecd/kubepivot/internal/metrics"
	"github.com/Ixecd/kubepivot/internal/sizing"
)

// ────────────────────────────────────────────────────────────
// 核心数据类型（乾枢自有，与 internal 包解耦）
// ────────────────────────────────────────────────────────────

// NodeInfo 表示调度器视角下的一个 K8s 节点。
// 字段仅包含 bin packing 需要的容量信息，
// 不引入 K8s 完整 Node 对象。
type NodeInfo struct {
	Name             string
	AllocatableCPU   int64 // 毫核 (millicores)
	AllocatableMemory int64 // 字节 (bytes)
}

// PodInfo 表示调度器视角下的一个 Pod。
// NodeName 是 bin packing 的关键字段——调度器需要知道
// 当前 Pod 已经占用了哪个节点的资源，才能计算节点剩余容量。
type PodInfo struct {
	Namespace string
	Name      string
	NodeName  string // ← 关键字段。当前从 kubectl get pod 获取，未来可由 Informer Skeleton 扩展
	Phase     string
	Labels    map[string]string // 用于亲和性/反亲和性判定
	// Requests 是 Pod 当前声明的资源请求。
	// 部署时调度：来自 sizing 引擎的推荐值
	// 运行时重调度：来自集群中实际 running Pod 的 spec.containers[].resources.requests
	Requests ResourceRequest
}

// ResourceRequest 表示一个 Pod 或容器的资源需求。
type ResourceRequest struct {
	CPU    int64 // 毫核
	Memory int64 // 字节
}

// SchedulingPlan 是调度器的一次完整输出。
type SchedulingPlan struct {
	// PodAssignments 是每个 Pod 到目标节点的映射。
	// key = "namespace/name"
	PodAssignments map[string]string
	// SizingSuggestions 是维度 B 对每个 Pod 的资源建议。
	// 仅在部署时调度（模式 3）中填充，运行时重调度不含此字段。
	SizingSuggestions map[string]*sizing.Suggestion
	// Iterations 记录双 DP 协同的实际迭代次数
	Iterations int
	// Converged 表示调度是否正常收敛
	Converged bool
}

// ────────────────────────────────────────────────────────────
// 依赖接口（由 cmd/kp 或 controller 在启动时注入具体实现）
// ────────────────────────────────────────────────────────────

// PodLister 提供 Pod 列表查询。
// 对应：eventstream.Informer.List / ListAll
type PodLister interface {
	// ListAllPods 返回集群中所有 Pod 的调度器视图。
	// 返回空切片表示没有 Pod（不是错误）。
	// 实现方负责从 Informer cache 或 kubectl 获取数据，
	// 并完成 Pod → Node 映射的填充。
	ListAllPods(ctx context.Context) ([]*PodInfo, error)
}

// NodeLister 提供 Node 列表查询。
// 对应：kubectl get nodes（当前）/ 未来 Node Informer
type NodeLister interface {
	// ListAllNodes 返回集群中所有 Node 的调度器视图。
	// 返回空切片表示没有 Node（不应在正常集群中出现）。
	ListAllNodes(ctx context.Context) ([]*NodeInfo, error)
}

// MetricsProvider 提供 Pod 历史指标查询。
// 对应：metrics.MetricsClient（仅 GetPodMetrics / QueryRange 子集）
type MetricsProvider interface {
	// GetPodMetrics 获取单个 Pod 的当前瞬时指标。
	// 用于维度 B sizing 的数据采样。
	GetPodMetrics(ctx context.Context, namespace, pod string) (*metrics.PodMetrics, error)
	// QueryRange 查询 Prometheus 历史窗口数据。
	// 返回的 []*metrics.PodMetrics 直接传给 sizing.Compute。
	QueryRange(ctx context.Context, cpuQuery, memQuery string, start time.Time, step time.Duration) ([]*metrics.PodMetrics, error)
}

// SizingProvider 封装维度 B 的资源优化计算。
// 对应：sizing.Compute
type SizingProvider interface {
	// Compute 计算单个 Pod 的最优资源请求。
	// samples 来自 MetricsProvider 的历史数据。
	// profile 是业务模板（web/batch/db/default）。
	Compute(ctx context.Context, samples []*metrics.PodMetrics, profile sizing.Profile) (*sizing.Suggestion, error)
}

// PlanWriter 将调度结果写回 GitOps 配置。
// 对应：deploy_sizing.go 的原子写入模式
type PlanWriter interface {
	// WriteAssignments 将 Pod 到 Node 的分配结果写入 resources.yaml。
	// path 是 configs/resources.yaml 的完整路径。
	// assignments 是 "namespace/name" → "nodeName" 的映射。
	WriteAssignments(path string, assignments map[string]string) error
}