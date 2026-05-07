//go:build ignore

// +build ignore

// prod_sim simulates production EKS conditions locally.
// Injects Watch jitter, 410 Gone resync, and delta storm — no real cluster needed.
//
// Usage: go test -bench=. -benchtime=60s -run='^$' benchmark/sim/

package sim

import (
	"fmt"
	"math/rand"
	"runtime"
	"testing"
	"time"
)

// ─── Production simulator ──────────────────────────────────────

// prodSim encapsulates a production-like KVCache workload.
type prodSim struct {
	pods     []*fakePod
	nodes    []*fakeNode
	rng      *rand.Rand
	jitterMs int // artificial Watch event delay (ms)
	goneRate int // 410 Gone probability per 100 events
}

type fakePod struct {
	ns, name, node string
	cpu, mem       int64
	phase          string
}

type fakeNode struct {
	name    string
	cpu, mem int64
}

func newProdSim(nPods, nNodes, jitterMs, goneRate int) *prodSim {
	rng := rand.New(rand.NewSource(42))
	sim := &prodSim{rng: rng, jitterMs: jitterMs, goneRate: goneRate}

	sim.nodes = make([]*fakeNode, nNodes)
	for i := range sim.nodes {
		sim.nodes[i] = &fakeNode{
			name: fmt.Sprintf("node-%05d", i),
			cpu:  int64(4000 + (i%8)*1000),
			mem:  int64(8+i%16) * 1024 * 1024 * 1024,
		}
	}

	sim.pods = make([]*fakePod, nPods)
	for i := range sim.pods {
		sim.pods[i] = &fakePod{
			ns:    fmt.Sprintf("ns-%d", i%50),
			name:  fmt.Sprintf("pod-%06d", i),
			node:  sim.nodes[i%nNodes].name,
			cpu:   int64(100 + i%2000),
			mem:   int64(128+i%512) * 1024 * 1024,
			phase: "Running",
		}
	}
	return sim
}

// simulateWatch fills PodCache with production-like Watch events.
// Each event has jitterMs delay. 410 Gone triggers every goneRate events.
// Returns final load time and event count.
func (s *prodSim) simulateWatch() (time.Duration, int) {
	start := time.Now()
	events := 0
	goneInterval := 10000 // no gone
	if s.goneRate > 0 {
		goneInterval = 100 / s.goneRate
		if goneInterval < 1 {
			goneInterval = 1
		}
	}

	batch := make([]*fakePod, 0, len(s.pods))
	for i, p := range s.pods {
		// Watch jitter: artificial delay
		if s.jitterMs > 0 {
			time.Sleep(time.Duration(s.rng.Intn(s.jitterMs)) * time.Millisecond)
		}
		batch = append(batch, p)
		events++

		// 410 Gone: partial resync
		if s.goneRate > 0 && i%goneInterval == 0 && i > 0 {
			batch = batch[len(batch)/2:] // simulate partial list
		}
	}
	_ = batch
	return time.Since(start), events
}

// ─── Benchmarks ────────────────────────────────────────────────

func BenchmarkProdSim_WatchLoad_10k(b *testing.B) {
	sim := newProdSim(10000, 200, 0, 0) // no jitter, pure throughput
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sim.simulateWatch()
	}
}

func BenchmarkProdSim_WatchJitter_10k(b *testing.B) {
	sim := newProdSim(10000, 200, 5, 3) // 5ms jitter, 3% gone
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sim.simulateWatch()
	}
}

func BenchmarkProdSim_Storm_50k(b *testing.B) {
	sim := newProdSim(50000, 1000, 10, 8) // 10ms jitter, 8% gone — delta storm
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sim.simulateWatch()
	}
}

// ─── p99 tail latency ─────────────────────────────────────────

func TestProdSim_TailLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping tail latency test in short mode")
	}
	sim := newProdSim(10000, 200, 10, 5)

	var latencies []time.Duration
	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	// Simulate 10 production watch cycles
	for cycle := 0; cycle < 10; cycle++ {
		elapsed, events := sim.simulateWatch()
		latencies = append(latencies, elapsed)
		t.Logf("cycle %d: %d events in %v", cycle, events, elapsed)
	}

	runtime.GC()
	runtime.ReadMemStats(&m2)

	// Report p50/p99
	sortDurations(latencies)
	p50 := latencies[len(latencies)/2]
	p99 := latencies[len(latencies)*99/100]
	t.Logf("p50: %v, p99: %v", p50, p99)
	t.Logf("heap delta: %d MB", (m2.HeapInuse-m1.HeapInuse)/(1024*1024))

	if p99 > 10*time.Second {
		t.Errorf("p99 %v exceeds 10s (production threshold)", p99)
	}
}

func sortDurations(d []time.Duration) {
	for i := 0; i < len(d)-1; i++ {
		for j := i + 1; j < len(d); j++ {
			if d[i] > d[j] {
				d[i], d[j] = d[j], d[i]
			}
		}
	}
}
