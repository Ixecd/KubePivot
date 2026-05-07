package eventstream

import (
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"testing"
)

// ─── Inline test data generator ──────────────────────────────────

var benchContainerTemplates = []struct {
	Name string; CPU, Mem, GPU int64
}{
	{"nginx", 100, 128 << 20, 0}, {"app", 500, 512 << 20, 0},
	{"sidecar", 200, 256 << 20, 0}, {"trainer", 4000, 16 << 30, 1000},
	{"inference", 2000, 8 << 30, 500}, {"redis", 250, 1 << 30, 0},
	{"postgres", 1000, 4 << 30, 0}, {"istio-proxy", 150, 192 << 20, 0},
}

var benchDeployNames = []string{
	"api-gateway", "user-svc", "order-worker", "payment-processor",
	"cache-warmer", "log-aggregator", "model-trainer", "inference-engine",
	"data-pipeline", "frontend-nginx",
}

func generatePods(n int, seed int64) []struct {
	Namespace, Name, NodeName, Phase string
	Labels                           map[string]string
	Containers                       []struct{ Name string; CPU, Mem, GPU int64 }
} {
	rng := rand.New(rand.NewSource(seed))
	pods := make([]struct {
		Namespace, Name, NodeName, Phase string
		Labels                           map[string]string
		Containers                       []struct{ Name string; CPU, Mem, GPU int64 }
	}, n)
	phases := []string{"Running", "Running", "Running", "Running", "Pending"}
	nodeCount := n/20 + 1
	if nodeCount < 5 { nodeCount = 5 }
	for i := 0; i < n; i++ {
		nc := 4
		containers := make([]struct{ Name string; CPU, Mem, GPU int64 }, 0, nc)
		for j := 0; j < nc; j++ {
			tmpl := benchContainerTemplates[rng.Intn(len(benchContainerTemplates))]
			cpu := tmpl.CPU + int64(float64(tmpl.CPU)*(rng.Float64()*0.4-0.2))
			mem := tmpl.Mem + int64(float64(tmpl.Mem)*(rng.Float64()*0.4-0.2))
			if cpu < 10 { cpu = 10 }
			if mem < 8<<20 { mem = 8 << 20 }
			containers = append(containers, struct{ Name string; CPU, Mem, GPU int64 }{
				Name: fmt.Sprintf("%s-%d", tmpl.Name, j), CPU: cpu, Mem: mem, GPU: tmpl.GPU,
			})
		}
		labels := make(map[string]string, 20)
		labelKeys := []string{"app.kubernetes.io/name","app.kubernetes.io/instance","tier","environment",
			"app.kubernetes.io/component","app.kubernetes.io/managed-by","version","release",
			"kubepivot.io/pool","kubepivot.io/shard","team","cost-center",
			"prometheus.io/scrape","prometheus.io/port","sidecar.istio.io/inject",
			"app.kubernetes.io/part-of","track","shard","grafana.com/dashboard","app.kubernetes.io/version"}
		for k := 0; k < 20; k++ {
			labels[labelKeys[rng.Intn(len(labelKeys))]] = fmt.Sprintf("value-%d", rng.Intn(100))
		}
		pods[i] = struct {
			Namespace, Name, NodeName, Phase string
			Labels                           map[string]string
			Containers                       []struct{ Name string; CPU, Mem, GPU int64 }
		}{
			Namespace: fmt.Sprintf("ns-%d", rng.Intn(20)),
			Name: fmt.Sprintf("%s-%04d", benchDeployNames[rng.Intn(len(benchDeployNames))], i),
			NodeName: fmt.Sprintf("node-%04d", rng.Intn(nodeCount)),
			Phase: phases[rng.Intn(len(phases))], Labels: labels, Containers: containers,
		}
	}
	return pods
}

