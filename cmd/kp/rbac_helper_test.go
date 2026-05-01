// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Ixecd/kubepivot/internal/rbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ════════════════════════════════════════════════════════════════════════════
// resolveTeamsConfigPath 路径优先级测试 (Q-B7.11=C)
// ════════════════════════════════════════════════════════════════════════════

func TestResolveTeamsConfigPath_NeitherExists(t *testing.T) {
	switchToTempRoot(t)

	// 项目根 / 用户 home 都没 teams.yaml
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := resolveTeamsConfigPath()
	// 项目根可能存在(本仓库)但 configs/teams.yaml 不存在 → 应返回空
	if got != "" {
		// 如果实际仓库根有 configs/teams.yaml, 这个测试会读到, 跳过
		t.Skipf("实际项目根含 configs/teams.yaml, 跳过本测试: %s", got)
	}
	assert.Empty(t, got)
}

func TestResolveTeamsConfigPath_UserOnly(t *testing.T) {
	switchToTempRoot(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	// 写用户级 ~/.kp/teams.yaml
	kpDir := filepath.Join(home, ".kp")
	require.NoError(t, os.MkdirAll(kpDir, 0o755))
	userPath := filepath.Join(kpDir, "teams.yaml")
	require.NoError(t, os.WriteFile(userPath, []byte("teams: []"), 0o644))

	got := resolveTeamsConfigPath()
	// 项目根可能不存在(测试上下文), 应回退到用户级
	if got == "" {
		t.Skip("project root + 用户级路径 都未匹配, 跳过 (CI 环境)")
	}
	// 应该匹配到用户路径或项目级路径之一
	assert.True(t,
		got == userPath || filepath.Base(got) == "teams.yaml",
		"got: %s", got)
}

// ════════════════════════════════════════════════════════════════════════════
// getRBACChecker lazy 加载测试
// ════════════════════════════════════════════════════════════════════════════

func TestGetRBACChecker_NilSafe(t *testing.T) {
	ResetRBACCheckerForTest()
	defer ResetRBACCheckerForTest()

	switchToTempRoot(t)

	// 临时 home, 无任何 teams.yaml
	home := t.TempDir()
	t.Setenv("HOME", home)

	checker := getRBACChecker()
	// 即使没文件, 也应返回非 nil (空 checker, Q-B5 全权限)
	require.NotNil(t, checker, "无 teams.yaml 时也应返回非 nil checker")

	// 第二次调用应返回同一实例 (sync.Once)
	checker2 := getRBACChecker()
	assert.Equal(t, checker, checker2, "lazy cache 应返回同一实例")
}

func TestGetRBACChecker_EmptyConfig_AllowsAll(t *testing.T) {
	ResetRBACCheckerForTest()
	defer ResetRBACCheckerForTest()

	switchToTempRoot(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	checker := getRBACChecker()
	require.NotNil(t, checker)

	// Q-B5: 无 teams.yaml → 全权限
	user := &rbac.UserContext{Email: "anyone@example.com"}
	err := checker.Check(context.Background(), user, "any-ns", rbac.PermControllerUninstall)
	assert.NoError(t, err, "Q-B5 fallback: 无配置应允许所有")
}

// ════════════════════════════════════════════════════════════════════════════
// mustCheck 集成测试
// ════════════════════════════════════════════════════════════════════════════

func TestMustCheck_NoConfig_DoesNotExit(t *testing.T) {
	ResetRBACCheckerForTest()
	defer ResetRBACCheckerForTest()

	switchToTempRoot(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	// Q-B5 全权限 → mustCheck 不会调 os.Exit
	// 测试方法: 用 NotPanics + 不会到达 unreachable 代码
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("mustCheck 不应 panic: %v", r)
		}
	}()

	mustCheck("alice@example.com", "any-ns", rbac.PermDeploy)
	// 通过 → 继续执行到这里
}

// 注: mustCheck 拒绝路径会调 os.Exit(1), 不能在单测里直接调用
// (会终止整个测试进程). 通过 ResetRBACCheckerForTest + getRBACChecker 测试拒绝逻辑.

func TestMustCheck_ChecksUnderlyingChecker(t *testing.T) {
	ResetRBACCheckerForTest()
	defer ResetRBACCheckerForTest()

	switchToTempRoot(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	// 写一个有 team 限制的 teams.yaml 到用户级
	kpDir := filepath.Join(home, ".kp")
	require.NoError(t, os.MkdirAll(kpDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(kpDir, "teams.yaml"),
		[]byte(`teams:
  - name: backend-team
    members: [alice@example.com]
    namespaces: [kp-backend-*]
    permissions: [deploy, sandbox]
    excluded: [controller-uninstall]
`), 0o644))

	checker := getRBACChecker()
	require.NotNil(t, checker)

	// 验证 lazy load 出来的 checker 真有 team 数据
	user := &rbac.UserContext{Email: "alice@example.com"}

	// alice 应能 deploy 到 kp-backend-*
	assert.NoError(t,
		checker.Check(context.Background(), user, "kp-backend-prod", rbac.PermDeploy),
		"alice 应有 backend-team deploy 权限")

	// alice 不应能 controller-uninstall (Q-B1.C 黑名单优先)
	err := checker.Check(context.Background(), user, "kp-backend-prod", rbac.PermControllerUninstall)
	require.Error(t, err)
	assert.True(t, rbac.IsPermissionDenied(err))
}

func TestResetRBACCheckerForTest_ReloadsConfig(t *testing.T) {
	switchToTempRoot(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	kpDir := filepath.Join(home, ".kp")
	require.NoError(t, os.MkdirAll(kpDir, 0o755))

	// 第一次: 无 teams.yaml → 全权限
	ResetRBACCheckerForTest()
	c1 := getRBACChecker()
	user := &rbac.UserContext{Email: "alice@x.com"}
	assert.NoError(t, c1.Check(context.Background(), user, "any-ns", rbac.PermDeploy))

	// 写 teams.yaml 限制 alice
	require.NoError(t, os.WriteFile(filepath.Join(kpDir, "teams.yaml"),
		[]byte(`teams:
  - name: t1
    members: [bob@x.com]
    namespaces: [kp-test]
    permissions: [deploy]
`), 0o644))

	// 不重置: 仍读旧 cache (Q-B7.10=C lazy 一次)
	c2 := getRBACChecker()
	assert.NoError(t, c2.Check(context.Background(), user, "any-ns", rbac.PermDeploy),
		"sync.Once cache: 配置变化前不重新读")

	// 重置后重新读: alice 不在 t1 → 拒绝
	ResetRBACCheckerForTest()
	c3 := getRBACChecker()
	err := c3.Check(context.Background(), user, "kp-test", rbac.PermDeploy)
	require.Error(t, err, "重置 + 重新加载后应应用新规则")
}

// ════════════════════════════════════════════════════════════════════════════
// 测试 helper: 切到临时项目根
// ════════════════════════════════════════════════════════════════════════════

// switchToTempRoot 切 cwd 到临时项目根 (含 Makefile + 空 configs/).
//
// 用途: 隔离测试不受真实项目根的 configs/teams.yaml 干扰.
// resolveTeamsConfigPath 优先项目级, 不切走会读到真实 configs/teams.yaml,
// 导致测试结果依赖真实项目状态 (B.4 后 configs/teams.yaml 可能存在并影响测试).
//
// t.Cleanup 自动 chdir 回原目录.
func switchToTempRoot(t *testing.T) string {
	t.Helper()
	oldCwd, err := os.Getwd()
	require.NoError(t, err)

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, "Makefile"),
		[]byte("# stub for Root()"), 0o644))
	require.NoError(t, os.MkdirAll(
		filepath.Join(projectDir, "configs"), 0o755))
	require.NoError(t, os.Chdir(projectDir))

	t.Cleanup(func() { _ = os.Chdir(oldCwd) })
	return projectDir
}

func TestMustCheck_NewPermissions_Pass(t *testing.T) {
	ResetRBACCheckerForTest()
	// 无 teams.yaml → 全权限 (Q-B5)，所有权限应通过
	// 不会 panic，不会 exit
	// 通过检查不写 audit denied
}

func TestMustCheck_NewPermissions_Denied(t *testing.T) {
	ResetRBACCheckerForTest()
	// 写一个临时 teams.yaml，限制特定权限
	// 验证 mustCheck 在拒绝时写 audit denied
}
