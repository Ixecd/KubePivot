// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package rbac

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ════════════════════════════════════════════════════════════════════════════
// 测试 fixture
// ════════════════════════════════════════════════════════════════════════════

const standardTeamsYAML = `teams:
  - name: backend-team
    members:
      - alice@example.com
      - bob@example.com
      - "group:dev-team"
    namespaces:
      - kp-backend-*
      - kp-shared
    permissions:
      - deploy
      - sandbox
      - status
    excluded:
      - controller-uninstall

  - name: frontend-team
    members:
      - charlie@example.com
    namespaces:
      - kp-frontend-*
    permissions:
      - deploy
      - status

  - name: ops-team
    members:
      - dave@example.com
      - "group:ops"
    namespaces:
      - "*"
    permissions:
      - "*"
`

func writeTestTeams(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "teams.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func mustChecker(t *testing.T, content string) *FileBasedChecker {
	t.Helper()
	path := writeTestTeams(t, content)
	c, err := NewFileBasedChecker(path)
	require.NoError(t, err)
	return c
}

func makeUser(email string, groups ...string) *UserContext {
	return &UserContext{Email: email, Groups: groups}
}

// ════════════════════════════════════════════════════════════════════════════
// Permission 类型测试
// ════════════════════════════════════════════════════════════════════════════

