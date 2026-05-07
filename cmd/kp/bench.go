package main

import (
	"fmt"
	"os"
	"os/exec"
)

func runBench(args []string) {
	if len(args) == 0 || args[0] == "help" {
		fmt.Println(`kp bench — KubePivot 性能基准工具

用法:
  kp bench kvcache    KVCache 基准 (Get/ListAll/Put/ColdStart/Memory 等)
  kp bench pool       池化调度基准 (FragmentRate/O(1)Counters/Sampling)
  kp bench memory     内存分析 (Labels压缩/放大率/Breakdown)
  kp bench concurrent 并发基准 (ShardedPodCache 64g Put)
  kp bench storm      Delta Storm 基准 (merge-on-read p99)
  kp bench all        全量基准
  kp bench scale      规模化 (1k/10k/100k FragmentRate)
  kp bench gen        生成测试数据
  kp bench chaos      混沌测试 (kind + jitter + gone)
  kp bench sim        生产模拟器

示例:
  kp bench kvcache               # KVCache 全部基准
  kp bench pool                  # 池化全部基准
  kp bench all --cpuprofile      # 全量 + CPU profile
  kp bench gen --pods=100000     # 生成 100k pod fixture`)
		return
	}

	sub := args[0]
	switch sub {
	case "kvcache":
		runBenchKVCache()
	case "pool":
		runBenchPool()
	case "memory":
		runBenchMemory()
	case "concurrent":
		runBenchConcurrent()
	case "storm":
		runBenchStorm()
	case "all":
		runBenchKVCache()
		fmt.Println()
		runBenchPool()
		fmt.Println()
		runBenchMemory()
		fmt.Println()
		runBenchConcurrent()
		fmt.Println()
		runBenchStorm()
	case "scale":
		runBenchScale()
	case "gen":
		runBenchGen(args[1:])
	case "chaos":
		runBenchChaos(args[1:])
	case "sim":
		runBenchSim()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %q\n", sub)
		os.Exit(1)
	}
}

