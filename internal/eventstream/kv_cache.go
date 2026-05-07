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
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"
)

// ─── 缓存条目类型（独立于 scheduler 包，避免循环依赖） ──────────

// PodEntry 调度视角下的 Pod 缓存条目。
// RV 是 etcd ResourceVersion（单调递增），用于双 Cache 一致性校验：
// Put 仅接受 RV >= 当前值的写入，防止旧 Watch 事件覆盖新数据。
type PodEntry struct {
	Namespace string
	Name      string
	NodeName  string
	Phase     string
	Labels    map[string]string
	Requests  ResourceRequest
	RV        int64 // etcd ResourceVersion（0=未初始化/测试数据）
}

// ResourceRequest Pod 资源请求。
type ResourceRequest struct {
	CPU    int64
	Memory int64
	GPU    int64
}

// NodeEntry 调度视角下的 Node 缓存条目。
// RV 同 PodEntry.RV。
type NodeEntry struct {
	Name              string
	AllocatableCPU    int64
	AllocatableMemory int64
	GPU               []GPUEntry
	RV                int64 // etcd ResourceVersion
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
// 读：lock-free（atomic.Value）+ delta RLock 合并
// 写：O(1) delta 写入，PutBulk / ListAll 触发 CoW 合并
// CAP 语义：AP（最终一致）—— Put 立即写 delta，Read 看合并视图，snapshot 定期追赶
type PodCache struct {
	snapshot      atomic.Value         // *podSnapshot (base state, swapped on Flush)
	writeMu       sync.Mutex           // guards snapshot swap (CoW)
	delta         map[string]*PodEntry // pending changes since last snapshot
	deltaMu       sync.RWMutex         // guards delta map (Put takes WLock, Get takes RLock)
	ready         atomic.Bool
	lastHeartbeat atomic.Int64
	generation    atomic.Int64 // 递增计数器，每次写入 +1（供 PoolUtilCache 判断是否需要重算）
	subscribers   []CacheSubscriber
}

// Generation 返回当前写入代数（lock-free，供调用方判断缓存是否 stale）。
func (c *PodCache) Generation() int64 { return c.generation.Load() }

// bumpGen 写入后递增 generation。
func (c *PodCache) bumpGen() { c.generation.Add(1) }

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
	pods   map[string]*PodEntry            // key: "ns/name"
	byNode map[string]map[string]*PodEntry // nodeName → set of pods
	list   []*PodEntry                     // 预缓存切片，ListAll 零分配
}

// NewPodCache 创建空 PodCache。
// ready=false 直到 PutBulk 完成初始填充（区分"空集群"和"未填充"）。
func NewPodCache() *PodCache {
	c := &PodCache{delta: make(map[string]*PodEntry)}
	c.snapshot.Store(&podSnapshot{
		pods:   make(map[string]*PodEntry),
		byNode: make(map[string]map[string]*PodEntry),
		list:   []*PodEntry{},
	})
	return c
}

// IsReady 缓存是否已完成初始填充（lock-free）。
func (c *PodCache) IsReady() bool { return c.ready.Load() }

// Get 单 Pod 读取。先查 delta（最新写入），再查 snapshot（已合并）。
// delta RLock 粒度极细，99% 调用在 snapshot 命中，delta 开销 ≈ 额外一次 map lookup。
func (c *PodCache) Get(ns, name string) (*PodEntry, bool) {
	k := key(ns, name)
	c.deltaMu.RLock()
	if p, ok := c.delta[k]; ok {
		c.deltaMu.RUnlock()
		return p, p != nil // nil = deleted tombstone
	}
	c.deltaMu.RUnlock()

	snap := c.snapshot.Load().(*podSnapshot)
	p, ok := snap.pods[k]
	return p, ok
}

// mergeThreshold triggers async flush when delta exceeds this size.
// Tuning: if pprof shows merge-on-read >5% CPU cycles in delta storm, lower to 100.
const mergeThreshold = 200

// seenPool reuses temporary maps for ListAll merge-on-read.
var seenPool = sync.Pool{New: func() any { return make(map[string]bool, mergeThreshold) }}

