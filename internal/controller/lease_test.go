package controller

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGenerateIdentity_UniquePerCall(t *testing.T) {
	a := generateIdentity()
	b := generateIdentity()
	if a == b {
		t.Errorf("generateIdentity 应每次返回不同值，got a=b=%q", a)
	}
	if !strings.Contains(a, "-") {
		t.Errorf("identity 格式应为 hostname-<hex>，got %q", a)
	}
	parts := strings.Split(a, "-")
	last := parts[len(parts)-1]
	if len(last) != 8 {
		t.Errorf("identity 后缀应为 8 个 hex 字符，got %q (len=%d)", last, len(last))
	}
}

func TestParseLeaseObject(t *testing.T) {
	jsonStr := `{
		"apiVersion": "coordination.k8s.io/v1",
		"kind": "Lease",
		"metadata": {"name": "test", "namespace": "ns"},
		"spec": {
			"holderIdentity": "pod-abc-12345678",
			"leaseDurationSeconds": 15,
			"acquireTime": "2026-04-25T08:00:00Z",
			"renewTime": "2026-04-25T08:00:10Z",
			"leaseTransitions": 3
		}
	}`

	var lease leaseObject
	if err := json.Unmarshal([]byte(jsonStr), &lease); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if lease.Spec.HolderIdentity != "pod-abc-12345678" {
		t.Errorf("HolderIdentity = %q", lease.Spec.HolderIdentity)
	}
	if lease.Spec.LeaseDurationSeconds != 15 {
		t.Errorf("LeaseDurationSeconds = %d", lease.Spec.LeaseDurationSeconds)
	}
	if lease.Spec.LeaseTransitions != 3 {
		t.Errorf("LeaseTransitions = %d", lease.Spec.LeaseTransitions)
	}
}

func TestLeaseExpiredCalculation(t *testing.T) {
	now := time.Now()
	ttl := 15 * time.Second

	tests := []struct {
		name        string
		renewedAgo  time.Duration
		wantExpired bool
	}{
		{"just-renewed", 1 * time.Second, false},
		{"near-timeout", 14 * time.Second, false},
		{"just-expired", 16 * time.Second, true},
		{"long-expired", 60 * time.Second, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lastRenew := now.Add(-tt.renewedAgo)
			leaseAge := now.Sub(lastRenew)
			expired := leaseAge > ttl
			if expired != tt.wantExpired {
				t.Errorf("renewedAgo=%v, expected expired=%v, got %v",
					tt.renewedAgo, tt.wantExpired, expired)
			}
		})
	}
}
