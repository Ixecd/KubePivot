package eventstreamench

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
)

// ═══════════════════════════════════════════════════════════════════
// Bench 2: Cache List 全量
//
// 关注：
//   - ns/op (时间)
//   - allocs/op (内存分配次数)
//   - bytes/op (内存分配总量)
//
// 期望对比：
//   client-go: 遍历 cache.Store + 持有 RWMutex → ~10-20μs / 1000 对象
//   KubePivot: 遍历 immutable map snapshot → ~5μs / 1000 对象
//   提升: ~2-4x
// ═══════════════════════════════════════════════════════════════════

func BenchmarkCacheList_ClientGo(b *testing.B) {
	indexer := buildClientGoCache(cacheSize)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		objs := indexer.List()
		// 防止编译器优化掉
		_ = len(objs)
	}
}

func BenchmarkCacheList_KubePivot(b *testing.B) {
	c := buildKubePivotCache(cacheSize)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// 列指定 namespace（实际 use case）
		objs := c.List("ns-25")
		_ = len(objs)
	}
}

// ─── ListAll 全量（跨 ns）─────────────────────────────────────────────

func BenchmarkCacheListAll_ClientGo(b *testing.B) {
	indexer := buildClientGoCache(cacheSize)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// client-go cache.List() 默认全量
		objs := indexer.List()
		// 转回 *Deployment（业务代码常做的）
		for _, o := range objs {
			_ = o.(*appsv1.Deployment)
		}
	}
}

func BenchmarkCacheListAll_KubePivot(b *testing.B) {
	c := buildKubePivotCache(cacheSize)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		objs := c.List("")  // ns="" = 全量
		_ = len(objs)
	}
}
