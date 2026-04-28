// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Ixecd/kubepivot/internal/audit"
	"github.com/Ixecd/kubepivot/internal/rbac"
)

// ════════════════════════════════════════════════════════════════════════════
// rbac_helper - cmd/kp 各命令的 RBAC + audit 接入封装
//
// 设计核心 (Q-B7.10=C):
//   - 各命令一行接入: mustCheck(actor, ns, perm)
//   - lazy load + cache: 全程一次 teams.yaml 解析
//   - audit hook: Check 失败时自动写 audit (outcome=denied)
//   - fail-fast: 拒绝即 os.Exit(1) (跟 KubePivot 既有错误处理一致)
//
// teams.yaml 路径优先级 (Q-B7.11=C):
//   1. <projectRoot>/configs/teams.yaml      项目级 (推荐)
//   2. ~/.kp/teams.yaml                      用户级 (开发者本地测试)
//   3. 都没有 → empty checker (Q-B5 全权限, 向后兼容)
//
// 7 critical 命令接入 (Q-B7.4=B + Q-B7.9=A):
//   runDeploy             → PermDeploy
//   runResume             → PermDeploy
//   runRollback           → PermRollback
//   runSandboxStart       → PermSandbox
//   runDown               → PermRollback (销毁性, 跟 rollback 同级)
//   runControllerInstall  → PermControllerInstall
//   runControllerUninstall → PermControllerUninstall
// ════════════════════════════════════════════════════════════════════════════

var (
	// rbacChecker 全程单例 (lazy load + cache).
	rbacChecker     rbac.Checker
	rbacCheckerOnce sync.Once
	rbacCheckerErr  error
)

// getRBACChecker lazy 加载 rbac.Checker.
//
// 第一次调用时尝试加载 teams.yaml (项目级 → 用户级 → 空).
// sync.Once 保证全程一次, 后续调用直接返回 cache.
//
// 加载错误本身不致命 (走 Q-B5 向后兼容):
//   - 文件不存在 → 返回 empty checker, Check 永远 nil
//   - 文件存在但损坏 → 返回 nil checker + 日志, Check 走 Q-B5 fallback
func getRBACChecker() rbac.Checker {
	rbacCheckerOnce.Do(func() {
		teamsPath := resolveTeamsConfigPath()
		if teamsPath == "" {
			// 文件不存在 → 用空 checker (Q-B5 全权限)
			rbacChecker, rbacCheckerErr = rbac.NewFileBasedChecker("")
			return
		}
		rbacChecker, rbacCheckerErr = rbac.NewFileBasedChecker(teamsPath)
		if rbacCheckerErr != nil {
			// 加载失败但不致命 (yaml 损坏等)
			// Q-B5 防御扩展: 加载失败 → 走全权限 fallback
			P.Info("⚠️", fmt.Sprintf("RBAC: teams.yaml 加载失败,运行在向后兼容模式: %v", rbacCheckerErr))
			// 用空路径再造一个 (保证 rbacChecker 非 nil)
			rbacChecker, _ = rbac.NewFileBasedChecker("")
		}
	})
	return rbacChecker
}

// resolveTeamsConfigPath 按 Q-B7.11=C 优先级返回 teams.yaml 实际路径.
//
// 优先级:
//   1. <projectRoot>/configs/teams.yaml   项目级
//   2. ~/.kp/teams.yaml                   用户级
//   3. 空字符串                          没找到
//
// 项目根查找失败时跳过项目级 (允许在非项目目录运行 kp 命令).
func resolveTeamsConfigPath() string {
	// 1. 项目级
	if root, err := projectRoot(); err == nil {
		projectPath := filepath.Join(root, "configs", "teams.yaml")
		if _, err := os.Stat(projectPath); err == nil {
			return projectPath
		}
	}

	// 2. 用户级
	if home, err := os.UserHomeDir(); err == nil {
		userPath := filepath.Join(home, ".kp", "teams.yaml")
		if _, err := os.Stat(userPath); err == nil {
			return userPath
		}
	}

	// 3. 没找到
	return ""
}

// mustCheck 验证 actor 对 namespace 的 permission, 失败 fail-fast + audit.
//
// 一行接入模式:
//
//	actor := audit.ResolveActor()
//	mustCheck(actor, cfg.namespace, rbac.PermDeploy)
//	// 通过 → 继续主逻辑
//	// 失败 → 已 P.Fail + audit denied + os.Exit(1), 不会 return
//
// Q-B7.3=A 的副作用: 拒绝时写 audit denied 到 ~/.kp/audit/rbac.jsonl
// Q-B5 防御: empty checker (无 teams.yaml) 时永远通过.
func mustCheck(actor, namespace string, perm rbac.Permission) {
	checker := getRBACChecker()
	if checker == nil {
		// 极端情况 (即使加载失败也应有 empty checker, 防御性)
		return
	}

	user := &rbac.UserContext{
		Email: actor,
		// Groups 字段本期不填 (UserContext 已有 Email 即可走 Q-B1.A 邮箱匹配)
		// B.7.x 后续如需 group 支持, 桥接 auth.UserInfo.Groups 进来
	}

	err := checker.Check(context.Background(), user, namespace, perm)
	if err == nil {
		return
	}

	// 拒绝路径: 写 audit denied + fail-fast
	audit.Record(
		"rbac",                                                  // source
		"rbac.denied",                                           // action
		actor,                                                   // actor
		string(perm),                                            // resource = 拒绝的权限
		namespace,                                               // namespace
		audit.OutcomeDenied,                                     // outcome
		err.Error(),                                             // reason = 完整错误 (含 team 名 / 拒绝原因)
	)

	P.Fail(err.Error())
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "如需获得权限,请联系管理员修改 teams.yaml")
	fmt.Fprintln(os.Stderr, "  路径: configs/teams.yaml (项目级) 或 ~/.kp/teams.yaml (用户级)")
	os.Exit(1)
}

// ResetRBACCheckerForTest 重置 lazy cache (仅供单元测试).
//
// 用于测试环境隔离: 不同测试用例间需要重新加载不同 teams.yaml.
// 生产代码不应调用此函数.
func ResetRBACCheckerForTest() {
	rbacCheckerOnce = sync.Once{}
	rbacChecker = nil
	rbacCheckerErr = nil
}
