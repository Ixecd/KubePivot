// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ════════════════════════════════════════════════════════════════════════════
// 测试 helpers
// ════════════════════════════════════════════════════════════════════════════

// withTempHome 隔离 ~/.kp/ 目录到临时目录, 测试结束自动清理.
//
// 同时处理 cachedHostname (sync.Once 保证测试间不互相干扰).
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// writeCredFiles 在临时 home 写 default.yaml + <provider>.yaml.
func writeCredFiles(t *testing.T, home, provider, email string) {
	t.Helper()
	credDir := filepath.Join(home, ".kp", "credentials")
	require.NoError(t, os.MkdirAll(credDir, 0o755))

	// default.yaml
	defaultData := []byte("provider: " + provider + "\n")
	require.NoError(t, os.WriteFile(
		filepath.Join(credDir, "default.yaml"), defaultData, 0o600))

	// <provider>.yaml (跟 v2.8 A internal/auth/credentials.go yaml schema 一致)
	credData := []byte(`provider: ` + provider + `
token:
  access_token: fake-token
  expires_at: 2099-12-31T23:59:59Z
user_info:
  subject: fake-sub
  email: ` + email + `
  name: Fake User
updated_at: 2026-04-28T12:00:00Z
`)
	require.NoError(t, os.WriteFile(
		filepath.Join(credDir, provider+".yaml"), credData, 0o600))
}

// readJSONLLines 读 jsonl 文件, 每行解析为 AuditEvent.
func readJSONLLines(t *testing.T, path string) []AuditEvent {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	var events []AuditEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e AuditEvent
		require.NoError(t, json.Unmarshal([]byte(line), &e), "解析行失败: %s", line)
		events = append(events, e)
	}
	require.NoError(t, scanner.Err())
	return events
}

// ════════════════════════════════════════════════════════════════════════════
// AuditEvent / NewEvent 测试
// ════════════════════════════════════════════════════════════════════════════

func TestNewEvent_AllFieldsSet(t *testing.T) {
	evt := NewEvent("deploy", "deploy.start", "alice@x.com", "myproject", "kp-prod")

	assert.Equal(t, "deploy", evt.Source)
	assert.Equal(t, "deploy.start", evt.Action)
	assert.Equal(t, "alice@x.com", evt.Actor)
	assert.Equal(t, "myproject", evt.Resource)
	assert.Equal(t, "kp-prod", evt.Namespace)
	assert.NotEmpty(t, evt.Timestamp, "Timestamp 应自动填")

	// Timestamp 格式校验 (RFC3339)
	_, err := time.Parse(time.RFC3339, evt.Timestamp)
	assert.NoError(t, err, "Timestamp 应为 RFC3339 格式")
}

func TestAuditEvent_JSONRoundTrip(t *testing.T) {
	evt := AuditEvent{
		Timestamp: "2026-04-28T15:00:00Z",
		Source:    "rollback", Action: "rollback.execute",
		Actor:    "alice@x.com",
		Resource: "wallet-service", Namespace: "kp-prod",
		From: "v1.2.3", To: "v1.2.2", Version: "v1.2.2",
		Reason: "DB migration failed", Outcome: OutcomeFailure,
	}

	data, err := json.Marshal(&evt)
	require.NoError(t, err)

	var got AuditEvent
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, evt, got)
}

func TestOutcome_Constants(t *testing.T) {
	assert.Equal(t, "success", OutcomeSuccess)
	assert.Equal(t, "failure", OutcomeFailure)
	assert.Equal(t, "warning", OutcomeWarning)
	assert.Equal(t, "denied", OutcomeDenied)
}

// ════════════════════════════════════════════════════════════════════════════
// ResolveActor 三档降级测试 (Q-B7.1=A)
// ════════════════════════════════════════════════════════════════════════════

func TestResolveActor_Tier1_SSOEmail(t *testing.T) {
	home := withTempHome(t)
	writeCredFiles(t, home, "google", "alice@example.com")

	got := ResolveActor()
	assert.Equal(t, "alice@example.com", got, "档位 1: SSO email 优先")
}

func TestResolveActor_Tier2_USERAtHost(t *testing.T) {
	withTempHome(t)
	t.Setenv("USER", "qc")

	got := ResolveActor()
	assert.True(t, strings.HasPrefix(got, "qc@"),
		"档位 2: USER@host (got: %s)", got)
	assert.NotContains(t, got, "@example.com", "无 SSO 时不应返回 SSO email")
}

