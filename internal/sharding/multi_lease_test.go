package sharding

import (
	"testing"
	"time"
)

// 注意：这个测试只覆盖配置 + 数据结构层面，
// 真实 lease 抢占逻辑必须真实集群验证（依赖 kubectl + K8s API）
// 单测里 mock kubectl 太重，跳过

func TestNewMultiLeaseManager_Defaults(t *testing.T) {
	mgr := NewMultiLeaseManager(MultiLeaseConfig{
		TotalShards: 0,
		Replicas:    3,
	})

	if mgr.totalShards != 10 {
		t.Errorf("totalShards 默认应为 10，got %d", mgr.totalShards)
	}
	if mgr.leasePrefix != "kubepivot-controller-shard-" {
		t.Errorf("leasePrefix 默认错误: %q", mgr.leasePrefix)
	}
	if mgr.namespace != "kubepivot-system" {
		t.Errorf("namespace 默认错误: %q", mgr.namespace)
	}
	if mgr.ttl != 15*time.Second {
		t.Errorf("ttl 默认应为 15s，got %v", mgr.ttl)
	}
	if mgr.quota != QuotaPerPod(10, 3) {
		t.Errorf("quota = %d, want %d", mgr.quota, QuotaPerPod(10, 3))
	}
	if mgr.identity == "" {
		t.Error("identity 不应为空")
	}
}

func TestNewMultiLeaseManager_CustomConfig(t *testing.T) {
	mgr := NewMultiLeaseManager(MultiLeaseConfig{
		TotalShards: 20,
		Replicas:    4,
		TTL:         30 * time.Second,
		Namespace:   "custom-ns",
		LeasePrefix: "my-shard-",
	})

	if mgr.totalShards != 20 {
		t.Errorf("totalShards = %d", mgr.totalShards)
	}
	if mgr.quota != 5 {
		t.Errorf("quota = %d, want 5", mgr.quota)
	}
	if mgr.ttl != 30*time.Second {
		t.Errorf("ttl = %v", mgr.ttl)
	}
	if mgr.namespace != "custom-ns" {
		t.Errorf("namespace = %q", mgr.namespace)
	}
	if mgr.leasePrefix != "my-shard-" {
		t.Errorf("leasePrefix = %q", mgr.leasePrefix)
	}
}

func TestMultiLeaseManager_LeaseName(t *testing.T) {
	mgr := NewMultiLeaseManager(MultiLeaseConfig{TotalShards: 10, Replicas: 3})

	tests := []struct {
		idx  int
		want string
	}{
		{0, "kubepivot-controller-shard-0"},
		{5, "kubepivot-controller-shard-5"},
		{9, "kubepivot-controller-shard-9"},
	}
	for _, tt := range tests {
		if got := mgr.leaseName(tt.idx); got != tt.want {
			t.Errorf("leaseName(%d) = %q, want %q", tt.idx, got, tt.want)
		}
	}
}

func TestMultiLeaseManager_Identity_Unique(t *testing.T) {
	a := NewMultiLeaseManager(MultiLeaseConfig{TotalShards: 10, Replicas: 3})
	b := NewMultiLeaseManager(MultiLeaseConfig{TotalShards: 10, Replicas: 3})

	if a.Identity() == b.Identity() {
		t.Errorf("两个 manager 的 identity 应不同：%q vs %q", a.Identity(), b.Identity())
	}
}

func TestMultiLeaseManager_ShardsAccessor(t *testing.T) {
	mgr := NewMultiLeaseManager(MultiLeaseConfig{TotalShards: 10, Replicas: 3})

	shards := mgr.Shards()
	if shards == nil {
		t.Fatal("Shards() 不应返回 nil")
	}
	if shards.Size() != 0 {
		t.Errorf("初始 ShardSet 应为空，got Size=%d", shards.Size())
	}

	shards.Add(3)
	if mgr.Shards().Size() != 1 {
		t.Errorf("Shards() 应返回同一实例，但 Size=%d", mgr.Shards().Size())
	}
}