func TestPermission_IsValid(t *testing.T) {
	cases := []struct {
		perm  Permission
		valid bool
	}{
		{PermDeploy, true},
		{PermSandbox, true},
		{PermAll, true},
		{Permission("unknown"), false},
		{Permission(""), false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.valid, tc.perm.IsValid(), "perm=%q", tc.perm)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Q-B5: teams.yaml 缺失 → 全权限
// ════════════════════════════════════════════════════════════════════════════

func TestCheck_NoConfig_AllowsAll(t *testing.T) {
	c, err := NewFileBasedChecker("/nonexistent/path/teams.yaml")
	require.NoError(t, err, "Q-B5: 文件不存在不应报错")

	user := makeUser("anyone@example.com")
	err = c.Check(context.Background(), user, "any-namespace", PermControllerUninstall)
	assert.NoError(t, err, "无 teams.yaml 时全权限")
}

func TestCheck_EmptyTeams_AllowsAll(t *testing.T) {
	c := mustChecker(t, "teams: []")
	user := makeUser("anyone@example.com")
	err := c.Check(context.Background(), user, "any-namespace", PermControllerUninstall)
	assert.NoError(t, err, "空 teams 列表等同于无配置")
}

// ════════════════════════════════════════════════════════════════════════════
// Q-B1.A: members 用 email
// ════════════════════════════════════════════════════════════════════════════

func TestCheck_EmailMember_Allowed(t *testing.T) {
	c := mustChecker(t, standardTeamsYAML)
	alice := makeUser("alice@example.com")
	err := c.Check(context.Background(), alice, "kp-backend-prod", PermDeploy)
	assert.NoError(t, err)
}

func TestCheck_NonMember_Denied(t *testing.T) {
	c := mustChecker(t, standardTeamsYAML)
	stranger := makeUser("unknown@example.com")
	err := c.Check(context.Background(), stranger, "kp-backend-prod", PermDeploy)
	require.Error(t, err)
	assert.True(t, IsPermissionDenied(err))
	assert.Contains(t, err.Error(), "no-team-match")
}

// ════════════════════════════════════════════════════════════════════════════
// Q-B2: Group 一锅端支持 (group:xxx)
// ════════════════════════════════════════════════════════════════════════════

func TestCheck_GroupMember_Allowed(t *testing.T) {
	c := mustChecker(t, standardTeamsYAML)
	// Eve 不在 members 邮箱列表, 但在 dev-team group 里 → backend-team 接受
	eve := makeUser("eve@example.com", "dev-team")
	err := c.Check(context.Background(), eve, "kp-backend-prod", PermDeploy)
	assert.NoError(t, err, "Q-B2: group 匹配应允许")
}

func TestCheck_GroupMismatch_Denied(t *testing.T) {
	c := mustChecker(t, standardTeamsYAML)
	// Frank 在 some-other-group, 不匹配任何 team
	frank := makeUser("frank@example.com", "some-other-group")
	err := c.Check(context.Background(), frank, "kp-backend-prod", PermDeploy)
	require.Error(t, err)
	assert.True(t, IsPermissionDenied(err))
}

func TestCheck_MultipleGroups_AnyMatchAllowed(t *testing.T) {
	c := mustChecker(t, standardTeamsYAML)
	// Grace 在多个 group 里, 其中一个匹配
	grace := makeUser("grace@example.com", "random-group", "dev-team", "another")
	err := c.Check(context.Background(), grace, "kp-backend-prod", PermDeploy)
	assert.NoError(t, err)
}

// ════════════════════════════════════════════════════════════════════════════
// Q-B1.B: namespaces 用 shell glob (* / ?)
// ════════════════════════════════════════════════════════════════════════════

func TestCheck_NamespacePrefix_Match(t *testing.T) {
	c := mustChecker(t, standardTeamsYAML)
	alice := makeUser("alice@example.com")

	// kp-backend-* 应匹配
	cases := []string{"kp-backend-prod", "kp-backend-staging", "kp-backend-"}
	for _, ns := range cases {
		err := c.Check(context.Background(), alice, ns, PermDeploy)
		assert.NoError(t, err, "ns=%q 应匹配 kp-backend-*", ns)
	}
}

func TestCheck_NamespaceExact_Match(t *testing.T) {
	c := mustChecker(t, standardTeamsYAML)
	alice := makeUser("alice@example.com")
	// "kp-shared" 是 exact match
	err := c.Check(context.Background(), alice, "kp-shared", PermDeploy)
	assert.NoError(t, err)
}

func TestCheck_NamespaceMismatch_Denied(t *testing.T) {
	c := mustChecker(t, standardTeamsYAML)
	alice := makeUser("alice@example.com")
	// alice 是 backend-team,不应能 access kp-frontend-*
	err := c.Check(context.Background(), alice, "kp-frontend-prod", PermDeploy)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-team-match")
}

func TestCheck_NamespaceGlob_QuestionMark(t *testing.T) {
	yaml := `teams:
  - name: t1
    members: [alice@example.com]
    namespaces: ["kp-?-prod"]
    permissions: [deploy]
`
	c := mustChecker(t, yaml)
	alice := makeUser("alice@example.com")

	// kp-a-prod 匹配 (单字符 ?)
	err := c.Check(context.Background(), alice, "kp-a-prod", PermDeploy)
	assert.NoError(t, err, "? 应匹配单字符")

	// kp-ab-prod 不匹配 (? 只匹配单字符)
	err = c.Check(context.Background(), alice, "kp-ab-prod", PermDeploy)
	assert.Error(t, err, "? 不应匹配多字符")
}

func TestCheck_NamespaceWildcardStar(t *testing.T) {
	yaml := `teams:
  - name: ops
    members: [admin@example.com]
    namespaces: ["*"]
    permissions: ["*"]
`
	c := mustChecker(t, yaml)
	admin := makeUser("admin@example.com")
	// "*" 应匹配任何 ns
	for _, ns := range []string{"a", "kp-anything", "kube-system"} {
		err := c.Check(context.Background(), admin, ns, PermDeploy)
		assert.NoError(t, err, "ns=%q 应被 * 匹配", ns)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Q-B1.C: excluded 优先级高于 permissions (单 team 内)
// ════════════════════════════════════════════════════════════════════════════

func TestCheck_Excluded_BeatsPermissions(t *testing.T) {
	c := mustChecker(t, standardTeamsYAML)
	alice := makeUser("alice@example.com")

	// alice 有 backend-team 的 deploy/sandbox/status
	// 但 excluded 含 controller-uninstall (不在 permissions 也得显式 excluded)
	err := c.Check(context.Background(), alice, "kp-backend-prod", PermControllerUninstall)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "excluded-by-team:backend-team")
}

func TestCheck_Excluded_BeatsWildcard(t *testing.T) {
	yaml := `teams:
  - name: t1
    members: [alice@example.com]
    namespaces: ["kp-*"]
    permissions: ["*"]
    excluded:
      - controller-uninstall
`
	c := mustChecker(t, yaml)
	alice := makeUser("alice@example.com")

	// permissions: ["*"] 但 excluded 仍优先 (Q-B1.C)
	err := c.Check(context.Background(), alice, "kp-anything", PermControllerUninstall)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "excluded-by-team")

	// 其他 perm 仍被 * 允许
	err = c.Check(context.Background(), alice, "kp-anything", PermDeploy)
	assert.NoError(t, err)
}

// ════════════════════════════════════════════════════════════════════════════
// Q-B6=C: 跨 team excluded 全局优先 (神来之笔)
// ════════════════════════════════════════════════════════════════════════════

func TestCheck_CrossTeam_ExcludedGlobal(t *testing.T) {
	// alice 同时在 backend-team (excluded controller-uninstall) 和 ops-team (perm: *)
	yaml := `teams:
  - name: backend-team
    members: [alice@example.com]
    namespaces: [kp-backend-*]
    permissions: [deploy]
    excluded: [controller-uninstall]

  - name: ops-team
    members: [alice@example.com]
    namespaces: [kp-backend-*]
    permissions: ["*"]
`
	c := mustChecker(t, yaml)
	alice := makeUser("alice@example.com")

	// Q-B6=C: 即使 ops-team 给了 *,backend-team 的 excluded 仍优先
	err := c.Check(context.Background(), alice, "kp-backend-prod", PermControllerUninstall)
	require.Error(t, err, "Q-B6=C: 跨 team excluded 全局优先")
	assert.Contains(t, err.Error(), "excluded-by-team:backend-team")

	// 其他 perm 仍被任一 team 允许
	err = c.Check(context.Background(), alice, "kp-backend-prod", PermSandbox)
	assert.NoError(t, err, "Q-B6: ops-team 的 * 允许")
}

func TestCheck_CrossTeam_AnyAllowSucceed(t *testing.T) {
	// bob 在两个 team, 其中一个允许 → 允许 (跟 K8s RBAC 一致)
	yaml := `teams:
  - name: t1
    members: [bob@example.com]
    namespaces: [kp-test]
    permissions: [status]

  - name: t2
    members: [bob@example.com]
    namespaces: [kp-test]
    permissions: [deploy]
`
	c := mustChecker(t, yaml)
	bob := makeUser("bob@example.com")

	// t1 给 status, t2 给 deploy → 两个都允许
	assert.NoError(t, c.Check(context.Background(), bob, "kp-test", PermStatus))
	assert.NoError(t, c.Check(context.Background(), bob, "kp-test", PermDeploy))

	// 都没给 sandbox → 拒绝
	err := c.Check(context.Background(), bob, "kp-test", PermSandbox)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission-not-granted")
}

// ════════════════════════════════════════════════════════════════════════════
// Wildcard permission 测试
// ════════════════════════════════════════════════════════════════════════════

func TestCheck_PermWildcard_AllowsAll(t *testing.T) {
	c := mustChecker(t, standardTeamsYAML)
	dave := makeUser("dave@example.com")
	// dave 是 ops-team, perm: *
	for _, perm := range AllPermissions() {
		if perm == PermControllerUninstall {
			// ops-team 没 excluded, 应允许
			err := c.Check(context.Background(), dave, "kp-anything", perm)
			assert.NoError(t, err, "perm=%q 应被 * 允许", perm)
		} else {
			err := c.Check(context.Background(), dave, "kp-anything", perm)
			assert.NoError(t, err)
		}
	}
}

// ════════════════════════════════════════════════════════════════════════════
// 防御性测试
// ════════════════════════════════════════════════════════════════════════════

func TestCheck_NilUser_Denied(t *testing.T) {
	c := mustChecker(t, standardTeamsYAML)
	err := c.Check(context.Background(), nil, "kp-backend-prod", PermDeploy)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user-context-missing")
}

func TestCheck_EmptyEmail_Denied(t *testing.T) {
	c := mustChecker(t, standardTeamsYAML)
	user := &UserContext{Email: ""}
	err := c.Check(context.Background(), user, "kp-backend-prod", PermDeploy)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user-context-missing")
}

// ════════════════════════════════════════════════════════════════════════════
// Reload (live reload)
// ════════════════════════════════════════════════════════════════════════════

func TestReload_PicksUpChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "teams.yaml")
	require.NoError(t, os.WriteFile(path,
		[]byte(`teams:
  - name: t1
    members: [alice@example.com]
    namespaces: [kp-test]
    permissions: [status]
`), 0o644))

	c, err := NewFileBasedChecker(path)
	require.NoError(t, err)

	alice := makeUser("alice@example.com")
	// 初始: alice 有 status
	assert.NoError(t, c.Check(context.Background(), alice, "kp-test", PermStatus))
	// 没 deploy
	require.Error(t, c.Check(context.Background(), alice, "kp-test", PermDeploy))

	// 改 yaml
	require.NoError(t, os.WriteFile(path,
		[]byte(`teams:
  - name: t1
    members: [alice@example.com]
    namespaces: [kp-test]
    permissions: [deploy, status]
`), 0o644))

	require.NoError(t, c.Reload())

	// 重载后 alice 有了 deploy
	assert.NoError(t, c.Check(context.Background(), alice, "kp-test", PermDeploy))
}