func TestResolveActor_Tier3_AnonymousAtHost(t *testing.T) {
	withTempHome(t)
	t.Setenv("USER", "")
	t.Setenv("USERNAME", "")

	got := ResolveActor()
	assert.True(t, strings.HasPrefix(got, "anonymous@"),
		"档位 3: anonymous@host (got: %s)", got)
}

func TestResolveActor_NeverEmpty(t *testing.T) {
	withTempHome(t)
	t.Setenv("USER", "")
	t.Setenv("USERNAME", "")

	for i := 0; i < 5; i++ {
		got := ResolveActor()
		assert.NotEmpty(t, got, "ResolveActor 永不返回空")
	}
}

func TestResolveActor_SSOWithBadCredFile(t *testing.T) {
	home := withTempHome(t)
	t.Setenv("USER", "qc")

	// 写 default.yaml 但 google.yaml 是损坏的 yaml
	credDir := filepath.Join(home, ".kp", "credentials")
	require.NoError(t, os.MkdirAll(credDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(credDir, "default.yaml"),
		[]byte("provider: google\n"), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(credDir, "google.yaml"),
		[]byte("not: [valid yaml"), 0o600))

	got := ResolveActor()
	// 应静默降级到档位 2 (USER@host)
	assert.True(t, strings.HasPrefix(got, "qc@"),
		"损坏 cred → 静默降级到档位 2 (got: %s)", got)
}

func TestResolveActor_SSOMissingProvider(t *testing.T) {
	home := withTempHome(t)
	t.Setenv("USER", "qc")

	credDir := filepath.Join(home, ".kp", "credentials")
	require.NoError(t, os.MkdirAll(credDir, 0o755))
	// default.yaml 缺 provider 字段
	require.NoError(t, os.WriteFile(
		filepath.Join(credDir, "default.yaml"),
		[]byte("other_field: x\n"), 0o600))

	got := ResolveActor()
	assert.True(t, strings.HasPrefix(got, "qc@"),
		"default.yaml 缺 provider → 降级")
}

func TestResolveActor_SSOEmptyEmailField(t *testing.T) {
	home := withTempHome(t)
	t.Setenv("USER", "qc")

	credDir := filepath.Join(home, ".kp", "credentials")
	require.NoError(t, os.MkdirAll(credDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(credDir, "default.yaml"),
		[]byte("provider: dex\n"), 0o600))
	// dex.yaml 有 schema 但 email 为空
	require.NoError(t, os.WriteFile(
		filepath.Join(credDir, "dex.yaml"),
		[]byte("user_info:\n  subject: x\n  email: \n  name: Y\n"), 0o600))

	got := ResolveActor()
	// email 为空字符串 → readSSOEmail 返回 "" → 降级到 USER@host
	assert.True(t, strings.HasPrefix(got, "qc@"),
		"email 为空 → 降级 (got: %s)", got)
}

func TestResolveControllerActor_WithPodName(t *testing.T) {
	t.Setenv("POD_NAME", "kubepivot-controller-abc123")

	got := ResolveControllerActor()
	assert.Equal(t, "kubepivot-controller@kubepivot-controller-abc123", got)
}

func TestResolveControllerActor_NoPodName(t *testing.T) {
	t.Setenv("POD_NAME", "")

	got := ResolveControllerActor()
	assert.True(t, strings.HasPrefix(got, "kubepivot-controller@"),
		"无 POD_NAME 应 fallback hostname (got: %s)", got)
	// 应包含 @ 且非空
	parts := strings.SplitN(got, "@", 2)
	require.Len(t, parts, 2)
	assert.NotEmpty(t, parts[1])
}

// ════════════════════════════════════════════════════════════════════════════
// Write 写入测试 (Q-B7.3=A 按 source 分文件)
// ════════════════════════════════════════════════════════════════════════════

func TestWrite_NewFile_CreateAndAppend(t *testing.T) {
	home := withTempHome(t)

	evt := NewEvent("deploy", "deploy.start", "alice@x.com", "proj", "kp-prod")
	evt.Outcome = OutcomeSuccess

	require.NoError(t, Write(&evt))

	// 文件路径
	auditFile := filepath.Join(home, ".kp", "audit", "deploy.jsonl")
	stat, err := os.Stat(auditFile)
	require.NoError(t, err)
	assert.True(t, stat.Size() > 0, "文件应非空")

	events := readJSONLLines(t, auditFile)
	require.Len(t, events, 1)
	assert.Equal(t, "deploy", events[0].Source)
	assert.Equal(t, "alice@x.com", events[0].Actor)
}

