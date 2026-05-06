package sizing

import (
	"testing"
)

func TestQuantizeGPUMem_A100_40GB(t *testing.T) {
	tests := []struct {
		name     string
		rawBytes int64
		want     int64
	}{
		{"below smallest", 3 * 1024 * 1024 * 1024, 5 * 1024 * 1024 * 1024},
		{"exact match", 10 * 1024 * 1024 * 1024, 10 * 1024 * 1024 * 1024},
		{"between profiles", 12 * 1024 * 1024 * 1024, 20 * 1024 * 1024 * 1024},
		{"above largest", 60 * 1024 * 1024 * 1024, 40 * 1024 * 1024 * 1024},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := QuantizeGPUMem(tt.rawBytes, "A100-SXM4-40GB")
			if got != tt.want {
				t.Errorf("QuantizeGPUMem(%d, A100) = %d, want %d", tt.rawBytes, got, tt.want)
			}
		})
	}
}

func TestQuantizeGPUMem_H100_80GB(t *testing.T) {
	got := QuantizeGPUMem(15*1024*1024*1024, "H100-80GB")
	want := int64(20 * 1024 * 1024 * 1024)
	if got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}

func TestQuantizeGPUMem_UnknownProduct(t *testing.T) {
	raw := int64(13 * 1024 * 1024 * 1024)
	got := QuantizeGPUMem(raw, "UnknownGPU")
	if got != raw {
		t.Errorf("unknown product should return raw value, got %d", got)
	}
}

func TestMIGProfiles_NotEmpty(t *testing.T) {
	if len(MIGProfiles) == 0 {
		t.Error("MIGProfiles should not be empty")
	}
	for product, sizes := range MIGProfiles {
		if len(sizes) == 0 {
			t.Errorf("product %s has no profiles", product)
		}
	}
}
