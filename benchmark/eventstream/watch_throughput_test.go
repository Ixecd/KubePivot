package eventstreamench

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/tools/cache"
)

// ═══════════════════════════════════════════════════════════════════
// Bench 3: Watch 稳态吞吐
//
// 测试目标：
//   模拟"接收到 Watch Event → 解析 → 更新 Cache"这一闭环的处理速率
//   不涉及真实 K8s API（与 Bench 1/2/4/5 一致：避免网络栈和 API Server
//   开销掩盖核心逻辑差异，保证"基准公平性"）
//
// 公平赛道：
//   client-go: json.Unmarshal(*Deployment) + Indexer.Update()
//   KubePivot: ParseSkeleton + SkeletonCache.Put()
//
//   两侧都是"reflector watch loop hot path"的核心两步。
//
// 期望对比：
//   client-go: ~30-50µs/event（反序列化重 + map+lock 写入）
//   KubePivot: ~10-20µs/event（Skeleton 轻量 + lock-free snapshot 写入）
//   提升: 2-3x
//
// 注意：
//   每个 b.N 是处理 1 个 watch event
//   events/sec = 1 / (ns/op * 1e-9)
//   通过 b.N 自动放大到 1k / 10k 量级（go bench 框架）
// ═══════════════════════════════════════════════════════════════════

// ─── Bench 3.1: Watch 单事件处理（b.N 自动决定规模） ─────────────

// BenchmarkWatchThroughput_ClientGo 测 client-go 在 reflector hot path
// （json.Unmarshal + Indexer.Update）的单事件处理延迟。
//
// 用于计算 events/sec：1 / ns_per_op
func BenchmarkWatchThroughput_ClientGo(b *testing.B) {
	// 预生成 1000 个不同的 sample（足够多样，避免 cache 命中走特殊路径）
	samples := make([][]byte, 1000)
	for i := range samples {
		samples[i] = generateComplexDeployment(i)
	}

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		raw := samples[i%len(samples)]

		// 模拟 reflector watch loop 的两步：
		var deploy appsv1.Deployment
		if err := json.Unmarshal(raw, &deploy); err != nil {
			b.Fatalf("unmarshal failed: %v", err)
		}
		_ = indexer.Update(&deploy)
	}
}

// BenchmarkWatchThroughput_KubePivot 测 KubePivot 在 reflector hot path
// （ParseSkeleton + SkeletonCache.Put）的单事件处理延迟。
func BenchmarkWatchThroughput_KubePivot(b *testing.B) {
	samples := make([][]byte, 1000)
	for i := range samples {
		samples[i] = generateComplexDeployment(i)
	}

	c := NewSkeletonCache()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		raw := samples[i%len(samples)]

		s, err := ParseSkeleton(raw)
		if err != nil {
			b.Fatalf("parse skeleton failed: %v", err)
		}
		c.Put(s)
	}
}

// ─── Bench 3.2: 固定规模 1000 events 端到端时间 ──────────────────

// BenchmarkWatchThroughput_ClientGo_1000Events 测 client-go 处理 1000 个
// 不同事件的端到端总耗时（不依赖 b.N 自动调整）。
//
// b.N 表示"重复 1000 events 这个批次的次数"。
// 单次 op = 1000 events 的总处理时间。
func BenchmarkWatchThroughput_ClientGo_1000Events(b *testing.B) {
	samples := make([][]byte, 1000)
	for i := range samples {
		samples[i] = generateComplexDeployment(i)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// 每次 op 都新建 cache，避免上一轮残留影响
		indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
		for _, raw := range samples {
			var deploy appsv1.Deployment
			_ = json.Unmarshal(raw, &deploy)
			_ = indexer.Update(&deploy)
		}
	}
}

func BenchmarkWatchThroughput_KubePivot_1000Events(b *testing.B) {
	samples := make([][]byte, 1000)
	for i := range samples {
		samples[i] = generateComplexDeployment(i)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c := NewSkeletonCache()
		for _, raw := range samples {
			s, _ := ParseSkeleton(raw)
			c.Put(s)
		}
	}
}

// ─── Bench 3.3: 固定规模 10000 events ───────────────────────────

// 10000 events 量级：放大调度 / GC 噪声，验证稳态行为
func BenchmarkWatchThroughput_ClientGo_10000Events(b *testing.B) {
	samples := make([][]byte, 10000)
	for i := range samples {
		samples[i] = generateComplexDeployment(i)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
		for _, raw := range samples {
			var deploy appsv1.Deployment
			_ = json.Unmarshal(raw, &deploy)
			_ = indexer.Update(&deploy)
		}
	}
}

