package metrics

import (
	"fmt"
	"strconv"
	"strings"
)

// ─── Quantity ────────────────────────────────────────────────────
//
// Quantity 表示 K8s 资源量（CPU / Memory）。
//
// 标准化基准：
//   - CPU:    milli-cores (1 core = 1000m)
//   - Memory: bytes
//
// 公式：Value = Base × Multiplier
//   Base       原始数字部分（可含小数）
//   Multiplier 单位倍率（见 cpuMultiplier / memoryMultiplier）
//
// 设计哲学：0 client-go 依赖（不引入 k8s.io/apimachinery）
// 与 K8s 官方 quantity.ParseQuantity 行为基本一致：
//   - 支持小数（"1.5Gi" / "0.5"）
//   - 支持 SI 单位（K M G T P）
//   - 支持二进制单位（Ki Mi Gi Ti Pi）
//   - 支持 milli (m) 仅用于 CPU
//
// 不支持的 K8s spec 边缘情况（v2.7.0 范围）:
//   - 科学计数法（"1e3"）
//   - 负数（K8s 资源量无意义）
//   - 单字符 "n"（nano，不在常见 metrics 输出）

// Quantity 表示一个标准化资源量。
type Quantity struct {
	// Value 标准化值
	// CPU: milli-cores 整数（"1"→1000，"100m"→100，"1.5"→1500）
	// Memory: bytes 整数（"1Gi"→1073741824）
	Value int64

	// Raw 原始字符串（保留用于 debug / display）
	// 聚合值（如 PodMetrics.TotalCPU）的 Raw 为空
	Raw string
}

// String 返回 Raw（如有）或 Value 的字符串表示。
func (q Quantity) String() string {
	if q.Raw != "" {
		return q.Raw
	}
	return strconv.FormatInt(q.Value, 10)
}

// IsZero 是否未设置（Value=0 且 Raw=""）。
func (q Quantity) IsZero() bool {
	return q.Value == 0 && q.Raw == ""
}

// ─── CPU 单位倍率 ─────────────────────────────────────────────────

// cpuMultiplier 把单位映射到"乘以多少 milli-cores"
//
// 例：
//   "100m" → Base=100, Multiplier=1     → 100 milli-cores
//   "1"    → Base=1,   Multiplier=1000  → 1000 milli-cores（1 core）
//   "1.5"  → Base=1.5, Multiplier=1000  → 1500 milli-cores
//   "1k"   → Base=1,   Multiplier=1e6   → 1,000,000 milli-cores（1000 cores）
var cpuMultiplier = map[string]int64{
	"":  1000, // 无单位 = cores（默认）
	"n": 0,    // nanocores → < 1 milli-core，向下取 0
	"u": 0,    // microcores → < 1 milli-core，向下取 0
	"m": 1,    // milli-cores
	"k": 1000 * 1000,
	"M": 1000 * 1000 * 1000,
	"G": 1000 * 1000 * 1000 * 1000,
}

// ─── Memory 单位倍率 ─────────────────────────────────────────────

// memoryMultiplier 把单位映射到"乘以多少 bytes"
//
// SI 单位（10 进制）和二进制单位（2 进制）不同：
//   "1G"  = 1,000,000,000 bytes
//   "1Gi" = 1,073,741,824 bytes
//
// K8s spec 推荐用二进制（Mi/Gi），但接受两种。
var memoryMultiplier = map[string]int64{
	"":   1,
	"k":  1000,
	"K":  1000,
	"M":  1000 * 1000,
	"G":  1000 * 1000 * 1000,
	"T":  1000 * 1000 * 1000 * 1000,
	"P":  1000 * 1000 * 1000 * 1000 * 1000,
	"Ki": 1024,
	"Mi": 1024 * 1024,
	"Gi": 1024 * 1024 * 1024,
	"Ti": 1024 * 1024 * 1024 * 1024,
	"Pi": 1024 * 1024 * 1024 * 1024 * 1024,
}

