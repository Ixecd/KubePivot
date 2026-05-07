package eventstream

import (
	"fmt"
	"runtime"
	"testing"
)

// ─── 并发写压力测试 ─────────────────────────────────────────────

func BenchmarkPodCache_ParallelPut(b *testing.B) {
	sc := NewShardedPodCache(16)
	// Pre-populate one shard
	sc.PutBulk([]*PodEntry{{Namespace: "ns-a", Name: "base", NodeName: "n1"}})

	b.ResetTimer()
	b.ReportAllocs()
	b.Run("64goroutines", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				ns := fmt.Sprintf("ns-%d", i%32)
				sc.Put(&PodEntry{Namespace: ns, Name: fmt.Sprintf("p-%d", i), NodeName: "n1"}, "")
				i++
			}
		})
	})
	b.StopTimer()
	if len(sc.ListAll()) == 0 {
		b.Fatal("ListAll should return pods after parallel puts")
	}
}

// ─── Delta storm 压力测试 ──────────────────────────────────────

func BenchmarkPodCache_DeltaStorm(b *testing.B) {
	sc := NewShardedPodCache(16)
	sc.PutBulk(makePods(5000, "ns-%d", "pod-%d"))

	b.ResetTimer()
	b.ReportAllocs()
	// Write 500 deltas to force merge-on-read path
	for i := 0; i < b.N; i++ {
		for j := 0; j < 500; j++ {
			ns := fmt.Sprintf("ns-%d", j%32)
			sc.Put(&PodEntry{Namespace: ns, Name: fmt.Sprintf("storm-%d", j), NodeName: "n1", RV: int64(j + i*500)}, "")
		}
		// ListAll on dirty cache (delta=500 > mergeThreshold 200)
		all := sc.ListAll()
		if len(all) == 0 {
			b.Fatal("ListAll should return pods")
		}
	}
}

func BenchmarkPodCache_DeltaStorm_ListByNode(b *testing.B) {
	sc := NewShardedPodCache(16)
	sc.PutBulk(makePods(5000, "ns-%d", "pod-%d"))

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 500; j++ {
			ns := fmt.Sprintf("ns-%d", j%32)
			sc.Put(&PodEntry{Namespace: ns, Name: fmt.Sprintf("storm-%d", j), NodeName: "n1", RV: int64(j + i*500)}, "")
		}
		byNode := sc.ListByNode("n1")
		if len(byNode) == 0 {
			b.Fatal("ListByNode should return pods")
		}
	}
}

func makePods(n int, nsFmt, nameFmt string) []*PodEntry {
	pods := make([]*PodEntry, n)
	for i := range pods {
		pods[i] = &PodEntry{
			Namespace: fmt.Sprintf(nsFmt, i%32),
			Name:      fmt.Sprintf(nameFmt, i),
			NodeName:  fmt.Sprintf("node-%d", i%50),
			Phase:     "Running",
			Labels:    makeLabels(20),
		}
	}
	return pods
}

func makeLabels(n int) map[string]string {
	keys := []string{
		"app.kubernetes.io/name", "app.kubernetes.io/instance",
		"app.kubernetes.io/version", "app.kubernetes.io/component",
		"app.kubernetes.io/part-of", "app.kubernetes.io/managed-by",
		"tier", "environment", "team", "cost-center",
		"version", "release", "track", "shard",
		"kubepivot.io/shard", "kubepivot.io/pool",
		"prometheus.io/scrape", "prometheus.io/port",
		"sidecar.istio.io/inject", "grafana.com/dashboard",
	}
	labels := make(map[string]string, n)
	for i := 0; i < n && i < len(keys); i++ {
		labels[keys[i]] = fmt.Sprintf("value-%d", i)
	}
	return labels
}

// ─── 内存 breakdown ────────────────────────────────────────────

