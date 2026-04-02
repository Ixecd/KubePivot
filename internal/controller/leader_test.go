package controller

import (
	"context"
	"testing"
)

func TestLeaderKey(t *testing.T) {
	key := leaderKey("web3-blitz", "web3-blitz")
	expected := "/kubepivot/web3-blitz/web3-blitz/leader"
	if key != expected {
		t.Errorf("got %s, want %s", key, expected)
	}
}

func TestLeaderKey_DifferentProjects(t *testing.T) {
	cases := []struct {
		project   string
		namespace string
		want      string
	}{
		{"myapp", "prod", "/kubepivot/myapp/prod/leader"},
		{"web3", "staging", "/kubepivot/web3/staging/leader"},
	}
	for _, c := range cases {
		got := leaderKey(c.project, c.namespace)
		if got != c.want {
			t.Errorf("leaderKey(%s,%s) = %s, want %s", c.project, c.namespace, got, c.want)
		}
	}
}

func TestIdentity_NotEmpty(t *testing.T) {
	id := identity()
	if id == "" {
		t.Error("identity 不应为空")
	}
	if len(id) < 3 {
		t.Errorf("identity 格式异常: %s", id)
	}
}

func TestIdentity_ContainsSlash(t *testing.T) {
	// 格式应为 hostname/pid
	id := identity()
	found := false
	for _, c := range id {
		if c == '/' {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("identity 应包含 '/'，got %s", id)
	}
}

func TestRunWithLeaderElection_NoEtcd(t *testing.T) {
	// 无 etcd 时应直接运行 fn（单机降级模式）
	ran := false
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消，防止 fn 里的 reconciler 卡住

	RunWithLeaderElection(ctx, "", "test-project", "test-ns", func(leaderCtx context.Context) {
		ran = true
	})

	if !ran {
		t.Error("无 etcd 时应该直接运行 fn（单机模式）")
	}
}