func TestReload_BadYAML_KeepsOldConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "teams.yaml")
	require.NoError(t, os.WriteFile(path,
		[]byte(`teams:
  - name: t1
    members: [alice@example.com]
    namespaces: [kp-test]
    permissions: [deploy]
`), 0o644))

	c, err := NewFileBasedChecker(path)
	require.NoError(t, err)

	alice := makeUser("alice@example.com")
	assert.NoError(t, c.Check(context.Background(), alice, "kp-test", PermDeploy))

	// 写入坏 yaml
	require.NoError(t, os.WriteFile(path, []byte("not: [valid: yaml"), 0o644))
	err = c.Reload()
	require.Error(t, err)

	// 旧配置仍然 work (Reload 失败不污染当前)
	assert.NoError(t, c.Check(context.Background(), alice, "kp-test", PermDeploy),
		"Reload 失败应保留旧配置")
}

// ════════════════════════════════════════════════════════════════════════════
// 编译预优化测试 (qc 拍的优化点 1)
// ════════════════════════════════════════════════════════════════════════════

func TestCompileNsPattern_Exact(t *testing.T) {
	m := compileNsPattern("kp-backend-prod")
	assert.Equal(t, matcherExact, m.kind, "无 glob 字符 → exact")
	assert.Equal(t, "kp-backend-prod", m.pattern)
}

