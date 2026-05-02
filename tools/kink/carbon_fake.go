package main

import (
	"fmt"
	"math"
	"time"
)

// CarbonPoint 碳强度时间点.
type CarbonPoint struct {
	Timestamp time.Time
	Intensity float64 // gCO₂eq/kWh
}

// CarbonSimulator 碳强度模拟器.
//
// 模拟一天 24 小时的碳强度波动：
//   - 凌晨 0-6 点: 低碳 (150-250), 可再生能源占比高
//   - 上午 6-12 点: 上升 (250-400), 工业用电增加
//   - 下午 12-18 点: 高峰 (350-500), 火电全开
//   - 晚上 18-24 点: 下降 (250-350), 工业用电减少
type CarbonSimulator struct {
	Region string
}

// NewCarbonSimulator 创建碳强度模拟器.
func NewCarbonSimulator(region string) *CarbonSimulator {
	if region == "" {
		region = "US-West"
	}
	return &CarbonSimulator{Region: region}
}

// GetCurrentIntensity 返回当前时刻的碳强度.
func (s *CarbonSimulator) GetCurrentIntensity(now time.Time) float64 {
	return s.intensityAt(now)
}

// GetForecast 返回未来 N 小时的碳强度预测（每小时一个点）.
func (s *CarbonSimulator) GetForecast(now time.Time, hours int) []CarbonPoint {
	points := make([]CarbonPoint, hours)
	for i := 0; i < hours; i++ {
		t := now.Add(time.Duration(i) * time.Hour)
		points[i] = CarbonPoint{
			Timestamp: t,
			Intensity: s.intensityAt(t),
		}
	}
	return points
}

// FindLowCarbonWindow 在预测中找到第一个低碳窗口.
//
// 返回 (窗口起始时间, 是否找到).
// 低碳窗口定义: 碳强度 < threshold 且持续至少 minWindowHours 小时.
func (s *CarbonSimulator) FindLowCarbonWindow(now time.Time, threshold float64, maxHours int, minWindowHours int) (time.Time, bool) {
	forecast := s.GetForecast(now, maxHours)

	consecutiveLow := 0
	for _, p := range forecast {
		if p.Intensity < threshold {
			consecutiveLow++
			if consecutiveLow >= minWindowHours {
				return p.Timestamp.Add(-time.Duration(minWindowHours-1) * time.Hour), true
			}
		} else {
			consecutiveLow = 0
		}
	}
	return time.Time{}, false
}

// intensityAt 计算指定时刻的碳强度.
//
// 模拟公式:
//   base = 200 (基线碳强度)
//   daytime = sin²((hour-6)/12 * π)  (6-18 点 = 正半周期, 峰值在 12 点)
//   intensity = base + daytime * 300
func (s *CarbonSimulator) intensityAt(t time.Time) float64 {
	hour := float64(t.Hour()) + float64(t.Minute())/60.0

	// 将 6-18 点映射到正弦函数的 [0, π]
	if hour < 6 {
		hour = 6 // 凌晨最小值
	}
	if hour > 18 {
		hour = 18 // 晚上降至基线
	}

	daytime := math.Pow(math.Sin((hour-6)/12.0*math.Pi), 2)
	intensity := 200.0 + daytime*300.0

	// 加 5% 随机噪声
	intensity *= 0.95 + 0.1*math.Sin(float64(t.Minute())*0.1)

	return intensity
}

// FormatIntensity 格式化碳强度为可读字符串.
func FormatIntensity(intensity float64) string {
	if intensity < 250 {
		return fmt.Sprintf("%.0f gCO₂eq/kWh 🟢 (低碳)", intensity)
	}
	if intensity < 400 {
		return fmt.Sprintf("%.0f gCO₂eq/kWh 🟡 (中等)", intensity)
	}
	return fmt.Sprintf("%.0f gCO₂eq/kWh 🔴 (高碳)", intensity)
}
