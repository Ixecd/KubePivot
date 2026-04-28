// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package controller

import (
	"context"
	"log/slog"
	"os"
	"sync"

	"github.com/Ixecd/kubepivot/internal/audit"
	"github.com/Ixecd/kubepivot/internal/rbac"
)

// ════════════════════════════════════════════════════════════════════════════
// rbac_hook - controller reconcile 端 RBAC + audit denied 接入
//
// v2.8 B.5 (D-Level1 最小可行实现):
//   - 4 处 reconcile 操作接入 mustCheckController (drift-sync/heal/sandbox-gc/sweeper-lease)
//   - 失败时 audit denied + slog.Warn (默认 fail-open, 向后兼容)
//   - ENV KUBEPIVOT_RBAC_ENFORCE=true 时 fail-close (拒绝操作)
//
// 设计哲学 (跟 cmd/kp/rbac_helper.go 关键差异):
//   - controller 不能 os.Exit (会杀整个 pod, 影响所有项目)
//   - 默认 fail-open: 校验失败 → 继续 reconcile (保证 v2.7 之前部署的项目仍工作)
//   - ENFORCE 模式: 校验失败 → 拒绝操作 + audit denied (生产环境推荐)
//
// fake user (Q-D.8=A1):
//   "kubepivot-controller@system"  固定字符串
//   teams.yaml 配置 system-team:
//     members: ["kubepivot-controller@system"]
//     namespaces: ["*"]
//     permissions: [drift-sync, heal, sandbox-gc, sweeper-lease]
//
// teams.yaml 路径 (Q-D.7=C):
//   /etc/kubepivot/teams.yaml  默认 (K8s 标准 ConfigMap mount 路径)
//   可由 ENV KUBEPIVOT_TEAMS_FILE 覆盖
//   不存在时 fallback 全权限 (跟 cmd/kp 端 Q-B5 一致)
//
// 不做 (留 v2.9 D-Level3):
//   - teams.yaml ConfigMap 同步 (v2.9 controller_installer 扩展)
//   - controller watch ConfigMap + live reload
//   - state.History Actor 字段 (v2.9 state 模块重构)
// ════════════════════════════════════════════════════════════════════════════

const (
	// controllerActorEmail 是 controller 端 fake user 的固定 email.
	//
	// teams.yaml 必须配置 system-team 含此成员才能授权 controller 操作.
	// 不存在 teams.yaml 时 (Q-B5 fallback) 永远通过 (向后兼容).
	controllerActorEmail = "kubepivot-controller@system"

	// defaultTeamsFilePath 是 controller pod 内 teams.yaml 默认路径.
	//
	// K8s 标准 ConfigMap mount 路径. 不存在时 fallback 全权限.
	defaultTeamsFilePath = "/etc/kubepivot/teams.yaml"

	// envTeamsFilePath 是覆盖默认路径的环境变量名.
	envTeamsFilePath = "KUBEPIVOT_TEAMS_FILE"

	// envEnforceMode 是切换 fail-close 模式的环境变量名.
	//
	// 值为 "true" / "1" / "yes" 时启用 ENFORCE 模式 (拒绝操作).
	// 默认 fail-open (向后兼容).
	envEnforceMode = "KUBEPIVOT_RBAC_ENFORCE"
)

var (
	// controllerChecker 全程单例 (lazy load + cache).
	controllerChecker     rbac.Checker
	controllerCheckerOnce sync.Once
)

// getControllerChecker lazy 加载 controller 端 rbac.Checker.
//
// 路径优先级 (Q-D.7=C):
//   1. ENV KUBEPIVOT_TEAMS_FILE 指定路径
//   2. /etc/kubepivot/teams.yaml (默认)
//   3. 都不存在 → 空 checker (Q-B5 fallback 全权限)
//
// 加载失败永不致命 (controller 不能因配置问题死掉).
// 错误情况都走 empty checker → 全权限 fallback.
func getControllerChecker() rbac.Checker {
	controllerCheckerOnce.Do(func() {
		path := resolveControllerTeamsPath()
		if path == "" {
			// 没找到 teams.yaml → empty checker (全权限)
			controllerChecker, _ = rbac.NewFileBasedChecker("")
			slog.Info("RBAC: controller teams.yaml 未配置, 运行在向后兼容模式 (全权限)",
				"hint", "set "+envTeamsFilePath+" or mount /etc/kubepivot/teams.yaml")
			return
		}

		checker, err := rbac.NewFileBasedChecker(path)
		if err != nil {
			// 加载失败 → empty checker (全权限) + 警告
			slog.Warn("RBAC: controller teams.yaml 加载失败, fallback 全权限",
				"path", path, "err", err)
			controllerChecker, _ = rbac.NewFileBasedChecker("")
			return
		}

		controllerChecker = checker
		slog.Info("RBAC: controller teams.yaml 已加载", "path", path)
	})
	return controllerChecker
}

