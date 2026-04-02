package main

import (
	"fmt"
	"sort"
	"time"
)

// checkApiserverLatency 采样 10 次 kubectl get nodes，统计 P50/P99 延迟
func checkApiserverLatency(kubeconfig, context string) checkResult {
	const samples = 10
	var latencies []time.Duration

	for i := 0; i < samples; i++ {
		start := time.Now()
		args := kubectlBaseArgs(kubeconfig, context, "")
		args = append(args, "get", "nodes", "--request-timeout=5s", "-o", "name")
		runOutput(args...)
		latencies = append(latencies, time.Since(start))
	}

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	p50 := latencies[samples/2]
	// p99idx := int(float64(samples-1) * 0.99)
	p99 := latencies[samples-1]
	if p99 == 0 {
		p99 = latencies[samples-1]
	}

	detail := fmt.Sprintf("P50=%dms  P99=%dms（采样 %d 次）",
		p50.Milliseconds(), p99.Milliseconds(), samples)

	// 建议并发度
	suggestion := recommendedParallelism(p99)

	if p99 > 2000*time.Millisecond {
		return checkResult{
			name:    "Apiserver 延迟",
			ok:      false,
			isError: true,
			detail:  detail,
			fix:     fmt.Sprintf("P99 > 2s，集群负载极高，建议 --parallelism=%d，慎重执行大规模部署", suggestion),
		}
	}
	if p99 > 500*time.Millisecond {
		return checkResult{
			name:    "Apiserver 延迟",
			ok:      false,
			isError: false,
			detail:  detail,
			fix:     fmt.Sprintf("P99 > 500ms，建议降低并发：--parallelism=%d", suggestion),
		}
	}
	return checkResult{
		name:   "Apiserver 延迟",
		ok:     true,
		detail: fmt.Sprintf("%s  建议并发度: %d", detail, suggestion),
	}
}

// recommendedParallelism 根据 P99 延迟推荐并发度
func recommendedParallelism(p99 time.Duration) int {
	switch {
	case p99 > 2000*time.Millisecond:
		return 1
	case p99 > 1000*time.Millisecond:
		return 2
	case p99 > 500*time.Millisecond:
		return 4
	default:
		return 8
	}
}