// ListAll 返回所有 Pod。delta 为空 → 零分配预缓存 slice。
// delta 非空 → merge-on-read（非阻塞），超过阈值时异步 flush。
// 返回的 slice（merge 路径）由调用方负责回收：eventstream.ReleaseMergeList(list)。
func (c *PodCache) ListAll() []*PodEntry {
	snap := c.snapshot.Load().(*podSnapshot)
	c.deltaMu.RLock()
	nd := len(c.delta)
	if nd == 0 {
		c.deltaMu.RUnlock()
		return snap.list // fast path: pre-built, 0 alloc
	}
	list := make([]*PodEntry, 0, len(snap.list)+nd)
	seen := seenPool.Get().(map[string]bool)
	for k := range seen {
		delete(seen, k)
	}
	for _, p := range snap.list {
		k := key(p.Namespace, p.Name)
		if d, ok := c.delta[k]; ok {
			if d != nil {
				list = append(list, d)
			}
			seen[k] = true
		} else {
			list = append(list, p)
		}
	}
	for k, d := range c.delta {
		if !seen[k] && d != nil {
			list = append(list, d)
		}
	}
	c.deltaMu.RUnlock()

	seenPool.Put(seen)
	if nd > mergeThreshold {
		go c.FlushDelta()
	}
	return list
}

// ListByNode 返回指定节点上的 Pod。delta 非空时 merge-on-read。
func (c *PodCache) ListByNode(nodeName string) []*PodEntry {
	snap := c.snapshot.Load().(*podSnapshot)
	snapPods := snap.byNode[nodeName]

	c.deltaMu.RLock()
	nd := len(c.delta)
	if nd == 0 {
		c.deltaMu.RUnlock()
		result := make([]*PodEntry, 0, len(snapPods))
		for _, p := range snapPods {
			result = append(result, p)
		}
		return result
	}

	// Merge delta with byNode result
	byNodeSet := make(map[string]*PodEntry, len(snapPods))
	for _, p := range snapPods {
		byNodeSet[key(p.Namespace, p.Name)] = p
	}
	for k, d := range c.delta {
		if d == nil {
			delete(byNodeSet, k) // tombstone
		} else if d.NodeName == nodeName {
			byNodeSet[k] = d // add/replace on this node
		} else {
			delete(byNodeSet, k) // moved to another node → remove
		}
	}
	c.deltaMu.RUnlock()

	result := make([]*PodEntry, 0, len(byNodeSet))
	for _, p := range byNodeSet {
		result = append(result, p)
	}
	if nd > mergeThreshold {
		go c.FlushDelta()
	}
	return result
}

// PutBulk 批量写入（ListAll 初始填充 / 410 Gone 重建 / 内存压缩）。
// 先合并 pending delta 再全量替换 snapshot。
func (c *PodCache) PutBulk(pods []*PodEntry) {
	c.FlushDelta()
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
	c.bumpGen()
	c.notifySubscribers(ChangeBulkResync)
}

// Put 单个 Pod 写入（Watch ADDED/MODIFIED 事件），O(1)。
// RV > 0 时做 CAS 校验：拒绝 event.RV < current.RV 的旧事件（Watch 乱序 / 410 Gone 回放）。
// 写入 delta map，全量合并仅在 PutBulk / FlushDelta 触发。
func (c *PodCache) Put(pod *PodEntry, oldNodeName string) {
	k := key(pod.Namespace, pod.Name)

	// RV CAS: reject stale events (only when RV is wired, RV>0)
	if pod.RV > 0 {
		c.deltaMu.RLock()
		if d, ok := c.delta[k]; ok && d.RV >= pod.RV {
			c.deltaMu.RUnlock()
			return // delta has newer
		}
		c.deltaMu.RUnlock()
		if s, ok := c.snapshot.Load().(*podSnapshot).pods[k]; ok && s.RV >= pod.RV {
			return // snapshot has newer
		}
	}

	_, isUpdate := c.Get(pod.Namespace, pod.Name)

	c.deltaMu.Lock()
	c.delta[k] = pod
	c.deltaMu.Unlock()

	c.lastHeartbeat.Store(time.Now().UnixNano())
	c.bumpGen()
	if isUpdate {
		c.notifySubscribers(ChangePodModified, k)
	} else {
		c.notifySubscribers(ChangePodAdded, k)
	}
}

// Delete 删除单个 Pod（Watch DELETED 事件）。CoW 仅拷贝受影响的节点。
// Delete 写入 delta（标记删除），O(1)。
// 被删 Pod 在 FlushDelta 时从 CoW snapshot 移除。
func (c *PodCache) Delete(ns, name, nodeName string) {
	k := key(ns, name)
	c.deltaMu.Lock()
	c.delta[k] = nil // nil = tombstone
	c.deltaMu.Unlock()

	c.lastHeartbeat.Store(time.Now().UnixNano())
	c.bumpGen()
	c.notifySubscribers(ChangePodDeleted, k)
}