// resolveControllerTeamsPath 按 Q-D.7=C 决定 teams.yaml 实际路径.
//
// 1. ENV KUBEPIVOT_TEAMS_FILE 显式指定 (生产环境优先)
// 2. /etc/kubepivot/teams.yaml (K8s ConfigMap mount 标准路径)
// 3. 空字符串 (都不存在 → empty checker)
func resolveControllerTeamsPath() string {
	// 1. 显式 env 配置
	if envPath := os.Getenv(envTeamsFilePath); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath
		}
		slog.Warn("RBAC: "+envTeamsFilePath+" 指定的文件不存在, 尝试默认路径",
			"path", envPath)
	}

	// 2. 默认 K8s 标准路径
	if _, err := os.Stat(defaultTeamsFilePath); err == nil {
		return defaultTeamsFilePath
	}

	// 3. 都不存在
	return ""
}

// isEnforceMode 检测是否启用 fail-close 模式.
//
// ENV KUBEPIVOT_RBAC_ENFORCE 为 "true" / "1" / "yes" 时启用.
// 默认 false (fail-open, 向后兼容).
func isEnforceMode() bool {
	v := os.Getenv(envEnforceMode)
	return v == "true" || v == "1" || v == "yes"
}

// mustCheckController controller 端 RBAC + audit 接入入口.
//
// 默认 fail-open: 校验失败 → audit denied + slog.Warn + return true (继续 reconcile)
// ENFORCE 模式: 校验失败 → audit denied + slog.Warn + return false (拒绝 reconcile)
//
// 调用方应该:
//
//	if !mustCheckController(ctx, "kp-prod-test", rbac.PermDriftSync, "force-sync") {
//	    return // ENFORCE 拒绝
//	}
//	// 主逻辑继续
//
// 跟 cmd/kp/mustCheck 关键差异:
//   - 永不 os.Exit (controller 不能死)
//   - 返回 bool (调用方决定是否继续)
//   - audit denied 走 controller actor (audit.ResolveControllerActor)
//   - reason 字段含具体动作 (action 区分 "force-sync"/"recreate"/"gc"/"sweep")
func mustCheckController(ctx context.Context, namespace string, perm rbac.Permission, action string) bool {
	checker := getControllerChecker()
	if checker == nil {
		// 极端情况, 防御性 (即使加载失败 getControllerChecker 也应返回非 nil)
		return true
	}

	user := &rbac.UserContext{
		Email: controllerActorEmail,
	}

	err := checker.Check(ctx, user, namespace, perm)
	if err == nil {
		return true
	}

	// 拒绝路径: 写 audit denied
	audit.Record(
		"rbac",                              // source
		"rbac.denied",                       // action (统一 source=rbac action=rbac.denied)
		audit.ResolveControllerActor(),      // actor (真实 pod 标识, 不是 fake user)
		string(perm),                        // resource = 拒绝的权限
		namespace,                           // namespace
		audit.OutcomeDenied,                 // outcome
		"controller "+action+" denied: "+err.Error(), // reason
	)

	if isEnforceMode() {
		// ENFORCE: 拒绝操作
		slog.Warn("RBAC ENFORCE: controller 操作被拒绝",
			"action", action,
			"namespace", namespace,
			"permission", perm,
			"err", err)
		return false
	}

	// fail-open (默认): 警告但继续
	slog.Warn("RBAC: controller 操作未授权 (fail-open 模式继续执行)",
		"action", action,
		"namespace", namespace,
		"permission", perm,
		"err", err,
		"hint", "set "+envEnforceMode+"=true to enforce denial")
	return true
}

// resetControllerCheckerForTest 重置 lazy cache (仅供单元测试).
//
// 用于测试环境隔离: 不同测试用例间需重新加载不同 teams.yaml.
// 生产代码不应调用此函数.
func resetControllerCheckerForTest() {
	controllerCheckerOnce = sync.Once{}
	controllerChecker = nil
}
