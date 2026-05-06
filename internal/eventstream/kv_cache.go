// internal/eventstream/kv_cache.go — v3.2: Informer KV Cache
//
// 将 Informer Hot 层从 Skeleton 升级为完整 PodInfo/NodeInfo，
// 供 scheduler/rescheduler 通过 InformerAdapter 读缓存替代 kubectl 调用。
//
// 与 SkeletonCache 的区别：
//   - 存完整调度字段（CPU/Mem/GPU/Phase/Labels），非 Skeleton 元数据
//   - 加 byNode 二级索引：按 nodeName 快速查找 Pod
//   - 复用 atomic.Value + writeMu 模式（lock-free 读，写间互斥）
package eventstream

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)


// ─── 缓存条目类型（独立于 scheduler 包，避免循环依赖） ──────────

// PodEntry 调度视角下的 Pod 缓存条目。
type PodEntry struct {
	Namespace string
	Name      string
	NodeName  string
	Phase     string
	Labels    map[string]string
	Requests  ResourceRequest
}

// ResourceRequest Pod 资源请求。
type ResourceRequest struct {
	CPU    int64
	Memory int64
	GPU    int64
}

// NodeEntry 调度视角下的 Node 缓存条目。
type NodeEntry struct {
	Name              string
	AllocatableCPU    int64
	AllocatableMemory int64
	GPU               []GPUEntry
}

// GPUEntry 单个 GPU 设备。
type GPUEntry struct {
	Product      string
	Index        int
	MemTotal     int64
	Health       string
	NVLinkDomain int
}

// ─── PodCache ────────────────────────────────────────────────────


// ─── Subscribe ───────────────────────────────────────────────────

// CacheSubscriber 接收缓存变更通知。
type CacheSubscriber interface {
	OnChange(event CacheChangeEvent)
}

// CacheChangeEvent 缓存变更事件。
type CacheChangeEvent struct {
	Type      string   // pod_added | pod_modified | pod_deleted | node_changed | bulk_resync
	Keys      []string // 受影响的 cache key
	Timestamp time.Time
}

// CacheChangeType 常量。
const (
	ChangePodAdded    = "pod_added"
	ChangePodModified = "pod_modified"
	ChangePodDeleted  = "pod_deleted"
	ChangeNodeChanged = "node_changed"
	ChangeBulkResync  = "bulk_resync"
)

// ─── PodCache ────────────────────────────────────────────────────

// PodCache 存储完整 PodEntry 的 KV 缓存。
// 读：lock-free（atomic.Value）
// 写：writeMu 互斥 + 双缓冲指针交换
type PodCache struct {
	snapshot      atomic.Value  // *podSnapshot
	writeMu       sync.Mutex
	ready         atomic.Bool   // 无锁读，防 data race
	lastHeartbeat atomic.Int64  // UnixNano，无锁读写，避免 Heartbeat 与 Put 抢 writeMu
	subscribers   []CacheSubscriber
}

// Heartbeat 记录一次 Watch 心跳（lock-free）。
// 外部 Watch goroutine 每 30s 调用一次，或收到任何 K8s 事件时调用。
func (c *PodCache) Heartbeat() {
	c.lastHeartbeat.Store(time.Now().UnixNano())
}

// StaleDuration 返回距上次 Heartbeat 的时长（lock-free）。
func (c *PodCache) StaleDuration() time.Duration {
	ns := c.lastHeartbeat.Load()
	if ns == 0 {
		return 0
	}
	return time.Since(time.Unix(0, ns))
}

// StartStaleWatchdog 启动后台 goroutine 检测缓存陈旧。
// 若 Heartbeat 超时 > maxStale，强制 ready=false，触发降级 kubectl。
// ticker 间隔 = maxStale/2，确保最坏情况下陈旧时间不超过 maxStale + ticker。
func (c *PodCache) StartStaleWatchdog(ctx context.Context, maxStale time.Duration) {
	tickInterval := maxStale / 2
	if tickInterval < 5*time.Second {
		tickInterval = 5 * time.Second
	}
	if tickInterval > 30*time.Second {
		tickInterval = 30 * time.Second
	}

	go func() {
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if c.StaleDuration() > maxStale {
					c.ready.Store(false)
				}
			}
		}
	}()
}

type podSnapshot struct {
	pods   map[string]*PodEntry           // key: "ns/name"
	byNode map[string]map[string]*PodEntry // nodeName → set of pods
	list   []*PodEntry                    // 预缓存切片，ListAll 零分配
}

// NewPodCache 创建空 PodCache。
func NewPodCache() *PodCache {
	c := &PodCache{}
	c.snapshot.Store(&podSnapshot{
		pods:   make(map[string]*PodEntry),
		byNode: make(map[string]map[string]*PodEntry),
		list:   []*PodEntry{},
	})
	return c
}

