// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package rbac

import (
	"context"
	"fmt"
)

// ════════════════════════════════════════════════════════════════════════════
// Checker 接口 + 通用错误类型
// ════════════════════════════════════════════════════════════════════════════

// UserContext 用户身份上下文 (Q-B2: email + groups 两个字段).
//
// 来自 internal/auth.UserInfo, 但拆出最小必需字段 (避免包依赖循环).
//
// 注: rbac 包不直接依赖 auth 包. 调用方 (cmd/kp / controller) 自己从 auth.UserInfo
//     构造 UserContext, 传给 Checker.Check().
type UserContext struct {
	Email  string
	Groups []string
}

// Checker 权限校验接口.
//
// 实现:
//   - FileBasedChecker  internal/rbac/file_based.go (本期一锅端)
//   - 未来扩展: EtcdChecker / DBChecker (留 hook)
//
// 实现类必须线程安全 (controller reconcile 多 goroutine 调用).
type Checker interface {
	// Check 校验用户对指定 namespace 的指定 permission 是否被允许.
	//
	// Q-B4=A 拍板: 失败返回 error (含明确文案).
	// 成功返回 nil.
	//
	// 语义 (Q-B6=C 黑名单绝对优先):
	//   1. teams.yaml 缺失 → 允许 (Q-B5 向后兼容)
	//   2. 任一 team excluded 该 permission → 拒绝 (跨 team 黑名单优先)
	//   3. 任一 team 允许 (member 匹配 + namespace 匹配 + permission 匹配) → 允许
	//   4. 否则 → 拒绝
	Check(ctx context.Context, user *UserContext, namespace string, perm Permission) error

	// Reload 重新加载配置 (live reload, 文件变化时调用).
	//
	// 实现应保证原子性 (旧配置仍服务直到新配置 ready).
	Reload() error
}

// ════════════════════════════════════════════════════════════════════════════
// 错误类型
// ════════════════════════════════════════════════════════════════════════════

// ErrPermissionDenied 权限被拒绝错误.
//
// 包含:
//   - User:       拒绝的用户 (email)
//   - Namespace:  操作目标 ns
//   - Permission: 尝试的权限
//   - Reason:     拒绝原因 (excluded / no-team / no-permission)
type ErrPermissionDenied struct {
	User       string
	Namespace  string
	Permission Permission
	Reason     string // "excluded-by-team:backend-team" / "no-team-match" / "permission-not-granted"
}

func (e *ErrPermissionDenied) Error() string {
	return fmt.Sprintf("用户 %q 无 %q 权限于 namespace %q (原因: %s)",
		e.User, e.Permission, e.Namespace, e.Reason)
}

// IsPermissionDenied 判断是否为权限拒绝错误.
//
// 用于上层选择性处理 (跟其他错误如 "config 加载失败" 区分).
func IsPermissionDenied(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*ErrPermissionDenied)
	return ok
}
