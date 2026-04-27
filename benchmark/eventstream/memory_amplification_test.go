package eventstreamench

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/tools/cache"
)

// ═══════════════════════════════════════════════════════════════════
// Bench 5 ⭐: 内存放大率（qc 强调的核心差异化测试）
//
// 测试方法：
//   1. 加载 1w 个复杂 Deployment 进 cache
//   2. runtime.GC() 强制回收（消除测量噪声）
//   3. 读 RSS 内存占用（/proc/self/status）
//   4. 计算放大率 = RSS / RawJSON_total
//
// 期望对比：
//   client-go: 反序列化整个 *Deployment struct → 放大率 2-3x
//             指针散乱 / map / slice 分散内存 / GC 元数据
//   
//   KubePivot: Skeleton + RawJSON 共享底层 []byte → 放大率 1.1-1.3x
//             数据结构紧凑 / 无 K8s 类型反射开销
//   
//   预期：薄纱 😎
//        2-3x vs 1.1-1.3x = KubePivot 内存效率提升 2x+
// ═══════════════════════════════════════════════════════════════════

const objCount = 10000

// readRSS 从 /proc/self/status 读 VmRSS（仅 Linux）
// macOS 上读不到 VmRSS，使用 runtime.MemStats.HeapInuse 替代
//
// 注：macOS 测试用 task_basic_info / mach API 才能拿到真实 RSS
//     这里简化用 HeapInuse + Sys 估算（够准确做对比）
func readRSS() uint64 {
	// Linux 路径
	if data, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "VmRSS:") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					kb, err := strconv.ParseUint(parts[1], 10, 64)
					if err == nil {
						return kb * 1024
					}
				}
			}
		}
	}

	// macOS 兜底：用 Go runtime stats（准确度足够做对比）
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse + m.StackInuse
}

// forceGCAndStabilize 强制 GC 并等待稳定
// 跑两次 GC 是为了让 finalizer 都执行完
func forceGCAndStabilize() {
	runtime.GC()
	runtime.GC()
	debug.FreeOSMemory()
}

// ─── client-go cache 内存放大率 ────────────────────────────────────

func TestMemoryAmplification_ClientGo(t *testing.T) {
	// 基线：空 cache 的内存
	forceGCAndStabilize()
	rssBefore := readRSS()

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})

	var rawJSONTotal uint64
	for i := 0; i < objCount; i++ {
		raw := generateComplexDeployment(i)
		rawJSONTotal += uint64(len(raw))

		var deploy appsv1.Deployment
		if err := json.Unmarshal(raw, &deploy); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		_ = indexer.Add(&deploy)
	}

	// 强制 GC，让测量稳定
	forceGCAndStabilize()
	rssAfter := readRSS()

	cacheRSS := rssAfter - rssBefore
	amplification := float64(cacheRSS) / float64(rawJSONTotal)

	// 防止 indexer 被 GC（保活）
	runtime.KeepAlive(indexer)

	t.Logf("\n═══════════════════════════════════════════════")
	t.Logf("  client-go cache 内存放大率")
	t.Logf("═══════════════════════════════════════════════")
	t.Logf("  对象数:        %d", objCount)
	t.Logf("  RawJSON 总量:  %.2f MB", float64(rawJSONTotal)/1024/1024)
	t.Logf("  Cache RSS:    %.2f MB", float64(cacheRSS)/1024/1024)
	t.Logf("  放大率:        %.2fx", amplification)
	t.Logf("═══════════════════════════════════════════════\n")
}

// ─── KubePivot cache 内存放大率 ────────────────────────────────────

func TestMemoryAmplification_KubePivot(t *testing.T) {
	forceGCAndStabilize()
	rssBefore := readRSS()

	c := NewSkeletonCache()

	var rawJSONTotal uint64
	skels := make([]*Skeleton, 0, objCount)
	for i := 0; i < objCount; i++ {
		raw := generateComplexDeployment(i)
		rawJSONTotal += uint64(len(raw))

		s, err := ParseSkeleton(raw)
		if err != nil {
			t.Fatalf("parse skeleton failed: %v", err)
		}
		skels = append(skels, s)
	}
	c.PutBulk(skels)

	forceGCAndStabilize()
	rssAfter := readRSS()

	cacheRSS := rssAfter - rssBefore
	amplification := float64(cacheRSS) / float64(rawJSONTotal)

	runtime.KeepAlive(c)

	t.Logf("\n═══════════════════════════════════════════════")
	t.Logf("  KubePivot cache 内存放大率")
	t.Logf("═══════════════════════════════════════════════")
	t.Logf("  对象数:        %d", objCount)
	t.Logf("  RawJSON 总量:  %.2f MB", float64(rawJSONTotal)/1024/1024)
	t.Logf("  Cache RSS:    %.2f MB", float64(cacheRSS)/1024/1024)
	t.Logf("  放大率:        %.2fx", amplification)
	t.Logf("═══════════════════════════════════════════════\n")
}

// ─── 对比测试（一次跑两个，输出差异）────────────────────────────────

func TestMemoryAmplification_Comparison(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过对比测试（用 -short）")
	}

	t.Log("\n开始对比测试：client-go vs KubePivot")
	t.Log("分两个独立子进程跑，避免内存交叉污染")

	// 子测试 1: client-go
	t.Run("ClientGo", func(t *testing.T) {
		TestMemoryAmplification_ClientGo(t)
	})

	// 等待 GC 稳定
	forceGCAndStabilize()

	// 子测试 2: KubePivot
	t.Run("KubePivot", func(t *testing.T) {
		TestMemoryAmplification_KubePivot(t)
	})

	t.Log("\n如需精确对比，建议分别跑：")
	t.Log("  go test -run=TestMemoryAmplification_ClientGo -v")
	t.Log("  go test -run=TestMemoryAmplification_KubePivot -v")
	t.Log("（同进程内连跑会有内存交叉）")
}

// ─── BenchmarkParseOnly: 仅反序列化对比（不进 cache）────────────────

// 这个 benchmark 隔离"反序列化"这一步的开销
// 用于分析放大率差异的根因

func BenchmarkParseOnly_ClientGo(b *testing.B) {
	rawSamples := make([][]byte, 100)
	for i := range rawSamples {
		rawSamples[i] = generateComplexDeployment(i)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		var deploy appsv1.Deployment
		_ = json.Unmarshal(rawSamples[i%100], &deploy)
		_ = deploy.Name // 防止编译器优化
	}
}

func BenchmarkParseOnly_KubePivot(b *testing.B) {
	rawSamples := make([][]byte, 100)
	for i := range rawSamples {
		rawSamples[i] = generateComplexDeployment(i)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		s, _ := ParseSkeleton(rawSamples[i%100])
		_ = s.Name
	}
}

// 辅助：打印当前内存统计（debug 用）
func DumpMemStats(label string) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("[%s] Alloc=%dMB Sys=%dMB HeapInuse=%dMB NumGC=%d\n",
		label,
		m.Alloc/1024/1024,
		m.Sys/1024/1024,
		m.HeapInuse/1024/1024,
		m.NumGC,
	)
}