func TestCompileNsPattern_Prefix(t *testing.T) {
	m := compileNsPattern("kp-backend-*")
	assert.Equal(t, matcherPrefix, m.kind, "末尾单 * → prefix (优化常见 case)")
	assert.Equal(t, "kp-backend-", m.pattern)
}

func TestCompileNsPattern_Glob_QuestionMark(t *testing.T) {
	m := compileNsPattern("kp-?-prod")
	assert.Equal(t, matcherGlob, m.kind, "含 ? → glob")
}

func TestCompileNsPattern_Glob_MultiStar(t *testing.T) {
	m := compileNsPattern("kp-*-prod-*")
	assert.Equal(t, matcherGlob, m.kind, "中间含 * → glob")
}

func TestCompileNsPattern_SingleStar(t *testing.T) {
	m := compileNsPattern("*")
	assert.Equal(t, matcherPrefix, m.kind, "* → prefix(空) 仍命中所有")
	assert.Equal(t, "", m.pattern)

	// 验证语义: 空 prefix 匹配任何字符串
	t1 := compiledTeam{namespacePats: []nsMatcher{m}}
	assert.True(t, t1.namespaceMatches("anything"))
	assert.True(t, t1.namespaceMatches(""))
}

// ════════════════════════════════════════════════════════════════════════════
// IsPermissionDenied 工具
// ════════════════════════════════════════════════════════════════════════════

func TestIsPermissionDenied(t *testing.T) {
	assert.False(t, IsPermissionDenied(nil))
	assert.False(t, IsPermissionDenied(os.ErrNotExist))

	denied := &ErrPermissionDenied{User: "alice@x.com", Permission: PermDeploy, Reason: "test"}
	assert.True(t, IsPermissionDenied(denied))

	// Error() 包含关键信息
	msg := denied.Error()
	assert.Contains(t, msg, "alice@x.com")
	assert.Contains(t, msg, "deploy")
	assert.Contains(t, msg, "test")
}

// ════════════════════════════════════════════════════════════════════════════
// teams.yaml 解析错误
// ════════════════════════════════════════════════════════════════════════════

func TestNewFileBasedChecker_BadYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "teams.yaml")
	require.NoError(t, os.WriteFile(path, []byte("teams: [not closed"), 0o644))

	_, err := NewFileBasedChecker(path)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "解析"),
		"应给出解析错误信息: %v", err)
}

func TestNewFileBasedChecker_EmptyTeamName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "teams.yaml")
	require.NoError(t, os.WriteFile(path,
		[]byte(`teams:
  - members: [alice@x.com]
    namespaces: [kp-test]
    permissions: [deploy]
`), 0o644))

	_, err := NewFileBasedChecker(path)
	require.Error(t, err, "缺少 name 字段应报错")
	assert.Contains(t, err.Error(), "name")
}

func TestNewFileBasedChecker_EmptyGroupSyntax(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "teams.yaml")
	require.NoError(t, os.WriteFile(path,
		[]byte(`teams:
  - name: t1
    members: ["group:"]
    namespaces: [kp-test]
    permissions: [deploy]
`), 0o644))

	_, err := NewFileBasedChecker(path)
	require.Error(t, err, "空 group: 应报错")
}
