// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package rbac

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

// ════════════════════════════════════════════════════════════════════════════
// FileBasedChecker
//
// 配置来源: configs/teams.yaml (项目级) 或 ~/.kp/teams.yaml (用户级).
// 通过 Reload() 实现 live reload (controller 监听 ConfigMap 变化时调用).
//
// 性能优化 (qc 拍的两个工程优化点):
//   1. 预编译 glob: LoadFromYAML 时把 namespace 模式分类为 exact/prefix/glob
//   2. O(1) 查找:   members / permissions / excluded 用 map[string]bool
// ════════════════════════════════════════════════════════════════════════════

// FileBasedChecker 默认 Checker 实现 (基于 yaml 文件).
type FileBasedChecker struct {
	configPath string
	// atomic.Pointer 实现配置 swap (Reload 时不阻塞 Check)
	teams atomic.Pointer[[]compiledTeam]
	// 序列化 Reload 调用 (防止并发 Reload 互相覆盖)
	reloadMu sync.Mutex
}

// compiledTeam Team 的预编译表示 (hot path 用此结构,不动原始 Team).
type compiledTeam struct {
	name           string
	memberEmails   map[string]bool   // O(1) 邮箱查找
	memberGroups   map[string]bool   // O(1) group:xxx 查找
	namespacePats  []nsMatcher       // 预编译 ns 模式
	permissions    map[string]bool   // O(1) 权限查找 (含 "*")
	permAllowAll   bool              // permissions 含 "*" 缓存
	excluded       map[string]bool   // O(1) 黑名单
}

// matcherKind ns 模式分类 (优化 1: 80% 实际场景是 prefix,避免 path.Match 开销).
type matcherKind int

const (
	matcherExact  matcherKind = iota // ns == pattern (最快)
	matcherPrefix                    // strings.HasPrefix
	matcherGlob                      // path.Match (兜底,最慢)
)

// nsMatcher 命名空间匹配器.
type nsMatcher struct {
	kind    matcherKind
	pattern string // exact: 完整 / prefix: 去掉末尾 * / glob: 原始
}

// NewFileBasedChecker 构造 checker, 立即加载 configPath.
//
// configPath 不存在时不报错 (Q-B5 缺失 → 全权限). 后续可 Reload 加载.
func NewFileBasedChecker(configPath string) (*FileBasedChecker, error) {
	c := &FileBasedChecker{
		configPath: configPath,
	}
	// 初始化空 teams (满足 Check 不为 nil 的前提)
	empty := []compiledTeam{}
	c.teams.Store(&empty)

	if err := c.Reload(); err != nil {
		// 文件不存在不算错误 (Q-B5)
		if !os.IsNotExist(err) {
			return nil, err
		}
		slog.Info("📋 RBAC: teams.yaml 不存在,运行在向后兼容模式 (全权限)",
			"path", configPath)
	}
	return c, nil
}

// Reload 重新加载 teams.yaml.
//
// 失败时保留旧配置 (不污染当前服务). 不存在时清空成 empty 配置.
func (c *FileBasedChecker) Reload() error {
	c.reloadMu.Lock()
	defer c.reloadMu.Unlock()

	data, err := os.ReadFile(c.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Q-B5: 文件不存在 → empty teams (后续 Check 走"无 team → 允许"分支)
			empty := []compiledTeam{}
			c.teams.Store(&empty)
			return err // 但仍返回 IsNotExist 错误,让构造函数判断
		}
		return fmt.Errorf("读取 %s 失败: %w", c.configPath, err)
	}

	var raw TeamConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("解析 %s 失败: %w", c.configPath, err)
	}

	compiled, err := compileTeams(raw.Teams)
	if err != nil {
		return fmt.Errorf("编译 teams 失败: %w", err)
	}

	c.teams.Store(&compiled)
	slog.Info("📋 RBAC: teams 配置已加载",
		"path", c.configPath,
		"teams", len(compiled))
	return nil
}

// Check 实现 Checker 接口 (Q-B6=C 跨 team excluded 全局优先).
//
// 算法:
//   1. teams 为空 → 允许 (Q-B5)
//   2. 找出所有"用户匹配 + namespace 匹配"的 team
//   3. 任一匹配 team 把 perm 列入 excluded → 拒绝 (Q-B6=C)
//   4. 任一匹配 team 在 permissions 含 perm 或 "*" → 允许
//   5. 否则 → 拒绝
//
// 注意: 黑名单检查用所有匹配的 team, 不只是当前正在评估的那个 (跨 team 优先).
func (c *FileBasedChecker) Check(ctx context.Context, user *UserContext, namespace string, perm Permission) error {
	if user == nil || user.Email == "" {
		return &ErrPermissionDenied{
			User: "(未登录)", Namespace: namespace, Permission: perm,
			Reason: "user-context-missing",
		}
	}

	teams := *c.teams.Load()
	if len(teams) == 0 {
		// Q-B5: 没配 teams → 全权限
		return nil
	}

	// 第一遍: 找匹配的 teams (member + namespace 命中)
	var matchedTeams []*compiledTeam
	for i := range teams {
		t := &teams[i]
		if !t.userMatches(user) {
			continue
		}
		if !t.namespaceMatches(namespace) {
			continue
		}
		matchedTeams = append(matchedTeams, t)
	}

	if len(matchedTeams) == 0 {
		return &ErrPermissionDenied{
			User: user.Email, Namespace: namespace, Permission: perm,
			Reason: "no-team-match",
		}
	}

	// 第二遍: 跨 team excluded 全局检查 (Q-B6=C)
	for _, t := range matchedTeams {
		if t.excluded[string(perm)] {
			return &ErrPermissionDenied{
				User: user.Email, Namespace: namespace, Permission: perm,
				Reason: fmt.Sprintf("excluded-by-team:%s", t.name),
			}
		}
	}

	// 第三遍: 任一 team 允许即放行
	for _, t := range matchedTeams {
		if t.permAllowAll || t.permissions[string(perm)] {
			return nil
		}
	}

	return &ErrPermissionDenied{
		User: user.Email, Namespace: namespace, Permission: perm,
		Reason: "permission-not-granted",
	}
}

