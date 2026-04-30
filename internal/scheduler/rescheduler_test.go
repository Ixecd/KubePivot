// internal/scheduler/rescheduler_test.go
package scheduler

import (
	"context"
	"testing"
	"time"
)

// ─── Mock 适配器用于 Rescheduler 测试 ────────────────────────

type mockReschedulerPodLister struct {
	pods []*PodInfo
}

func (m *mockReschedulerPodLister) ListAllPods(ctx context.Context) ([]*PodInfo, error) {
	return m.pods, nil
}

type mockReschedulerNodeLister struct {
	nodes []*NodeInfo
}

func (m *mockReschedulerNodeLister) ListAllNodes(ctx context.Context) ([]*NodeInfo, error) {
	return m.nodes, nil
}

// 为 Scheduler 创建轻量 mock，仅实现 Rescheduler 需要的方法
type mockSchedulerForRescheduler struct {
	assignNode string
	assignErr  error
}

func (m *mockSchedulerForRescheduler) AssignPod(ctx context.Context, pod *PodInfo) (string, error) {
	return m.assignNode, m.assignErr
}

// 补全 Scheduler 接口的其他方法（Rescheduler 不需要）
func (m *mockSchedulerForRescheduler) Schedule(ctx context.Context) (*SchedulingPlan, error) {
	return nil, nil
}

// ─── 测试用例 ──────────────────────────────────────────

func TestComputeNodeUtilization(t *testing.T) {
	nodes := []*NodeInfo{
		{Name: "node1", AllocatableCPU: 4000, AllocatableMemory: 8 * 1024 * 1024 * 1024},
		{Name: "node2", AllocatableCPU: 4000, AllocatableMemory: 8 * 1024 * 1024 * 1024},
	}
	pods := []*PodInfo{
		{Name: "pod-a", NodeName: "node1", Phase: "Running", Requests: ResourceRequest{CPU: 1000, Memory: 2 * 1024 * 1024 * 1024}},
		{Name: "pod-b", NodeName: "node1", Phase: "Running", Requests: ResourceRequest{CPU: 500, Memory: 1 * 1024 * 1024 * 1024}},
		{Name: "pod-c", NodeName: "node2", Phase: "Running", Requests: ResourceRequest{CPU: 2000, Memory: 4 * 1024 * 1024 * 1024}},
	}

	rs := &Rescheduler{}
	utils := rs.computeNodeUtilization(pods, nodes)
	if len(utils) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(utils))
	}
	node1 := utils[0]
	if node1.Usage.CPU != 1500 || node1.Usage.Memory != 3*1024*1024*1024 {
		t.Errorf("node1 usage incorrect: cpu=%d, mem=%d", node1.Usage.CPU, node1.Usage.Memory)
	}
	if node1.CPUUtil != 0.375 || node1.MemoryUtil != 0.375 {
		t.Errorf("node1 util incorrect: cpu=%.2f, mem=%.2f", node1.CPUUtil, node1.MemoryUtil)
	}
	node2 := utils[1]
	if node2.Usage.CPU != 2000 || node2.Usage.Memory != 4*1024*1024*1024 {
		t.Errorf("node2 usage incorrect: cpu=%d, mem=%d", node2.Usage.CPU, node2.Usage.Memory)
	}
	if node2.CPUUtil != 0.5 || node2.MemoryUtil != 0.5 {
		t.Errorf("node2 util incorrect: cpu=%.2f, mem=%.2f", node2.CPUUtil, node2.MemoryUtil)
	}
}

