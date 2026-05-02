// KinK (K8s in K8s) — 乾枢调度器大规模压测加速器
//
// 在 K8s 集群内启动 fake K8s API Server + fake Kubelet，
// 模拟 N 个节点（含 GPU 标注）+ M 个 Pod。
// 乾枢调度器通过标准 K8s API 交互，不感知真假。
//
// 用法:
//
//	go run tools/kink/main.go --scenario=s1 --nodes=100 --gpus-per-node=8
//	go run tools/kink/main.go --scenario=s5 --nodes=10000 --gpus-per-node=8 --topology-frag=0.3
//
// 场景:
//
//	S1 基础整卡调度
//	S2 MPS 并发上限测试
//	S3 NVLink 全碎降级
//	S4 碳延迟批量
//	S5 万节点压测
//	S6 时空折叠（MPS + 碳延迟 + NVLink 组合）
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	scenario := flag.String("scenario", "s1", "压测场景: s1|s2|s3|s4|s5|s6")
	nodes := flag.Int("nodes", 100, "fake 节点数")
	gpusPerNode := flag.Int("gpus-per-node", 8, "每个节点 GPU 数")
	topologyFrag := flag.Float64("topology-frag", 0.0, "NVLink 拓扑碎片化程度 0-1")
	mpsPods := flag.Int("mps-pods", 0, "MPS 推理 Pod 数")
	carbonJobs := flag.Int("carbon-jobs", 0, "碳延迟 Job 数")
	flag.Parse()

	fmt.Printf("═ KinK v3.2  乾枢调度器压测加速器 ═\n")
	fmt.Printf("  场景:     %s\n", *scenario)
	fmt.Printf("  节点数:   %d\n", *nodes)
	fmt.Printf("  GPU/节点: %d\n", *gpusPerNode)
	fmt.Printf("  拓扑碎片: %.0f%%\n", *topologyFrag*100)
	fmt.Printf("  MPS Pod:  %d\n", *mpsPods)
	fmt.Printf("  碳 Job:   %d\n", *carbonJobs)
	fmt.Println()

	// Step 1: 生成 fake GPU 集群
	cluster := NewFakeGPUCluster(FakeGPUClusterConfig{
		Nodes:        *nodes,
		GPUsPerNode:  *gpusPerNode,
		GPUProduct:   "A100-SXM4-40GB",
		TopologyFrag: *topologyFrag,
	})
	fakeNodes := cluster.GenerateNodes()
	fakePods := cluster.GeneratePods()
	fmt.Printf("✓ 生成 %d fake GPU 节点, %d fake Pod\n", len(fakeNodes), len(fakePods))

	// Step 2: 启动 fake K8s API Server
	kinkServer := NewKinkAPIServer(fakeNodes, fakePods)
	go kinkServer.Start(":18443")
	fmt.Printf("✓ KinK API Server 启动在 :18443\n")

	// Step 3: 运行压测场景
	runner := NewStressRunner(kinkServer, StressConfig{
		Scenario:    *scenario,
		Nodes:       fakeNodes,
		Pods:        fakePods,
		MPSPodCount: *mpsPods,
		CarbonJobs:  *carbonJobs,
	})
	result := runner.Run()
	fmt.Println()
	fmt.Println(result.Summary())

	if result.Passed {
		os.Exit(0)
	}
	os.Exit(1)
}