func goTest(name string, args ...string) {
	fmt.Printf("=== %s ===\n", name)
	cmd := exec.Command("go", append([]string{"test"}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
	fmt.Println()
}

func runBenchKVCache() {
	fmt.Println("═══ KVCache 基准 ═══")
	goTest("读: Get", "-bench=BenchmarkPodCache_Get$", "-benchmem", "-benchtime=1s", "-run=^$", "./internal/eventstream/")
	goTest("读: ListAll (1000/5000/10000)", "-bench=BenchmarkPodCache_ListAll", "-benchmem", "-benchtime=1s", "-run=^$", "./internal/eventstream/")
	goTest("读: ListByNode", "-bench=BenchmarkPodCache_ListByNode$", "-benchmem", "-benchtime=500ms", "-run=^$", "./internal/eventstream/")
	goTest("读: ConcurrentRead", "-bench=BenchmarkPodCache_ConcurrentRead$", "-benchmem", "-benchtime=500ms", "-run=^$", "./internal/eventstream/")
	goTest("读: ListByNS", "-bench=BenchmarkPodCache_ListByNS$", "-benchmem", "-benchtime=500ms", "-run=^$", "./internal/eventstream/")
	goTest("读: NodeCache Get", "-bench=BenchmarkNodeCache_Get$", "-benchmem", "-benchtime=500ms", "-run=^$", "./internal/eventstream/")
	goTest("读: NodeCache ListAll", "-bench=BenchmarkNodeCache_ListAll$", "-benchmem", "-benchtime=500ms", "-run=^$", "./internal/eventstream/")
	goTest("写: Put", "-bench=BenchmarkPodCache_Put$", "-benchmem", "-benchtime=1s", "-run=^$", "./internal/eventstream/")
	goTest("写: PutCrossNode", "-bench=BenchmarkPodCache_PutCrossNode$", "-benchmem", "-benchtime=500ms", "-run=^$", "./internal/eventstream/")
	goTest("写: Delete", "-bench=BenchmarkPodCache_Delete$", "-benchmem", "-benchtime=1s", "-run=^$", "./internal/eventstream/")
	goTest("写: PutBulk (1k/5k/10k)", "-bench=BenchmarkPodCache_PutBulk", "-benchmem", "-benchtime=1s", "-run=^$", "./internal/eventstream/")
	goTest("冷启动: ColdStart (1k/5k/10k)", "-bench=BenchmarkPodCache_ColdStart", "-benchmem", "-benchtime=1s", "-run=^$", "./internal/eventstream/")
	goTest("Subscribe: Notify", "-bench=BenchmarkPodCache_SubscribeNotify$", "-benchmem", "-benchtime=500ms", "-run=^$", "./internal/eventstream/")
}

func runBenchPool() {
	fmt.Println("═══ 池化调度基准 ═══")
	goTest("FragmentRate (1k/10k/100k)", "-bench=BenchmarkFragmentRate", "-benchmem", "-benchtime=3s", "-run=^$", "./internal/scheduler/")
	goTest("O(1) Counters (10k/100k)", "-bench=BenchmarkPoolUtil_O1", "-benchmem", "-benchtime=1s", "-run=^$", "./internal/scheduler/")
	goTest("Sampled FragmentRate (10k/100k)", "-bench=BenchmarkFragmentRateSampled", "-benchmem", "-benchtime=1s", "-run=^$", "./internal/scheduler/")
	goTest("PoolIndex Compute (10k/100k)", "-bench=BenchmarkPoolIndex_Compute", "-benchmem", "-benchtime=1s", "-run=^$", "./internal/scheduler/")
	goTest("采样误差验证", "-run=TestFragSamplingError", "-v", "./internal/scheduler/")
}

func runBenchMemory() {
	fmt.Println("═══ 内存分析 ═══")
	goTest("PodEntry Alloc (20 labels)", "-bench=BenchmarkPodEntry_Alloc$", "-benchmem", "-benchtime=1s", "-run=^$", "./internal/eventstream/")
	goTest("PodEntry Alloc (8 common labels)", "-bench=BenchmarkPodEntry_AllocCommon$", "-benchmem", "-benchtime=1s", "-run=^$", "./internal/eventstream/")
	goTest("Labels map alone", "-bench=BenchmarkLabels_Alloc", "-benchmem", "-benchtime=1s", "-run=^$", "./internal/eventstream/")
	goTest("Memory at scale (1k/5k/10k)", "-bench=BenchmarkPodCache_Memory$", "-benchmem", "-benchtime=1s", "-run=^$", "./internal/eventstream/")
	goTest("Memory Amplification (1k/5k/10k)", "-bench=BenchmarkPodCache_MemoryAmp", "-benchmem", "-benchtime=1x", "-run=^$", "./internal/eventstream/")
	goTest("Memory Breakdown", "-run=TestPodEntry_MemoryBreakdown", "-v", "./internal/eventstream/")
}

func runBenchConcurrent() {
	fmt.Println("═══ 并发基准 ═══")
	goTest("ShardedPodCache 64g Put", "-bench=BenchmarkPodCache_ParallelPut", "-benchmem", "-benchtime=2s", "-run=^$", "./internal/eventstream/")
}

func runBenchStorm() {
	fmt.Println("═══ Delta Storm 基准 ═══")
	goTest("DeltaStorm ListAll", "-bench=BenchmarkPodCache_DeltaStorm$", "-benchmem", "-benchtime=1s", "-run=^$", "./internal/eventstream/")
	goTest("DeltaStorm ListByNode", "-bench=BenchmarkPodCache_DeltaStorm_ListByNode$", "-benchmem", "-benchtime=1s", "-run=^$", "./internal/eventstream/")
}

func runBenchScale() {
	fmt.Println("═══ 规模化基准 ═══")
	goTest("FragmentRate 1k/10k/100k", "-bench=BenchmarkFragmentRate", "-benchmem", "-benchtime=3s", "-run=^$", "./internal/scheduler/")
	goTest("O(1) Counters 100k", "-bench=BenchmarkPoolUtil_O1_100k", "-benchmem", "-benchtime=1s", "-run=^$", "./internal/scheduler/")
}

func runBenchSim() {
	fmt.Println("=== 生产模拟器 ===")
	cmd := exec.Command("go", "test", "-bench=.", "-benchtime=1x", "-run=^$", "./benchmark/sim/")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
}

func runBenchGen(args []string) {
	pods := "10000"
	output := "benchmark/fixtures/pods.json"
	for _, a := range args {
		if len(a) > 7 && a[:7] == "--pods=" {
			pods = a[7:]
		} else if len(a) > 9 && a[:9] == "--output=" {
			output = a[9:]
		}
	}
	fmt.Printf("=== 生成测试数据 (pods=%s) ===\n", pods)
	cmd := exec.Command("go", "run", "benchmark/fixtures/gen_real.go", "--count", pods)
	f, err := os.Create(output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法创建: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()
	cmd.Stdout = f
	cmd.Stderr = os.Stderr
	cmd.Run()
	fmt.Printf("  输出: %s\n", output)
}

func runBenchChaos(args []string) {
	cmd := exec.Command("bash", "benchmark/chaos/kind-chaos-lite.sh")
	cmd.Args = append(cmd.Args, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
}
