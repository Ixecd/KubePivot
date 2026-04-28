// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package controller

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
// resolveControllerTeamsPath 路径优先级 (Q-D.7=C)
// ════════════════════════════════════════════════════════════════════════════

func TestResolveControllerTeamsPath_NeitherExists(t *testing.T) {
	// 清空 env, 默认路径 /etc/kubepivot/teams.yaml 通常不存在 (除非真集群)
	t.Setenv(envTeamsFilePath, "")

	got := resolveControllerTeamsPath()
	// CI 环境通常都不存在, 跳过 if 默认路径恰好存在
	if _, err := os.Stat(defaultTeamsFilePath); err == nil {
		t.Skip("默认路径 /etc/kubepivot/teams.yaml 实际存在, 跳过本测试")
	}
	assert.Empty(t, got)
}

func TestResolveControllerTeamsPath_EnvExists(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "teams.yaml")
	require.NoError(t, os.WriteFile(envPath, []byte("teams: []"), 0o644))

	t.Setenv(envTeamsFilePath, envPath)

	got := resolveControllerTeamsPath()
	assert.Equal(t, envPath, got)
}

func TestResolveControllerTeamsPath_EnvNotExists_FallsBackToDefault(t *testing.T) {
	t.Setenv(envTeamsFilePath, "/nonexistent/path/teams.yaml")

	got := resolveControllerTeamsPath()
	// env 指向不存在的路径 → fallback 到默认 (CI 通常也不存在)
	if _, err := os.Stat(defaultTeamsFilePath); err == nil {
		assert.Equal(t, defaultTeamsFilePath, got)
	} else {
		assert.Empty(t, got)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// getControllerChecker lazy 加载
// ════════════════════════════════════════════════════════════════════════════

func TestGetControllerChecker_NoConfig_ReturnsEmptyChecker(t *testing.T) {
	resetControllerCheckerForTest()
	defer resetControllerCheckerForTest()

	t.Setenv(envTeamsFilePath, "")

	checker := getControllerChecker()
	require.NotNil(t, checker, "无 teams.yaml 时也应返回非 nil checker")

	// Q-B5 fallback: 全权限
	user := &rbac.UserContext{Email: controllerActorEmail}
	err := checker.Check(context.Background(), user, "any-ns", rbac.PermDriftSync)
	assert.NoError(t, err, "无配置应允许所有")
}

func TestGetControllerChecker_LazyOnce(t *testing.T) {
	resetControllerCheckerForTest()
	defer resetControllerCheckerForTest()

	t.Setenv(envTeamsFilePath, "")

	c1 := getControllerChecker()
	c2 := getControllerChecker()
	assert.Equal(t, c1, c2, "lazy cache: 多次调用返回同一实例")
}

func TestGetControllerChecker_LoadsValidConfig(t *testing.T) {
	resetControllerCheckerForTest()
	defer resetControllerCheckerForTest()

	dir := t.TempDir()
	teamsPath := filepath.Join(dir, "teams.yaml")
	require.NoError(t, os.WriteFile(teamsPath, []byte(`teams:
  - name: system-team
    members: ["kubepivot-controller@system"]
    namespaces: ["*"]
    permissions: [drift-sync, heal, sandbox-gc, sweeper-lease]
`), 0o644))

	t.Setenv(envTeamsFilePath, teamsPath)

	checker := getControllerChecker()
	require.NotNil(t, checker)

	// fake user 应能 drift-sync 任意 ns
	user := &rbac.UserContext{Email: controllerActorEmail}
	assert.NoError(t,
		checker.Check(context.Background(), user, "kp-anything", rbac.PermDriftSync))

	// fake user 不能 deploy (system-team 没这个权限)
	err := checker.Check(context.Background(), user, "kp-anything", rbac.PermDeploy)
	assert.Error(t, err, "system-team 无 deploy 权限")
}

// ════════════════════════════════════════════════════════════════════════════
// isEnforceMode
// ════════════════════════════════════════════════════════════════════════════

func TestIsEnforceMode_Default(t *testing.T) {
	t.Setenv(envEnforceMode, "")
	assert.False(t, isEnforceMode())
}

func TestIsEnforceMode_True(t *testing.T) {
	for _, v := range []string{"true", "1", "yes"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv(envEnforceMode, v)
			assert.True(t, isEnforceMode(), "ENV %s 应启用 ENFORCE", v)
		})
	}
}

func TestIsEnforceMode_OtherValues(t *testing.T) {
	for _, v := range []string{"false", "0", "no", "TRUE", "True", "Yes"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv(envEnforceMode, v)
			assert.False(t, isEnforceMode(),
				"ENV %s 不应启用 ENFORCE (严格小写匹配)", v)
		})
	}
}

