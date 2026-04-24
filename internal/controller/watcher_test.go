package controller

import (
	"testing"
)

func TestWatchEvent_Meta(t *testing.T) {
	e := WatchEvent{
		Action: WatchAdded,
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      "my-app",
				"namespace": "default",
				"labels": map[string]interface{}{
					"kubepivot.io/managed": "true",
					"app":                  "my-app",
				},
				"annotations": map[string]interface{}{
					"kubepivot.io/sha256": "abc123",
				},
			},
		},
	}

	m := e.Meta()
	if m.Name != "my-app" {
		t.Errorf("Name = %q, want my-app", m.Name)
	}
	if m.Namespace != "default" {
		t.Errorf("Namespace = %q, want default", m.Namespace)
	}
	if m.Labels["kubepivot.io/managed"] != "true" {
		t.Errorf("label kubepivot.io/managed = %q, want true", m.Labels["kubepivot.io/managed"])
	}
	if m.Annotations["kubepivot.io/sha256"] != "abc123" {
		t.Errorf("annotation sha256 = %q, want abc123", m.Annotations["kubepivot.io/sha256"])
	}
}

func TestWatchEvent_Meta_Empty(t *testing.T) {
	// 空事件应返回零值 ObjectMeta，不 panic
	e := WatchEvent{Action: WatchDeleted}
	m := e.Meta()
	if m.Name != "" || m.Namespace != "" {
		t.Errorf("空 Object 应返回空 Meta，got %+v", m)
	}
	if len(m.Labels) != 0 || len(m.Annotations) != 0 {
		t.Error("空 Object 的 Labels/Annotations 应为空")
	}
}

func TestKubectlWatcher_BuildArgs_AllNamespaces(t *testing.T) {
	w := NewKubectlWatcher("configmap", "kubepivot.io/managed=true")
	args := w.buildArgs()

	expected := []string{
		"get", "configmap",
		"-A",
		"-l", "kubepivot.io/managed=true",
		"--watch",
		"--output-watch-events=true",
		"-o", "json",
	}
	if !equalSlices(args, expected) {
		t.Errorf("buildArgs() = %v\nwant %v", args, expected)
	}
}

func TestKubectlWatcher_BuildArgs_SingleNamespace(t *testing.T) {
	w := &KubectlWatcher{
		Resource:      "deployment",
		Namespace:     "feelings-server",
		LabelSelector: "app=my-app",
	}
	args := w.buildArgs()

	expected := []string{
		"get", "deployment",
		"-n", "feelings-server",
		"-l", "app=my-app",
		"--watch",
		"--output-watch-events=true",
		"-o", "json",
	}
	if !equalSlices(args, expected) {
		t.Errorf("buildArgs() = %v\nwant %v", args, expected)
	}
}

func TestKubectlWatcher_BuildArgs_NoLabel(t *testing.T) {
	w := &KubectlWatcher{Resource: "namespace"}
	args := w.buildArgs()
	// 无 label 时不应出现 -l 参数
	for i, a := range args {
		if a == "-l" {
			t.Errorf("未设置 LabelSelector 不应出现 -l（idx=%d, args=%v）", i, args)
		}
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