func BenchmarkWatchThroughput_KubePivot_10000Events(b *testing.B) {
	samples := make([][]byte, 10000)
	for i := range samples {
		samples[i] = generateComplexDeployment(i)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c := NewSkeletonCache()
		for _, raw := range samples {
			s, _ := ParseSkeleton(raw)
			c.Put(s)
		}
	}
}

// ─── 对比报告测试（输出 events/sec） ─────────────────────────────

// TestWatchThroughputComparison 跑两侧 1000 events 处理，输出对比报告。
//
// 与 Bench 1/2/4/5 一致：BenchmarkXxx 输出原始数据（go bench 框架）
// TestXxx 输出人类可读对比（含 events/sec 和提升倍数）
//
// 用法：
//   go test -run=TestWatchThroughputComparison -v
func TestWatchThroughputComparison(t *testing.T) {
	const eventCount = 1000

	samples := make([][]byte, eventCount)
	for i := range samples {
		samples[i] = generateComplexDeployment(i)
	}

	// ── client-go ──
	clientGoStart := time.Now()
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, raw := range samples {
		var deploy appsv1.Deployment
		if err := json.Unmarshal(raw, &deploy); err != nil {
			t.Fatalf("client-go unmarshal failed: %v", err)
		}
		_ = indexer.Update(&deploy)
	}
	clientGoDuration := time.Since(clientGoStart)

	// ── KubePivot ──
	kpStart := time.Now()
	c := NewSkeletonCache()
	for _, raw := range samples {
		s, err := ParseSkeleton(raw)
		if err != nil {
			t.Fatalf("KubePivot parse failed: %v", err)
		}
		c.Put(s)
	}
	kpDuration := time.Since(kpStart)

	// ── 计算 events/sec ──
	clientGoEPS := float64(eventCount) / clientGoDuration.Seconds()
	kpEPS := float64(eventCount) / kpDuration.Seconds()

	t.Logf("\n═══════════════════════════════════════════════")
	t.Logf("  Watch Throughput 对比（%d events）", eventCount)
	t.Logf("═══════════════════════════════════════════════")
	t.Logf("  client-go:    %v   (%.0f events/sec)", clientGoDuration, clientGoEPS)
	t.Logf("  KubePivot:    %v   (%.0f events/sec)", kpDuration, kpEPS)
	t.Logf("  提升:         %.2fx", float64(clientGoDuration)/float64(kpDuration))
	t.Logf("  延迟差:       %v / event vs %v / event",
		clientGoDuration/eventCount, kpDuration/eventCount)
	t.Logf("═══════════════════════════════════════════════\n")

	// 防止编译器优化
	_ = indexer
	_ = c
}

// TestWatchThroughputComparison_10000 同上但 10000 events
func TestWatchThroughputComparison_10000(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过 10000 events 对比（用 -short）")
	}

	const eventCount = 10000

	samples := make([][]byte, eventCount)
	for i := range samples {
		samples[i] = generateComplexDeployment(i)
	}

	// client-go
	clientGoStart := time.Now()
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, raw := range samples {
		var deploy appsv1.Deployment
		_ = json.Unmarshal(raw, &deploy)
		_ = indexer.Update(&deploy)
	}
	clientGoDuration := time.Since(clientGoStart)

	// KubePivot
	kpStart := time.Now()
	c := NewSkeletonCache()
	for _, raw := range samples {
		s, _ := ParseSkeleton(raw)
		c.Put(s)
	}
	kpDuration := time.Since(kpStart)

	clientGoEPS := float64(eventCount) / clientGoDuration.Seconds()
	kpEPS := float64(eventCount) / kpDuration.Seconds()

	t.Logf("\n═══════════════════════════════════════════════")
	t.Logf("  Watch Throughput 对比（%d events，稳态规模）", eventCount)
	t.Logf("═══════════════════════════════════════════════")
	t.Logf("  client-go:    %v   (%.0f events/sec)", clientGoDuration, clientGoEPS)
	t.Logf("  KubePivot:    %v   (%.0f events/sec)", kpDuration, kpEPS)
	t.Logf("  提升:         %.2fx", float64(clientGoDuration)/float64(kpDuration))
	t.Logf("═══════════════════════════════════════════════\n")

	_ = indexer
	_ = c

	fmt.Println() // 输出可见性
}
