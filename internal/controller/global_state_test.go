package controller

import (
	"sync"
	"testing"

	"github.com/Ixecd/kubepivot/internal/state"
)

const sampleResourcesYAML = `resources:
  - kind: Deployment
    name: feelings-server
    on-missing: auto-heal
    max-retry: 3
  - kind: StatefulSet
    name: feelings-server-postgres
    on-missing: alert
`

func TestGlobalState_UpsertAndGet(t *testing.T) {
	g := NewGlobalState()

	changed, err := g.UpsertProject("feelings-server", sampleResourcesYAML)
	if err != nil {
		t.Fatalf("UpsertProject err: %v", err)
	}
	if !changed {
		t.Error("首次 upsert 应返回 changed=true")
	}

	if g.ProjectCount() != 1 {
		t.Errorf("ProjectCount = %d, want 1", g.ProjectCount())
	}

	resources, ok := g.GetProject("feelings-server")
	if !ok {
		t.Fatal("GetProject 应返回 true")
	}
	if len(resources) != 2 {
		t.Errorf("resources 长度 = %d, want 2", len(resources))
	}
	if resources[0].Kind != "Deployment" || resources[0].Name != "feelings-server" {
		t.Errorf("resources[0] = %+v", resources[0])
	}
	// namespace 应被自动补全
	if resources[0].Namespace != "feelings-server" {
		t.Errorf("resources[0].Namespace = %q, want feelings-server", resources[0].Namespace)
	}
}

func TestGlobalState_UpsertSameContent_NoChange(t *testing.T) {
	g := NewGlobalState()
	g.UpsertProject("feelings-server", sampleResourcesYAML)

	// 第二次 upsert 相同内容 → 应跳过
	changed, err := g.UpsertProject("feelings-server", sampleResourcesYAML)
	if err != nil {
		t.Fatalf("第二次 upsert err: %v", err)
	}
	if changed {
		t.Error("相同内容第二次 upsert 应返回 changed=false")
	}
}

func TestGlobalState_UpsertDifferentContent_Changed(t *testing.T) {
	g := NewGlobalState()
	g.UpsertProject("feelings-server", sampleResourcesYAML)

	newYAML := sampleResourcesYAML + `
  - kind: Service
    name: feelings-server
    on-missing: alert
`
	changed, err := g.UpsertProject("feelings-server", newYAML)
	if err != nil {
		t.Fatalf("UpsertProject err: %v", err)
	}
	if !changed {
		t.Error("内容变化应返回 changed=true")
	}

	resources, _ := g.GetProject("feelings-server")
	if len(resources) != 3 {
		t.Errorf("更新后 resources 数 = %d, want 3", len(resources))
	}
}

func TestGlobalState_Remove(t *testing.T) {
	g := NewGlobalState()
	g.UpsertProject("feelings-server", sampleResourcesYAML)

	if !g.RemoveProject("feelings-server") {
		t.Error("Remove 存在的项目应返回 true")
	}
	if g.RemoveProject("feelings-server") {
		t.Error("Remove 不存在的项目应返回 false")
	}
	if g.ProjectCount() != 0 {
		t.Errorf("Remove 后 ProjectCount = %d, want 0", g.ProjectCount())
	}
}

func TestGlobalState_ProtectedNamespace(t *testing.T) {
	g := NewGlobalState()
	changed, _ := g.UpsertProject("kube-system", sampleResourcesYAML)
	if changed {
		t.Error("protected namespace 不应被 upsert")
	}
	if g.ProjectCount() != 0 {
		t.Error("ProjectCount 应为 0")
	}
}

func TestGlobalState_InvalidYAML(t *testing.T) {
	g := NewGlobalState()
	g.UpsertProject("feelings-server", sampleResourcesYAML) // 先有个正常的

	// 投递坏 YAML
	changed, err := g.UpsertProject("feelings-server", "not: [valid yaml: here")
	if err == nil {
		t.Error("坏 YAML 应返回 err")
	}
	if changed {
		t.Error("坏 YAML 应保留旧状态，返回 changed=false")
	}

	// 旧状态应保留
	resources, ok := g.GetProject("feelings-server")
	if !ok || len(resources) != 2 {
		t.Errorf("旧状态应保留，got ok=%v resources=%d", ok, len(resources))
	}
}

func TestGlobalState_ListProjects(t *testing.T) {
	g := NewGlobalState()
	g.UpsertProject("proj-a", sampleResourcesYAML)
	g.UpsertProject("proj-b", sampleResourcesYAML)
	g.UpsertProject("proj-c", sampleResourcesYAML)

	list := g.ListProjects()
	if len(list) != 3 {
		t.Errorf("ListProjects 长度 = %d, want 3", len(list))
	}
}

func TestGlobalState_GetProjectReturnsCopy(t *testing.T) {
	g := NewGlobalState()
	g.UpsertProject("feelings-server", sampleResourcesYAML)

	r1, _ := g.GetProject("feelings-server")
	r1[0].Name = "tampered"

	r2, _ := g.GetProject("feelings-server")
	if r2[0].Name == "tampered" {
		t.Error("GetProject 应返回副本，外部修改不应影响内部")
	}
}