// FlushDelta 将 delta 层的所有待定变更合并到 snapshot（一次 CoW）。
// 调用方场景：PutBulk（全量替换前）、ListAll/ListByNode（delta 非空时）。
// 持有 writeMu + deltaMu 双锁，先 writeMu 后 deltaMu 避免死锁。
func (c *PodCache) FlushDelta() {
	c.deltaMu.RLock()
	if len(c.delta) == 0 {
		c.deltaMu.RUnlock()
		return
	}
	c.deltaMu.RUnlock()

	c.writeMu.Lock()
	c.deltaMu.Lock()
	defer c.deltaMu.Unlock()
	defer c.writeMu.Unlock()

	// 二次检查：获取写锁期间可能已被其他 goroutine 刷新
	if len(c.delta) == 0 {
		return
	}

	old := c.snapshot.Load().(*podSnapshot)

	// 拷贝 pods map + 应用 delta
	newPods := copyMap(old.pods)
	newByNode := old.byNode
	byNodeDirty := false

	for k, v := range c.delta {
		if v == nil {
			// tombstone: 删除
			if oldPod, ok := old.pods[k]; ok {
				// 从 byNode 索引移除
				if nodePods, ok2 := newByNode[oldPod.NodeName]; ok2 {
					if !byNodeDirty {
						newByNode = copyByNodeTop(newByNode)
						byNodeDirty = true
					}
					dst := make(map[string]*PodEntry, len(nodePods)-1)
					for pk, pv := range nodePods {
						if pk != k {
							dst[pk] = pv
						}
					}
					if len(dst) > 0 {
						newByNode[oldPod.NodeName] = dst
					} else {
						delete(newByNode, oldPod.NodeName)
					}
				}
				delete(newPods, k)
			}
		} else {
			// 新增或更新
			oldPod, existed := old.pods[k]
			newPods[k] = v

			// 跨节点迁移：从旧索引移除
			if existed && oldPod.NodeName != v.NodeName {
				if nodePods, ok := newByNode[oldPod.NodeName]; ok {
					if !byNodeDirty {
						newByNode = copyByNodeTop(newByNode)
						byNodeDirty = true
					}
					dst := make(map[string]*PodEntry, len(nodePods)-1)
					for pk, pv := range nodePods {
						if pk != k {
							dst[pk] = pv
						}
					}
					if len(dst) > 0 {
						newByNode[oldPod.NodeName] = dst
					} else {
						delete(newByNode, oldPod.NodeName)
					}
				}
			}

			// 加入新节点索引
			if !byNodeDirty {
				newByNode = copyByNodeTop(newByNode)
				byNodeDirty = true
			}
			if newByNode[v.NodeName] == nil {
				newByNode[v.NodeName] = make(map[string]*PodEntry)
			} else {
				src := newByNode[v.NodeName]
				if _, exists := src[k]; !exists {
					dst := make(map[string]*PodEntry, len(src)+1)
					for pk, pv := range src {
						dst[pk] = pv
					}
					newByNode[v.NodeName] = dst
				}
			}
			newByNode[v.NodeName][k] = v
		}
	}

	// 清空 delta
	c.delta = make(map[string]*PodEntry)

	c.snapshot.Store(&podSnapshot{
		pods: newPods, byNode: newByNode, list: buildList(newPods),
	})
	c.bumpGen()
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
	list  []*NodeEntry          // pre-built ListAll result（零拷贝读）
}

// NewNodeCache 创建空 NodeCache。
func NewNodeCache() *NodeCache {
	c := &NodeCache{}
	c.snapshot.Store(&nodeSnapshot{nodes: make(map[string]*NodeEntry), list: nil})
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

// ListAll lock-free 返回所有 Node，零分配零拷贝。
// 返回的切片由 write 路径预构建，CAP 语义：最终一致（与 snapshot 原子交换）。
func (c *NodeCache) ListAll() []*NodeEntry {
	return c.snapshot.Load().(*nodeSnapshot).list
}

// PutBulk 批量写入。
func (c *NodeCache) PutBulk(nodes []*NodeEntry) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	newNodes := make(map[string]*NodeEntry, len(nodes))
	list := make([]*NodeEntry, 0, len(nodes))
	for _, n := range nodes {
		newNodes[n.Name] = n
		list = append(list, n)
	}

	c.snapshot.Store(&nodeSnapshot{nodes: newNodes, list: list})
	c.ready.Store(true)
}

// Put 单个 Node 写入（CoW：拷贝 map + 重建 list）。
func (c *NodeCache) Put(node *NodeEntry) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	old := c.snapshot.Load().(*nodeSnapshot)
	newNodes := copyNodeMap(old.nodes)
	newNodes[node.Name] = node

	c.snapshot.Store(&nodeSnapshot{nodes: newNodes, list: buildNodeList(newNodes)})
}

