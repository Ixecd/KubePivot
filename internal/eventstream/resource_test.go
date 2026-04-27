package eventstream

import (
	"testing"
	"time"
)

// ─── SkeletonChanged 测试 ──────────────────────────────────────────

func TestSkeletonChanged_Identity(t *testing.T) {
	// nil cases
	if !SkeletonChanged(nil, &Resource{}) {
		t.Error("nil → 非 nil 应视为 changed")
	}
	if !SkeletonChanged(&Resource{}, nil) {
		t.Error("非 nil → nil 应视为 changed")
	}
	if SkeletonChanged(nil, nil) {
		t.Error("nil → nil 应视为 unchanged")
	}
}

func TestSkeletonChanged_UID(t *testing.T) {
	old := &Resource{UID: "uid-1"}
	new := &Resource{UID: "uid-2"}
	if !SkeletonChanged(old, new) {
		t.Error("UID 不同应视为 changed")
	}
}

func TestSkeletonChanged_Generation(t *testing.T) {
	old := &Resource{UID: "u", Generation: 1}
	new := &Resource{UID: "u", Generation: 2}
	if !SkeletonChanged(old, new) {
		t.Error("Generation 变化应视为 changed")
	}
}

func TestSkeletonChanged_Phase(t *testing.T) {
	old := &Resource{UID: "u", Phase: "Pending"}
	new := &Resource{UID: "u", Phase: "Running"}
	if !SkeletonChanged(old, new) {
		t.Error("Phase 变化应视为 changed")
	}
}

func TestSkeletonChanged_Replicas(t *testing.T) {
	r1 := int32(3)
	r2 := int32(5)

	tests := []struct {
		name    string
		old     *int32
		new     *int32
		changed bool
	}{
		{"both nil", nil, nil, false},
		{"nil to value", nil, &r1, true},
		{"value to nil", &r1, nil, true},
		{"same value", &r1, &r1, false},
		{"different value", &r1, &r2, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := &Resource{UID: "u", Replicas: tt.old}
			new := &Resource{UID: "u", Replicas: tt.new}
			if got := SkeletonChanged(old, new); got != tt.changed {
				t.Errorf("Replicas %v → %v: got changed=%v, want %v",
					tt.old, tt.new, got, tt.changed)
			}
		})
	}
}

func TestSkeletonChanged_Labels(t *testing.T) {
	old := &Resource{
		UID:    "u",
		Labels: map[string]string{"app": "foo", "version": "v1"},
	}
	new := &Resource{
		UID:    "u",
		Labels: map[string]string{"app": "foo", "version": "v2"}, // 改 version
	}
	if !SkeletonChanged(old, new) {
		t.Error("Labels 变化应视为 changed")
	}
}

func TestSkeletonChanged_LabelsAddedKey(t *testing.T) {
	old := &Resource{
		UID:    "u",
		Labels: map[string]string{"app": "foo"},
	}
	new := &Resource{
		UID:    "u",
		Labels: map[string]string{"app": "foo", "new": "label"}, // 加 key
	}
	if !SkeletonChanged(old, new) {
		t.Error("Labels 新增 key 应视为 changed")
	}
}

func TestSkeletonChanged_DeletionTimestamp(t *testing.T) {
	now := time.Now()
	old := &Resource{UID: "u", DeletionTimestamp: nil}
	new := &Resource{UID: "u", DeletionTimestamp: &now}
	if !SkeletonChanged(old, new) {
		t.Error("DeletionTimestamp 设置应视为 changed（标记删除）")
	}
}

func TestSkeletonChanged_ResourceVersionOnly(t *testing.T) {
	// 关键测试：仅 ResourceVersion 变化（K8s housekeeping）
	// 不应视为 changed —— 这是增量序列化的核心价值
	old := &Resource{
		UID:             "u",
		Generation:      1,
		ResourceVersion: "1000",
		Phase:           "Running",
		Labels:          map[string]string{"app": "foo"},
	}
	new := &Resource{
		UID:             "u",
		Generation:      1,
		ResourceVersion: "1001", // 仅 RV 变
		Phase:           "Running",
		Labels:          map[string]string{"app": "foo"},
	}
	if SkeletonChanged(old, new) {
		t.Error("仅 ResourceVersion 变化不应视为 changed（K8s housekeeping）")
	}
}

func TestSkeletonChanged_Unchanged(t *testing.T) {
	r1 := int32(3)
	old := &Resource{
		UID:           "u",
		Generation:    1,
		Phase:         "Running",
		Replicas:      &r1,
		Labels:        map[string]string{"app": "foo"},
		Annotations:   map[string]string{"key": "val"},
	}
	r2 := int32(3)
	new := &Resource{
		UID:           "u",
		Generation:    1,
		Phase:         "Running",
		Replicas:      &r2,
		Labels:        map[string]string{"app": "foo"},
		Annotations:   map[string]string{"key": "val"},
	}
	if SkeletonChanged(old, new) {
		t.Error("完全等价的对象不应视为 changed")
	}
}

// ─── 辅助函数测试 ──────────────────────────────────────────────────

func TestInt32PtrEqual(t *testing.T) {
	v1 := int32(1)
	v2 := int32(2)
	v1Copy := int32(1)

	tests := []struct {
		name string
		a, b *int32
		want bool
	}{
		{"both nil", nil, nil, true},
		{"a nil", nil, &v1, false},
		{"b nil", &v1, nil, false},
		{"same value", &v1, &v1Copy, true},
		{"different value", &v1, &v2, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := int32PtrEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("int32PtrEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestStringMapEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b map[string]string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"nil and empty", nil, map[string]string{}, true},
		{"a nil b non-empty", nil, map[string]string{"k": "v"}, false},
		{"same content", map[string]string{"k": "v"}, map[string]string{"k": "v"}, true},
		{"different value", map[string]string{"k": "v1"}, map[string]string{"k": "v2"}, false},
		{"different keys", map[string]string{"k1": "v"}, map[string]string{"k2": "v"}, false},
		{"different size", map[string]string{"k": "v"}, map[string]string{"k": "v", "k2": "v2"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stringMapEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("stringMapEqual = %v, want %v", got, tt.want)
			}
		})
	}
}

// ─── Resource accessor 测试 ────────────────────────────────────────

func TestResource_Key(t *testing.T) {
	r := &Resource{Namespace: "default", Name: "my-app"}
	if got := r.Key(); got != "default/my-app" {
		t.Errorf("Key() = %q, want %q", got, "default/my-app")
	}
}

func TestCacheLayer_String(t *testing.T) {
	tests := []struct {
		layer CacheLayer
		want  string
	}{
		{LayerHot, "hot"},
		{LayerWarm, "warm"},
		{LayerCold, "cold"},
		{CacheLayer(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.layer.String(); got != tt.want {
			t.Errorf("CacheLayer(%d).String() = %q, want %q", tt.layer, got, tt.want)
		}
	}
}
