// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Ixecd/kubepivot/internal/rbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ════════════════════════════════════════════════════════════════════════════
// teamsFilePath 路径解析测试 (Q-B4.2)
// ════════════════════════════════════════════════════════════════════════════

func TestTeamsFilePath_UserLevel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := teamsFilePath(true)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".kp", "teams.yaml"), got)
}

// ════════════════════════════════════════════════════════════════════════════
// loadTeamConfig / saveTeamConfig roundtrip
// ════════════════════════════════════════════════════════════════════════════

func TestLoadTeamConfig_NotExists_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "teams.yaml")
	cfg, err := loadTeamConfig(path)
	require.NoError(t, err)
	assert.Empty(t, cfg.Teams, "不存在文件应返回 empty config")
}

func TestSaveAndLoadTeamConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "teams.yaml")

	original := &rbac.TeamConfig{
		Teams: []rbac.Team{
			{
				Name:        "backend",
				Members:     []string{"alice@x.com", "group:dev-team"},
				Namespaces:  []string{"kp-backend-*"},
				Permissions: []string{"deploy", "sandbox"},
				Excluded:    []string{"controller-uninstall"},
			},
		},
	}

	require.NoError(t, saveTeamConfig(path, original))

	loaded, err := loadTeamConfig(path)
	require.NoError(t, err)
	require.Len(t, loaded.Teams, 1)
	got := loaded.Teams[0]
	assert.Equal(t, "backend", got.Name)
	assert.Equal(t, []string{"alice@x.com", "group:dev-team"}, got.Members)
	assert.Equal(t, []string{"kp-backend-*"}, got.Namespaces)
	assert.Equal(t, []string{"deploy", "sandbox"}, got.Permissions)
	assert.Equal(t, []string{"controller-uninstall"}, got.Excluded)
}

func TestLoadTeamConfig_NilTeamsBecomesEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "teams.yaml")
	require.NoError(t, os.WriteFile(path, []byte("teams:"), 0o644))

	cfg, err := loadTeamConfig(path)
	require.NoError(t, err)
	assert.NotNil(t, cfg.Teams, "nil teams 应被规范化为 empty slice")
	assert.Empty(t, cfg.Teams)
}

func TestLoadTeamConfig_BadYAML_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "teams.yaml")
	require.NoError(t, os.WriteFile(path, []byte("teams: [not closed"), 0o644))

	_, err := loadTeamConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "解析")
}

// ════════════════════════════════════════════════════════════════════════════
// findTeamIndex
// ════════════════════════════════════════════════════════════════════════════

func TestFindTeamIndex_Found(t *testing.T) {
	teams := []rbac.Team{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}
	assert.Equal(t, 0, findTeamIndex(teams, "a"))
	assert.Equal(t, 1, findTeamIndex(teams, "b"))
	assert.Equal(t, 2, findTeamIndex(teams, "c"))
}

func TestFindTeamIndex_NotFound(t *testing.T) {
	teams := []rbac.Team{{Name: "a"}}
	assert.Equal(t, -1, findTeamIndex(teams, "nonexistent"))
}

func TestFindTeamIndex_EmptyList(t *testing.T) {
	assert.Equal(t, -1, findTeamIndex([]rbac.Team{}, "anything"))
}

// ════════════════════════════════════════════════════════════════════════════
// splitCSV / joinPerms helpers
// ════════════════════════════════════════════════════════════════════════════

func TestSplitCSV_Basic(t *testing.T) {
	got := splitCSV("a,b,c")
	assert.Equal(t, []string{"a", "b", "c"}, got)
}

func TestSplitCSV_TrimsWhitespace(t *testing.T) {
	got := splitCSV("  a , b ,c  ")
	assert.Equal(t, []string{"a", "b", "c"}, got)
}

func TestSplitCSV_SkipsEmptyEntries(t *testing.T) {
	got := splitCSV("a,,b,")
	assert.Equal(t, []string{"a", "b"}, got)
}

func TestSplitCSV_EmptyString_ReturnsNil(t *testing.T) {
	got := splitCSV("")
	assert.Nil(t, got)
}

func TestJoinPerms_Multiple(t *testing.T) {
	perms := []string{"deploy", "sandbox", "rollback"}
	assert.Equal(t, "deploy,sandbox,rollback", joinPerms(perms))
}

func TestJoinPerms_Empty(t *testing.T) {
	assert.Equal(t, "", joinPerms([]string{}))
}