func TestDetectImbalance(t *testing.T) {
	// 构造不平衡场景：node1 利用率极高，node2 利用率极低
	utils := []*nodeUtilInfo{
		{
			Node:       &NodeInfo{Name: "node1", AllocatableCPU: 4000, AllocatableMemory: 8 * 1024 * 1024 * 1024},
			Usage:      nodeUsage{CPU: 3500, Memory: 7 * 1024 * 1024 * 1024},
			CPUUtil:    0.875,
			MemoryUtil: 0.875,
		},
		{
			Node:       &NodeInfo{Name: "node2", AllocatableCPU: 4000, AllocatableMemory: 8 * 1024 * 1024 * 1024},
			Usage:      nodeUsage{CPU: 500, Memory: 512 * 1024 * 1024},
			CPUUtil:    0.125,
			MemoryUtil: 0.0625,
		},
	}
	rs := &Rescheduler{}
	pairs := rs.detectImbalance(utils)
	if len(pairs) != 1 {
		t.Fatalf("expected 1 imbalance pair, got %d", len(pairs))
	}
	pair := pairs[0]
	if pair.High.Node.Name != "node1" || pair.Low.Node.Name != "node2" {
		t.Errorf("unexpected pair: high=%s, low=%s", pair.High.Node.Name, pair.Low.Node.Name)
	}
	// 建议迁移量应基本等于高负载节点超出的部分
	if pair.SuggestedCPU <= 0 || pair.SuggestedMemory <= 0 {
		t.Errorf("expected positive suggested resources, got cpu=%d mem=%d", pair.SuggestedCPU, pair.SuggestedMemory)
	}
}

func TestDetectImbalance_NoImbalance(t *testing.T) {
	utils := []*nodeUtilInfo{
		{
			Node:       &NodeInfo{Name: "node1", AllocatableCPU: 4000, AllocatableMemory: 8 * 1024 * 1024 * 1024},
			Usage:      nodeUsage{CPU: 2000, Memory: 4 * 1024 * 1024 * 1024},
			CPUUtil:    0.5,
			MemoryUtil: 0.5,
		},
		{
			Node:       &NodeInfo{Name: "node2", AllocatableCPU: 4000, AllocatableMemory: 8 * 1024 * 1024 * 1024},
			Usage:      nodeUsage{CPU: 2000, Memory: 4 * 1024 * 1024 * 1024},
			CPUUtil:    0.5,
			MemoryUtil: 0.5,
		},
	}
	rs := &Rescheduler{}
	pairs := rs.detectImbalance(utils)
	if len(pairs) != 0 {
		t.Errorf("expected no imbalance, got %d pairs", len(pairs))
	}
}

