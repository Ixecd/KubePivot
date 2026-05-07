package controller

import "testing"

func TestIsCRDKind_Builtin(t *testing.T) {
	builtins := []string{"Deployment", "StatefulSet", "daemonset", "pod", "Service", "ConfigMap", "Secret"}
	for _, k := range builtins {
		if isCRDKind(k) {
			t.Errorf("isCRDKind(%s) should be false for builtin", k)
		}
	}
}

func TestIsCRDKind_CRD(t *testing.T) {
	crds := []string{"VirtualService", "Certificate", "ServiceMonitor", "PrometheusRule"}
	for _, k := range crds {
		if !isCRDKind(k) {
			t.Errorf("isCRDKind(%s) should be true for CRD", k)
		}
	}
}

func TestIsCRDKind_Empty(t *testing.T) {
	if isCRDKind("") {
		t.Error("isCRDKind('') should be false")
	}
}

func TestIsSSAConflict_True(t *testing.T) {
	tests := []string{
		"Apply failed with conflict",
		"another manager owns this field",
		"field manager conflict detected",
		"UPGRADE FAILED: rendered manifests contain a new resource",
	}
	for _, msg := range tests {
		if !isSSAConflict(msg) {
			t.Errorf("isSSAConflict(%q) should be true", msg)
		}
	}
}

func TestIsSSAConflict_False(t *testing.T) {
	if isSSAConflict("normal error message") {
		t.Error("isSSAConflict(normal) should be false")
	}
}
