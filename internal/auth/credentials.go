// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ════════════════════════════════════════════════════════════════════════════
// Credentials 文件管理 (~/.kp/credentials/)
//
// 路径设计 (V11 校准: 不放 ~/.kube/kubepivot/ , 而是放 ~/.kp/credentials/):
//   - ~/.kube/ 是 kubectl 领地, KubePivot 不该污染
//   - ~/.kp/ 是 KubePivot 自己的配置目录 (envs/ / audit/ / policies/ 已经在这里)
//   - ~/.kp/credentials/ 跟 ~/.kp/envs/ 同模式
//
// 文件结构:
//   ~/.kp/credentials/
//   ├── default.yaml              指向当前 provider (含 provider 字段)
//   ├── dex.yaml                  Dex token + userinfo
//   ├── google.yaml               Google token + userinfo
//   └── github.yaml               GitHub token + userinfo
//
// Q-A3=C 拍板: kp login 同时写 default.yaml 指向 provider, 后续 kp 命令读 default.
// ════════════════════════════════════════════════════════════════════════════

// CredentialsDir 返回 credentials 目录路径 (~/.kp/credentials/).
//
// 跟 cmd/kp/env.go 的 kpEnvsDir 同模式 (~/.kp/envs/).
func CredentialsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kp", "credentials")
}

// CredentialsPath 返回指定 provider 的 credentials 文件路径.
func CredentialsPath(provider string) string {
	return filepath.Join(CredentialsDir(), provider+".yaml")
}

// defaultPointerPath 返回 default.yaml 路径.
//
// 注: default.yaml 是个"指针文件", 内容只有一个字段 provider, 指向当前活跃 provider.
func defaultPointerPath() string {
	return filepath.Join(CredentialsDir(), "default.yaml")
}

// CredentialsFile credentials yaml 文件结构.
type CredentialsFile struct {
	Provider  string    `yaml:"provider"`
	Token     *Token    `yaml:"token"`
	UserInfo  *UserInfo `yaml:"user_info"`
	UpdatedAt time.Time `yaml:"updated_at"`
}

// defaultPointerFile default.yaml 结构 (指针文件).
type defaultPointerFile struct {
	Provider string `yaml:"provider"`
}

// SaveCredentials 保存 token + userinfo 到 ~/.kp/credentials/<provider>.yaml.
//
// 文件权限 0600 (owner read/write only, 因含敏感 token).
func SaveCredentials(provider string, token *Token, info *UserInfo) error {
	if err := os.MkdirAll(CredentialsDir(), 0o755); err != nil {
		return fmt.Errorf("创建 credentials 目录失败: %w", err)
	}

	cf := &CredentialsFile{
		Provider:  provider,
		Token:     token,
		UserInfo:  info,
		UpdatedAt: time.Now().UTC(),
	}

	data, err := yaml.Marshal(cf)
	if err != nil {
		return fmt.Errorf("yaml.Marshal credentials 失败: %w", err)
	}

	path := CredentialsPath(provider)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", path, err)
	}
	return nil
}

// LoadCredentials 读取指定 provider 的 credentials.
//
// 文件不存在时返回 fail-fast 错误 (含 "kp login --provider X" hint).
func LoadCredentials(provider string) (*Token, *UserInfo, error) {
	path := CredentialsPath(provider)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("provider %q 未登录, 请先运行: kp login --provider %s",
				provider, provider)
		}
		return nil, nil, fmt.Errorf("读取 %s 失败: %w", path, err)
	}

	var cf CredentialsFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return nil, nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}

	if cf.Token == nil {
		return nil, nil, fmt.Errorf("%s 缺少 token 字段, 请重新登录", path)
	}
	if cf.UserInfo == nil {
		return nil, nil, fmt.Errorf("%s 缺少 user_info 字段, 请重新登录", path)
	}

	return cf.Token, cf.UserInfo, nil
}

// SetDefault 设置当前活跃 provider (写 ~/.kp/credentials/default.yaml).
//
// Q-A3=C 拍板: kp login 同时把 provider 写入 default.yaml, 后续命令默认从 default 读.
func SetDefault(provider string) error {
	if err := os.MkdirAll(CredentialsDir(), 0o755); err != nil {
		return fmt.Errorf("创建 credentials 目录失败: %w", err)
	}

	pointer := defaultPointerFile{Provider: provider}
	data, err := yaml.Marshal(&pointer)
	if err != nil {
		return fmt.Errorf("yaml.Marshal default 失败: %w", err)
	}

	if err := os.WriteFile(defaultPointerPath(), data, 0o600); err != nil {
		return fmt.Errorf("写入 default.yaml 失败: %w", err)
	}
	return nil
}

// GetDefault 获取当前活跃 provider 名.
//
// 默认指针不存在时返回 fail-fast 错误.
func GetDefault() (string, error) {
	data, err := os.ReadFile(defaultPointerPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("当前未登录 (没有默认 provider), 请先运行 kp login --provider <google|github|dex>")
		}
		return "", fmt.Errorf("读取 default.yaml 失败: %w", err)
	}

	var pointer defaultPointerFile
	if err := yaml.Unmarshal(data, &pointer); err != nil {
		return "", fmt.Errorf("解析 default.yaml 失败: %w", err)
	}
	if pointer.Provider == "" {
		return "", fmt.Errorf("default.yaml 缺少 provider 字段")
	}
	return pointer.Provider, nil
}

// ListCredentials 列出所有已登录的 provider.
//
// 用于 kp whoami --all 等场景 (本 commit 不接入命令, 留 hook).
func ListCredentials() ([]string, error) {
	entries, err := os.ReadDir(CredentialsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 空目录
		}
		return nil, err
	}

	var providers []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yaml")
		if name == "default" {
			continue // 跳过指针文件
		}
		providers = append(providers, name)
	}
	return providers, nil
}

// DeleteCredentials 删除指定 provider 的 credentials (kp logout 用).
//
// 本 commit 不实现 kp logout, 留 hook.
func DeleteCredentials(provider string) error {
	path := CredentialsPath(provider)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil // 幂等
		}
		return fmt.Errorf("删除 %s 失败: %w", path, err)
	}
	return nil
}
