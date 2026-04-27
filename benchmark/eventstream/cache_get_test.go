package eventstreamench

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/tools/cache"
)

// ═══════════════════════════════════════════════════════════════════
// Bench 1: Cache Get 单条读延迟
//
// 期望对比：
//   client-go cache.Store: ~200-500ns（RWMutex 锁竞争）
//   KubePivot Cache:       < 50ns（atomic.Value snapshot，lock-free）
//   提升：5-10x
// ═══════════════════════════════════════════════════════════════════

const cacheSize = 1000

// ─── client-go baseline ────────────────────────────────────────────

func buildClientGoCache(n int) cache.Indexer {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for i := 0; i < n; i++ {
		raw := generateComplexDeployment(i)
		var deploy appsv1.Deployment
		if err := json.Unmarshal(raw, &deploy); err != nil {
			panic(err)
		}
		_ = indexer.Add(&deploy)
	}
	return indexer
}

func BenchmarkCacheGet_ClientGo(b *testing.B) {
	indexer := buildClientGoCache(cacheSize)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			key := fmt.Sprintf("ns-%d/test-deploy-%d", 500%50, 500)
			_, _, _ = indexer.GetByKey(key)
		}
	})
}

// ─── KubePivot 自研 ────────────────────────────────────────────────

// SkeletonCache 简化版自研 cache（仅供 benchmark，最终实现在 internal/eventstream/）
type SkeletonCache struct {
	snapshot atomic.Value // *cacheSnapshot
	writeMu  sync.Mutex
}

type cacheSnapshot struct {
	// items 二级索引：map[namespace]map[name]*Skeleton
	// List(ns) 直接 items[ns]，O(N_ns) 而不是 O(N_total)
	items map[string]map[string]*Skeleton
}

type Skeleton struct {
	APIVersion        string
	Kind              string
	Namespace         string
	Name              string
	UID               string
	Generation        int64
	ResourceVersion   string // qc 强调的关键字段
	Labels            map[string]string
	Replicas          *int32
	Phase             string
	ReadyReplicas     *int32
	RawJSON           []byte
}

func NewSkeletonCache() *SkeletonCache {
	c := &SkeletonCache{}
	c.snapshot.Store(&cacheSnapshot{items: make(map[string]map[string]*Skeleton)})
	return c
}

func (c *SkeletonCache) Get(ns, name string) (*Skeleton, bool) {
	snap := c.snapshot.Load().(*cacheSnapshot)
	nsItems, ok := snap.items[ns]
	if !ok {
		return nil, false
	}
	s, ok := nsItems[name]
	return s, ok
}

func (c *SkeletonCache) Put(s *Skeleton) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	old := c.snapshot.Load().(*cacheSnapshot)

	// 拷贝外层 map（namespace 数量通常小，~50-200）
	newItems := make(map[string]map[string]*Skeleton, len(old.items))
	for ns, nsMap := range old.items {
		if ns == s.Namespace {
			// 只拷贝目标 ns 的内层 map
			newNsMap := make(map[string]*Skeleton, len(nsMap)+1)
			for k, v := range nsMap {
				newNsMap[k] = v
			}
			newNsMap[s.Name] = s
			newItems[ns] = newNsMap
		} else {
			// 其他 ns 直接复用旧 map（immutable，无副作用）
			newItems[ns] = nsMap
		}
	}
	// ns 不存在则新建
	if _, ok := newItems[s.Namespace]; !ok {
		newItems[s.Namespace] = map[string]*Skeleton{s.Name: s}
	}

	c.snapshot.Store(&cacheSnapshot{items: newItems})
}