// ════════════════════════════════════════════════════════════════════════════
// compiledTeam 内部方法
// ════════════════════════════════════════════════════════════════════════════

// userMatches 判断用户是否属于本 team (email 或 group).
func (t *compiledTeam) userMatches(user *UserContext) bool {
	if t.memberEmails[user.Email] {
		return true
	}
	for _, g := range user.Groups {
		if t.memberGroups[g] {
			return true
		}
	}
	return false
}

// namespaceMatches 判断 namespace 是否匹配本 team 任一模式.
//
// 走预编译的 nsMatcher,大部分场景命中 exact/prefix 不调 path.Match.
func (t *compiledTeam) namespaceMatches(ns string) bool {
	for _, m := range t.namespacePats {
		switch m.kind {
		case matcherExact:
			if ns == m.pattern {
				return true
			}
		case matcherPrefix:
			if strings.HasPrefix(ns, m.pattern) {
				return true
			}
		case matcherGlob:
			ok, _ := path.Match(m.pattern, ns)
			if ok {
				return true
			}
		}
	}
	return false
}

// ════════════════════════════════════════════════════════════════════════════
// teams 编译 (yaml → compiledTeam)
// ════════════════════════════════════════════════════════════════════════════

// compileTeams 编译 teams.yaml 解析结果到 hot-path 优化结构.
func compileTeams(teams []Team) ([]compiledTeam, error) {
	compiled := make([]compiledTeam, 0, len(teams))
	for _, t := range teams {
		if t.Name == "" {
			return nil, fmt.Errorf("team 缺少 name 字段")
		}

		c := compiledTeam{
			name:         t.Name,
			memberEmails: make(map[string]bool),
			memberGroups: make(map[string]bool),
			permissions:  make(map[string]bool),
			excluded:     make(map[string]bool),
		}

		// members: email + group: 前缀分类 (Q-B2)
		for _, m := range t.Members {
			if strings.HasPrefix(m, MemberPrefix) {
				groupName := strings.TrimPrefix(m, MemberPrefix)
				if groupName == "" {
					return nil, fmt.Errorf("team %q: 空 group: 成员", t.Name)
				}
				c.memberGroups[groupName] = true
			} else {
				c.memberEmails[m] = true
			}
		}

		// namespaces: 预编译 glob → exact / prefix / glob 三类
		c.namespacePats = make([]nsMatcher, 0, len(t.Namespaces))
		for _, p := range t.Namespaces {
			c.namespacePats = append(c.namespacePats, compileNsPattern(p))
		}

		// permissions
		for _, p := range t.Permissions {
			c.permissions[p] = true
			if p == string(PermAll) {
				c.permAllowAll = true
			}
			if !Permission(p).IsValid() {
				slog.Warn("RBAC: 未知 permission (允许扩展但建议核对拼写)",
					"team", t.Name, "permission", p)
			}
		}

		// excluded
		for _, p := range t.Excluded {
			c.excluded[p] = true
		}

		compiled = append(compiled, c)
	}
	return compiled, nil
}

// compileNsPattern 把单个 namespace pattern 分类成 nsMatcher.
//
// 策略:
//   "kp-backend-prod"  → exact   (无 * / ?)
//   "kp-backend-*"     → prefix  (末尾单 *,前面无 ?)
//   "kp-?-prod-*"      → glob    (含 ? 或多个 *)
//   "*"                → prefix("") → 等价 prefix 匹配空串始终为 true
func compileNsPattern(p string) nsMatcher {
	// 无任何 glob 字符 → exact
	if !strings.ContainsAny(p, "*?") {
		return nsMatcher{kind: matcherExact, pattern: p}
	}

	// 单 * 在末尾,前面无 ? → prefix
	if strings.HasSuffix(p, "*") && !strings.ContainsAny(p[:len(p)-1], "*?") {
		return nsMatcher{kind: matcherPrefix, pattern: p[:len(p)-1]}
	}

	// 兜底: 走 path.Match
	return nsMatcher{kind: matcherGlob, pattern: p}
}
