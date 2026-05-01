package eventstream

import (
	"sync"
	"sync/atomic"
)

// Cache 是 Resource 的存储抽象。
//
// 实现：SkeletonCache
//
// 设计要点（详见 docs/design/eventstream-draft.md 第 3 节）：
//
//  1. 三层架构（Hot / Warm / Cold）—— v2.7.0 仅实现 Hot 层
//     Warm / Cold 留接口位，v2.7.1+ 补完
//
//  2. lock-free 读路径：
//     immutable snapshot + atomic.Value
//     读零锁竞争，写时构造新快照原子替换
//
//  3. ns 二级索引：
//     map[namespace]map[name]*Resource
//     List(ns) O(N_ns) 而不是 O(N_total)
//     ListAll() 仍需遍历两层
//
//  4. 单条 Put + 批量 PutBulk 双 API：
//     Put 适合运行时 reconcile 单条更新
//     PutBulk 适合 ColdStart / Resync 批量加载（避免 O(N²) 拷贝）
type Cache interface {
	// Get 按 namespace + name 读取单个 Resource。
	// lock-free 路径，性能极致（< 20ns）。
	Get(ns, name string) (*Resource, bool)

	// List 按 namespace 列出所有 Resource。
	// 通过二级索引直接 map 查找，O(N_ns)。
	List(ns string) []*Resource

	// ListAll 跨 namespace 全量列出。
	// 遍历二级索引，O(N_total)。
	ListAll() []*Resource

	// Put 写入单条 Resource（运行时 reconcile）。
	// 复杂度：O(N_ns) 拷贝目标 ns 的内层 map。
	Put(r *Resource)

	// PutBulk 批量写入（ColdStart / Resync）。
	// 复杂度：O(N+M) 一次性拷贝。
	PutBulk(items []*Resource)

	// Delete 按 ns + name 删除。
	Delete(ns, name string)

	// Stats 返回 Cache 监控指标。
	Stats() CacheStats
}

// CacheStats Cache 监控指标。
//
// 用于 Prometheus 暴露：
//   - kubepivot_cache_size_total
//   - kubepivot_cache_size_by_layer{layer=hot|warm|cold}
//   - kubepivot_cache_namespaces_total
type CacheStats struct {
	TotalItems     int // 全部对象数
	NamespaceCount int // 含资源的 ns 数
	ItemsByLayer   map[CacheLayer]int
	BytesEstimated uint64 // 估算内存占用
}

// SkeletonCache 是 Cache 接口的标准实现。
//
// 当前 v2.7.0 仅实现 Hot 层（lock-free immutable snapshot）。
// Warm / Cold 层在 v2.7.1+ 补完。
type SkeletonCache struct {
	// snapshot 存储当前 Cache 快照
	// 类型：*cacheSnapshot
	// 读：snapshot.Load()
	// 写：snapshot.Store(newSnapshot)
	snapshot atomic.Value

	// writeMu 写者间互斥
	// 读者完全 lock-free，不持有此锁
	writeMu sync.Mutex
}

// cacheSnapshot 是不可变的 Cache 快照。
//
// 写时构造新 snapshot，原子替换 root pointer。
// 旧 snapshot 由读者持有的引用保活，GC 自然回收。
type cacheSnapshot struct {
	// items 二级索引：map[namespace]map[name]*Resource
	//
	// 优势：
	//   List(ns) 直接 items[ns]，O(N_ns)
	//   不需要遍历过滤
	//
	// 代价：
	//   Get 多一次 map 查找（~5-10ns）
	//   Put 内层 map 拷贝（O(N_ns)）
	//   总体 trade-off 划算
	items map[string]map[string]*Resource

	size int // 总数缓存
}

// NewCache 创建一个空的 SkeletonCache 实例。
func NewCache() *SkeletonCache {
	c := &SkeletonCache{}
	c.snapshot.Store(&cacheSnapshot{items: make(map[string]map[string]*Resource)})
	return c
}

