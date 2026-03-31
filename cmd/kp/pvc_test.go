package main

import (
	"strings"
	"testing"
)

// ── buildSnapshotYAML ─────────────────────────────────────────────────────────

func TestBuildSnapshotYAML_ContainsRequiredFields(t *testing.T) {
	yaml := buildSnapshotYAML(
		"data-0-snap-1234",
		"data-0",
		"postgres",
		"csi-hostpath-snapclass",
		"web3-blitz",
	)

	checks := []struct {
		field string
		want  string
	}{
		{"name", "name: data-0-snap-1234"},
		{"namespace", "namespace: web3-blitz"},
		{"kp.io/service label", "kp.io/service: postgres"},
		{"kp.io/pvc label", "kp.io/pvc: data-0"},
		{"snapshotClassName", "volumeSnapshotClassName: csi-hostpath-snapclass"},
		{"source pvc", "persistentVolumeClaimName: data-0"},
		{"apiVersion", "snapshot.storage.k8s.io/v1"},
		{"kind", "kind: VolumeSnapshot"},
	}

	for _, c := range checks {
		if !strings.Contains(yaml, c.want) {
			t.Errorf("buildSnapshotYAML 缺少 %s：期望包含 %q", c.field, c.want)
		}
	}
}

func TestBuildSnapshotYAML_NameInjected(t *testing.T) {
	yaml := buildSnapshotYAML("my-snap", "my-pvc", "svc", "class", "ns")
	if !strings.Contains(yaml, "name: my-snap") {
		t.Error("snapshot 名称未正确注入")
	}
}

// ── buildPVCFromSnapshotYAML ──────────────────────────────────────────────────

func TestBuildPVCFromSnapshotYAML_ContainsRequiredFields(t *testing.T) {
	yaml := buildPVCFromSnapshotYAML("data-0", "data-0-snap-1234", "web3-blitz")

	checks := []struct {
		field string
		want  string
	}{
		{"pvc name", "name: data-0"},
		{"namespace", "namespace: web3-blitz"},
		{"snapshot source name", "name: data-0-snap-1234"},
		{"kind VolumeSnapshot", "kind: VolumeSnapshot"},
		{"apiGroup", "apiGroup: snapshot.storage.k8s.io"},
		{"accessMode", "ReadWriteOnce"},
		{"storage request", "storage: 1Gi"},
	}

	for _, c := range checks {
		if !strings.Contains(yaml, c.want) {
			t.Errorf("buildPVCFromSnapshotYAML 缺少 %s：期望包含 %q", c.field, c.want)
		}
	}
}

func TestBuildPVCFromSnapshotYAML_PVCNameMatchesOriginal(t *testing.T) {
	// PVC 名必须和原 PVC 完全一致，StatefulSet 扩容后才能自动绑定
	pvcName := "postgres-data-web3-blitz-postgres-0"
	yaml := buildPVCFromSnapshotYAML(pvcName, "snap-123", "ns")
	if !strings.Contains(yaml, "name: "+pvcName) {
		t.Errorf("PVC 名称必须和原 PVC 完全一致，got yaml:\n%s", yaml)
	}
}

// ── extractOrdinal ────────────────────────────────────────────────────────────

func TestExtractOrdinal_Normal(t *testing.T) {
	cases := []struct {
		podName string
		stsName string
		want    int
	}{
		{"web3-blitz-postgres-0", "web3-blitz-postgres", 0},
		{"web3-blitz-etcd-0", "web3-blitz-etcd", 0},
		{"myapp-redis-3", "myapp-redis", 3},
		{"svc-12", "svc", 12},
	}
	for _, c := range cases {
		got := extractOrdinal(c.podName, c.stsName)
		if got != c.want {
			t.Errorf("extractOrdinal(%q, %q) = %d, want %d",
				c.podName, c.stsName, got, c.want)
		}
	}
}

func TestExtractOrdinal_InvalidSuffix(t *testing.T) {
	// 非数字后缀应返回 -1
	got := extractOrdinal("web3-blitz-postgres-abc", "web3-blitz-postgres")
	if got != -1 {
		t.Errorf("非数字后缀应返回 -1，got %d", got)
	}
}

func TestExtractOrdinal_NoMatch(t *testing.T) {
	// pod 名和 sts 名不匹配，suffix 不是纯数字
	got := extractOrdinal("other-pod-0", "web3-blitz-postgres")
	if got != -1 {
		t.Errorf("不匹配的 pod 名应返回 -1，got %d", got)
	}
}
