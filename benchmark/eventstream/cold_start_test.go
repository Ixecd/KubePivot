package eventstreamench

import (
	"encoding/json"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/tools/cache"
)

// ═══════════════════════════════════════════════════════════════════
// Bench 4: 启动时间（cold start）
//
// 测试目标：
//   从 cache 创建 → 加载 N 个对象 → 完成首次"全量同步"
//   不涉及真实 K8s API（避免网络抖动），用本地数据模拟
//
// 期望对比：
//   client-go: ~50-100ms / 1000 对象（反序列化重）
//   KubePivot: ~20-50ms / 1000 对象（Skeleton 轻量）
//   提升: 2-3x
// ═══════════════════════════════════════════════════════════════════

func BenchmarkColdStart_ClientGo(b *testing.B) {
	// 预生成数据
	samples := make([][]byte, cacheSize)
	for i := range samples {
		samples[i] = generateComplexDeployment(i)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// 每次冷启动新 cache + 全量加载
		_ = buildClientGoCacheFromSamples(samples)
	}
}

func BenchmarkColdStart_KubePivot(b *testing.B) {
	samples := make([][]byte, cacheSize)
	for i := range samples {
		samples[i] = generateComplexDeployment(i)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = buildKubePivotCacheFromSamples(samples)
	}
}

// ─── 不同规模 cold start ─────────────────────────────────────────

func BenchmarkColdStart_ClientGo_10000(b *testing.B) {
	samples := make([][]byte, 10000)
	for i := range samples {
		samples[i] = generateComplexDeployment(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildClientGoCacheFromSamples(samples)
	}
}

func BenchmarkColdStart_KubePivot_10000(b *testing.B) {
	samples := make([][]byte, 10000)
	for i := range samples {
		samples[i] = generateComplexDeployment(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildKubePivotCacheFromSamples(samples)
	}
}

// ─── 测量"前 N 个对象"的加载延迟 ─────────────────────────────────

// 工程价值：v2.7 期望 informer 能 "5s 内加载 1000 对象"
// 这个测试给出延迟分布

func TestColdStartLatencyDistribution(t *testing.T) {
	samples := make([][]byte, 1000)
	for i := range samples {
		samples[i] = generateComplexDeployment(i)
	}

	// client-go
	clientGoStart := time.Now()
	_ = buildClientGoCacheFromSamples(samples)
	clientGoDuration := time.Since(clientGoStart)

	// KubePivot
	kpStart := time.Now()
	_ = buildKubePivotCacheFromSamples(samples)
	kpDuration := time.Since(kpStart)

	t.Logf("\n═══════════════════════════════════════════════")
	t.Logf("  Cold Start 延迟（1000 对象）")
	t.Logf("═══════════════════════════════════════════════")
	t.Logf("  client-go: %v", clientGoDuration)
	t.Logf("  KubePivot: %v", kpDuration)
	t.Logf("  提升:      %.2fx", float64(clientGoDuration)/float64(kpDuration))
	t.Logf("═══════════════════════════════════════════════\n")
}

// ─── 辅助函数 ─────────────────────────────────────────────────────

func buildClientGoCacheFromSamples(samples [][]byte) interface{} {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, raw := range samples {
		var deploy appsv1.Deployment
		if err := json.Unmarshal(raw, &deploy); err != nil {
			continue
		}
		_ = indexer.Add(&deploy)
	}
	return indexer
}

func buildKubePivotCacheFromSamples(samples [][]byte) *SkeletonCache {
	c := NewSkeletonCache()

	// 用 PutBulk 批量写入：避免 N 次 Put 的 O(N²) 拷贝
	skels := make([]*Skeleton, 0, len(samples))
	for _, raw := range samples {
		s, err := ParseSkeleton(raw)
		if err != nil {
			continue
		}
		skels = append(skels, s)
	}
	c.PutBulk(skels)
	return c
}