// ─── 解析函数 ────────────────────────────────────────────────────

// parseCPU 解析 CPU 字符串为 milli-cores。
//
// 支持格式：
//   "100m"   → 100
//   "1"      → 1000
//   "1.5"    → 1500
//   "0.5"    → 500
//   "2k"     → 2,000,000
//   "320509n"→ 0（nanocores < 1 milli-core，向下取 0）
//   ""       → 0（空字符串视为 0，方便测试）
//
// 不支持：负数 / 科学计数法
func parseCPU(s string) (Quantity, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Quantity{}, nil
	}

	base, unit, err := splitBaseUnit(s)
	if err != nil {
		return Quantity{}, fmt.Errorf("metrics: parseCPU(%q): %w", s, err)
	}

	mult, ok := cpuMultiplier[unit]
	if !ok {
		return Quantity{}, fmt.Errorf("metrics: parseCPU(%q): unknown unit %q", s, unit)
	}

	value := int64(base * float64(mult))
	if value < 0 {
		return Quantity{}, fmt.Errorf("metrics: parseCPU(%q): negative value", s)
	}

	return Quantity{Value: value, Raw: s}, nil
}

// parseMemory 解析 Memory 字符串为 bytes。
//
// 支持格式：
//   "256Mi"  → 268,435,456
//   "1Gi"    → 1,073,741,824
//   "1.5Gi"  → 1,610,612,736
//   "1024"   → 1024 (无单位 = bytes)
//   "1K"     → 1000 (SI 大写 K)
//   "1k"     → 1000 (SI 小写 k)
//
// 注意：1G ≠ 1Gi（SI vs 二进制）
func parseMemory(s string) (Quantity, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Quantity{}, nil
	}

	base, unit, err := splitBaseUnit(s)
	if err != nil {
		return Quantity{}, fmt.Errorf("metrics: parseMemory(%q): %w", s, err)
	}

	mult, ok := memoryMultiplier[unit]
	if !ok {
		return Quantity{}, fmt.Errorf("metrics: parseMemory(%q): unknown unit %q", s, unit)
	}

	value := int64(base * float64(mult))
	if value < 0 {
		return Quantity{}, fmt.Errorf("metrics: parseMemory(%q): negative value", s)
	}

	return Quantity{Value: value, Raw: s}, nil
}

// splitBaseUnit 把 "1.5Gi" 拆为 (1.5, "Gi")。
//
// 算法：从右向左扫描，找到第一个非字母位置。
// 字母后缀全部视为单位（含 "Mi" / "Gi" 等二字母单位）。
//
// 边界：
//   "1.5"   → (1.5, "")
//   "100m"  → (100, "m")
//   "256Mi" → (256, "Mi")
//   ""      → error (不应到达此处，调用方先检查)
//   "abc"   → error (无数字部分)
//   "1.5x"  → (1.5, "x") — 单位是否合法由调用方查 multiplier map 判断
func splitBaseUnit(s string) (float64, string, error) {
	if s == "" {
		return 0, "", fmt.Errorf("empty string")
	}

	// 从右向左找数字结束位置
	splitIdx := len(s)
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '.' {
			splitIdx = i + 1
			break
		}
	}

	numStr := s[:splitIdx]
	unit := s[splitIdx:]

	if numStr == "" {
		return 0, "", fmt.Errorf("no numeric part in %q", s)
	}

	base, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, "", fmt.Errorf("invalid number %q: %w", numStr, err)
	}

	return base, unit, nil
}

// ─── 聚合 ────────────────────────────────────────────────────────

// sumQuantities 累加多个 Quantity 的 Value。
//
// 用于 PodMetrics.TotalCPU / TotalMemory 计算。
// 返回的 Quantity.Raw 为空（聚合值无原始字符串）。
func sumQuantities(qs ...Quantity) Quantity {
	var total int64
	for _, q := range qs {
		total += q.Value
	}
	return Quantity{Value: total}
}