// BenchmarkPodEntry_Alloc measures per-PodEntry allocation.
func BenchmarkPodEntry_Alloc(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = &PodEntry{
			Namespace: "default",
			Name:      fmt.Sprintf("api-gateway-%d", i),
			NodeName:  "node-0001",
			Phase:     "Running",
			Labels:    makeLabels(20),
			Requests:  ResourceRequest{CPU: 500, Memory: 512 << 20},
			RV:        12345,
		}
	}
}

// BenchmarkLabels_Alloc measures per-labels-map allocation.
func BenchmarkLabels_Alloc(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = makeLabels(20)
	}
}

func TestPodEntry_MemoryBreakdown(t *testing.T) {
	// Force GC + read baseline
	runtime.GC()
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)

	// Allocate 5000 PodEntry with 20 labels (simulating real PodCache load)
	pods := make([]*PodEntry, 5000)
	for i := range pods {
		pods[i] = &PodEntry{
			Namespace: fmt.Sprintf("ns-%d", i%20),
			Name:      fmt.Sprintf("pod-%05d", i),
			NodeName:  fmt.Sprintf("node-%04d", i%200),
			Phase:     "Running",
			Labels:    makeLabels(20),
			Requests:  ResourceRequest{CPU: 500, Memory: 512 << 20},
			RV:        int64(i + 1),
		}
	}
	_ = pods

	runtime.GC()
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)

	totalAlloc := int64(m2.TotalAlloc) - int64(m1.TotalAlloc)
	heapInUse := int64(m2.HeapInuse) - int64(m1.HeapInuse)
	t.Logf("5000 PodEntry + 20 labels each:")
	t.Logf("  TotalAlloc: %d bytes (%.0f B/pod)", totalAlloc, float64(totalAlloc)/5000)
	t.Logf("  HeapInuse:  %d bytes (%.0f B/pod)", heapInUse, float64(heapInUse)/5000)

	// Estimate struct sizes
	t.Logf("Breakdown estimates:")
	t.Logf("  PodEntry struct: ~104 B (7 string headers×16 + 3 int64s×8 + map ptr×8 + RV×8)")
	t.Logf("  Labels map (20 keys): ~1500 B (Go map overhead ~40B + 20 keys×~50B + 20 values×~20B)")
	t.Logf("  20 label strings: ~800 B (avg 40B per key string)")
	t.Logf("  Total estimated: ~2400 B/pod")
}

// ─── 内存放大率 ───────────────────────────────────────────────

// BenchmarkPodCache_MemoryAmp 测量 PodCache 内存放大率。
// 放大率 = TotalAlloc / (N_pods × theoretical_per_pod)。
// theoretical = 1500B (PodEntry 104B + Labels 1400B) × 2 (CoW 双拷贝) = 3000B。
// v2.7 基准：KP 1.65x, client-go 4.69x。
func BenchmarkPodCache_MemoryAmp(b *testing.B) {
	for _, n := range []int{1000, 5000, 10000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			b.StopTimer()
			runtime.GC()
			var m1 runtime.MemStats
			runtime.ReadMemStats(&m1)

			cache := NewPodCache()
			pods := makePods(n, "ns-%d", "pod-%d")
			cache.PutBulk(pods)
			_ = cache.ListAll()

			runtime.GC()
			var m2 runtime.MemStats
			runtime.ReadMemStats(&m2)

			totalAlloc := int64(m2.TotalAlloc) - int64(m1.TotalAlloc)
			heapInUse := int64(m2.HeapInuse) - int64(m1.HeapInuse)

			theoretical := int64(n * 3000) // PodEntry+Labels × CoW 2 copies
			ampAlloc := float64(totalAlloc) / float64(theoretical)

			b.ReportMetric(ampAlloc, "amp-alloc")
			b.ReportMetric(float64(totalAlloc)/float64(n), "B/pod-alloc")
			b.ReportMetric(float64(heapInUse)/float64(n), "B/pod-heap")
			b.ReportMetric(float64(heapInUse)/(1024*1024), "heap-MB")

			_ = cache.Generation()
			b.StartTimer()
		})
	}
}
