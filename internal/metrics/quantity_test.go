package metrics

import (
	"strings"
	"testing"
)

// ─── parseCPU ────────────────────────────────────────────────────

func TestParseCPU(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantValue int64
		wantErr   bool
	}{
		// 基础 milli-cores
		{"100m", "100m", 100, false},
		{"1m", "1m", 1, false},
		{"500m", "500m", 500, false},

		// 整数 cores → milli-cores
		{"1 core", "1", 1000, false},
		{"2 cores", "2", 2000, false},
		{"10 cores", "10", 10000, false},

		// 小数 cores
		{"1.5 cores", "1.5", 1500, false},
		{"0.5 cores", "0.5", 500, false},
		{"0.1 cores", "0.1", 100, false},
		{"2.5 cores", "2.5", 2500, false},

		// SI 大单位（罕见但合法）
		{"1k milli", "1k", 1000 * 1000, false},
		{"2M milli", "2M", 2 * 1000 * 1000 * 1000, false},

		// 空字符串 → 0
		{"empty", "", 0, false},
		{"whitespace", "   ", 0, false},

		// 错误情形
		{"unknown unit", "100x", 0, true},
		{"invalid number", "abc", 0, true},
		{"negative", "-100m", 0, true},
		{"only unit", "m", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := ParseCPU(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseCPU(%q) err=%v, wantErr=%v", tt.input, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if q.Value != tt.wantValue {
				t.Errorf("parseCPU(%q) = %d, want %d", tt.input, q.Value, tt.wantValue)
			}
			// Raw 应保留原输入（trim 过）
			expectedRaw := strings.TrimSpace(tt.input)
			if expectedRaw != "" && q.Raw != expectedRaw {
				t.Errorf("Raw=%q, want %q", q.Raw, expectedRaw)
			}
		})
	}
}

// ─── parseMemory ─────────────────────────────────────────────────

func TestParseMemory(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantValue int64
		wantErr   bool
	}{
		// 二进制单位
		{"1Ki", "1Ki", 1024, false},
		{"256Mi", "256Mi", 256 * 1024 * 1024, false},
		{"1Gi", "1Gi", 1024 * 1024 * 1024, false},
		{"2Gi", "2Gi", 2 * 1024 * 1024 * 1024, false},
		{"1Ti", "1Ti", 1024 * 1024 * 1024 * 1024, false},

		// SI 单位（10 进制）
		{"1K SI", "1K", 1000, false},
		{"1k SI lower", "1k", 1000, false},
		{"1M SI", "1M", 1000 * 1000, false},
		{"1G SI", "1G", 1000 * 1000 * 1000, false},

		// SI vs 二进制差异（关键测试）
		{"1G != 1Gi (SI)", "1G", 1000000000, false},
		{"1Gi binary", "1Gi", 1073741824, false},

		// 无单位 = bytes
		{"1024 bytes", "1024", 1024, false},
		{"0 bytes", "0", 0, false},

		// 小数
		{"1.5Gi", "1.5Gi", int64(1.5 * 1024 * 1024 * 1024), false},
		{"0.5Mi", "0.5Mi", int64(0.5 * 1024 * 1024), false},

		// 空字符串 → 0
		{"empty", "", 0, false},
		{"whitespace", "  ", 0, false},

		// 错误情形
		{"unknown unit", "100Xi", 0, true},
		{"unknown unit lowercase", "100mi", 0, true}, // mi 不合法（必须 Mi）
		{"invalid number", "abc", 0, true},
		{"negative", "-1Gi", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := ParseMemory(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseMemory(%q) err=%v, wantErr=%v", tt.input, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if q.Value != tt.wantValue {
				t.Errorf("parseMemory(%q) = %d, want %d", tt.input, q.Value, tt.wantValue)
			}
		})
	}
}

// ─── splitBaseUnit ───────────────────────────────────────────────

func TestSplitBaseUnit(t *testing.T) {
	tests := []struct {
		input    string
		wantBase float64
		wantUnit string
		wantErr  bool
	}{
		{"100", 100, "", false},
		{"100m", 100, "m", false},
		{"256Mi", 256, "Mi", false},
		{"1.5", 1.5, "", false},
		{"1.5Gi", 1.5, "Gi", false},
		{"0.5", 0.5, "", false},

		// 错误情形
		{"", 0, "", true},
		{"abc", 0, "", true},
		{"Mi", 0, "", true}, // 没有数字部分
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			base, unit, err := splitBaseUnit(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("splitBaseUnit(%q) err=%v, wantErr=%v", tt.input, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if base != tt.wantBase {
				t.Errorf("base=%v, want %v", base, tt.wantBase)
			}
			if unit != tt.wantUnit {
				t.Errorf("unit=%q, want %q", unit, tt.wantUnit)
			}
		})
	}
}

// ─── Quantity 行为 ───────────────────────────────────────────────

func TestQuantity_String(t *testing.T) {
	tests := []struct {
		name string
		q    Quantity
		want string
	}{
		{"with raw", Quantity{Value: 100, Raw: "100m"}, "100m"},
		{"only value", Quantity{Value: 1024}, "1024"},
		{"zero", Quantity{}, "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.q.String(); got != tt.want {
				t.Errorf("String()=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestQuantity_IsZero(t *testing.T) {
	if !(Quantity{}).IsZero() {
		t.Error("zero Quantity 应 IsZero=true")
	}
	if (Quantity{Value: 1}).IsZero() {
		t.Error("Value=1 不应 IsZero")
	}
	if (Quantity{Raw: "1m"}).IsZero() {
		t.Error("Raw 非空不应 IsZero")
	}
}

// ─── sumQuantities ───────────────────────────────────────────────

func TestSumQuantities(t *testing.T) {
	q1 := Quantity{Value: 100, Raw: "100m"}
	q2 := Quantity{Value: 200, Raw: "200m"}
	q3 := Quantity{Value: 300, Raw: "300m"}

	sum := sumQuantities(q1, q2, q3)
	if sum.Value != 600 {
		t.Errorf("sum.Value=%d, want 600", sum.Value)
	}
	if sum.Raw != "" {
		t.Errorf("聚合 Quantity.Raw 应为空, got %q", sum.Raw)
	}
}

func TestSumQuantities_Empty(t *testing.T) {
	sum := sumQuantities()
	if sum.Value != 0 {
		t.Errorf("空 sum.Value=%d, want 0", sum.Value)
	}
}