// PutBulk 批量写入，用于 ColdStart / Resync 场景
// 复杂度 O(N+M)，避免 N 次 Put 的 O(N²)
func (c *SkeletonCache) PutBulk(items []*Skeleton) {
	if len(items) == 0 {
		return
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	old := c.snapshot.Load().(*cacheSnapshot)

	// 一次性构建 newItems：拷贝旧 + 加新
	// 按 ns 分组，每个 ns 的内层 map 只拷贝一次
	nsGroups := make(map[string][]*Skeleton)
	for _, s := range items {
		nsGroups[s.Namespace] = append(nsGroups[s.Namespace], s)
	}

	newItems := make(map[string]map[string]*Skeleton, len(old.items)+len(nsGroups))

	// 处理已存在的 ns
	for ns, nsMap := range old.items {
		if newSkels, hasNew := nsGroups[ns]; hasNew {
			// 该 ns 有新增，拷贝旧 + 加新
			merged := make(map[string]*Skeleton, len(nsMap)+len(newSkels))
			for k, v := range nsMap {
				merged[k] = v
			}
			for _, s := range newSkels {
				merged[s.Name] = s
			}
			newItems[ns] = merged
			delete(nsGroups, ns)
		} else {
			// 该 ns 无新增，直接复用旧 map
			newItems[ns] = nsMap
		}
	}

	// 处理新增的 ns
	for ns, newSkels := range nsGroups {
		nsMap := make(map[string]*Skeleton, len(newSkels))
		for _, s := range newSkels {
			nsMap[s.Name] = s
		}
		newItems[ns] = nsMap
	}

	c.snapshot.Store(&cacheSnapshot{items: newItems})
}

func (c *SkeletonCache) List(ns string) []*Skeleton {
	snap := c.snapshot.Load().(*cacheSnapshot)
	if ns != "" {
		// 指定 ns：直接 map 索引，O(N_ns)
		nsMap, ok := snap.items[ns]
		if !ok {
			return nil
		}
		result := make([]*Skeleton, 0, len(nsMap))
		for _, s := range nsMap {
			result = append(result, s)
		}
		return result
	}

	// ns 为空：全量遍历
	total := 0
	for _, nsMap := range snap.items {
		total += len(nsMap)
	}
	result := make([]*Skeleton, 0, total)
	for _, nsMap := range snap.items {
		for _, s := range nsMap {
			result = append(result, s)
		}
	}
	return result
}

// ParseSkeleton 增量序列化：仅解 Skeleton 字段，不反序列化整个对象
func ParseSkeleton(rawJSON []byte) (*Skeleton, error) {
	var partial struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			Namespace       string            `json:"namespace"`
			Name            string            `json:"name"`
			UID             string            `json:"uid"`
			Generation      int64             `json:"generation"`
			ResourceVersion string            `json:"resourceVersion"`
			Labels          map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			Replicas *int32 `json:"replicas"`
		} `json:"spec"`
		Status struct {
			Phase         string `json:"phase"`
			ReadyReplicas *int32 `json:"readyReplicas"`
		} `json:"status"`
	}

	if err := json.Unmarshal(rawJSON, &partial); err != nil {
		return nil, err
	}

	return &Skeleton{
		APIVersion:      partial.APIVersion,
		Kind:            partial.Kind,
		Namespace:       partial.Metadata.Namespace,
		Name:            partial.Metadata.Name,
		UID:             partial.Metadata.UID,
		Generation:      partial.Metadata.Generation,
		ResourceVersion: partial.Metadata.ResourceVersion,
		Labels:          partial.Metadata.Labels,
		Replicas:        partial.Spec.Replicas,
		Phase:           partial.Status.Phase,
		ReadyReplicas:   partial.Status.ReadyReplicas,
		RawJSON:         rawJSON,
	}, nil
}

func buildKubePivotCache(n int) *SkeletonCache {
	c := NewSkeletonCache()
	for i := 0; i < n; i++ {
		raw := generateComplexDeployment(i)
		s, err := ParseSkeleton(raw)
		if err != nil {
			panic(err)
		}
		c.Put(s)
	}
	return c
}

func BenchmarkCacheGet_KubePivot(b *testing.B) {
	c := buildKubePivotCache(cacheSize)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ns := fmt.Sprintf("ns-%d", 500%50)
			_, _ = c.Get(ns, "test-deploy-500")
		}
	})
}
