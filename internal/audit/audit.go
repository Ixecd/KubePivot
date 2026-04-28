// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.

// Package audit 提供 KubePivot v2.8 审计日志写入与统一类型定义.
//
// 本包是 cmd/kp/audit.go (查询端) 与 各命令 (写入端) + controller 端的
// 统一类型定义中心. 设计哲学:
//
//   - 0 第三方依赖 (encoding/json + os/log/slog 标准库)
//   - 写入永不阻断主流程 (Q-B7.2=B): Record() 失败只 slog.Warn
//   - Actor 三档降级 (Q-B7.1=A): SSO email > USER@host > anonymous@host
//   - 按 source 分文件 (Q-B7.3=A): ~/.kp/audit/<source>.jsonl
//
// v2.8 B.7 之前的 audit 模块现状 (诚实标注):
//   - cmd/kp/audit.go 已有 AuditEvent struct (本 commit 提到 internal/audit)
//   - 既有 Actor 字段 hard-code 为 "kp-cli" / "kubepivot-controller" (无意义)
//   - 既有写入点: cmd/kp/secret.go (3 处) + internal/controller/drift_sync.go
//   - 既有查询: kp audit 命令 (jsonl/csv/table 三格式输出)
//
// v2.8 B.7 升级:
//   - AuditEvent 类型迁移到 internal/audit (cmd/kp/audit.go 改 import)
//   - 新增 ResolveActor() 三档降级
//   - 新增 Record() 不阻断写入 helper
//   - 7 个 critical 命令接入 audit 写入 (deploy/sandbox/rollback/controller)
//
// 详细设计: docs/design/enterprise-governance.md (后续 commit)
package audit

import "time"

// AuditEvent 统一审计事件格式 (SOC2/ISO27001 字段友好).
//
// 历史: 本类型源于 cmd/kp/audit.go (v2.8 B.7 之前), v2.8 B.7 迁移到 internal/audit
// 实现命令端 + controller 端 + 查询端的类型统一.
//
// JSON 序列化保持向后兼容 (字段名不变, ~/.kp/audit/secret.jsonl 历史数据可读).
type AuditEvent struct {
	Timestamp string `json:"timestamp"`         // RFC3339, time.Now().UTC().Format(time.RFC3339)
	Source    string `json:"source"`            // deploy / sandbox / secret / drift / rollback / controller / rbac
	Action    string `json:"action"`            // <source>.<verb>: deploy.start / sandbox.commit / rollback.execute
	Actor     string `json:"actor"`             // SSO email > USER@host > anonymous@host (Q-B7.1=A)
	Resource  string `json:"resource"`          // 项目名/服务名
	Namespace string `json:"namespace"`         // K8s namespace
	From      string `json:"from,omitempty"`    // 状态转换: from
	To        string `json:"to,omitempty"`      // 状态转换: to
	Version   string `json:"version,omitempty"` // 部署版本/git commit
	Reason    string `json:"reason,omitempty"`  // 失败原因 / RBAC 拒绝原因
	Outcome   string `json:"outcome"`           // success / failure / warning / denied
}

// Outcome 枚举.
//
// 用 string 别名而非 enum 类型 (跟 Permission 同模式), 直接 yaml/json 兼容.
const (
	OutcomeSuccess = "success" // 命令成功执行
	OutcomeFailure = "failure" // 命令执行失败 (业务错误)
	OutcomeWarning = "warning" // 部分成功 / 有警告
	OutcomeDenied  = "denied"  // RBAC 拒绝 (B.7.3 用)
)

// NewEvent 构造一个 AuditEvent (Timestamp 自动填 UTC).
//
// 调用方填关键字段, 可选字段 (From/To/Version/Reason) 单独 setter 不强制.
//
// 典型用法:
//
//	evt := audit.NewEvent("deploy", "deploy.start", actor, projectName, namespace)
//	evt.Outcome = audit.OutcomeSuccess
//	evt.Version = gitSHA
//	audit.Write(&evt)
func NewEvent(source, action, actor, resource, namespace string) AuditEvent {
	return AuditEvent{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Source:    source,
		Action:    action,
		Actor:     actor,
		Resource:  resource,
		Namespace: namespace,
	}
}
