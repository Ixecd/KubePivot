package eventstream

import (
	"testing"
	"time"
)

// ─── DefaultResyncPeriod 资源类型映射测试 ─────────────────────────

func TestDefaultResyncPeriod_HighFrequency(t *testing.T) {
	tests := []string{"pods", "events"}
	for _, r := range tests {
		got := DefaultResyncPeriod(r)
		if got != ResyncPeriodHigh {
			t.Errorf("%s: got %v, want %v (HighFrequency 10min)",
				r, got, ResyncPeriodHigh)
		}
	}
}

func TestDefaultResyncPeriod_MediumFrequency(t *testing.T) {
	tests := []string{
		"deployments", "statefulsets", "daemonsets",
		"replicasets", "jobs", "cronjobs",
	}
	for _, r := range tests {
		got := DefaultResyncPeriod(r)
		if got != ResyncPeriodMedium {
			t.Errorf("%s: got %v, want %v (Medium 30min)",
				r, got, ResyncPeriodMedium)
		}
	}
}

func TestDefaultResyncPeriod_LowFrequency(t *testing.T) {
	tests := []string{
		"services", "configmaps", "secrets",
		"ingresses", "horizontalpodautoscalers",
		"persistentvolumeclaims",
		"endpoints", "endpointslices",
	}
	for _, r := range tests {
		got := DefaultResyncPeriod(r)
		if got != ResyncPeriodLow {
			t.Errorf("%s: got %v, want %v (Low 60min)",
				r, got, ResyncPeriodLow)
		}
	}
}

func TestDefaultResyncPeriod_VeryLowFrequency(t *testing.T) {
	tests := []string{
		"namespaces", "nodes",
		"storageclasses", "persistentvolumes",
		"customresourcedefinitions",
	}
	for _, r := range tests {
		got := DefaultResyncPeriod(r)
		if got != ResyncPeriodVeryLow {
			t.Errorf("%s: got %v, want %v (VeryLow 120min)",
				r, got, ResyncPeriodVeryLow)
		}
	}
}

// ─── 大小写 / 单复数容错测试 ──────────────────────────────────────

func TestDefaultResyncPeriod_CaseInsensitive(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"Deployments", ResyncPeriodMedium},
		{"DEPLOYMENTS", ResyncPeriodMedium},
		{"deployments", ResyncPeriodMedium},
	}
	for _, tt := range tests {
		got := DefaultResyncPeriod(tt.input)
		if got != tt.want {
			t.Errorf("%q: got %v, want %v (大小写应不敏感)",
				tt.input, got, tt.want)
		}
	}
}

func TestDefaultResyncPeriod_SingularToPlural(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"pod", ResyncPeriodHigh},               // pod → pods
		{"deployment", ResyncPeriodMedium},      // deployment → deployments
		{"service", ResyncPeriodLow},            // service → services (es)
		{"ingress", ResyncPeriodLow},            // ingress → ingresses (es)
		{"namespace", ResyncPeriodVeryLow},      // namespace → namespaces
	}
	for _, tt := range tests {
		got := DefaultResyncPeriod(tt.input)
		if got != tt.want {
			t.Errorf("%q: got %v, want %v (单数应转复数)",
				tt.input, got, tt.want)
		}
	}
}

// ─── 容错：未知 / 空 / 空格 ───────────────────────────────────────

func TestDefaultResyncPeriod_UnknownResource(t *testing.T) {
	got := DefaultResyncPeriod("foobar")
	if got != ResyncPeriodMedium {
		t.Errorf("未知资源应默认 Medium 30min, got %v", got)
	}
}

func TestDefaultResyncPeriod_EmptyString(t *testing.T) {
	got := DefaultResyncPeriod("")
	if got != ResyncPeriodMedium {
		t.Errorf("空字符串应默认 Medium 30min, got %v", got)
	}
}

func TestDefaultResyncPeriod_Whitespace(t *testing.T) {
	tests := []string{"  pods  ", "\tpods\n", " pods"}
	for _, input := range tests {
		got := DefaultResyncPeriod(input)
		if got != ResyncPeriodHigh {
			t.Errorf("%q: 应处理空格, got %v", input, got)
		}
	}
}

// ─── 周期值合理性测试 ────────────────────────────────────────────

func TestResyncPeriods_Ordering(t *testing.T) {
	// 验证周期单调递增（高频 < 中频 < 低频 < 极低频）
	if !(ResyncPeriodHigh < ResyncPeriodMedium) {
		t.Errorf("ResyncPeriodHigh(%v) 应 < Medium(%v)",
			ResyncPeriodHigh, ResyncPeriodMedium)
	}
	if !(ResyncPeriodMedium < ResyncPeriodLow) {
		t.Errorf("ResyncPeriodMedium(%v) 应 < Low(%v)",
			ResyncPeriodMedium, ResyncPeriodLow)
	}
	if !(ResyncPeriodLow < ResyncPeriodVeryLow) {
		t.Errorf("ResyncPeriodLow(%v) 应 < VeryLow(%v)",
			ResyncPeriodLow, ResyncPeriodVeryLow)
	}
}

func TestResyncPeriods_Values(t *testing.T) {
	// 锁定具体数值，避免误改
	if ResyncPeriodHigh != 10*time.Minute {
		t.Errorf("ResyncPeriodHigh = %v, want 10min", ResyncPeriodHigh)
	}
	if ResyncPeriodMedium != 30*time.Minute {
		t.Errorf("ResyncPeriodMedium = %v, want 30min", ResyncPeriodMedium)
	}
	if ResyncPeriodLow != 60*time.Minute {
		t.Errorf("ResyncPeriodLow = %v, want 60min", ResyncPeriodLow)
	}
	if ResyncPeriodVeryLow != 120*time.Minute {
		t.Errorf("ResyncPeriodVeryLow = %v, want 120min", ResyncPeriodVeryLow)
	}
}

// ─── normalizeResource 内部函数测试 ───────────────────────────────

func TestNormalizeResource(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"deployments", "deployments"}, // 已是复数
		{"Deployment", "deployments"},  // 大小写 + 单数
		{"POD", "pods"},                // 全大写单数
		{"  pods  ", "pods"},           // 空格
		{"", ""},                       // 空字符串
		{"unknownresource", "unknownresource"}, // 未知保留
	}

	for _, tt := range tests {
		got := normalizeResource(tt.input)
		if got != tt.want {
			t.Errorf("normalizeResource(%q) = %q, want %q",
				tt.input, got, tt.want)
		}
	}
}