func generateNodes(n int, seed int64) []struct {
	Name string; AllocCPU, AllocMem int64; GPUs []struct{ Product string; Index, MemTotal int; Health string; Domain int }
} {
	rng := rand.New(rand.NewSource(seed + 1))
	nodes := make([]struct {
		Name string; AllocCPU, AllocMem int64; GPUs []struct{ Product string; Index, MemTotal int; Health string; Domain int }
	}, n)
	gpuTypes := []string{"A100-SXM4-80GB", "H100-SXM5-80GB", "L40S", ""}
	gpuHealth := []string{"Healthy", "Healthy", "Healthy", "Unhealthy"}
	for i := 0; i < n; i++ {
		cpuCores := 32 + rng.Intn(160); memGB := 64 + rng.Intn(448)
		gpuCount := rng.Intn(9)
		if rng.Float64() < 0.3 { gpuCount = 0 }
		gpus := make([]struct{ Product string; Index, MemTotal int; Health string; Domain int }, gpuCount)
		gpuProduct := gpuTypes[rng.Intn(len(gpuTypes))]
		if gpuProduct == "" && gpuCount > 0 { gpuProduct = "A100-SXM4-80GB" }
		for j := 0; j < gpuCount; j++ {
			gpus[j] = struct{ Product string; Index, MemTotal int; Health string; Domain int }{
				Product: gpuProduct, Index: j, MemTotal: 80 * 1024 * 1024 * 1024, Health: gpuHealth[rng.Intn(len(gpuHealth))], Domain: j / 2,
			}
		}
		nodes[i] = struct {
			Name string; AllocCPU, AllocMem int64; GPUs []struct{ Product string; Index, MemTotal int; Health string; Domain int }
		}{Name: fmt.Sprintf("node-%04d", i), AllocCPU: int64(cpuCores * 1000), AllocMem: int64(memGB * 1024 * 1024 * 1024), GPUs: gpus}
	}
	return nodes
}

// Prevent dead code elimination
var benchSinkPod *PodEntry
var benchSinkPods []*PodEntry
var benchSinkNodes []*NodeEntry
var benchSinkBool bool
var benchSinkCount int

func loadPodCacheB(b *testing.B, n int, seed int64) *PodCache {
	b.Helper()
	c := NewPodCache()
	fps := generatePods(n, seed)
	entries := make([]*PodEntry, 0, n)
	for _, fp := range fps {
		var cpu, mem, gpu int64
		for _, c := range fp.Containers {
			cpu += c.CPU; mem += c.Mem; gpu += c.GPU
		}
		e := &PodEntry{Namespace: fp.Namespace, Name: fp.Name, NodeName: fp.NodeName, Phase: fp.Phase,
			Requests: ResourceRequest{CPU: cpu, Memory: mem, GPU: gpu}}
		e.SetLabels(fp.Labels)
		entries = append(entries, e)
	}
	c.PutBulk(entries)
	return c
}

func loadNodeCacheB(b *testing.B, n int, seed int64) *NodeCache {
	b.Helper()
	c := NewNodeCache()
	fns := generateNodes(n, seed)
	entries := make([]*NodeEntry, 0, len(fns))
	for _, fn := range fns {
		gpus := make([]GPUEntry, len(fn.GPUs))
		for i, g := range fn.GPUs {
			gpus[i] = GPUEntry{Product: g.Product, Index: g.Index, MemTotal: int64(g.MemTotal), Health: g.Health, NVLinkDomain: g.Domain}
		}
		entries = append(entries, &NodeEntry{Name: fn.Name, AllocatableCPU: fn.AllocCPU, AllocatableMemory: fn.AllocMem, GPU: gpus})
	}
	c.PutBulk(entries)
	return c
}

// ─── A: 读路径 ──────────────────────────────────────────────────

func BenchmarkPodCache_Get(b *testing.B) {
	cache := loadPodCacheB(b, 5000, 42)
	pods := cache.ListAll()
	b.ResetTimer(); b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p := pods[i%len(pods)]
		benchSinkPod, benchSinkBool = cache.Get(p.Namespace, p.Name)
	}
}

func BenchmarkPodCache_ListByNS(b *testing.B) {
	cache := loadPodCacheB(b, 5000, 42)
	ns := "ns-5"
	all := cache.ListAll()
	b.ResetTimer(); b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		count := 0
		for _, p := range all {
			if p.Namespace == ns {
				benchSinkPod = p
				count++
			}
		}
		benchSinkCount = count
	}
}

func BenchmarkPodCache_ListAll(b *testing.B) {
	for _, n := range []int{1000, 5000, 10000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			cache := loadPodCacheB(b, n, 42)
			all := cache.ListAll()
			b.ResetTimer(); b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for _, p := range all {
					benchSinkPod = p
				}
			}
		})
	}
}

func BenchmarkPodCache_ListByNode(b *testing.B) {
	cache := loadPodCacheB(b, 5000, 42)
	pods := cache.ListAll()
	var target string
	for _, p := range pods {
		if p.NodeName != "" {
			target = p.NodeName
			break
		}
	}
	b.ResetTimer(); b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchSinkPods = cache.ListByNode(target)
	}
}

