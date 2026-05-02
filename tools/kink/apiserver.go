package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// KinkAPIServer 模拟 K8s API Server.
//
// 暴露 /api/v1/nodes 和 /api/v1/pods 端点，
// 返回 fake 节点和 Pod 列表。乾枢调度器通过标准 HTTP 调此 API。
type KinkAPIServer struct {
	nodes []*FakeNode
	pods  []*FakePod
}

// NewKinkAPIServer 创建模拟 API Server.
func NewKinkAPIServer(nodes []*FakeNode, pods []*FakePod) *KinkAPIServer {
	return &KinkAPIServer{nodes: nodes, pods: pods}
}

// Start 启动 HTTP server.
func (s *KinkAPIServer) Start(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/nodes", s.handleNodes)
	mux.HandleFunc("/api/v1/pods", s.handlePods)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/metrics", s.handleMetrics)

	fmt.Printf("KinK API Server listening on %s\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Printf("KinK API Server 退出: %v\n", err)
	}
}

func (s *KinkAPIServer) handleNodes(w http.ResponseWriter, r *http.Request) {
	items := make([]map[string]interface{}, len(s.nodes))
	for i, n := range s.nodes {
		items[i] = map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":   n.Name,
				"labels": n.Labels,
			},
			"status": map[string]interface{}{
				"allocatable": n.Allocatable,
				"capacity":    n.Allocatable,
			},
		}
	}
	resp := map[string]interface{}{
		"kind":       "NodeList",
		"apiVersion": "v1",
		"items":      items,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *KinkAPIServer) handlePods(w http.ResponseWriter, r *http.Request) {
	items := make([]map[string]interface{}, len(s.pods))
	for i, p := range s.pods {
		items[i] = map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      p.Name,
				"namespace": p.Namespace,
				"labels":    p.Labels,
			},
			"spec": map[string]interface{}{
				"nodeName": p.NodeName,
				"containers": []map[string]interface{}{
					{
						"resources": map[string]interface{}{
							"requests": map[string]interface{}{
								"cpu":             fmt.Sprintf("%dm", p.RequestsCPU),
								"memory":          fmt.Sprintf("%dMi", p.RequestsMem>>20),
								"nvidia.com/gpu":  fmt.Sprintf("%.1f", p.RequestsGPU),
							},
						},
					},
				},
			},
			"status": map[string]interface{}{
				"phase": p.Phase,
			},
		}
	}
	resp := map[string]interface{}{
		"kind":       "PodList",
		"apiVersion": "v1",
		"items":      items,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *KinkAPIServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)
	w.Write([]byte("ok"))
}

func (s *KinkAPIServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "# HELP kink_fake_nodes_total Total fake nodes\n")
	fmt.Fprintf(w, "# TYPE kink_fake_nodes_total gauge\n")
	fmt.Fprintf(w, "kink_fake_nodes_total %d\n", len(s.nodes))
	fmt.Fprintf(w, "# HELP kink_fake_pods_total Total fake pods\n")
	fmt.Fprintf(w, "# TYPE kink_fake_pods_total gauge\n")
	fmt.Fprintf(w, "kink_fake_pods_total %d\n", len(s.pods))
}