func TestGlobalState_Concurrent(t *testing.T) {
	g := NewGlobalState()

	var wg sync.WaitGroup
	// 10 个 writer + 10 个 reader 并发跑，不触发 race（go test -race 会抓）
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				g.UpsertProject("proj", sampleResourcesYAML)
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				g.GetProject("proj")
				g.ListProjects()
				g.ProjectCount()
			}
		}()
	}
	wg.Wait()
}

func TestFingerprint_SameContent(t *testing.T) {
	a := fingerprint(sampleResourcesYAML)
	b := fingerprint(sampleResourcesYAML)
	if a != b {
		t.Error("相同内容应产生相同指纹")
	}
}

func TestFingerprint_DifferentContent(t *testing.T) {
	a := fingerprint("a")
	b := fingerprint("b")
	if a == b {
		t.Error("不同内容应产生不同指纹")
	}
	if len(a) != 64 {
		t.Errorf("sha256 hex 长度应为 64, got %d", len(a))
	}
}

// TestGetOrCreateMachine_ReturnsConsistentInstance
// 同一 namespace 多次调用应返回同一个 machine + 同一把锁
func TestGetOrCreateMachine_ReturnsConsistentInstance(t *testing.T) {
	gs := NewGlobalState()

	m1, lock1, err1 := gs.GetOrCreateMachine("test-ns", "v1.0.0")
	if err1 != nil {
		t.Fatalf("第一次创建失败: %v", err1)
	}
	if m1 == nil {
		t.Fatal("machine 不应为 nil")
	}

	m2, lock2, err2 := gs.GetOrCreateMachine("test-ns", "v1.0.0")
	if err2 != nil {
		t.Fatalf("第二次获取失败: %v", err2)
	}
	if m1 != m2 {
		t.Errorf("两次调用应返回同一个 machine 实例")
	}
	if lock1 != lock2 {
		t.Errorf("两次调用应返回同一把锁")
	}
}

// TestGetOrCreateMachine_DifferentNamespaceIsolated
// 不同 namespace 应得到不同 machine，互不干扰
func TestGetOrCreateMachine_DifferentNamespaceIsolated(t *testing.T) {
	gs := NewGlobalState()

	m1, _, _ := gs.GetOrCreateMachine("ns-a", "v1.0.0")
	m2, _, _ := gs.GetOrCreateMachine("ns-b", "v1.0.0")

	if m1 == m2 {
		t.Errorf("不同 namespace 应得到不同 machine 实例")
	}

	if got := gs.MachineCount(); got != 2 {
		t.Errorf("MachineCount = %d, want 2", got)
	}
}

// TestRemoveProject_AlsoRemovesMachine
// RemoveProject 应同步清理 machine 缓存
func TestRemoveProject_AlsoRemovesMachine(t *testing.T) {
	gs := NewGlobalState()

	const ns = "to-be-removed"
	yamlContent := `resources:
  - kind: Deployment
    name: foo
    on-missing: alert
`
	_, _ = gs.UpsertProject(ns, yamlContent)

	if gs.MachineCount() != 1 {
		t.Fatalf("UpsertProject 后 machine 应为 1，got %d", gs.MachineCount())
	}

	gs.RemoveProject(ns)

	if gs.MachineCount() != 0 {
		t.Errorf("RemoveProject 后 machine 应为 0，got %d", gs.MachineCount())
	}

	if _, ok := gs.GetProject(ns); ok {
		t.Errorf("RemoveProject 后 project 不应存在")
	}
}

// TestGetOrCreateMachine_ConcurrentSafety
// 50 个 goroutine 同时拿同一个 namespace 的 machine，必须始终是同一个指针
func TestGetOrCreateMachine_ConcurrentSafety(t *testing.T) {
	gs := NewGlobalState()
	const ns = "concurrent-ns"
	const N = 50

	var wg sync.WaitGroup
	machinePointers := make([]*state.Machine, N)
	lockPointers := make([]*sync.Mutex, N)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			m, lock, err := gs.GetOrCreateMachine(ns, "v1.0.0")
			if err != nil {
				t.Errorf("goroutine %d 获取失败: %v", idx, err)
				return
			}
			machinePointers[idx] = m
			lockPointers[idx] = lock
		}(i)
	}
	wg.Wait()

	// 所有 goroutine 应拿到完全相同的 machine + 锁指针
	for i := 1; i < N; i++ {
		if machinePointers[i] != machinePointers[0] {
			t.Errorf("goroutine %d 拿到不同 machine 指针", i)
		}
		if lockPointers[i] != lockPointers[0] {
			t.Errorf("goroutine %d 拿到不同 lock 指针", i)
		}
	}

	if gs.MachineCount() != 1 {
		t.Errorf("MachineCount = %d, want 1（缓存应只创建 1 个 machine）", gs.MachineCount())
	}
}
