// KinK — K8s in KubePivot: fake GPU 集群压力测试工具。
// 模拟 N 个节点（含 GPU + NVLink 拓扑）+ M 个 Pod，验证乾枢调度器。
// 独立编译：go build -o kink ./tools/kink/，不增 kp 二进制体积。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"
)

func main() {
	cfg := FakeGPUClusterConfig{}

	flag.IntVar(&cfg.Nodes, "nodes", 100, "fake node count")
	flag.IntVar(&cfg.GPUsPerNode, "gpus", 8, "GPUs per node")
	flag.StringVar(&cfg.GPUProduct, "product", "A100-SXM4-40GB", "GPU model")
	flag.IntVar(&cfg.NVSwitchCount, "nvswitch", 2, "NVSwitch domains per node")
	flag.Float64Var(&cfg.TopologyFrag, "frag", 0.0, "NVLink topology fragmentation 0-1")
	flag.IntVar(&podsFlag, "pods", 1000, "fake Pod count")
	flag.Float64Var(&gpuPodRatio, "gpu-pods", 0.3, "GPU Pod ratio 0-1")
	flag.Int64Var(&seedFlag, "seed", 0, "random seed (0=time.Now)")

	flag.Parse()

	if seedFlag == 0 {
		seedFlag = time.Now().UnixNano()
	}
	rng := rand.New(rand.NewSource(seedFlag))

	fmt.Fprintf(os.Stderr, "[kink] nodes=%d gpusPerNode=%d product=%s frag=%.0f%% pods=%d gpuRatio=%.0f%%\n",
		cfg.Nodes, cfg.GPUsPerNode, cfg.GPUProduct, cfg.TopologyFrag*100, podsFlag, gpuPodRatio*100)

	cluster := NewFakeGPUCluster(cfg)
	nodes := cluster.GenerateNodes()
	pods := cluster.GeneratePods()

	// Override pod count based on flags
	if podsFlag > 0 && podsFlag < len(pods) {
		pods = pods[:podsFlag]
	}
	_ = rng

	fmt.Fprintf(os.Stderr, "[kink] generated %d nodes, %d pods\n", len(nodes), len(pods))

	report := struct {
		Nodes []*FakeNode `json:"nodes"`
		Pods  []*FakePod  `json:"pods"`
	}{Nodes: nodes, Pods: pods}

	b, _ := json.MarshalIndent(report, "", "  ")
	os.Stdout.Write(b)
}

var (
	podsFlag    int
	gpuPodRatio float64
	seedFlag    int64
)