func TestIsMigratable(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		expected bool
	}{
		{"no labels", nil, true},
		{"statefulset pod", map[string]string{"statefulset.kubernetes.io/pod-name": "postgres-0"}, false},
		{"blue-green locked", map[string]string{"kubepivot.io/blue-green-locked": "true"}, false},
		{"blue-green unlocked", map[string]string{"kubepivot.io/blue-green-locked": "false"}, true},
		{"other labels", map[string]string{"app": "web"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &PodInfo{Labels: tt.labels}
			if got := isMigratable(pod); got != tt.expected {
				t.Errorf("isMigratable() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// realMigratePodsTest 包装真实逻辑，便于注入mock
func realMigratePodsTest(assigner PodAssigner, pairs []*imbalancePair, allPods []*PodInfo, allNodes []*NodeInfo, maxMigrations int) int {
	rs := &Rescheduler{
		assigner:      assigner,
		maxMigrations: maxMigrations,
	}
	// 捕获信息
	migrated := 0
	// 我们无法直接访问 migratePods 因为它未导出且内部有log。
	// 为了测试，我们可以将 migratePods 的核心逻辑提取为一个可测试的函数，或者直接调用 run 的一个变体。
	// 更简单的方法是直接调用 migratePods（虽然未导出，但在同一个包内测试可以访问）。
	// 这里为了清晰，我们假设 migratePods 是包内可访问的，或者我们在此测试文件中直接实现核心逻辑的测试版。
	// 由于 migratePods 是*Rescheduler 的方法，且我们在包内，可以直接访问。
	// 我们构造一个假的上下文。
	ctx := context.Background()
	rs.migratePods(ctx, pairs, allPods, allNodes)
	// 如何得知迁移了多少？需要在 rs 上暴露，或者从日志中无法捕获。
	// 更好的方式：让 migratePods 返回迁移的 Pod 列表，或者用计数器。
	// 临时方案：修改 migratePods 以返回迁移数，或接受一个回调。
	// 我们先假设 migratePods 会返回一个 int。
	// 实际上，我们将在测试中直接验证效果：被迁移的 Pod 的 NodeName 应该变了。
	_ = ctx
	_ = rs
	return migrated
}

// mockAssigner 实现 PodAssigner 接口
type mockAssigner struct {
	assignFunc func(ctx context.Context, pod *PodInfo) (string, error)
}

func (m *mockAssigner) AssignPod(ctx context.Context, pod *PodInfo) (string, error) {
	return m.assignFunc(ctx, pod)
}

func TestMigratePods_Simple(t *testing.T) {
	// 构造一个待迁移的 Pod
	podToMigrate := &PodInfo{
		Namespace: "default", Name: "migratable-pod", NodeName: "node1", Phase: "Running",
		Requests: ResourceRequest{CPU: 500, Memory: 256 * 1024 * 1024},
		Labels:   map[string]string{"app": "web"},
	}
	allPods := []*PodInfo{podToMigrate}
	allNodes := []*NodeInfo{
		{Name: "node1", AllocatableCPU: 1000, AllocatableMemory: 512 * 1024 * 1024},
		{Name: "node2", AllocatableCPU: 1000, AllocatableMemory: 512 * 1024 * 1024},
	}

	pair := &imbalancePair{
		High:            &nodeUtilInfo{Node: allNodes[0]}, // 高负载节点node1
		Low:             &nodeUtilInfo{Node: allNodes[1]}, // 低负载节点node2
		SuggestedCPU:    500,
		SuggestedMemory: 256 * 1024 * 1024,
	}

	// 注入 mock Assigner，让它返回 node2（低负载节点）
	assigner := &mockAssigner{
		assignFunc: func(ctx context.Context, pod *PodInfo) (string, error) {
			return "node2", nil
		},
	}

	rs := &Rescheduler{
		assigner:      assigner,
		maxMigrations: 1,
	}

	// 执行迁移
	migrated := rs.migratePods(context.Background(), []*imbalancePair{pair}, allPods, allNodes)

	if migrated != 1 {
		t.Fatalf("expected 1 migration, got %d", migrated)
	}
	if podToMigrate.NodeName != "node2" {
		t.Errorf("expected pod to be migrated to node2, but still on %s", podToMigrate.NodeName)
	}
}
func TestDetectJitter(t *testing.T) {
	rs := &Rescheduler{
		jitterWindow:     5 * time.Minute,
		jitterThreshold:  0.95,
		jitterSpikeCount: 3,
	}
	utils := []*nodeUtilInfo{
		{Node: &NodeInfo{Name: "node1"}, CPUUtil: 0.98},
	}
	for i := 0; i < 3; i++ {
		rs.detectJitter(utils)
	}
	if lvl := rs.DegradedLevel(); lvl != 1 {
		t.Errorf("expected degraded level 1, got %d", lvl)
	}
	// 继续触发 3 次
	for i := 0; i < 3; i++ {
		rs.detectJitter(utils)
	}
	if lvl := rs.DegradedLevel(); lvl < 2 {
		t.Errorf("expected degraded level >=2, got %d", lvl)
	}
}

func TestPauseAndResume(t *testing.T) {
	rs := &Rescheduler{}
	rs.Pause()
	if lvl := rs.DegradedLevel(); lvl != 3 {
		t.Errorf("expected paused level 3, got %d", lvl)
	}
	rs.Resume()
	if lvl := rs.DegradedLevel(); lvl != 0 {
		t.Errorf("expected resumed level 0, got %d", lvl)
	}
}

func TestReportOOM(t *testing.T) {
	rs := &Rescheduler{
		jitterWindow:     5 * time.Minute,
		jitterThreshold:  0.95,
		jitterSpikeCount: 3,
	}
	rs.ReportOOM()
	rs.detectJitter(nil)
	if lvl := rs.DegradedLevel(); lvl < 1 {
		t.Errorf("expected degraded level >=1 after OOM, got %d", lvl)
	}
}
