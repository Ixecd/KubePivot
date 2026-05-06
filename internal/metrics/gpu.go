// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package metrics

import (
	"context"
	"fmt"
	"time"
)

// ─── GPU 指标类型 ────────────────────────────────────────────────

// GPUMetrics 描述单个 GPU 设备的瞬时指标。
// 数据来源：DCGM exporter → Prometheus HTTP API。
type GPUMetrics struct {
	UUID        string  // GPU UUID
	Product     string  // 型号，如 "NVIDIA-A100-SXM4-40GB"
	Index       int     // GPU index on node
	Utilization float64 // 0-100
	MemUsed     int64   // 显存使用量 (bytes)
	MemTotal    int64   // 显存总量 (bytes)
	PowerUsage  float64 // 功耗 (W)
	Timestamp   time.Time
}

// NodeGPUMetrics 是一个节点上所有 GPU 的指标集合。
type NodeGPUMetrics struct {
	NodeName string
	GPUs     []GPUMetrics
}

// GPUStalenessError 表示 GPU 指标已过期（超过 staleness 阈值）。
// Sizing 引擎收到此错误后应回退到保守模式（不降 GPU）。
type GPUStalenessError struct {
	NodeName   string
	LastSample time.Time
}

func (e *GPUStalenessError) Error() string {
	return fmt.Sprintf("GPU metrics for node %s are stale (last sample: %s ago)",
		e.NodeName, time.Since(e.LastSample).Truncate(time.Second))
}

// ─── Staleness / 常量 ────────────────────────────────────────────

const (
	// DefaultGPUStalenessThreshold 超过此阈值认为 DCGM 数据过期。
	// DCGM exporter 的 scrape interval 通常 15-30s，KubePivot 保守取 60s。
	DefaultGPUStalenessThreshold = 60 * time.Second

	// MaxGPUUtilization DCGM_FI_DEV_GPU_UTIL 的最大值（百分比）。
	MaxGPUUtilization = 100.0
)

// ─── Prometheus GPU 查询 ─────────────────────────────────────────

// QueryGPUMetrics 查询指定节点的 GPU 指标。
//
// DCGM exporter 暴露的 metric 名称（NVIDIA 标准化）：
//
//	DCGM_FI_DEV_GPU_UTIL              → GPU 利用率 (%)
//	DCGM_FI_DEV_FB_USED              → 显存使用量 (MiB)
//	DCGM_FI_DEV_FB_TOTAL             → 显存总量 (MiB) — 从 label gpu_product 推断
//	DCGM_FI_DEV_POWER_USAGE          → 功耗 (W)
//
// 参数 window 控制查询窗口（Prometheus range vector [window]）。
//
// 返回值：如果 staleness > DefaultGPUStalenessThreshold，返回 GPUStalenessError。
func (c *PrometheusClient) QueryGPUMetrics(ctx context.Context, nodeName string, window time.Duration) (*NodeGPUMetrics, error) {
	// DCGM 指标按 node label 过滤
	nodeFilter := fmt.Sprintf(`node="%s"`, nodeName)

	// 并行查询四个指标
	type queryResult struct {
		points []promPoint
		err    error
	}

	results := make(map[string]*queryResult, 4)
	queries := map[string]string{
		"util":  fmt.Sprintf(`DCGM_FI_DEV_GPU_UTIL{%s}`, nodeFilter),
		"fb":    fmt.Sprintf(`DCGM_FI_DEV_FB_USED{%s}`, nodeFilter),
		"power": fmt.Sprintf(`DCGM_FI_DEV_POWER_USAGE{%s}`, nodeFilter),
	}

	ch := make(chan struct {
		name string
		res  queryResult
	}, len(queries))

	now := time.Now()
	start := now.Add(-window)

	for name, q := range queries {
		go func(name, q string) {
			points, err := c.querySingle(ctx, q, start, now, 15*time.Second)
			ch <- struct {
				name string
				res  queryResult
			}{name, queryResult{points, err}}
		}(name, q)
	}

	for range queries {
		r := <-ch
		results[r.name] = &r.res
	}

	// 检查错误
	for name, r := range results {
		if r.err != nil {
			return nil, fmt.Errorf("DCGM query %s: %w", name, r.err)
		}
	}

	// 取每个指标的最新采样点
	getLatest := func(name string) (float64, time.Time, error) {
		points := results[name].points
		if len(points) == 0 {
			return 0, time.Time{}, fmt.Errorf("no data for DCGM metric %s on node %s", name, nodeName)
		}
		latest := points[len(points)-1]
		ts := time.Unix(latest.ts, 0)
		return latest.val, ts, nil
	}

	utilVal, utilTs, err := getLatest("util")
	if err != nil {
		return nil, err
	}
	fbVal, fbTs, err := getLatest("fb")
	if err != nil {
		return nil, err
	}
	powerVal, powerTs, err := getLatest("power")
	if err != nil {
		return nil, err
	}

	// Staleness 检查：取最旧的采样时间
	oldestTs := utilTs
	if fbTs.Before(oldestTs) {
		oldestTs = fbTs
	}
	if powerTs.Before(oldestTs) {
		oldestTs = powerTs
	}
	if time.Since(oldestTs) > DefaultGPUStalenessThreshold {
		return nil, &GPUStalenessError{NodeName: nodeName, LastSample: oldestTs}
	}

	// 构造 GPUMetrics。DCGM 不直接暴露 MemTotal，从已知 product 查表。
	// FB_USED 单位为 MiB → 转 bytes。
	memUsed := int64(fbVal * 1024 * 1024)

	metrics := &NodeGPUMetrics{
		NodeName: nodeName,
		GPUs: []GPUMetrics{{
			UUID:        "", // DCGM 单次 query_range 不返回 UUID，需额外 label
			Product:     "", // 从 node label nvidia.com/gpu.product 获取
			Index:       0,
			Utilization: utilVal,
			MemUsed:     memUsed,
			MemTotal:    0, // 由调用方从 NodeInfo.GPU 填充
			PowerUsage:  powerVal,
			Timestamp:   oldestTs,
		}},
	}

	return metrics, nil
}
