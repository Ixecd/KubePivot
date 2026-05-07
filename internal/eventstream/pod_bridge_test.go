package eventstream

import (
	"testing"
)

func TestResourceToPodEntry(t *testing.T) {
	// Simulate a minimal Pod JSON from K8s API
	podJSON := []byte(`{
		"metadata": {"name": "test-pod", "namespace": "default", "labels": {"app": "nginx", "tier": "frontend"}},
		"spec": {
			"nodeName": "node-1",
			"containers": [
				{"name": "nginx", "resources": {"requests": {"cpu": "100m", "memory": "128Mi"}}},
				{"name": "sidecar", "resources": {"requests": {"cpu": "50m", "memory": "64Mi"}}}
			]
		},
		"status": {"phase": "Running"}
	}`)

	r := &Resource{
		APIVersion:      "v1",
		Kind:            "Pod",
		Namespace:       "default",
		Name:            "test-pod",
		Phase:           "Running",
		ResourceVersion: "123456",
		Labels:          map[string]string{"app": "nginx", "tier": "frontend"},
		RawJSON:         podJSON,
	}

	entry, err := ResourceToPodEntry(r)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Namespace != "default" || entry.Name != "test-pod" {
		t.Errorf("identity mismatch: %s/%s", entry.Namespace, entry.Name)
	}
	if entry.NodeName != "node-1" {
		t.Errorf("NodeName = %s, want node-1", entry.NodeName)
	}
	if entry.Phase != "Running" {
		t.Errorf("Phase = %s, want Running", entry.Phase)
	}
	if entry.RV != 123456 {
		t.Errorf("RV = %d, want 123456", entry.RV)
	}
	// CPU: 100m + 50m = 150m
	if entry.Requests.CPU != 150 {
		t.Errorf("CPU = %d, want 150 (100m+50m)", entry.Requests.CPU)
	}
	// Mem: 128Mi + 64Mi = 128*1024*1024 + 64*1024*1024
	expectedMem := int64(128*1024*1024 + 64*1024*1024)
	if entry.Requests.Memory != expectedMem {
		t.Errorf("Memory = %d, want %d", entry.Requests.Memory, expectedMem)
	}
	// Labels compressed
	if v, ok := entry.GetLabel("app"); !ok || v != "nginx" {
		t.Errorf("GetLabel(app) = (%s, %v), want (nginx, true)", v, ok)
	}
	if v, ok := entry.GetLabel("tier"); !ok || v != "frontend" {
		t.Errorf("GetLabel(tier) = (%s, %v), want (frontend, true)", v, ok)
	}
}

func TestParseQuantityToMilli(t *testing.T) {
	tests := []struct {
		in  string
		out int64
	}{
		{"100m", 100},
		{"1", 1000},
		{"1.5", 1500},
		{"", 0},
	}
	for _, tt := range tests {
		if got := parseQuantityToMilli(tt.in); got != tt.out {
			t.Errorf("parseQuantityToMilli(%q) = %d, want %d", tt.in, got, tt.out)
		}
	}
}

func TestParseQuantityToBytes(t *testing.T) {
	tests := []struct {
		in  string
		out int64
	}{
		{"128Mi", 128 * 1024 * 1024},
		{"1Gi", 1024 * 1024 * 1024},
		{"512M", 512 * 1000 * 1000},
		{"", 0},
	}
	for _, tt := range tests {
		if got := parseQuantityToBytes(tt.in); got != tt.out {
			t.Errorf("parseQuantityToBytes(%q) = %d, want %d", tt.in, got, tt.out)
		}
	}
}