func TestWrite_AppendExisting(t *testing.T) {
	home := withTempHome(t)

	for i := 0; i < 3; i++ {
		evt := NewEvent("sandbox", "sandbox.start", "alice@x.com", "proj", "kp-prod")
		evt.Outcome = OutcomeSuccess
		require.NoError(t, Write(&evt))
	}

	auditFile := filepath.Join(home, ".kp", "audit", "sandbox.jsonl")
	events := readJSONLLines(t, auditFile)
	assert.Len(t, events, 3, "3 次 Write 应有 3 行")
}

func TestWrite_DifferentSources_DifferentFiles(t *testing.T) {
	home := withTempHome(t)

	// Q-B7.3=A 按 source 分文件
	sources := []string{"deploy", "sandbox", "rollback", "controller"}
	for _, src := range sources {
		evt := NewEvent(src, src+".test", "alice@x.com", "proj", "kp-prod")
		evt.Outcome = OutcomeSuccess
		require.NoError(t, Write(&evt))
	}

	for _, src := range sources {
		auditFile := filepath.Join(home, ".kp", "audit", src+".jsonl")
		assert.FileExists(t, auditFile, "source=%s 应有独立文件", src)
	}
}

func TestWrite_NilEvent_Error(t *testing.T) {
	withTempHome(t)
	err := Write(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil event")
}

func TestWrite_EmptySource_Error(t *testing.T) {
	withTempHome(t)
	evt := AuditEvent{Source: "", Action: "test"}
	err := Write(&evt)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "空 source")
}

func TestWrite_FilePermission0644(t *testing.T) {
	home := withTempHome(t)

	evt := NewEvent("deploy", "deploy.start", "alice@x.com", "proj", "kp-prod")
	require.NoError(t, Write(&evt))

	stat, err := os.Stat(filepath.Join(home, ".kp", "audit", "deploy.jsonl"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), stat.Mode().Perm(),
		"audit 文件 0644 (跟既有 secret.jsonl 一致)")
}

// ════════════════════════════════════════════════════════════════════════════
// Record 不阻断测试 (Q-B7.2=B)
// ════════════════════════════════════════════════════════════════════════════

func TestRecord_Success_DoesNotPanic(t *testing.T) {
	withTempHome(t)
	// 永不 panic 是核心保证
	assert.NotPanics(t, func() {
		Record("deploy", "deploy.start", "alice@x.com", "proj", "kp-prod",
			OutcomeSuccess, "")
	})
}

func TestRecord_WriteSuccess_PersistsEvent(t *testing.T) {
	home := withTempHome(t)

	Record("deploy", "deploy.start", "alice@x.com", "myproj", "kp-prod",
		OutcomeSuccess, "")

	events := readJSONLLines(t,
		filepath.Join(home, ".kp", "audit", "deploy.jsonl"))
	require.Len(t, events, 1)
	assert.Equal(t, "deploy.start", events[0].Action)
	assert.Equal(t, OutcomeSuccess, events[0].Outcome)
	assert.Equal(t, "myproj", events[0].Resource)
}

func TestRecord_WriteFailsBecauseDirIsFile_DoesNotPanic(t *testing.T) {
	home := withTempHome(t)

	// 制造写入失败: 把 ~/.kp/audit 创建成普通文件而非目录
	auditPath := filepath.Join(home, ".kp")
	require.NoError(t, os.MkdirAll(auditPath, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(auditPath, "audit"), []byte("blocking"), 0o644))

	// Q-B7.2=B: 不阻断, 不 panic
	assert.NotPanics(t, func() {
		Record("deploy", "deploy.start", "alice@x.com", "proj", "kp-prod",
			OutcomeSuccess, "")
	})
}

func TestRecordEvent_NilEvent_DoesNotPanic(t *testing.T) {
	withTempHome(t)
	assert.NotPanics(t, func() {
		RecordEvent(nil)
	})
}

func TestRecordEvent_AutoTimestampIfMissing(t *testing.T) {
	home := withTempHome(t)

	evt := &AuditEvent{
		Source: "controller", Action: "controller.install",
		Actor: "alice@x.com", Resource: "proj", Namespace: "kp-prod",
		Outcome: OutcomeSuccess,
		// Timestamp 故意留空
	}
	RecordEvent(evt)

	events := readJSONLLines(t,
		filepath.Join(home, ".kp", "audit", "controller.jsonl"))
	require.Len(t, events, 1)
	assert.NotEmpty(t, events[0].Timestamp, "Timestamp 应自动填")
	_, err := time.Parse(time.RFC3339, events[0].Timestamp)
	assert.NoError(t, err)
}

