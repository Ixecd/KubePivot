// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package supplychain

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestSupplyChainConfig_IsValid 验证配置校验逻辑
func TestSupplyChainConfig_IsValid(t *testing.T) {
	tests := []struct {
		name    string
		cfg     SupplyChainConfig
		wantErr bool
	}{
		{
			name:    "empty config is valid",
			cfg:     SupplyChainConfig{},
			wantErr: false,
		},
		{
			name: "signing enforce without key is invalid",
			cfg: SupplyChainConfig{
				Signing: struct {
					Enforce   bool   `yaml:"enforce" json:"enforce"`
					CosignKey string `yaml:"cosign-key" json:"cosign_key"`
					Keyless   *struct {
						Identity string `yaml:"identity" json:"identity"`
						Issuer   string `yaml:"issuer" json:"issuer"`
						RegExp   bool   `yaml:"regexp,omitempty" json:"regexp"`
					} `yaml:"keyless,omitempty" json:"keyless,omitempty"`
				}{Enforce: true, Keyless: nil}, // ← 显式设置 Keyless: nil
			},
			wantErr: true,
		},
		{
			name: "sbom require without format is invalid",
			cfg: SupplyChainConfig{
				SBOM: struct {
					Require bool   `yaml:"require" json:"require"`
					Format  string `yaml:"format" json:"format"`
				}{Require: true},
			},
			wantErr: true,
		},
		{
			name: "invalid cve severity",
			cfg: SupplyChainConfig{
				CVE: struct {
					MaxSeverity string   `yaml:"max-severity" json:"max_severity"`
					Exceptions  []string `yaml:"exceptions" json:"exceptions"`
				}{MaxSeverity: "super-critical"},
			},
			wantErr: true,
		},
		{
			name: "valid full config",
			cfg: SupplyChainConfig{
				Signing: struct {
					Enforce   bool   `yaml:"enforce" json:"enforce"`
					CosignKey string `yaml:"cosign-key" json:"cosign_key"`
					Keyless   *struct {
						Identity string `yaml:"identity" json:"identity"`
						Issuer   string `yaml:"issuer" json:"issuer"`
						RegExp   bool   `yaml:"regexp,omitempty" json:"regexp"`
					} `yaml:"keyless,omitempty" json:"keyless,omitempty"`
				}{Enforce: true, CosignKey: "/path/to/key.pub"},
				SBOM: struct {
					Require bool   `yaml:"require" json:"require"`
					Format  string `yaml:"format" json:"format"`
				}{Require: true, Format: "cyclonedx-json"},
				CVE: struct {
					MaxSeverity string   `yaml:"max-severity" json:"max_severity"`
					Exceptions  []string `yaml:"exceptions" json:"exceptions"`
				}{MaxSeverity: "high"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.IsValid()
			if (err != nil) != tt.wantErr {
				t.Errorf("IsValid() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestSupplyChainConfig_Merge 验证配置合并优先级
func TestSupplyChainConfig_Merge(t *testing.T) {
	base := &SupplyChainConfig{
		Registries: struct {
			Allow []string `yaml:"allow" json:"allow"`
			Deny  []string `yaml:"deny" json:"deny"`
		}{Allow: []string{"registry.io"}},
		Signing: struct {
			Enforce   bool   `yaml:"enforce" json:"enforce"`
			CosignKey string `yaml:"cosign-key" json:"cosign_key"`
			Keyless   *struct {
				Identity string `yaml:"identity" json:"identity"`
				Issuer   string `yaml:"issuer" json:"issuer"`
				RegExp   bool   `yaml:"regexp,omitempty" json:"regexp"`
			} `yaml:"keyless,omitempty" json:"keyless,omitempty"`
		}{Enforce: true, CosignKey: "/base/key.pub"},
	}

	override := &SupplyChainConfig{
		Registries: struct {
			Allow []string `yaml:"allow" json:"allow"`
			Deny  []string `yaml:"deny" json:"deny"`
		}{Deny: []string{"docker.io"}},
		
		Signing: struct {
			Enforce   bool   `yaml:"enforce" json:"enforce"`
			CosignKey string `yaml:"cosign-key" json:"cosign_key"`
			Keyless   *struct {
				Identity string `yaml:"identity" json:"identity"`
				Issuer   string `yaml:"issuer" json:"issuer"`
				RegExp   bool `yaml:"regexp,omitempty" json:"regexp"`
			} `yaml:"keyless,omitempty" json:"keyless,omitempty"`
		}{Enforce: true, CosignKey: "/override/key.pub"},
	}

	base.Merge(override)

	// Allow 应保留 (override 未设置)
	if len(base.Registries.Allow) != 1 || base.Registries.Allow[0] != "registry.io" {
		t.Errorf("expected Allow=[registry.io], got %v", base.Registries.Allow)
	}
	// Deny 应被覆盖
	if len(base.Registries.Deny) != 1 || base.Registries.Deny[0] != "docker.io" {
		t.Errorf("expected Deny=[docker.io], got %v", base.Registries.Deny)
	}
	// Signing.Enforce 应被覆盖为 true
	if !base.Signing.Enforce {
		t.Error("expected Signing.Enforce=true after merge")
	}
	// CosignKey 应被覆盖
	if base.Signing.CosignKey != "/override/key.pub" {
		t.Errorf("expected CosignKey=/override/key.pub, got %s", base.Signing.CosignKey)
	}
}

// TestCheckRegistry 验证注册表策略检查
func TestCheckRegistry(t *testing.T) {
	tests := []struct {
		name    string
		image   string
		allow   []string
		deny    []string
		wantErr bool
	}{
		{
			name:    "no policy = allow all",
			image:   "docker.io/library/alpine:latest",
			wantErr: false,
		},
		{
			name:    "in allow list",
			image:   "registry.io/app:v1",
			allow:   []string{"registry.io"},
			wantErr: false,
		},
		{
			name:    "not in allow list",
			image:   "ghcr.io/app:v1",
			allow:   []string{"registry.io"},
			wantErr: true,
		},
		{
			name:    "in deny list (priority)",
			image:   "docker.io/malicious:latest",
			allow:   []string{"docker.io"},
			deny:    []string{"docker.io/malicious"},
			wantErr: true,
		},
		{
			name:    "default registry extraction",
			image:   "alpine:latest",
			allow:   []string{"docker.io"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkRegistry(tt.image, tt.allow, tt.deny)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkRegistry() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestExtractRegistry 验证注册表提取逻辑
func TestExtractRegistry(t *testing.T) {
	tests := []struct {
		image    string
		expected string
	}{
		{"registry.io/repo:tag", "registry.io"},
		{"ghcr.io/org/img", "ghcr.io"},
		{"localhost:5000/app", "localhost:5000"},
		{"alpine:latest", "docker.io"},
		{"library/nginx", "docker.io"},
	}

	for _, tt := range tests {
		if got := extractRegistry(tt.image); got != tt.expected {
			t.Errorf("extractRegistry(%q) = %q, want %q", tt.image, got, tt.expected)
		}
	}
}

// TestCache_TTL 验证缓存过期逻辑
func TestCache_TTL(t *testing.T) {
	key := "test-image|/key.pub|cyclonedx-json"
	testErr := fmt.Errorf("test error")

	// 设置缓存 (100ms TTL)
	setCache(key, testErr, 100*time.Millisecond)

	// 立即读取应命中
	if err, ok := getCache(key); !ok || err != testErr {
		t.Error("expected cache hit immediately after set")
	}

	// 等待过期
	time.Sleep(150 * time.Millisecond)

	// 读取应未命中
	if _, ok := getCache(key); ok {
		t.Error("expected cache miss after TTL")
	}
}

// TestValidatePolicy_EmptyConfig 验证空配置放行
func TestValidatePolicy_EmptyConfig(t *testing.T) {
	ctx := context.Background()
	err := ValidatePolicy(ctx, "any/image:v1", nil)
	if err != nil {
		t.Errorf("expected no error with nil config, got %v", err)
	}
}