// Get lock-free 读取。
//
// 性能（基于 Day 1 benchmark）：
//
//	~14.5ns / 4B / 1 alloc
//	vs client-go cache.Indexer ~45ns
func (c *SkeletonCache) Get(ns, name string) (*Resource, bool) {
	snap := c.snapshot.Load().(*cacheSnapshot)
	nsItems, ok := snap.items[ns]
	if !ok {
		return nil, false
	}
	r, ok := nsItems[name]
	return r, ok
}

// List 按 namespace 列出。
//
// 性能（基于 Day 1 benchmark）：
//
//	1000 items 单 ns: ~147ns / 160B
//	vs client-go: ~6800ns
//	46x 提升（业务场景核心优势）
//
// 当 ns 为空字符串时，等同 ListAll。
func (c *SkeletonCache) List(ns string) []*Resource {
	if ns == "" {
		return c.ListAll()
	}

	snap := c.snapshot.Load().(*cacheSnapshot)
	nsMap, ok := snap.items[ns]
	if !ok {
		return nil
	}
	result := make([]*Resource, 0, len(nsMap))
	for _, r := range nsMap {
		result = append(result, r)
	}
	return result
}

// ListAll 全量列出（跨 namespace）。
//
// 性能（cache 内 size 计数器，省去第一遍遍历）：
//
//	1000 items: ~7500ns / 8KB
//	vs client-go: ~7300ns
//	持平（内存仍 2x 优势）
func (c *SkeletonCache) ListAll() []*Resource {
	snap := c.snapshot.Load().(*cacheSnapshot)

	result := make([]*Resource, 0, snap.size)
	for _, nsMap := range snap.items {
		for _, r := range nsMap {
			result = append(result, r)
		}
	}
	return result
}

