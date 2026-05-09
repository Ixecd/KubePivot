// internal/scheduler/informer_adapter.go — v3.2: Informer KV Cache 适配
//
// InformerAdapter 实现 PodLister 和 NodeLister 接口，
// 从 eventstream PodCache/NodeCache 读取数据，替代 kubectlAdapter 的 kubectl 调用。
//
// 影子降级：cache == nil 或 cache.IsReady() == false 时透传到 kubectlAdapter。
// v3.4: KVCache disabled (config) → cache=nil → 纯 kubectl 路径。

package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Ixecd/kubepivot/internal/eventstream"
)

// ErrCacheNotReady 缓存未就绪或限流中，无法返回数据。
var ErrCacheNotReady = errors.New("informer cache not ready or rate limited")

// InformerAdapter 从 Informer KV Cache 提供 Pod/Node 列表。
// 实现 PodLister + NodeLister 接口，与 kubectlAdapter 可互换。
// cache 未就绪时降级到 kubectlAdapter，降级路径受 rate limit 保护。
type InformerAdapter struct {
	podCache       *eventstream.PodCache
	nodeCache      *eventstream.NodeCache
	fallback       *kubectlAdapter
	lastFallback   time.Time
	fallbackMu     sync.Mutex
	minFallbackGap time.Duration // 降级调用的最小间隔（默认 10s）
}

// NewInformerAdapter 创建 Informer 适配器。
// fallback 为 cache 未填充时的降级路径（通常为 kubectlAdapter）。
func NewInformerAdapter(podCache *eventstream.PodCache, nodeCache *eventstream.NodeCache, fallback *kubectlAdapter) *InformerAdapter {
	return &InformerAdapter{
		podCache:       podCache,
		nodeCache:      nodeCache,
		fallback:       fallback,
		minFallbackGap: 10 * time.Second,
	}
}

// allowFallback 检查降级调用是否超过速率限制。
func (a *InformerAdapter) allowFallback() bool {
	a.fallbackMu.Lock()
	defer a.fallbackMu.Unlock()
	if time.Since(a.lastFallback) < a.minFallbackGap {
		return false
	}
	a.lastFallback = time.Now()
	return true
}

// ListAllPods 从缓存读取 Pod 列表。缓存未就绪或未启用时降级到 kubectl。
// v3.4: podCache=nil (KVCache disabled) → 直接走 fallback。
func (a *InformerAdapter) ListAllPods(ctx context.Context) ([]*PodInfo, error) {
	if a.podCache != nil && a.podCache.IsReady() {
		entries := a.podCache.ListAll()
		return convertPodEntries(entries), nil
	}
	if a.fallback != nil && a.allowFallback() {
		return a.fallback.ListAllPods(ctx)
	}
	return nil, ErrCacheNotReady
}

// ListAllNodes 从缓存读取 Node 列表。缓存未就绪或未启用时降级到 kubectl。
func (a *InformerAdapter) ListAllNodes(ctx context.Context) ([]*NodeInfo, error) {
	if a.nodeCache != nil && a.nodeCache.IsReady() {
		entries := a.nodeCache.ListAll()
		return convertNodeEntries(entries), nil
	}
	if a.fallback != nil && a.allowFallback() {
		return a.fallback.ListAllNodes(ctx)
	}
	return nil, ErrCacheNotReady
}

// ─── 类型转换（eventstream ↔ scheduler） ─────────────────────────

func convertPodEntries(entries []*eventstream.PodEntry) []*PodInfo {
	pods := make([]*PodInfo, len(entries))
	for i, e := range entries {
		pods[i] = &PodInfo{
			Namespace: e.Namespace,
			Name:      e.Name,
			NodeName:  e.NodeName,
			Phase:     e.Phase,
			Labels:    e.LabelsToMap(), // 从 CommonLabels+ExtraLabels 重建 map（深拷贝）
			Requests: ResourceRequest{
				CPU:    e.Requests.CPU,
				Memory: e.Requests.Memory,
				GPU:    e.Requests.GPU,
			},
		}
	}
	return pods
}

func convertNodeEntries(entries []*eventstream.NodeEntry) []*NodeInfo {
	nodes := make([]*NodeInfo, len(entries))
	for i, e := range entries {
		gpus := make([]GPUInfo, len(e.GPU))
		for j, g := range e.GPU {
			gpus[j] = GPUInfo{
				Product:      g.Product,
				Index:        g.Index,
				MemTotal:     g.MemTotal,
				Health:       g.Health,
				NVLinkDomain: g.NVLinkDomain,
			}
		}
		nodes[i] = &NodeInfo{
			Name:              e.Name,
			AllocatableCPU:    e.AllocatableCPU,
			AllocatableMemory: e.AllocatableMemory,
			GPU:               gpus,
		}
	}
	return nodes
}
