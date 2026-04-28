// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package audit

import (
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// ════════════════════════════════════════════════════════════════════════════
// ResolveActor - Actor 字段三档降级 (Q-B7.1=A)
//
// 优先级:
//   1. SSO email   读 ~/.kp/credentials/default.yaml → <provider>.yaml → email
//   2. USER@host   os.Getenv("USER") + "@" + hostname
//   3. anonymous@host
//
// 关键约束:
//   - 永不返回空字符串 (audit Actor 必须有值)
//   - 永不 panic (audit 是侧支, 不能因解析失败影响主命令)
//   - 文件 I/O 失败静默降级 (不打印 error, 也不 slog)
//
// 设计权衡:
//   - 不直接 import internal/auth (避免 audit → auth 单向依赖)
//   - 直接读 yaml 解析 ~/.kp/credentials/, 跟 auth 包格式约定耦合
//   - 后续如 auth 包 credentials schema 变更, 需同步本文件解析
// ════════════════════════════════════════════════════════════════════════════

var (
	cachedHostname     string
	cachedHostnameOnce sync.Once
)

// ResolveActor 返回当前执行命令的 Actor 字符串.
//
// 永远返回非空, 永远不 panic. 调用频率: 每个 audit.Record() 一次.
// 性能: hostname 缓存 (sync.Once), credentials 文件每次读取 (~1ms).
func ResolveActor() string {
	// 档位 1: SSO email
	if email := readSSOEmail(); email != "" {
		return email
	}

	// 档位 2: USER@hostname
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME") // Windows fallback
	}
	host := getHostname()

	if user != "" {
		return user + "@" + host
	}

	// 档位 3: anonymous@hostname
	return "anonymous@" + host
}

// readSSOEmail 尝试从 ~/.kp/credentials/ 读出当前 SSO email.
//
// 流程:
//   1. 读 ~/.kp/credentials/default.yaml → provider 字段
//   2. 读 ~/.kp/credentials/<provider>.yaml → user_info.email 字段
//   3. 任一步失败返回空字符串 (静默降级)
//
// 不缓存: credentials 可能在命令执行期间变化 (kp login 切换 provider).
func readSSOEmail() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	// Step 1: 读 default.yaml 拿 provider 名
	defaultPath := filepath.Join(home, ".kp", "credentials", "default.yaml")
	defaultData, err := os.ReadFile(defaultPath)
	if err != nil {
		return "" // 没登录 SSO, 静默降级
	}

	var pointer struct {
		Provider string `yaml:"provider"`
	}
	if err := yaml.Unmarshal(defaultData, &pointer); err != nil {
		return ""
	}
	if pointer.Provider == "" {
		return ""
	}

	// Step 2: 读 <provider>.yaml 拿 email
	credPath := filepath.Join(home, ".kp", "credentials", pointer.Provider+".yaml")
	credData, err := os.ReadFile(credPath)
	if err != nil {
		return ""
	}

	var cred struct {
		UserInfo struct {
			Email string `yaml:"email"`
		} `yaml:"user_info"`
	}
	if err := yaml.Unmarshal(credData, &cred); err != nil {
		return ""
	}

	return cred.UserInfo.Email
}

// getHostname 缓存版 os.Hostname.
//
// 失败时返回 "unknown-host" (永不空).
func getHostname() string {
	cachedHostnameOnce.Do(func() {
		h, err := os.Hostname()
		if err != nil || h == "" {
			cachedHostname = "unknown-host"
			return
		}
		cachedHostname = h
	})
	return cachedHostname
}

// ResolveControllerActor controller 端专用 Actor.
//
// controller 不走 SSO (它是后台进程), 用固定标识符:
//   "kubepivot-controller@<pod-name>"
// 如果 POD_NAME 未设置则用 hostname.
//
// 用于 controller 内部 audit 写入 (drift_sync.go 等).
func ResolveControllerActor() string {
	pod := os.Getenv("POD_NAME")
	if pod == "" {
		pod = getHostname()
	}
	return "kubepivot-controller@" + pod
}
