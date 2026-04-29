// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

// Package metrics 提供 K8s 资源指标采集能力.
// Level2 扩展: Prometheus HTTP API wrapper (零额外依赖，对齐 CLI wrapper 哲学).

package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// PrometheusClient 封装 Prometheus HTTP API 查询.
// 设计原则:
//   1. 零额外依赖: 纯 net/http + encoding/json，不引入 prometheus/client_golang
//   2. 接口对齐: 返回 []*PodMetrics，与 KubectlMetricsClient 保持一致
//   3. 可测试: 用函数变量注入 (var httpClient = http.DefaultClient) 方便 mock
//   4. 单位转换: CPU seconds→millicores, Memory bytes→bytes (零转换)
type PrometheusClient struct {
	// BaseURL Prometheus 地址，如 "http://prometheus:9090"
	BaseURL string
}

// NewPrometheusClient 创建 Prometheus 客户端.
func NewPrometheusClient(baseURL string) *PrometheusClient {
	return &PrometheusClient{BaseURL: baseURL}
}

// QueryRange 执行 /api/v1/query_range 查询，返回时间序列的 PodMetrics.
//
// 参数:
//   - ctx: 控制超时/取消
//   - cpuQuery: CPU PromQL，如 `rate(container_cpu_usage_seconds_total{pod="xxx",namespace="yyy"}[5m])`
//   - memQuery: Memory PromQL，如 `container_memory_working_set_bytes{pod="xxx",namespace="yyy"}`
//   - start/end: 查询时间范围
//   - step: 采样步长，如 "15s", "1m"
//
// 返回:
//   - []*PodMetrics: 解析后的指标点列表 (对齐既有接口)
//   - error: 网络/解析/API 错误
//
// 设计:
//   - 同时查询 CPU + Memory，按时间戳对齐合并
//   - Prometheus CPU 单位: seconds → *1000 → millicores (Quantity.Value)
//   - Prometheus Memory 单位: bytes → 直接赋值 (Quantity.Value)
//   - 只处理 resultType="matrix" (范围查询)
func (c *PrometheusClient) QueryRange(ctx context.Context, cpuQuery, memQuery string, start, end time.Time, step time.Duration) ([]*PodMetrics, error) {
	// 1. 并行查询 CPU + Memory (减少延迟)
	type result struct {
		points []promPoint
		err    error
	}
	cpuCh := make(chan result, 1)
	memCh := make(chan result, 1)

	go func() {
		points, err := c.querySingle(ctx, cpuQuery, start, end, step)
		cpuCh <- result{points, err}
	}()
	go func() {
		points, err := c.querySingle(ctx, memQuery, start, end, step)
		memCh <- result{points, err}
	}()

	cpuRes := <-cpuCh
	if cpuRes.err != nil {
		return nil, fmt.Errorf("cpu query: %w", cpuRes.err)
	}
	memRes := <-memCh
	if memRes.err != nil {
		return nil, fmt.Errorf("mem query: %w", memRes.err)
	}

	// 2. 按时间戳对齐合并 (简化: 假设时间戳完全匹配，Level3 支持插值)
	//    构建 map[timestamp]promPoint 方便查找
	cpuMap := make(map[int64]promPoint, len(cpuRes.points))
	for _, p := range cpuRes.points {
		cpuMap[p.ts] = p
	}

	var merged []*PodMetrics
	for _, mp := range memRes.points {
		if cp, ok := cpuMap[mp.ts]; ok {
			// 单位转换:
			// - CPU: Prometheus seconds → millicores (*1000)
			// - Memory: Prometheus bytes → bytes (直接赋值)
			merged = append(merged, &PodMetrics{
				Timestamp:   time.Unix(mp.ts, 0),
				TotalCPU:    Quantity{Value: int64(cp.val * 1000), Raw: ""}, // seconds→millicores
				TotalMemory: Quantity{Value: int64(mp.val), Raw: ""},         // bytes→bytes
			})
		}
	}

	if len(merged) == 0 {
		return nil, fmt.Errorf("no aligned data points for cpu/mem queries")
	}

	return merged, nil
}

// promPoint 内部辅助类型: 单指标时间序列点.
type promPoint struct {
	ts  int64   // Unix timestamp (seconds)
	val float64 // 原始值 (Prometheus 返回 string，解析为 float64)
}

// querySingle 查询单个指标 (CPU 或 Memory) 的时间序列.
func (c *PrometheusClient) querySingle(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]promPoint, error) {
	// 1. 构造查询参数
	//    对齐 Prometheus API: https://prometheus.io/docs/prometheus/latest/querying/api/#range-queries
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", formatTime(start))
	params.Set("end", formatTime(end))
	params.Set("step", formatDuration(step))

	apiURL := fmt.Sprintf("%s/api/v1/query_range?%s", c.BaseURL, params.Encode())

	// 2. 发起请求 (可被测试注入)
	//    复用项目既有模式: var httpClient = http.DefaultClient
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	// 3. 解析响应
	//    Prometheus API 返回格式:
	//    { "status": "success", "data": { "resultType": "matrix", "result": [...] } }
	var promResp prometheusResponse
	if err := json.NewDecoder(resp.Body).Decode(&promResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if promResp.Status != "success" {
		return nil, fmt.Errorf("prometheus api error: status=%s", promResp.Status)
	}
	if promResp.Data.ResultType != "matrix" {
		return nil, fmt.Errorf("unexpected resultType=%q, expected matrix", promResp.Data.ResultType)
	}

	// 4. 提取时间序列点
	//    简化: 只取第一个 result (单 Pod 查询)，Level3 支持多指标合并
	if len(promResp.Data.Result) == 0 {
		return nil, fmt.Errorf("no data returned for query: %s", query)
	}

	var points []promPoint
	for _, pair := range promResp.Data.Result[0].Values {
		if len(pair) != 2 {
			continue // 跳过格式异常点
		}
		// Prometheus 返回: [unix_timestamp_float, value_string]
		tsFloat, ok := pair[0].(float64)
		if !ok {
			continue
		}
		valStr, ok := pair[1].(string)
		if !ok {
			continue
		}
		val, err := strconv.ParseFloat(valStr, 64)
		if err != nil {
			continue // 跳过解析失败点
		}

		points = append(points, promPoint{
			ts:  int64(tsFloat),
			val: val,
		})
	}

	return points, nil
}

// prometheusResponse 定义 API 响应结构 (仅解析所需字段).
type prometheusResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string   `json:"metric"`
			Values [][]interface{}     `json:"values"` // [timestamp_float, value_string]
		} `json:"result"`
	} `json:"data"`
}

// formatTime 转换 time.Time → RFC3339 (Prometheus API 要求).
func formatTime(t time.Time) string {
	return t.Format(time.RFC3339)
}

// formatDuration 转换 time.Duration → Prometheus step 格式 (如 "15s", "1m").
func formatDuration(d time.Duration) string {
	// 简化: 直接秒数 + "s"，Level3 支持智能单位 (m/h/d)
	return fmt.Sprintf("%.0fs", d.Seconds())
}

// --- 测试钩子: 函数变量注入，对齐项目既有 mock 模式 ---

// httpClient 可被测试替换，避免依赖全局状态.
// 用法: old := httpClient; httpClient = mockClient; defer func(){ httpClient = old }()
var httpClient = http.DefaultClient