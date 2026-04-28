// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.

// Package rbac 实现 KubePivot v2.8 多团队权限隔离.
//
// 设计核心 (与 v2.6.1 工程纪律一致):
//   - 不引入第三方 RBAC 库 (Casbin / Open Policy Agent 等)
//   - 标准库 + KubePivot 既有 yaml.v3 (跟 KPEnv / state.Store 同模式)
//   - Checker 接口隔离实现, 测试通过 mock 注入
//
// 7 个 Q 拍板:
//   Q-B1.A  members 用 email
//   Q-B1.B  namespaces 用 shell glob (* / ?)
//   Q-B1.C  excluded 优先级高于 permissions (黑名单绝对优先)
//   Q-B2    Group 一锅端支持 (members 可用 group:<name> 语法)
//   Q-B3    Permission enum 风格
//   Q-B4    Check 返回 error (跟既有 Go 风格一致)
//   Q-B5    teams.yaml 缺失 → 全权限 (向后兼容, ROADMAP 一致)
//   Q-B6=C  跨 team excluded 全局优先 (任一 team excluded → 拒绝)
//
// 性能优化 (qc 拍的 2 个工程优化点):
//   - teams.yaml 解析时预编译 glob → 区分 exact/prefix/glob 三类
//   - members / permissions / excluded 用 map[string]bool 做 O(1) 查找
//   - 详见 file_based.go compiledTeam 结构
//
// 详细设计: docs/design/enterprise-governance.md (后续 commit)
package rbac

// Permission 权限类型 (Q-B3=A enum 风格).
//
// 跟 cmd/kp 各命令一一对应:
//   PermDeploy              ↔ kp deploy / kp sandbox start (运行时业务部署)
//   PermSandbox             ↔ kp sandbox start/status/unlock
//   PermRollback            ↔ kp rollback / kp pvc restore
//   PermStatus              ↔ kp status / kp diff / kp drift (只读)
//   PermControllerInstall   ↔ kp controller install (controller 安装)
//   PermControllerUninstall ↔ kp controller uninstall (危险操作)
//   PermAll                 ↔ "*" 通配符
type Permission string

const (
	PermDeploy              Permission = "deploy"
	PermSandbox             Permission = "sandbox"
	PermRollback            Permission = "rollback"
	PermStatus              Permission = "status"
	PermControllerInstall   Permission = "controller-install"
	PermControllerUninstall Permission = "controller-uninstall"
	PermAll                 Permission = "*"
)

// String 实现 fmt.Stringer.
func (p Permission) String() string {
	return string(p)
}

// IsValid 检查 permission 是否在已知列表 (yaml 解析后调用).
//
// 未知 permission 不会让 Check 异常,但会在 LoadTeams 时给出 warn (允许扩展).
func (p Permission) IsValid() bool {
	switch p {
	case PermDeploy, PermSandbox, PermRollback, PermStatus,
		PermControllerInstall, PermControllerUninstall, PermAll:
		return true
	}
	return false
}

// AllPermissions 返回所有已知 permission 列表 (按重要性排序, kp team check 输出用).
func AllPermissions() []Permission {
	return []Permission{
		PermStatus,
		PermDeploy,
		PermSandbox,
		PermRollback,
		PermControllerInstall,
		PermControllerUninstall,
	}
}

// Team teams.yaml 中单个团队定义.
//
// yaml schema:
//
//	teams:
//	  - name: backend-team
//	    members:
//	      - alice@example.com           # 邮箱直接匹配
//	      - bob@example.com
//	      - "group:dev-team"            # group: 前缀匹配 UserInfo.Groups (Q-B2)
//	    namespaces:
//	      - kp-backend-*                # shell glob (Q-B1.B)
//	      - kp-shared
//	    permissions:
//	      - deploy
//	      - sandbox
//	    excluded:
//	      - controller-uninstall        # 即使 permissions: ["*"] 也挡 (Q-B1.C)
type Team struct {
	Name        string   `yaml:"name"`
	Members     []string `yaml:"members"`
	Namespaces  []string `yaml:"namespaces"`
	Permissions []string `yaml:"permissions"`
	Excluded    []string `yaml:"excluded,omitempty"`
}

// TeamConfig teams.yaml 顶层结构.
type TeamConfig struct {
	Teams []Team `yaml:"teams"`
}

// MemberPrefix group 成员前缀 (Q-B2).
//
// teams.yaml 里 "group:dev-team" 表示该 team 所有 OAuth Groups 含 "dev-team" 的用户.
// "alice@example.com" (无前缀) 表示直接邮箱匹配.
const MemberPrefix = "group:"
