// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ─── CarbonIntensityProvider ────────────────────────────────────

// CarbonIntensityProvider 提供电网碳排放强度查询。
// v3.1: 基础设施层 — 采集 + 展示，不参与调度决策。
// v3.2: 进入调度 CostFactor，驱动 Waiting Queue 延迟调度。
type CarbonIntensityProvider interface {
	GetCurrentIntensity(ctx context.Context, region string) (float64, error)
	GetForecast(ctx context.Context, region string, hours int) ([]CarbonPoint, error)
}

// CarbonPoint 碳排放强度时间点。
type CarbonPoint struct {
	Timestamp time.Time
	Intensity float64 // gCO₂eq/kWh
}

// ─── CarbonSDK Client ───────────────────────────────────────────

// CarbonSDKClient 通过 Carbon SDK (carbon-aware-sdk) 获取实时碳强度。
// 免费 + 全球覆盖，KubePivot 默认数据源。
// 内置简单缓存：碳强度数据更新周期通常 15-60 分钟，LRU 缓存避免高频 API 调用。
type CarbonSDKClient struct {
	BaseURL string
	cache   *carbonCache
}

type carbonCache struct {
	region    string
	intensity float64
	expiresAt time.Time
}

// NewCarbonSDKClient 创建 CarbonSDK 客户端。
func NewCarbonSDKClient(baseURL string) *CarbonSDKClient {
	if baseURL == "" {
		baseURL = "https://carbon-aware-sdk.azurewebsites.net"
	}
	return &CarbonSDKClient{BaseURL: baseURL}
}

// carbonCacheTTL 碳强度缓存有效期（15 分钟，对齐数据更新周期）。
const carbonCacheTTL = 15 * time.Minute

// GetCurrentIntensity 获取指定区域当前碳强度（带 15 分钟缓存）。
func (c *CarbonSDKClient) GetCurrentIntensity(ctx context.Context, region string) (float64, error) {
	// 缓存命中
	if c.cache != nil && c.cache.region == region && time.Now().Before(c.cache.expiresAt) {
		return c.cache.intensity, nil
	}

	// 创建子超时（2s），避免碳 SDK 慢响应拖慢 CLI
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/emissions/bylocation/current?location=%s", c.BaseURL, region)

	type carbonResp struct {
		Rating   float64 `json:"rating"`
		Location string  `json:"location"`
		Time     string  `json:"time"`
	}

	var results []carbonResp
	if err := c.doJSON(reqCtx, url, &results); err != nil {
		return 0, fmt.Errorf("carbon-sdk current: %w", err)
	}
	if len(results) == 0 {
		return 0, fmt.Errorf("carbon-sdk: no data for region %s", region)
	}

	// 更新缓存
	c.cache = &carbonCache{
		region:    region,
		intensity: results[0].Rating,
		expiresAt: time.Now().Add(carbonCacheTTL),
	}
	return results[0].Rating, nil
}

// GetForecast 获取指定区域未来 N 小时的碳强度预测。
func (c *CarbonSDKClient) GetForecast(ctx context.Context, region string, hours int) ([]CarbonPoint, error) {
	url := fmt.Sprintf("%s/emissions/bylocation/forecast?location=%s&windowSize=%d",
		c.BaseURL, region, hours)

	type carbonRow struct {
		Time  string  `json:"time"`
		Value float64 `json:"value"`
	}

	type forecastResp struct {
		OptimalData []carbonRow `json:"optimalDataPoints"`
	}

	var resp forecastResp
	if err := c.doJSON(ctx, url, &resp); err != nil {
		return nil, fmt.Errorf("carbon-sdk forecast: %w", err)
	}

	points := make([]CarbonPoint, 0, len(resp.OptimalData))
	for _, row := range resp.OptimalData {
		ts, err := time.Parse(time.RFC3339, row.Time)
		if err != nil {
			continue
		}
		points = append(points, CarbonPoint{Timestamp: ts, Intensity: row.Value})
	}
	return points, nil
}

// doJSON 通用 HTTP GET + JSON 解析。零额外依赖，对齐 PrometheusClient 风格。
func (c *CarbonSDKClient) doJSON(ctx context.Context, url string, v interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("carbon-sdk request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("carbon-sdk http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("carbon-sdk status %d: %s", resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("carbon-sdk json: %w", err)
	}
	return nil
}
