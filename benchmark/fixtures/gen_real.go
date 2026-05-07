//go:build ignore

// +build ignore

// gen_real generates realistic K8s Pod fixtures for benchmark use.
// Produces JSON files with production-like label distributions,
// mixed resource profiles (web/GPU/batch), and heterogeneous node topology.
//
// Usage: go run benchmark/fixtures/gen_real.go --count 5000 > benchmark/fixtures/pods-5k.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
)

func main() {
	count := flag.Int("count", 5000, "number of pods to generate")
	flag.Parse()

	pods := generateRealPods(*count)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(pods); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

type realPod struct {
	Namespace  string              `json:"namespace"`
	Name       string              `json:"name"`
	NodeName   string              `json:"nodeName"`
	Phase      string              `json:"phase"`
	Labels     map[string]string   `json:"labels"`
	Containers []realContainer     `json:"containers"`
}

type realContainer struct {
	Name      string        `json:"name"`
	Resources realResources `json:"resources"`
}

type realResources struct {
	Requests realRequests `json:"requests"`
}

type realRequests struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

var phases = []string{"Running", "Running", "Running", "Running", "Pending"}
var webServices = []string{"api-gateway", "user-svc", "order-svc", "payment-svc", "notification-svc"}
var batchJobs = []string{"data-pipeline", "etl-worker", "report-gen", "log-processor", "index-builder"}
var gpuJobs = []string{"model-trainer", "inference-engine", "embedding-svc", "llm-api"}

func generateRealPods(n int) []realPod {
	rng := rand.New(rand.NewSource(42))
	pods := make([]realPod, n)
	nodeCount := n/15 + 1

	for i := 0; i < n; i++ {
		profile := rng.Float64()
		var svc string
		var cpuReq, memReq string
		var gpuNode bool

		switch {
		case profile < 0.7: // 70% web services
			svc = webServices[rng.Intn(len(webServices))]
			cpuReq = fmt.Sprintf("%dm", 100+rng.Intn(900))
			memReq = fmt.Sprintf("%dMi", 128+rng.Intn(896))
		case profile < 0.9: // 20% batch
			svc = batchJobs[rng.Intn(len(batchJobs))]
			cpuReq = fmt.Sprintf("%dm", 500+rng.Intn(3500))
			memReq = fmt.Sprintf("%dMi", 512+rng.Intn(3584))
		default: // 10% GPU
			svc = gpuJobs[rng.Intn(len(gpuJobs))]
			cpuReq = fmt.Sprintf("%dm", 2000+rng.Intn(6000))
			memReq = fmt.Sprintf("%dGi", 8+rng.Intn(24))
			gpuNode = true
		}

		nodeName := fmt.Sprintf("node-%04d", rng.Intn(nodeCount))
		if gpuNode {
			nodeName = fmt.Sprintf("gpu-node-%04d", rng.Intn(nodeCount/5+1))
		}

		containers := []realContainer{
			{Name: svc, Resources: realResources{Requests: realRequests{CPU: cpuReq, Memory: memReq}}},
		}
		// Add sidecar 30% of the time
		if rng.Float64() < 0.3 {
			containers = append(containers, realContainer{
				Name: "sidecar",
				Resources: realResources{Requests: realRequests{CPU: "50m", Memory: "64Mi"}},
			})
		}

		pods[i] = realPod{
			Namespace:  fmt.Sprintf("ns-%d", rng.Intn(20)),
			Name:       fmt.Sprintf("%s-%04d", svc, i),
			NodeName:   nodeName,
			Phase:      phases[rng.Intn(len(phases))],
			Labels:     realLabels(svc, rng),
			Containers: containers,
		}
	}
	return pods
}

func realLabels(svc string, rng *rand.Rand) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/name":     svc,
		"app.kubernetes.io/instance": fmt.Sprintf("%s-%d", svc, rng.Intn(3)),
		"app.kubernetes.io/component": "backend",
		"tier":                        pickOne(rng, "frontend", "backend", "data"),
		"environment":                 pickOne(rng, "production", "staging", "development"),
	}
	// 30% have pool/shard labels
	if rng.Float64() < 0.3 {
		labels["kubepivot.io/pool"] = pickOne(rng, "cpu", "gpu", "memory")
		labels["kubepivot.io/shard"] = fmt.Sprintf("shard-%d", rng.Intn(10))
	}
	return labels
}

func pickOne(rng *rand.Rand, opts ...string) string {
	return opts[rng.Intn(len(opts))]
}
