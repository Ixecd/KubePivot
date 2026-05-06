package main

import (
	"fmt"
	"math/rand"
)

// FakeGPUClusterConfig 模拟 GPU 集群的配置参数.
type FakeGPUClusterConfig struct {
	Nodes        int     // 节点数
	GPUsPerNode  int     // 每个节点的 GPU 数
	GPUProduct   string  // GPU 型号 (如 "A100-SXM4-40GB")
	NVSwitchCount int    // 每个节点 NVSwitch domain 数 (默认 GPUsPerNode/4)
	TopologyFrag float64 // 0-1, NVLink 拓扑碎片化程度
}

// FakeGPUCluster 模拟的 GPU 集群.
type FakeGPUCluster struct {
	cfg   FakeGPUClusterConfig
	nodes []*FakeNode
	pods  []*FakePod
}

// FakeNode 模拟的 K8s 节点（含 GPU 标注）.
type FakeNode struct {
	Name        string
	GPUProduct  string
	GPUs        []FakeGPU
	Allocatable map[string]string // cpu, memory, nvidia.com/gpu
	Labels      map[string]string
}

// FakeGPU 模拟的单张 GPU 卡.
type FakeGPU struct {
	Index    int    // GPU 序号 0-7
	NVSwitch int    // 所属 NVSwitch domain
	UUID     string // 模拟的 GPU UUID
}

// FakePod 模拟的 K8s Pod（含 GPU 请求）.
type FakePod struct {
	Namespace   string
	Name        string
	NodeName    string // 分配到的节点
	RequestsCPU int64
	RequestsMem int64
	RequestsGPU float64 // v3.1: 浮点数 GPU（整卡 = 1.0，共享 = 0.1-1.0）
	Phase       string
	Labels      map[string]string
}

// NewFakeGPUCluster 构造模拟 GPU 集群.
func NewFakeGPUCluster(cfg FakeGPUClusterConfig) *FakeGPUCluster {
	if cfg.NVSwitchCount == 0 {
		cfg.NVSwitchCount = cfg.GPUsPerNode / 4
		if cfg.NVSwitchCount < 1 {
			cfg.NVSwitchCount = 1
		}
	}
	return &FakeGPUCluster{cfg: cfg}
}

// GenerateNodes 生成 fake GPU 节点.
func (c *FakeGPUCluster) GenerateNodes() []*FakeNode {
	c.nodes = make([]*FakeNode, c.cfg.Nodes)
	gpusPerNVSwitch := c.cfg.GPUsPerNode / c.cfg.NVSwitchCount
	for i := range c.nodes {
		gpus := make([]FakeGPU, c.cfg.GPUsPerNode)
		for j := range gpus {
			nvswitch := j / gpusPerNVSwitch
			// 按 TopologyFrag 决定 NVLink 拓扑的碎片程度
			if c.cfg.TopologyFrag > 0 && rand.Float64() < c.cfg.TopologyFrag {
				nvswitch = rand.Intn(c.cfg.NVSwitchCount) // 随机打散
			}
			gpus[j] = FakeGPU{
				Index:    j,
				NVSwitch: nvswitch,
				UUID:     fmt.Sprintf("GPU-%s-%d-%d", c.cfg.GPUProduct, i, j),
			}
		}
		c.nodes[i] = &FakeNode{
			Name:       fmt.Sprintf("fake-gpu-node-%d", i),
			GPUProduct: c.cfg.GPUProduct,
			GPUs:       gpus,
			Allocatable: map[string]string{
				"cpu":            "64",
				"memory":         "256Gi",
				"nvidia.com/gpu": fmt.Sprintf("%d", c.cfg.GPUsPerNode),
			},
			Labels: map[string]string{
				"nvidia.com/gpu.product": c.cfg.GPUProduct,
				"nvidia.com/gpu.count":   fmt.Sprintf("%d", c.cfg.GPUsPerNode),
				"kubepivot.io/fake":      "true",
			},
		}
	}
	return c.nodes
}

// GeneratePods 生成 fake Pod（随机分配 GPU 需求）.
func (c *FakeGPUCluster) GeneratePods() []*FakePod {
	if c.nodes == nil {
		c.GenerateNodes()
	}

	totalGPUs := c.cfg.Nodes * c.cfg.GPUsPerNode
	// 生成约 2x GPU 总数的 Pod（部分整卡、部分共享）
	podCount := totalGPUs * 2
	if podCount > 50000 {
		podCount = 50000
	}
	c.pods = make([]*FakePod, podCount)

	for i := range c.pods {
		// 70% 的 Pod 请求整卡, 30% 请求共享 GPU
		var gpuReq float64
		if rand.Float64() < 0.7 {
			gpuReq = 1.0
		} else {
			gpuReq = float64(rand.Intn(10)+1) / 10.0 // 0.1-1.0
		}

		c.pods[i] = &FakePod{
			Namespace:   fmt.Sprintf("ns-%d", i%50),  // 50 个 ns 分散
			Name:        fmt.Sprintf("fake-pod-%d", i),
			RequestsCPU: int64(rand.Intn(4000) + 100), // 100m-4000m
			RequestsMem: int64(rand.Intn(4096)+128) * 1024 * 1024, // 128Mi-4Gi
			RequestsGPU: gpuReq,
			Phase:       "Running",
			Labels: map[string]string{
				"app":                     fmt.Sprintf("app-%d", i%20),
				"kubepivot.io/fake":       "true",
				"kubepivot.io/gpu-shared": fmt.Sprintf("%v", gpuReq < 1.0),
			},
		}
	}
	return c.pods
}

// FreeGPUsInNVSwitch 统计指定 NVSwitch domain 的空闲 GPU 数.
func (n *FakeNode) FreeGPUsInNVSwitch(nvswitch int) int {
	count := 0
	for _, g := range n.GPUs {
		if g.NVSwitch == nvswitch {
			count++
		}
	}
	return count
}
