// Package metrics 提供 K8s 资源使用率 / 容量数据的拉取能力。
//
// v2.7 Step 3 引入此包，作为 v2.9 智能调度（Sizing Engine）的数据基础。
//
// 设计哲学：
//   - 0 client-go 依赖（K8s Quantity 自实现解析）
//   - 0 prometheus client 依赖（v2.7.0 仅 KubectlMetricsClient）
//   - 0 cache（数据时间敏感，每次查询都是实时）
//
// 包关系：
//   - 独立于 internal/eventstream（避免 v2.9 sizing engine 的循环依赖）
//   - 依赖 internal/executor 调 kubectl top
//
// v2.7.0 范围：
//   - MetricsClient 接口
//   - KubectlMetricsClient 实现（kubectl top）
//   - Quantity 解析（CPU milli-cores / Memory bytes）
//
// v2.7.x / v2.8 计划：
//   - PrometheusClient 实现（高质量数据源 + 历史趋势）
//   - 历史趋势查询（5min / 1h / 24h）
//
// v2.9 计划：
//   - Sizing Engine 二维 DP 直接消费此包数据
//
// 用法示例：
//
//	client := metrics.NewKubectlMetricsClient(kubeconfig)
//	pod, err := client.GetPodMetrics(ctx, "default", "myapp-abc123")
//	if err == nil {
//	    fmt.Printf("CPU: %s (%dm), Memory: %s (%dB)\n",
//	        pod.TotalCPU.Raw, pod.TotalCPU.Value,
//	        pod.TotalMemory.Raw, pod.TotalMemory.Value)
//	}
package metrics