// ════════════════════════════════════════════════════════════════════════════
// matchNamespacePattern (Q-B4.5 explainAllowedDecision 用)
// ════════════════════════════════════════════════════════════════════════════

func TestMatchNamespacePattern_Exact(t *testing.T) {
	assert.True(t, matchNamespacePattern("kp-backend-prod", "kp-backend-prod"))
	assert.False(t, matchNamespacePattern("kp-backend-prod", "kp-backend-dev"))
}

func TestMatchNamespacePattern_Prefix(t *testing.T) {
	assert.True(t, matchNamespacePattern("kp-backend-*", "kp-backend-prod"))
	assert.True(t, matchNamespacePattern("kp-backend-*", "kp-backend-staging"))
	assert.False(t, matchNamespacePattern("kp-backend-*", "kp-frontend-prod"))
}

func TestMatchNamespacePattern_Glob(t *testing.T) {
	// ? 匹配单字符
	assert.True(t, matchNamespacePattern("kp-?-prod", "kp-a-prod"))
	assert.False(t, matchNamespacePattern("kp-?-prod", "kp-ab-prod"))
}

// ════════════════════════════════════════════════════════════════════════════
// 集成场景: add → list → show → member add → check → remove
// ════════════════════════════════════════════════════════════════════════════

func TestE2E_AddListShowMemberAddCheckRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "teams.yaml")

	// 1. 初始 empty
	cfg, err := loadTeamConfig(path)
	require.NoError(t, err)
	assert.Empty(t, cfg.Teams)

	// 2. 模拟 kp team add backend (CLI 模式)
	cfg.Teams = append(cfg.Teams, rbac.Team{
		Name:        "backend",
		Members:     []string{"alice@x.com"},
		Namespaces:  []string{"kp-backend-*"},
		Permissions: []string{"deploy", "sandbox"},
	})
	require.NoError(t, saveTeamConfig(path, cfg))

	// 3. 模拟 kp team show backend
	loaded, err := loadTeamConfig(path)
	require.NoError(t, err)
	idx := findTeamIndex(loaded.Teams, "backend")
	require.GreaterOrEqual(t, idx, 0)
	assert.Equal(t, []string{"alice@x.com"}, loaded.Teams[idx].Members)

	// 4. 模拟 kp team member add backend bob@x.com
	loaded.Teams[idx].Members = append(loaded.Teams[idx].Members, "bob@x.com")
	require.NoError(t, saveTeamConfig(path, loaded))

	loaded2, _ := loadTeamConfig(path)
	idx2 := findTeamIndex(loaded2.Teams, "backend")
	assert.Equal(t, []string{"alice@x.com", "bob@x.com"}, loaded2.Teams[idx2].Members)

	// 5. 模拟 kp team check (用 rbac.NewFileBasedChecker 直接走真链路)
	// 注意: NewFileBasedChecker 严格校验, teams.yaml 必须包含必填字段
	// (Name 在 saveTeamConfig 已写)
	checker, err := rbac.NewFileBasedChecker(path)
	require.NoError(t, err)

	user := &rbac.UserContext{Email: "alice@x.com"}
	// alice 应能 deploy 到 kp-backend-prod
	require.NoError(t, checker.Check(nil, user, "kp-backend-prod", rbac.PermDeploy))
	// alice 没 controller-uninstall 权限
	err = checker.Check(nil, user, "kp-backend-prod", rbac.PermControllerUninstall)
	require.Error(t, err)

	// 6. 模拟 kp team remove backend
	loaded2.Teams = append(loaded2.Teams[:idx2], loaded2.Teams[idx2+1:]...)
	require.NoError(t, saveTeamConfig(path, loaded2))

	loaded3, _ := loadTeamConfig(path)
	assert.Empty(t, loaded3.Teams)
}

// ════════════════════════════════════════════════════════════════════════════
// warnYAMLOverwrite (Q-B4.4=C)
// ════════════════════════════════════════════════════════════════════════════

func TestWarnYAMLOverwrite_FileExists_DoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "teams.yaml")
	require.NoError(t, os.WriteFile(path, []byte("teams: []"), 0o644))

	assert.NotPanics(t, func() {
		warnYAMLOverwrite(path)
	})
}

func TestWarnYAMLOverwrite_FileNotExists_NoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.yaml")

	assert.NotPanics(t, func() {
		warnYAMLOverwrite(path)
	})
}
