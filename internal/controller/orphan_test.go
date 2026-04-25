// 这是新增到 internal/controller/global_state_test.go 的测试
// （或者放在新文件 orphan_test.go 里）
package controller

import "testing"

const orphanTestYAML = `resources:
  - kind: Deployment
    name: foo
    on-missing: alert
`

// TestRemoveOrphanProjects_BasicCleanup
// 加 3 个项目，定义 isOwned 让其中 2 个返回 true，验证只有第 3 个被清理
func TestRemoveOrphanProjects_BasicCleanup(t *testing.T) {
	gs := NewGlobalState()

	// 3 个项目都加进去
	for _, ns := range []string{"keep-me-1", "keep-me-2", "remove-me"} {
		_, _ = gs.UpsertProject(ns, orphanTestYAML)
	}
	if gs.ProjectCount() != 3 {
		t.Fatalf("初始 ProjectCount = %d, want 3", gs.ProjectCount())
	}
	if gs.MachineCount() != 3 {
		t.Fatalf("初始 MachineCount = %d, want 3", gs.MachineCount())
	}

	// isOwned: 仅 keep-me-* 返回 true
	isOwned := func(ns string) bool {
		return ns != "remove-me"
	}

	cleaned := gs.RemoveOrphanProjects(isOwned)

	if len(cleaned) != 1 || cleaned[0] != "remove-me" {
		t.Errorf("cleaned = %v, want [remove-me]", cleaned)
	}
	if gs.ProjectCount() != 2 {
		t.Errorf("清理后 ProjectCount = %d, want 2", gs.ProjectCount())
	}
	if gs.MachineCount() != 2 {
		t.Errorf("清理后 MachineCount = %d, want 2", gs.MachineCount())
	}

	// 验证留下来的是哪两个
	if _, ok := gs.GetProject("keep-me-1"); !ok {
		t.Error("keep-me-1 应该留下")
	}
	if _, ok := gs.GetProject("keep-me-2"); !ok {
		t.Error("keep-me-2 应该留下")
	}
	if _, ok := gs.GetProject("remove-me"); ok {
		t.Error("remove-me 应该被清理")
	}
}

// TestRemoveOrphanProjects_AllOwned_NoOp
// 所有项目都归属本 pod 时，isOwned 全 true，不清理任何东西
func TestRemoveOrphanProjects_AllOwned_NoOp(t *testing.T) {
	gs := NewGlobalState()

	for _, ns := range []string{"a", "b", "c"} {
		_, _ = gs.UpsertProject(ns, orphanTestYAML)
	}

	cleaned := gs.RemoveOrphanProjects(func(ns string) bool {
		return true // 全归本 pod
	})

	if len(cleaned) != 0 {
		t.Errorf("cleaned = %v, want []", cleaned)
	}
	if gs.ProjectCount() != 3 {
		t.Errorf("ProjectCount = %d, want 3", gs.ProjectCount())
	}
}

// TestRemoveOrphanProjects_AllOrphan_AllCleaned
// 极端 case：所有项目都不归属本 pod，全清理
func TestRemoveOrphanProjects_AllOrphan_AllCleaned(t *testing.T) {
	gs := NewGlobalState()

	for _, ns := range []string{"a", "b", "c"} {
		_, _ = gs.UpsertProject(ns, orphanTestYAML)
	}

	cleaned := gs.RemoveOrphanProjects(func(ns string) bool {
		return false // 一个都不归本 pod
	})

	if len(cleaned) != 3 {
		t.Errorf("cleaned 数量 = %d, want 3", len(cleaned))
	}
	if gs.ProjectCount() != 0 {
		t.Errorf("ProjectCount = %d, want 0", gs.ProjectCount())
	}
	if gs.MachineCount() != 0 {
		t.Errorf("MachineCount = %d, want 0", gs.MachineCount())
	}
}

// TestRemoveOrphanProjects_Empty_NoCrash
// 边界：GlobalState 为空时调用，不应崩溃
func TestRemoveOrphanProjects_Empty_NoCrash(t *testing.T) {
	gs := NewGlobalState()
	cleaned := gs.RemoveOrphanProjects(func(ns string) bool { return true })
	if len(cleaned) != 0 {
		t.Errorf("空 state 应返回空，got %v", cleaned)
	}
}