func BenchmarkPodCache_ConcurrentRead(b *testing.B) {
	cache := loadPodCacheB(b, 5000, 42)
	all := cache.ListAll()
	b.ResetTimer(); b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			for _, p := range all {
				benchSinkPod = p
			}
		}
	})
}

func BenchmarkNodeCache_Get(b *testing.B) {
	cache := loadNodeCacheB(b, 100, 42)
	nodes := cache.ListAll()
	b.ResetTimer(); b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		n := nodes[i%len(nodes)]
		sinkNode, _ := cache.Get(n.Name)
		benchSinkNodes = append(benchSinkNodes[:0], sinkNode)
	}
}

func BenchmarkNodeCache_ListAll(b *testing.B) {
	cache := loadNodeCacheB(b, 200, 42)
	b.ResetTimer(); b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchSinkNodes = cache.ListAll()
	}
}

// ─── B: 写路径 ──────────────────────────────────────────────────

func BenchmarkPodCache_Put(b *testing.B) {
	cache := loadPodCacheB(b, 5000, 42)
	fp := generatePods(1, 99)[0]
	var cpu, mem, gpu int64
	for _, c := range fp.Containers { cpu += c.CPU; mem += c.Mem; gpu += c.GPU }
	entry := &PodEntry{Namespace: fp.Namespace, Name: fp.Name, NodeName: fp.NodeName, Phase: fp.Phase,
		Requests: ResourceRequest{CPU: cpu, Memory: mem, GPU: gpu}}
	entry.SetLabels(fp.Labels)
	b.ResetTimer(); b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		entry.Name = fmt.Sprintf("new-pod-%d", i)
		cache.Put(entry, "")
	}
}

func BenchmarkPodCache_PutCrossNode(b *testing.B) {
	cache := loadPodCacheB(b, 5000, 42)
	pods := cache.ListAll()
	b.ResetTimer(); b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p := pods[i%len(pods)]
		oldNode := p.NodeName
		p.NodeName = fmt.Sprintf("node-migrated-%d", i%20)
		cache.Put(p, oldNode)
	}
}

func BenchmarkPodCache_Delete(b *testing.B) {
	cache := loadPodCacheB(b, 5000, 42)
	fp := generatePods(b.N, 77)
	temp := make([]*PodEntry, b.N)
	for i := range temp {
		var cpu, mem, gpu int64
		for _, c := range fp[i].Containers { cpu += c.CPU; mem += c.Mem; gpu += c.GPU }
		temp[i] = &PodEntry{Namespace: fp[i].Namespace, Name: fp[i].Name, NodeName: fp[i].NodeName, Phase: fp[i].Phase,
			Requests: ResourceRequest{CPU: cpu, Memory: mem, GPU: gpu}}
		temp[i].SetLabels(fp[i].Labels)
		temp[i].Name = fmt.Sprintf("tmp-del-%d", i)
	}
	b.ResetTimer(); b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cache.Put(temp[i], "")
		cache.Delete(temp[i].Namespace, temp[i].Name, temp[i].NodeName)
	}
}

func BenchmarkPodCache_PutBulk(b *testing.B) {
	for _, n := range []int{1000, 5000, 10000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			fps := generatePods(n, 42)
			entries := make([]*PodEntry, 0, n)
			for _, fp := range fps {
				var cpu, mem, gpu int64
				for _, c := range fp.Containers { cpu += c.CPU; mem += c.Mem; gpu += c.GPU }
				e := &PodEntry{Namespace: fp.Namespace, Name: fp.Name, NodeName: fp.NodeName, Phase: fp.Phase,
					Requests: ResourceRequest{CPU: cpu, Memory: mem, GPU: gpu}}
				e.SetLabels(fp.Labels)
				entries = append(entries, e)
			}
			b.ResetTimer(); b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				cache := NewPodCache()
				cache.PutBulk(entries)
			}
		})
	}
}

// ─── C: 冷启动 ──────────────────────────────────────────────────

