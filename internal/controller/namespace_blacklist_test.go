package controller

import (
	"os"
	"testing"
)

func TestIsProtectedNamespace(t *testing.T) {
	tests := []struct {
		ns   string
		want bool
	}{
		{"kube-system", true},
		{"kube-public", true},
		{"kube-node-lease", true},
		{"kubepivot-system", true},
		{"default", true},
		{"feelings-server", false},
		{"web3-blitz", false},
		{"my-app", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.ns, func(t *testing.T) {
			if got := IsProtectedNamespace(tt.ns); got != tt.want {
				t.Errorf("IsProtectedNamespace(%q) = %v, want %v", tt.ns, got, tt.want)
			}
		})
	}
}

func TestIsProtectedNamespace_ExtraFromEnv(t *testing.T) {
	t.Setenv("KUBEPIVOT_EXTRA_PROTECTED_NS", "istio-system, monitoring ,,logging")
	defer os.Unsetenv("KUBEPIVOT_EXTRA_PROTECTED_NS")

	if !IsProtectedNamespace("istio-system") {
		t.Error("istio-system 应被 extra env 加入黑名单")
	}
	if !IsProtectedNamespace("monitoring") {
		t.Error("monitoring 应被 extra env 加入黑名单（带前后空格）")
	}
	if !IsProtectedNamespace("logging") {
		t.Error("logging 应被 extra env 加入黑名单")
	}
	if IsProtectedNamespace("feelings-server") {
		t.Error("未加入 extra 的 ns 不应被保护")
	}
}
