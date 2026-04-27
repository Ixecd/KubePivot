package eventstream

import (
	"strings"
	"testing"
)

const validDeploymentJSON = `{
  "apiVersion": "apps/v1",
  "kind": "Deployment",
  "metadata": {
    "namespace": "default",
    "name": "my-app",
    "uid": "abc-123",
    "generation": 5,
    "resourceVersion": "12345",
    "labels": {
      "app": "my-app",
      "version": "v1"
    },
    "annotations": {
      "deploy.kubepivot.io/revision": "3"
    },
    "creationTimestamp": "2026-04-27T09:00:00Z"
  },
  "spec": {
    "replicas": 3
  },
  "status": {
    "phase": "Running",
    "readyReplicas": 3
  }
}`

func TestParseSkeleton_ValidJSON(t *testing.T) {
	r, err := ParseSkeleton([]byte(validDeploymentJSON))
	if err != nil {
		t.Fatalf("ParseSkeleton 失败: %v", err)
	}

	tests := []struct {
		field string
		got   string
		want  string
	}{
		{"APIVersion", r.APIVersion, "apps/v1"},
		{"Kind", r.Kind, "Deployment"},
		{"Namespace", r.Namespace, "default"},
		{"Name", r.Name, "my-app"},
		{"UID", r.UID, "abc-123"},
		{"ResourceVersion", r.ResourceVersion, "12345"},
		{"Phase", r.Phase, "Running"},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %q, want %q", tt.field, tt.got, tt.want)
			}
		})
	}

	if r.Generation != 5 {
		t.Errorf("Generation = %d, want 5", r.Generation)
	}
	if r.Replicas == nil || *r.Replicas != 3 {
		t.Errorf("Replicas = %v, want 3", r.Replicas)
	}
	if r.ReadyReplicas == nil || *r.ReadyReplicas != 3 {
		t.Errorf("ReadyReplicas = %v, want 3", r.ReadyReplicas)
	}
}

func TestParseSkeleton_LabelsPreserved(t *testing.T) {
	r, _ := ParseSkeleton([]byte(validDeploymentJSON))
	if r.Labels["app"] != "my-app" {
		t.Errorf("Labels[app] = %q, want my-app", r.Labels["app"])
	}
	if r.Labels["version"] != "v1" {
		t.Errorf("Labels[version] = %q, want v1", r.Labels["version"])
	}
}

func TestParseSkeleton_AnnotationsPreserved(t *testing.T) {
	r, _ := ParseSkeleton([]byte(validDeploymentJSON))
	got := r.Annotations["deploy.kubepivot.io/revision"]
	if got != "3" {
		t.Errorf("Annotations[deploy.kubepivot.io/revision] = %q, want 3", got)
	}
}

func TestParseSkeleton_RawJSONPreserved(t *testing.T) {
	rawIn := []byte(validDeploymentJSON)
	r, _ := ParseSkeleton(rawIn)
	// RawJSON 应该共享底层 buffer（不拷贝）
	if &r.RawJSON[0] != &rawIn[0] {
		t.Error("RawJSON 应共享底层 buffer，不应拷贝")
	}
}

func TestParseSkeleton_CreationTimestamp(t *testing.T) {
	r, _ := ParseSkeleton([]byte(validDeploymentJSON))
	if r.CreationTimestamp.IsZero() {
		t.Error("CreationTimestamp 应被解析为非零")
	}
	if r.CreationTimestamp.Year() != 2026 {
		t.Errorf("CreationTimestamp year = %d, want 2026", r.CreationTimestamp.Year())
	}
}

// ─── 错误路径 ──────────────────────────────────────────────────────

func TestParseSkeleton_EmptyJSON(t *testing.T) {
	_, err := ParseSkeleton(nil)
	if err == nil {
		t.Error("空 JSON 应返回 error")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("错误信息应包含 'empty', got: %v", err)
	}
}

func TestParseSkeleton_MalformedJSON(t *testing.T) {
	_, err := ParseSkeleton([]byte("{not valid json"))
	if err == nil {
		t.Error("无效 JSON 应返回 error")
	}
	if !strings.Contains(err.Error(), "json unmarshal") {
		t.Errorf("错误信息应说明 unmarshal 失败, got: %v", err)
	}
}

func TestParseSkeleton_MissingKind(t *testing.T) {
	noKindJSON := `{"metadata":{"name":"foo"}}`
	_, err := ParseSkeleton([]byte(noKindJSON))
	if err == nil {
		t.Error("缺 kind 应返回 error")
	}
	if !strings.Contains(err.Error(), "kind") {
		t.Errorf("错误信息应提及 kind, got: %v", err)
	}
}

func TestParseSkeleton_MissingName(t *testing.T) {
	noNameJSON := `{"kind":"Pod","metadata":{}}`
	_, err := ParseSkeleton([]byte(noNameJSON))
	if err == nil {
		t.Error("缺 metadata.name 应返回 error")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("错误信息应提及 name, got: %v", err)
	}
}

// ─── 边界场景 ──────────────────────────────────────────────────────

func TestParseSkeleton_MinimalJSON(t *testing.T) {
	minimalJSON := `{"kind":"Pod","metadata":{"name":"my-pod"}}`
	r, err := ParseSkeleton([]byte(minimalJSON))
	if err != nil {
		t.Fatalf("最小 JSON 应能解析: %v", err)
	}
	if r.Name != "my-pod" {
		t.Errorf("Name = %q, want my-pod", r.Name)
	}
	if r.Replicas != nil {
		t.Errorf("缺失 spec.replicas 应为 nil, got %v", r.Replicas)
	}
}

func TestParseSkeleton_DeletionTimestamp(t *testing.T) {
	withDelJSON := `{
  "kind": "Pod",
  "metadata": {
    "name": "deleting-pod",
    "deletionTimestamp": "2026-04-27T10:00:00Z"
  }
}`
	r, err := ParseSkeleton([]byte(withDelJSON))
	if err != nil {
		t.Fatalf("ParseSkeleton 失败: %v", err)
	}
	if r.DeletionTimestamp == nil {
		t.Error("DeletionTimestamp 应被解析")
	}
}

func TestParseSkeleton_BadTimestamp(t *testing.T) {
	// 时间戳格式异常：应容错（返回零值，不返回 error）
	badJSON := `{
  "kind": "Pod",
  "metadata": {
    "name": "p",
    "creationTimestamp": "not-a-time"
  }
}`
	r, err := ParseSkeleton([]byte(badJSON))
	if err != nil {
		t.Errorf("时间戳格式错误不应阻断解析: %v", err)
	}
	if !r.CreationTimestamp.IsZero() {
		t.Error("无效时间戳应返回零值")
	}
}