// Put 写入单条 Resource。
//
// 实现：immutable snapshot 模式
//  1. 加载当前 snapshot
//  2. 构造新 snapshot（外层 map 浅拷贝 + 目标 ns 内层 map 深拷贝）
//  3. 原子替换 root pointer
//  4. 旧 snapshot 由读者持有的引用保活
//
// 复杂度：
//
//	外层拷贝 O(N_ns_count)
//	内层拷贝 O(N_target_ns)
//	总体 ≈ O(N_ns)，远小于 List 全量
//
// 注意：批量加载场景（ColdStart）应使用 PutBulk 避免 O(N²)。
func (c *SkeletonCache) Put(r *Resource) {
	if r == nil {
		return
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	old := c.snapshot.Load().(*cacheSnapshot)
	newSize := old.size

	// 拷贝外层 map（namespace 数量通常 ~50-200）
	newItems := make(map[string]map[string]*Resource, len(old.items)+1)
	for ns, nsMap := range old.items {
		if ns == r.Namespace {
			// 目标 ns：拷贝内层 map + 加新对象
			newNsMap := make(map[string]*Resource, len(nsMap)+1)
			for k, v := range nsMap {
				newNsMap[k] = v
			}
			if _, exists := nsMap[r.Name]; !exists {
				newSize++
			}
			newNsMap[r.Name] = r
			newItems[ns] = newNsMap
		} else {
			// 其他 ns：直接复用旧 map（immutable，安全共享）
			newItems[ns] = nsMap
		}
	}

	// ns 不存在则新建
	if _, exists := newItems[r.Namespace]; !exists {
		newItems[r.Namespace] = map[string]*Resource{r.Name: r}
		newSize++
	}

	c.snapshot.Store(&cacheSnapshot{items: newItems, size: newSize})
}

// PutBulk 批量写入。
//
// 用于 ColdStart / Resync 场景：
//   - 一次性导入大量 Resource
//   - 避免 N 次 Put 的 O(N²) 拷贝
//
// 实现：
//  1. 按 ns 分组待写入对象
//  2. 一次性构造新 snapshot
//  3. 已存在的 ns 拷贝旧内层 + 加新；无新增 ns 直接复用
//  4. 原子替换 root pointer
//
// 复杂度：O(N + M)
//
//	N = 现有对象数
//	M = 新增对象数
//
// 性能（基于 Day 1 benchmark）：
//
//	ColdStart 10000 items: ~341ms / 40.7MB / 880K allocs
//	vs client-go: ~645ms / 349MB / 5170K allocs
//	1.9x 时间, 8.6x 内存
func (c *SkeletonCache) PutBulk(items []*Resource) {
	if len(items) == 0 {
		return
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	old := c.snapshot.Load().(*cacheSnapshot)
	newSize := old.size

	// 按 ns 分组待写入对象
	nsGroups := make(map[string][]*Resource)
	for _, r := range items {
		if r == nil {
			continue
		}
		nsGroups[r.Namespace] = append(nsGroups[r.Namespace], r)
	}

	newItems := make(map[string]map[string]*Resource, len(old.items)+len(nsGroups))

	// 处理已存在的 ns
	for ns, nsMap := range old.items {
		if newRes, hasNew := nsGroups[ns]; hasNew {
			// 该 ns 有新增，拷贝旧 + 加新
			merged := make(map[string]*Resource, len(nsMap)+len(newRes))
			for k, v := range nsMap {
				merged[k] = v
			}
			for _, r := range newRes {
				if _, exists := nsMap[r.Name]; !exists {
					newSize++
				}
				merged[r.Name] = r
			}
			newItems[ns] = merged
			delete(nsGroups, ns)
		} else {
			// 该 ns 无新增，直接复用旧 map
			newItems[ns] = nsMap
		}
	}

	// 处理新增的 ns
	for ns, newRes := range nsGroups {
		nsMap := make(map[string]*Resource, len(newRes))
		for _, r := range newRes {
			nsMap[r.Name] = r
		}
		newItems[ns] = nsMap
		newSize += len(nsMap)
	}

	c.snapshot.Store(&cacheSnapshot{items: newItems, size: newSize})
}

// Delete 删除指定 ns + name 的 Resource。
//
// 复杂度：O(N_ns) 拷贝目标 ns 的内层 map。
//
// 不存在时静默返回（不报错）。
func (c *SkeletonCache) Delete(ns, name string) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	old := c.snapshot.Load().(*cacheSnapshot)

	// 检查 ns 是否存在
	nsMap, ok := old.items[ns]
	if !ok {
		return
	}

	// 检查 name 是否存在
	if _, exists := nsMap[name]; !exists {
		return
	}

	// 构造新 snapshot
	newItems := make(map[string]map[string]*Resource, len(old.items))
	for n, m := range old.items {
		if n == ns {
			// 目标 ns：拷贝内层 + 删除目标
			newNsMap := make(map[string]*Resource, len(m)-1)
			for k, v := range m {
				if k != name {
					newNsMap[k] = v
				}
			}
			// 如果删完 ns 为空，跳过该 ns（不留空 map）
			if len(newNsMap) > 0 {
				newItems[n] = newNsMap
			}
		} else {
			newItems[n] = m
		}
	}

	c.snapshot.Store(&cacheSnapshot{items: newItems, size: old.size - 1})
}

// Stats 返回当前 Cache 监控指标。
//
// v2.7.0 仅 Hot 层有数据，Warm / Cold 计数为 0。
func (c *SkeletonCache) Stats() CacheStats {
	snap := c.snapshot.Load().(*cacheSnapshot)

	stats := CacheStats{
		TotalItems:     snap.size,
		NamespaceCount: len(snap.items),
		ItemsByLayer:   make(map[CacheLayer]int),
	}

	var bytesEst uint64
	for _, nsMap := range snap.items {
		for _, r := range nsMap {
			stats.ItemsByLayer[r.cacheLayer]++
			// 估算：Skeleton 字段 ~200B + RawJSON 长度
			bytesEst += 200 + uint64(len(r.RawJSON))
		}
	}
	stats.BytesEstimated = bytesEst

	return stats
}