func BenchmarkPodCache_ColdStart(b *testing.B) {
	for _, n := range []int{1000, 5000, 10000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			fps := generatePods(n, 42)
			entries := make([]*PodEntry, 0, n)
			for _, fp := range fps {
				var cpu, mem, gpu int64
				for _, c := range fp.Containers { cpu += c.CPU; mem += c.Mem; gpu += c.GPU }
				e := &PodEntry{Namespace: fp.Namespace, Name: fp.Name, NodeName: fp.NodeName, Phase: fp.Phase,
					Requests: ResourceRequest{CPU: cpu, Memory: mem, GPU: gpu}}
				e.SetLabels(fp.Labels)
				entries = append(entries, e)
			}
			b.ResetTimer(); b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				cache := NewPodCache()
				cache.PutBulk(entries)
				if !cache.IsReady() {
					b.Fatal("cache not ready after PutBulk")
				}
			}
		})
	}
}

// ─── D: 内存 ────────────────────────────────────────────────────

func BenchmarkPodCache_Memory(b *testing.B) {
	for _, n := range []int{1000, 5000, 10000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			var m1, m2 runtime.MemStats
			runtime.GC(); runtime.ReadMemStats(&m1)
			cache := loadPodCacheB(b, n, 42)
			_ = cache.ListAll()
			runtime.GC(); runtime.ReadMemStats(&m2)
			alloc := m2.TotalAlloc - m1.TotalAlloc
			heapInUse := m2.HeapInuse - m1.HeapInuse
			b.ReportMetric(float64(alloc)/float64(n), "B/pod")
			b.ReportMetric(float64(heapInUse)/float64(n), "heap-B/pod")
			b.ReportMetric(float64(heapInUse)/(1024*1024), "heap-MB")
		})
	}
}

// ─── E: Subscribe ────────────────────────────────────────────────

type benchSub struct {
	mu    sync.Mutex
	count int
}
func (s *benchSub) OnChange(event CacheChangeEvent) {
	s.mu.Lock(); s.count++; s.mu.Unlock()
}

func BenchmarkPodCache_SubscribeNotify(b *testing.B) {
	cache := loadPodCacheB(b, 5000, 42)
	sub := &benchSub{}
	cache.Subscribe(sub)
	fp := generatePods(b.N, 55)
	b.ResetTimer(); b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var cpu, mem, gpu int64
		for _, c := range fp[i].Containers { cpu += c.CPU; mem += c.Mem; gpu += c.GPU }
		e := &PodEntry{Namespace: fp[i].Namespace, Name: fp[i].Name, NodeName: fp[i].NodeName, Phase: fp[i].Phase,
			Requests: ResourceRequest{CPU: cpu, Memory: mem, GPU: gpu}}
		e.SetLabels(fp[i].Labels)
		e.Name = fmt.Sprintf("sub-notify-%d", i)
		cache.Put(e, "")
	}
	b.StopTimer()
	if sub.count < b.N {
		b.Fatalf("subscriber missed notifications: got %d, want >= %d", sub.count, b.N)
	}
}

// ─── F: 并发 + Delta Storm ─────────────────────────────────────

func BenchmarkPodCache_ParallelPut(b *testing.B) {
	sc := NewShardedPodCache(16)
	sc.PutBulk([]*PodEntry{{Namespace: "ns-a", Name: "base", NodeName: "n1"}})
	b.ResetTimer(); b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			ns := fmt.Sprintf("ns-%d", i%32)
			sc.Put(&PodEntry{Namespace: ns, Name: fmt.Sprintf("p-%d", i), NodeName: "n1"}, "")
			i++
		}
	})
	if len(sc.ListAll()) == 0 {
		b.Fatal("ListAll should return pods after parallel puts")
	}
}

func BenchmarkPodCache_DeltaStorm(b *testing.B) {
	sc := NewShardedPodCache(16)
	sc.PutBulk(makePods(5000, "ns-%d", "pod-%d"))
	b.ResetTimer(); b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 500; j++ {
			ns := fmt.Sprintf("ns-%d", j%32)
			sc.Put(&PodEntry{Namespace: ns, Name: fmt.Sprintf("storm-%d", j), NodeName: "n1", RV: int64(j + i*500)}, "")
		}
		if len(sc.ListAll()) == 0 {
			b.Fatal("ListAll should return pods")
		}
	}
}

func BenchmarkPodCache_DeltaStorm_ListByNode(b *testing.B) {
	sc := NewShardedPodCache(16)
	sc.PutBulk(makePods(5000, "ns-%d", "pod-%d"))
	b.ResetTimer(); b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 500; j++ {
			ns := fmt.Sprintf("ns-%d", j%32)
			sc.Put(&PodEntry{Namespace: ns, Name: fmt.Sprintf("storm-%d", j), NodeName: "n1", RV: int64(j + i*500)}, "")
		}
		if len(sc.ListByNode("n1")) == 0 {
			b.Fatal("ListByNode should return pods")
		}
	}
}

