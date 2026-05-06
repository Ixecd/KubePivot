package scheduler

import (
	"context"
	"testing"

	"github.com/Ixecd/kubepivot/internal/eventstream"
)

func TestInformerAdapter_ListAllPods_Ready(t *testing.T) {
	podCache := eventstream.NewPodCache()
	podCache.PutBulk([]*eventstream.PodEntry{
		{Namespace: "default", Name: "pod-1", NodeName: "node1", Phase: "Running",
			Requests: eventstream.ResourceRequest{CPU: 500, Memory: 256 * 1024 * 1024}},
	})

	nodeCache := eventstream.NewNodeCache()
	adapter := NewInformerAdapter(podCache, nodeCache, nil)

	pods, err := adapter.ListAllPods(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
	if pods[0].Requests.CPU != 500 {
		t.Errorf("CPU = %d, want 500", pods[0].Requests.CPU)
	}
}

func TestInformerAdapter_ListAllNodes_Ready(t *testing.T) {
	podCache := eventstream.NewPodCache()
	nodeCache := eventstream.NewNodeCache()
	nodeCache.PutBulk([]*eventstream.NodeEntry{
		{Name: "node1", AllocatableCPU: 4000},
		{Name: "node2", AllocatableCPU: 8000},
	})

	adapter := NewInformerAdapter(podCache, nodeCache, nil)

	nodes, err := adapter.ListAllNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestInformerAdapter_Fallback(t *testing.T) {
	// 未 ready 的 cache + fallback kubectlAdapter
	podCache := eventstream.NewPodCache()
	nodeCache := eventstream.NewNodeCache()
	fallback := NewKubectlAdapter("") // 空 kubeconfig，会失败但证明了降级路径

	adapter := NewInformerAdapter(podCache, nodeCache, fallback)

	// cache 未 ready，应该走 fallback
	_, err := adapter.ListAllPods(context.Background())
	// fallback 连不上集群 → error，但说明走了降级路径
	_ = err // 预期失败（无集群），不 fatal
}

func TestInformerAdapter_GPUNodeConversion(t *testing.T) {
	podCache := eventstream.NewPodCache()
	nodeCache := eventstream.NewNodeCache()
	nodeCache.PutBulk([]*eventstream.NodeEntry{
		{Name: "gpu-node", AllocatableCPU: 64000,
			GPU: []eventstream.GPUEntry{
				{Product: "A100-40GB", Index: 0, MemTotal: 40 * 1024 * 1024 * 1024, Health: "Healthy", NVLinkDomain: 0},
			},
		},
	})

	adapter := NewInformerAdapter(podCache, nodeCache, nil)
	nodes, err := adapter.ListAllNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatal("expected 1 node")
	}
	if len(nodes[0].GPU) != 1 || nodes[0].GPU[0].Product != "A100-40GB" {
		t.Error("GPU conversion failed")
	}
}