// ════════════════════════════════════════════════════════════════════════════
// mustCheckController 集成测试
// ════════════════════════════════════════════════════════════════════════════

func TestMustCheckController_NoConfig_AllowsAll(t *testing.T) {
	resetControllerCheckerForTest()
	defer resetControllerCheckerForTest()

	t.Setenv(envTeamsFilePath, "")
	t.Setenv(envEnforceMode, "")

	// Q-B5 fallback: 通过
	allowed := mustCheckController(context.Background(),
		"any-ns", rbac.PermDriftSync, "force-sync")
	assert.True(t, allowed, "无配置应允许所有 (Q-B5 fallback)")
}

func TestMustCheckController_FailOpen_DefaultMode(t *testing.T) {
	resetControllerCheckerForTest()
	defer resetControllerCheckerForTest()

	dir := t.TempDir()
	teamsPath := filepath.Join(dir, "teams.yaml")
	// teams.yaml 没含 system-team → controller fake user 无权限
	require.NoError(t, os.WriteFile(teamsPath, []byte(`teams:
  - name: backend
    members: [alice@example.com]
    namespaces: [kp-backend-*]
    permissions: [deploy]
`), 0o644))

	t.Setenv(envTeamsFilePath, teamsPath)
	t.Setenv(envEnforceMode, "") // 默认 fail-open

	// fail-open: 即使无权限也 return true
	allowed := mustCheckController(context.Background(),
		"kp-anything", rbac.PermDriftSync, "force-sync")
	assert.True(t, allowed, "fail-open 模式: 校验失败也应继续")
}

func TestMustCheckController_FailClose_EnforceMode(t *testing.T) {
	resetControllerCheckerForTest()
	defer resetControllerCheckerForTest()

	dir := t.TempDir()
	teamsPath := filepath.Join(dir, "teams.yaml")
	require.NoError(t, os.WriteFile(teamsPath, []byte(`teams:
  - name: backend
    members: [alice@example.com]
    namespaces: [kp-backend-*]
    permissions: [deploy]
`), 0o644))

	t.Setenv(envTeamsFilePath, teamsPath)
	t.Setenv(envEnforceMode, "true") // 启用 ENFORCE

	// ENFORCE: 校验失败 return false
	allowed := mustCheckController(context.Background(),
		"kp-anything", rbac.PermDriftSync, "force-sync")
	assert.False(t, allowed, "ENFORCE 模式: 校验失败应拒绝")
}

func TestMustCheckController_AllowedWithSystemTeam(t *testing.T) {
	resetControllerCheckerForTest()
	defer resetControllerCheckerForTest()

	dir := t.TempDir()
	teamsPath := filepath.Join(dir, "teams.yaml")
	require.NoError(t, os.WriteFile(teamsPath, []byte(`teams:
  - name: system-team
    members: ["kubepivot-controller@system"]
    namespaces: ["*"]
    permissions: [drift-sync, heal, sandbox-gc, sweeper-lease]
`), 0o644))

	t.Setenv(envTeamsFilePath, teamsPath)
	t.Setenv(envEnforceMode, "true") // 即使 ENFORCE 也应通过

	// 4 个 controller permission 全部测试
	for _, perm := range []rbac.Permission{
		rbac.PermDriftSync,
		rbac.PermHeal,
		rbac.PermSandboxGC,
		rbac.PermSweeperLease,
	} {
		t.Run(string(perm), func(t *testing.T) {
			allowed := mustCheckController(context.Background(),
				"kp-anywhere", perm, "test-action")
			assert.True(t, allowed,
				"system-team 应有 %s 权限", perm)
		})
	}
}

func TestMustCheckController_DeniedActionInEnforce(t *testing.T) {
	resetControllerCheckerForTest()
	defer resetControllerCheckerForTest()

	dir := t.TempDir()
	teamsPath := filepath.Join(dir, "teams.yaml")
	// system-team 只有 drift-sync, 没有 heal
	require.NoError(t, os.WriteFile(teamsPath, []byte(`teams:
  - name: system-team
    members: ["kubepivot-controller@system"]
    namespaces: ["*"]
    permissions: [drift-sync]
`), 0o644))

	t.Setenv(envTeamsFilePath, teamsPath)
	t.Setenv(envEnforceMode, "true")

	// drift-sync 通过
	assert.True(t, mustCheckController(context.Background(),
		"kp-test", rbac.PermDriftSync, "force-sync"))

	// heal 拒绝 (ENFORCE 模式)
	assert.False(t, mustCheckController(context.Background(),
		"kp-test", rbac.PermHeal, "recreate"))
}