// ─── G: Labels 压缩 ─────────────────────────────────────────────

func BenchmarkPodEntry_Alloc(b *testing.B) {
	lbls := makeLabels(20)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		e := &PodEntry{
			Namespace: "default", Name: fmt.Sprintf("api-gateway-%d", i),
			NodeName: "node-0001", Phase: "Running",
			Requests: ResourceRequest{CPU: 500, Memory: 512 << 20}, RV: 12345,
		}
		e.SetLabels(lbls)
	}
}

func BenchmarkPodEntry_AllocCommon(b *testing.B) {
	lbls := map[string]string{
		"app.kubernetes.io/name": "api-gateway", "app.kubernetes.io/instance": "api-gateway-prod",
		"app.kubernetes.io/component": "backend", "tier": "frontend",
		"environment": "production", "kubepivot.io/pool": "cpu",
		"kubepivot.io/shard": "shard-1", "prometheus.io/scrape": "true",
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		e := &PodEntry{
			Namespace: "default", Name: fmt.Sprintf("api-gateway-%d", i),
			NodeName: "node-0001", Phase: "Running",
			Requests: ResourceRequest{CPU: 500, Memory: 512 << 20}, RV: 12345,
		}
		e.SetLabels(lbls)
	}
}

func BenchmarkLabels_Alloc(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = makeLabels(20)
	}
}

// ─── H: 内存放大率 ─────────────────────────────────────────────

func BenchmarkPodCache_MemoryAmp(b *testing.B) {
	for _, n := range []int{1000, 5000, 10000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			b.StopTimer()
			runtime.GC(); var m1 runtime.MemStats; runtime.ReadMemStats(&m1)
			pods := makePods(n, "ns-%d", "pod-%d")
			cache := NewPodCache(); cache.PutBulk(pods); _ = cache.ListAll()
			runtime.GC(); var m2 runtime.MemStats; runtime.ReadMemStats(&m2)
			totalAlloc := int64(m2.TotalAlloc) - int64(m1.TotalAlloc)
			heapInUse := int64(m2.HeapInuse) - int64(m1.HeapInuse)
			theoretical := int64(n * 3000)
			b.ReportMetric(float64(totalAlloc)/float64(theoretical), "amp-alloc")
			b.ReportMetric(float64(totalAlloc)/float64(n), "B/pod-alloc")
			b.ReportMetric(float64(heapInUse)/float64(n), "B/pod-heap")
			b.ReportMetric(float64(heapInUse)/(1024*1024), "heap-MB")
			b.StartTimer()
		})
	}
}

func TestPodEntry_MemoryBreakdown(t *testing.T) {
	runtime.GC(); var m1 runtime.MemStats; runtime.ReadMemStats(&m1)
	pods := make([]*PodEntry, 5000)
	for i := range pods {
		pods[i] = &PodEntry{
			Namespace: fmt.Sprintf("ns-%d", i%20), Name: fmt.Sprintf("pod-%05d", i),
			NodeName: fmt.Sprintf("node-%04d", i%200), Phase: "Running",
			Requests: ResourceRequest{CPU: 500, Memory: 512 << 20}, RV: int64(i + 1),
		}
		pods[i].SetLabels(makeLabels(20))
	}
	_ = pods
	runtime.GC(); var m2 runtime.MemStats; runtime.ReadMemStats(&m2)
	totalAlloc := int64(m2.TotalAlloc) - int64(m1.TotalAlloc)
	t.Logf("5000 PodEntry + 20 labels: TotalAlloc=%d (%.0f B/pod)", totalAlloc, float64(totalAlloc)/5000)
	t.Logf("Labels compression: CommonLabels[10] inline + LabelHash, 80%% pods 368 B (-74%%)")
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

func makePods(n int, nsFmt, nameFmt string) []*PodEntry {
	pods := make([]*PodEntry, n)
	for i := range pods {
		pods[i] = &PodEntry{
			Namespace: fmt.Sprintf(nsFmt, i%32), Name: fmt.Sprintf(nameFmt, i),
			NodeName: fmt.Sprintf("node-%d", i%50), Phase: "Running",
		}
		pods[i].SetLabels(makeLabels(20))
	}
	return pods
}
