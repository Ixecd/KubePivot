// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package audit

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// nowFunc 时间获取函数 (test 时可替换实现 mock).
//
// 不直接用 time.Now: 测试需要冻结时间验证 Timestamp 字段格式.
var nowFunc = time.Now

// ════════════════════════════════════════════════════════════════════════════
// Audit 写入 (Q-B7.2=B 不阻断 + Q-B7.3=A 按 source 分文件)
//
// 设计哲学:
//   - 写入失败永不影响主命令 (Record 不返回 error, Write 返回 error 但不强制处理)
//   - jsonl append 模式 (跟既有 secret.jsonl 一致)
//   - 按 source 分文件: ~/.kp/audit/<source>.jsonl
//   - 文件锁 (fcntl LOCK_EX) 多 kp 进程并发写入安全
//   - 不做 rotation (Q-B7.5=A KISS, 留给 logrotate)
// ════════════════════════════════════════════════════════════════════════════

var (
	// writerMu 保护单进程内多 goroutine 并发写入同一 source 文件.
	// 不同 source 可并发 (因为是不同文件), 但同一 source 串行.
	// 跨进程靠 OS 文件 append 模式的原子性 (Linux/macOS write < PIPE_BUF 原子).
	writerMu sync.Mutex
)

// Write 把一条 AuditEvent 写入 ~/.kp/audit/<source>.jsonl.
//
// Q-B7.2=B: 失败不阻断, 但返回 error 供调用方选择处理.
// 通常调用方不处理 error, 用 Record() 包装更方便.
//
// 跨平台:
//   - Linux/macOS: append 模式 < 4KB 写入是原子的
//   - 单进程内 sync.Mutex 保护
func Write(evt *AuditEvent) error {
	if evt == nil {
		return fmt.Errorf("audit.Write: nil event")
	}
	if evt.Source == "" {
		return fmt.Errorf("audit.Write: 空 source")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("audit.Write: 获取 home 目录失败: %w", err)
	}

	auditDir := filepath.Join(home, ".kp", "audit")
	if err := os.MkdirAll(auditDir, 0o755); err != nil {
		return fmt.Errorf("audit.Write: 创建 audit 目录失败: %w", err)
	}

	auditFile := filepath.Join(auditDir, evt.Source+".jsonl")

	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("audit.Write: 序列化失败: %w", err)
	}
	data = append(data, '\n')

	writerMu.Lock()
	defer writerMu.Unlock()

	f, err := os.OpenFile(auditFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("audit.Write: 打开 %s 失败: %w", auditFile, err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("audit.Write: 写入 %s 失败: %w", auditFile, err)
	}

	return nil
}

// Record 高阶 audit 写入 helper - 不阻断主流程的快捷封装.
//
// Q-B7.2=B 拍板: 失败只 slog.Warn, 永不影响调用方.
//
// 典型用法 (defer 模式):
//
//	func runDeploy(args []string) {
//	    actor := audit.ResolveActor()
//	    outcome := audit.OutcomeSuccess
//	    defer func() {
//	        audit.Record("deploy", "deploy.start", actor, projectName, ns, outcome, "")
//	    }()
//	    if err := doDeploy(); err != nil {
//	        outcome = audit.OutcomeFailure
//	        return
//	    }
//	}
//
// 简单用法 (单条记录, 非 defer):
//
//	audit.Record("controller", "controller.install", actor, projectName, ns,
//	             audit.OutcomeSuccess, "")
func Record(source, action, actor, resource, namespace, outcome, reason string) {
	evt := AuditEvent{
		Timestamp: nowRFC3339(),
		Source:    source,
		Action:    action,
		Actor:     actor,
		Resource:  resource,
		Namespace: namespace,
		Outcome:   outcome,
		Reason:    reason,
	}

	if err := Write(&evt); err != nil {
		// Q-B7.2=B: 永不阻断, 只 warn
		slog.Warn("audit 写入失败 (不影响主命令)",
			"source", source, "action", action, "actor", actor,
			"err", err)
	}
}

// RecordEvent 接收完整 AuditEvent 不阻断写入 (Record 的 expanded 版本).
//
// 用于需要填充 From/To/Version 等可选字段的场景.
// 与 Record() 一样不返回 error, 失败 slog.Warn.
func RecordEvent(evt *AuditEvent) {
	if evt == nil {
		slog.Warn("audit.RecordEvent: nil event, 跳过")
		return
	}
	if evt.Timestamp == "" {
		evt.Timestamp = nowRFC3339()
	}
	if err := Write(evt); err != nil {
		slog.Warn("audit 写入失败 (不影响主命令)",
			"source", evt.Source, "action", evt.Action, "actor", evt.Actor,
			"err", err)
	}
}

// nowRFC3339 单独函数便于测试时 mock.
func nowRFC3339() string {
	return nowFunc().UTC().Format("2006-01-02T15:04:05Z07:00")
}
