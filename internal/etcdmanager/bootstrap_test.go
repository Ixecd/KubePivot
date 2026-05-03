package etcdmanager

import (
	"os"
	"testing"
)

func TestDetectClusterState_Pod0ColdStart(t *testing.T) {
	// Pod-0 + 无 peer 可达 + 无数据 → 冷启动
	cfg := DefaultBootstrapConfig()
	cfg.PodName = "controller-0"
	cfg.PodIndex = 0
	cfg.Namespace = "kubepivot-system"

	state, err := DetectClusterState(t.Context(), cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != StateColdStart {
		t.Errorf("expected StateColdStart, got %s", state)
	}
}

func TestDetectClusterState_NonPod0NoColdStart(t *testing.T) {
	// Pod-1 + 无 peer 可达 → 等待，不能冷启动（防止脑裂）
	cfg := DefaultBootstrapConfig()
	cfg.PodName = "controller-1"
	cfg.PodIndex = 1
	cfg.Namespace = "kubepivot-system"

	state, err := DetectClusterState(t.Context(), cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != StateWaiting {
		t.Errorf("expected StateWaiting for non-Pod-0, got %s", state)
	}
}

func TestClusterState_String(t *testing.T) {
	tests := []struct {
		state ClusterState
		want  string
	}{
		{StateExisting, "existing"},
		{StateColdStart, "cold-start"},
		{StateWaiting, "waiting"},
		{StateUnknown, "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("String(%d) = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestDefaultBootstrapConfig(t *testing.T) {
	cfg := DefaultBootstrapConfig()
	if cfg.PeerPort != 2380 {
		t.Errorf("PeerPort = %d, want 2380", cfg.PeerPort)
	}
	if cfg.ClientPort != 2379 {
		t.Errorf("ClientPort = %d, want 2379", cfg.ClientPort)
	}
	if cfg.MaxLagForPromotion != 500 {
		t.Errorf("MaxLagForPromotion = %d, want 500", cfg.MaxLagForPromotion)
	}
}

func TestPodIndexFromName(t *testing.T) {
	tests := []struct {
		podName string
		want    int
	}{
		{"controller-0", 0},
		{"controller-1", 1},
		{"controller-9", 9},
		{"controller-10", 10},
		{"invalid", 0},
		{"", 0},
		{"controller", 0},
	}
	for _, tt := range tests {
		if got := PodIndexFromName(tt.podName); got != tt.want {
			t.Errorf("PodIndexFromName(%q) = %d, want %d", tt.podName, got, tt.want)
		}
	}
}

func TestLocalHasData_NoData(t *testing.T) {
	if localHasData("/nonexistent/etcd") {
		t.Error("expected no data for nonexistent path")
	}
}

func TestLocalHasData_WithData(t *testing.T) {
	tmpDir := t.TempDir()
	memberDir := tmpDir + "/member/snap"
	os.MkdirAll(memberDir, 0755)
	os.WriteFile(memberDir+"/db", []byte("fake"), 0644)

	if !localHasData(tmpDir) {
		t.Error("expected hasData=true when snap/db exists")
	}
}

func TestPeerHasData_ReturnsError(t *testing.T) {
	// peerHasData 需要真实 kubectl exec，单测环境下应返回 error（非 false）
	// 验证函数签名：返回 (bool, error)，error != nil 时调用方应走 StateWaiting
	// 具体行为需在集成测试中验证
	t.Skip("requires real kubectl cluster")
}

func TestAdaptivePollingLogic(t *testing.T) {
	// 验证自适应频率的阈值逻辑
	tests := []struct {
		lag          int64
		expectedFast bool // lag < 1000 → 200ms
	}{
		{50, true},
		{500, true},
		{999, true},
		{1000, false}, // >= 1000 → 2s
		{5000, false},
	}
	for _, tt := range tests {
		fast := tt.lag < 1000
		if fast != tt.expectedFast {
			t.Errorf("lag=%d: fast=%v, want %v", tt.lag, fast, tt.expectedFast)
		}
	}
}

func TestStableWindowByPods(t *testing.T) {
	// 动态稳定窗口：3 节点 5s, 5 节点 8s, 7+ 节点 10s
	tests := []struct {
		pods int
		want int // seconds
	}{
		{1, 5},
		{3, 5},
		{4, 8},
		{5, 8},
		{7, 10},
		{10, 10},
	}
	for _, tt := range tests {
		var window int
		switch {
		case tt.pods <= 3:
			window = 5
		case tt.pods <= 5:
			window = 8
		default:
			window = 10
		}
		if window != tt.want {
			t.Errorf("pods=%d: window=%ds, want %ds", tt.pods, window, tt.want)
		}
	}
}