func TestRecordEvent_WithAllOptionalFields(t *testing.T) {
	home := withTempHome(t)

	evt := &AuditEvent{
		Timestamp: "2026-04-28T12:00:00Z",
		Source:    "rollback", Action: "rollback.execute",
		Actor:    "alice@x.com",
		Resource: "wallet-service", Namespace: "kp-prod",
		From: "v1.2.3", To: "v1.2.2", Version: "v1.2.2",
		Reason: "migration failed", Outcome: OutcomeFailure,
	}
	RecordEvent(evt)

	events := readJSONLLines(t,
		filepath.Join(home, ".kp", "audit", "rollback.jsonl"))
	require.Len(t, events, 1)
	got := events[0]
	assert.Equal(t, "v1.2.3", got.From)
	assert.Equal(t, "v1.2.2", got.To)
	assert.Equal(t, "migration failed", got.Reason)
	assert.Equal(t, OutcomeFailure, got.Outcome)
}

// ════════════════════════════════════════════════════════════════════════════
// nowFunc mock 测试 (验证可测试性设计)
// ════════════════════════════════════════════════════════════════════════════

func TestNowFunc_Mockable(t *testing.T) {
	home := withTempHome(t)

	fixed := time.Date(2026, 4, 28, 15, 30, 0, 0, time.UTC)
	original := nowFunc
	nowFunc = func() time.Time { return fixed }
	defer func() { nowFunc = original }()

	Record("deploy", "deploy.start", "alice@x.com", "proj", "kp-prod",
		OutcomeSuccess, "")

	events := readJSONLLines(t,
		filepath.Join(home, ".kp", "audit", "deploy.jsonl"))
	require.Len(t, events, 1)
	assert.Equal(t, "2026-04-28T15:30:00Z", events[0].Timestamp,
		"nowFunc mock 应让 Timestamp 可控")
}

// ════════════════════════════════════════════════════════════════════════════
// 并发安全性测试 (writerMu 保护)
// ════════════════════════════════════════════════════════════════════════════

func TestWrite_ConcurrentSameSource_NoCorruption(t *testing.T) {
	home := withTempHome(t)

	const N = 50
	done := make(chan bool, N)
	for i := 0; i < N; i++ {
		go func(i int) {
			evt := NewEvent("deploy", "deploy.start", "alice@x.com", "proj", "kp-prod")
			evt.Outcome = OutcomeSuccess
			_ = Write(&evt)
			done <- true
		}(i)
	}
	for i := 0; i < N; i++ {
		<-done
	}

	events := readJSONLLines(t,
		filepath.Join(home, ".kp", "audit", "deploy.jsonl"))
	assert.Len(t, events, N, "并发 %d 写入应得 %d 行 (无丢失/破损)", N, N)
}

// ════════════════════════════════════════════════════════════════════════════
// 集成场景: 完整 ResolveActor + Record 闭环
// ════════════════════════════════════════════════════════════════════════════

func TestE2E_SSOActor_RecordFlow(t *testing.T) {
	home := withTempHome(t)
	writeCredFiles(t, home, "google", "alice@example.com")

	actor := ResolveActor()
	require.Equal(t, "alice@example.com", actor)

	Record("deploy", "deploy.start", actor, "myproj", "kp-prod",
		OutcomeSuccess, "")

	events := readJSONLLines(t,
		filepath.Join(home, ".kp", "audit", "deploy.jsonl"))
	require.Len(t, events, 1)
	assert.Equal(t, "alice@example.com", events[0].Actor,
		"E2E: SSO email 应进入 audit Actor 字段")
}

func TestE2E_NoSSOActor_FallbackUSER(t *testing.T) {
	home := withTempHome(t)
	t.Setenv("USER", "qc")

	actor := ResolveActor()
	require.True(t, strings.HasPrefix(actor, "qc@"))

	Record("controller", "controller.install", actor, "myproj", "kp-prod",
		OutcomeSuccess, "")

	events := readJSONLLines(t,
		filepath.Join(home, ".kp", "audit", "controller.jsonl"))
	require.Len(t, events, 1)
	assert.True(t, strings.HasPrefix(events[0].Actor, "qc@"),
		"无 SSO 时 audit Actor 应是 USER@host")
}