// IsReady 缓存是否已完成初始填充（lock-free）。
func (c *PodCache) IsReady() bool { return c.ready.Load() }

// Get lock-free 读取单个 Pod。
func (c *PodCache) Get(ns, name string) (*PodEntry, bool) {
	snap := c.snapshot.Load().(*podSnapshot)
	p, ok := snap.pods[key(ns, name)]
	return p, ok
}

// ListAll lock-free 返回所有 Pod（预缓存切片，零分配）。
func (c *PodCache) ListAll() []*PodEntry {
	return c.snapshot.Load().(*podSnapshot).list
}

// ListByNode lock-free 返回指定节点上的 Pod。
func (c *PodCache) ListByNode(nodeName string) []*PodEntry {
	snap := c.snapshot.Load().(*podSnapshot)
	pods := snap.byNode[nodeName]
	result := make([]*PodEntry, 0, len(pods))
	for _, p := range pods {
		result = append(result, p)
	}
	return result
}

// PutBulk 批量写入（ListAll 初始填充 / 410 Gone 重建 / 内存压缩）。
// 双缓冲：先构造新 snapshot，再指针交换，纳秒级持写锁。
func (c *PodCache) PutBulk(pods []*PodEntry) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	newPods := make(map[string]*PodEntry, len(pods))
	newByNode := make(map[string]map[string]*PodEntry)

	for _, p := range pods {
		k := key(p.Namespace, p.Name)
		newPods[k] = p
		if newByNode[p.NodeName] == nil {
			newByNode[p.NodeName] = make(map[string]*PodEntry)
		}
		newByNode[p.NodeName][k] = p
	}

	list := make([]*PodEntry, 0, len(newPods))
	for _, p := range newPods {
		list = append(list, p)
	}

	c.snapshot.Store(&podSnapshot{pods: newPods, byNode: newByNode, list: list})
	c.ready.Store(true)
	c.lastHeartbeat.Store(time.Now().UnixNano())
	c.notifySubscribers(ChangeBulkResync)
}

// Put 单个 Pod 写入（Watch ADDED/MODIFIED 事件）。
// oldNodeName 仅作为 hint，实际旧节点名从缓存内部读取，不信任调用方。
func (c *PodCache) Put(pod *PodEntry, oldNodeName string) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	old := c.snapshot.Load().(*podSnapshot)
	k := key(pod.Namespace, pod.Name)

	// CoW: 只拷贝 pods map，byNode 共享 old 指针
	newPods := copyMap(old.pods)
	newPods[k] = pod

	// 从缓存内部获取真正的旧节点名（不信任调用方传入的 oldNodeName）
	if oldPod, ok := old.pods[k]; ok {
		oldNodeName = oldPod.NodeName
	}

	// byNode: true CoW — 默认共享 old 指针，首次变更时懒拷贝顶层 map
	newByNode := old.byNode
	topCopied := false
	ensureTopCopy := func() {
		if !topCopied {
			newByNode = copyByNodeTop(newByNode)
			topCopied = true
		}
	}

	// 跨节点迁移：从旧节点索引删除
	if oldNodeName != "" && oldNodeName != pod.NodeName {
		oldNodePods := newByNode[oldNodeName]
		dst := make(map[string]*PodEntry, len(oldNodePods)-1)
		for pk, pv := range oldNodePods {
			if pk != k {
				dst[pk] = pv
			}
		}
		ensureTopCopy()
		if len(dst) > 0 {
			newByNode[oldNodeName] = dst
		} else {
			delete(newByNode, oldNodeName)
		}
	}

	// 确保新节点索引存在（CoW 拷贝）
	src, exists := newByNode[pod.NodeName]
	if !exists {
		ensureTopCopy()
		newByNode[pod.NodeName] = make(map[string]*PodEntry)
	} else {
		capacity := len(src)
		if _, alreadyThere := src[k]; !alreadyThere {
			capacity++
		}
		dst := make(map[string]*PodEntry, capacity)
		for pk, pv := range src {
			dst[pk] = pv
		}
		ensureTopCopy()
		newByNode[pod.NodeName] = dst
	}
	newByNode[pod.NodeName][k] = pod

	snap := &podSnapshot{pods: newPods, byNode: newByNode, list: buildList(newPods)}
	c.snapshot.Store(snap)
	c.lastHeartbeat.Store(time.Now().UnixNano())

	_, existed := old.pods[k]
	if existed {
		c.notifySubscribers(ChangePodModified, k)
	} else {
		c.notifySubscribers(ChangePodAdded, k)
	}
}