// Delete 删除 Node（CoW：拷贝 map + 重建 list）。
func (c *NodeCache) Delete(name string) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	old := c.snapshot.Load().(*nodeSnapshot)
	newNodes := copyNodeMap(old.nodes)
	delete(newNodes, name)

	c.snapshot.Store(&nodeSnapshot{nodes: newNodes, list: buildNodeList(newNodes)})
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

func buildNodeList(nodes map[string]*NodeEntry) []*NodeEntry {
	list := make([]*NodeEntry, 0, len(nodes))
	for _, n := range nodes {
		list = append(list, n)
	}
	return list
}

func copyNodeMap(src map[string]*NodeEntry) map[string]*NodeEntry {
	dst := make(map[string]*NodeEntry, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// ─── ShardedPodCache ────────────────────────────────────────────

// ShardedPodCache 按 namespace hash 将 PodCache 分为 N 个分片。
// 写路径：Put/Delete 路由到对应分片，N 路并发（无写互斥）
// 读路径：Get 路由到单分片，ListAll 合并所有分片
// CAP 语义：各分片独立 AP，ListAll 跨分片合并（最终一致）
type ShardedPodCache struct {
	shards []*PodCache
	n      int
}

// NewShardedPodCache 创建 N 路分片 PodCache（默认 N=16，power-of-2 防热点）。
func NewShardedPodCache(n int) *ShardedPodCache {
	if n <= 0 {
		n = 16
	}
	shards := make([]*PodCache, n)
	for i := range shards {
		s := NewPodCache()
		s.ready.Store(true) // 空分片即就绪（数据填充由 PutBulk 负责）
		shards[i] = s
	}
	return &ShardedPodCache{shards: shards, n: n}
}

func (sc *ShardedPodCache) shardIdx(ns string) int {
	h := fnv.New32a()
	h.Write([]byte(ns))
	return int(h.Sum32()) % sc.n
}

// ─── Delegated methods ─────────────────────────────────────────

func (sc *ShardedPodCache) IsReady() bool {
	for _, s := range sc.shards {
		if !s.IsReady() {
			return false
		}
	}
	return true
}

func (sc *ShardedPodCache) Get(ns, name string) (*PodEntry, bool) {
	return sc.shards[sc.shardIdx(ns)].Get(ns, name)
}

func (sc *ShardedPodCache) ListAll() []*PodEntry {
	result := make([]*PodEntry, 0)
	for _, s := range sc.shards {
		result = append(result, s.ListAll()...)
	}
	return result
}

func (sc *ShardedPodCache) ListByNode(nodeName string) []*PodEntry {
	result := make([]*PodEntry, 0)
	for _, s := range sc.shards {
		result = append(result, s.ListByNode(nodeName)...)
	}
	return result
}

func (sc *ShardedPodCache) Put(pod *PodEntry, oldNodeName string) {
	sc.shards[sc.shardIdx(pod.Namespace)].Put(pod, oldNodeName)
}

func (sc *ShardedPodCache) Delete(ns, name, nodeName string) {
	sc.shards[sc.shardIdx(ns)].Delete(ns, name, nodeName)
}

func (sc *ShardedPodCache) PutBulk(pods []*PodEntry) {
	// 按 namespace 分组到各分片
	buckets := make([][]*PodEntry, sc.n)
	for _, p := range pods {
		idx := sc.shardIdx(p.Namespace)
		buckets[idx] = append(buckets[idx], p)
	}
	for i, bucket := range buckets {
		if len(bucket) > 0 {
			sc.shards[i].PutBulk(bucket)
		}
	}
}

func (sc *ShardedPodCache) Heartbeat() {
	for _, s := range sc.shards {
		s.Heartbeat()
	}
}

func (sc *ShardedPodCache) StaleDuration() time.Duration {
	var maxStale time.Duration
	for _, s := range sc.shards {
		if d := s.StaleDuration(); d > maxStale {
			maxStale = d
		}
	}
	return maxStale
}

// Generation 返回所有分片 generation 之和（任何分片变更 → 值变化）。
func (sc *ShardedPodCache) Generation() int64 {
	var sum int64
	for _, s := range sc.shards {
		sum += s.Generation()
	}
	return sum
}

// FlushDelta 刷新所有分片的 delta 到 snapshot。
func (sc *ShardedPodCache) FlushDelta() {
	for _, s := range sc.shards {
		s.FlushDelta()
	}
}

// Subscribe 向所有分片注册订阅者。
func (sc *ShardedPodCache) Subscribe(sub CacheSubscriber) {
	for _, s := range sc.shards {
		s.Subscribe(sub)
	}
}

func (sc *ShardedPodCache) StartStaleWatchdog(ctx context.Context, maxStale time.Duration) {
	for _, s := range sc.shards {
		s.StartStaleWatchdog(ctx, maxStale)
	}
}