// Delete 删除单个 Pod（Watch DELETED 事件）。CoW 仅拷贝受影响的节点。
func (c *PodCache) Delete(ns, name, nodeName string) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	old := c.snapshot.Load().(*podSnapshot)
	k := key(ns, name)

	newPods := copyMap(old.pods)
	delete(newPods, k)

	// CoW: 只拷贝受影响节点的子 map，其余节点复用 old 指针
	newByNode := old.byNode
	if nodePods, ok := newByNode[nodeName]; ok {
		dst := make(map[string]*PodEntry, len(nodePods)-1)
		for pk, pv := range nodePods {
			if pk != k {
				dst[pk] = pv
			}
		}
		newByNode = copyByNodeTop(newByNode)
		if len(dst) > 0 {
			newByNode[nodeName] = dst
		} else {
			delete(newByNode, nodeName)
		}
	}

	snap := &podSnapshot{pods: newPods, byNode: newByNode, list: buildList(newPods)}
	c.snapshot.Store(snap)
	c.lastHeartbeat.Store(time.Now().UnixNano())
	c.notifySubscribers(ChangePodDeleted, k)
}

// Subscribe 注册变更订阅者。
func (c *PodCache) Subscribe(sub CacheSubscriber) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.subscribers = append(c.subscribers, sub)
}

// notifySubscribers 通知所有订阅者（需在 writeMu 保护下调用）。
func (c *PodCache) notifySubscribers(typ string, keys ...string) {
	event := CacheChangeEvent{Type: typ, Keys: keys, Timestamp: time.Now()}
	for _, sub := range c.subscribers {
		sub.OnChange(event)
	}
}

// ─── NodeCache ───────────────────────────────────────────────────

// NodeCache 存储完整 NodeEntry 的 KV 缓存。
type NodeCache struct {
	snapshot atomic.Value // *nodeSnapshot
	writeMu  sync.Mutex
	ready    atomic.Bool
}

type nodeSnapshot struct {
	nodes map[string]*NodeEntry // key: nodeName
}

// NewNodeCache 创建空 NodeCache。
func NewNodeCache() *NodeCache {
	c := &NodeCache{}
	c.snapshot.Store(&nodeSnapshot{nodes: make(map[string]*NodeEntry)})
	return c
}

// IsReady 缓存是否已完成初始填充（lock-free）。
func (c *NodeCache) IsReady() bool { return c.ready.Load() }

// Get lock-free 读取单个 Node。
func (c *NodeCache) Get(name string) (*NodeEntry, bool) {
	snap := c.snapshot.Load().(*nodeSnapshot)
	n, ok := snap.nodes[name]
	return n, ok
}

// ListAll lock-free 返回所有 Node。
func (c *NodeCache) ListAll() []*NodeEntry {
	snap := c.snapshot.Load().(*nodeSnapshot)
	result := make([]*NodeEntry, 0, len(snap.nodes))
	for _, n := range snap.nodes {
		result = append(result, n)
	}
	return result
}

// PutBulk 批量写入。
func (c *NodeCache) PutBulk(nodes []*NodeEntry) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	newNodes := make(map[string]*NodeEntry, len(nodes))
	for _, n := range nodes {
		newNodes[n.Name] = n
	}

	c.snapshot.Store(&nodeSnapshot{nodes: newNodes})
	c.ready.Store(true)
}

// Put 单个 Node 写入。
func (c *NodeCache) Put(node *NodeEntry) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	old := c.snapshot.Load().(*nodeSnapshot)
	newNodes := copyNodeMap(old.nodes)
	newNodes[node.Name] = node

	c.snapshot.Store(&nodeSnapshot{nodes: newNodes})
}

// Delete 删除 Node。
func (c *NodeCache) Delete(name string) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	old := c.snapshot.Load().(*nodeSnapshot)
	newNodes := copyNodeMap(old.nodes)
	delete(newNodes, name)

	c.snapshot.Store(&nodeSnapshot{nodes: newNodes})
}

// ─── helpers ─────────────────────────────────────────────────────

func key(ns, name string) string { return ns + "/" + name }

func buildList(pods map[string]*PodEntry) []*PodEntry {
	list := make([]*PodEntry, 0, len(pods))
	for _, p := range pods {
		list = append(list, p)
	}
	return list
}

// copyByNodeTop 浅拷贝 byNode 顶层 map（CoW：仅在修改节点子 map 时调用）。
func copyByNodeTop(src map[string]map[string]*PodEntry) map[string]map[string]*PodEntry {
	dst := make(map[string]map[string]*PodEntry, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func copyMap(src map[string]*PodEntry) map[string]*PodEntry {
	dst := make(map[string]*PodEntry, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func copyNodeMap(src map[string]*NodeEntry) map[string]*NodeEntry {
	dst := make(map[string]*NodeEntry, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

