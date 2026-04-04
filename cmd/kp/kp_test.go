package main

import (
	"testing"
	"time"
)

// ── chaos config builders ─────────────────────────────────────────────────────

func TestBuildPodKillConfig(t *testing.T) {
	cfg := buildPodKillConfig("test-exp", "default", "wallet-service", "30s")

	if cfg["apiVersion"] != "chaos-mesh.org/v1alpha1" {
		t.Errorf("apiVersion wrong: %v", cfg["apiVersion"])
	}
	if cfg["kind"] != "PodChaos" {
		t.Errorf("kind wrong: %v", cfg["kind"])
	}
	spec := cfg["spec"].(map[string]interface{})
	if spec["action"] != "pod-kill" {
		t.Errorf("action wrong: %v", spec["action"])
	}
	if spec["duration"] != "30s" {
		t.Errorf("duration wrong: %v", spec["duration"])
	}
	if spec["mode"] != "one" {
		t.Errorf("mode wrong: %v", spec["mode"])
	}
	selector := spec["selector"].(map[string]interface{})
	labels := selector["labelSelectors"].(map[string]string)
	if labels["app"] != "wallet-service" {
		t.Errorf("selector app wrong: %v", labels["app"])
	}
}

func TestBuildNetworkDelayConfig(t *testing.T) {
	cfg := buildNetworkDelayConfig("test", "ns", "svc", "1m", "200ms")

	if cfg["kind"] != "NetworkChaos" {
		t.Errorf("kind wrong: %v", cfg["kind"])
	}
	spec := cfg["spec"].(map[string]interface{})
	if spec["action"] != "delay" {
		t.Errorf("action wrong: %v", spec["action"])
	}
	delay := spec["delay"].(map[string]string)
	if delay["latency"] != "200ms" {
		t.Errorf("latency wrong: %v", delay["latency"])
	}
	if delay["jitter"] != "0ms" {
		t.Errorf("jitter wrong: %v", delay["jitter"])
	}
}

func TestBuildCPUStressConfig(t *testing.T) {
	cfg := buildCPUStressConfig("test", "ns", "svc", "5m", 2)

	if cfg["kind"] != "StressChaos" {
		t.Errorf("kind wrong: %v", cfg["kind"])
	}
	spec := cfg["spec"].(map[string]interface{})
	stressors := spec["stressors"].(map[string]interface{})
	cpu := stressors["cpu"].(map[string]interface{})
	if cpu["workers"] != 2 {
		t.Errorf("workers wrong: %v", cpu["workers"])
	}
	if cpu["load"] != 80 {
		t.Errorf("load wrong: %v", cpu["load"])
	}
}

func TestBuildMemoryStressConfig(t *testing.T) {
	cfg := buildMemoryStressConfig("test", "ns", "svc", "5m", 1)

	spec := cfg["spec"].(map[string]interface{})
	stressors := spec["stressors"].(map[string]interface{})
	mem := stressors["memory"].(map[string]interface{})
	if mem["size"] != "256MB" {
		t.Errorf("size wrong: %v", mem["size"])
	}
}

// ── doctor helpers ────────────────────────────────────────────────────────────

func TestParseGoVersion(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"go version go1.21.0 darwin/arm64", "1.21.0"},
		{"go version go1.25.7 linux/amd64", "1.25.7"},
		{"go1.19", "1.19"},
		{"invalid", "invalid"},
	}
	for _, c := range cases {
		got := parseGoVersion(c.input)
		if got != c.expected {
			t.Errorf("parseGoVersion(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestExtractYAMLField(t *testing.T) {
	yaml := `clientVersion:
  gitVersion: v1.29.0
  goVersion: go1.21.0`

	got := extractYAMLField(yaml, "gitVersion")
	if got != "v1.29.0" {
		t.Errorf("expected v1.29.0, got %q", got)
	}

	got = extractYAMLField(yaml, "missing")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// ── migrate helpers ───────────────────────────────────────────────────────────

func TestExtractVersion(t *testing.T) {
	cases := []struct {
		input    string
		expected int64
	}{
		{"000001_create_users.up.sql", 1},
		{"20260101_add_column.sql", 20260101},
		{"no_number.sql", 0},
		{"002_init.up.sql", 2},
	}
	for _, c := range cases {
		got := extractVersion(c.input)
		if got != c.expected {
			t.Errorf("extractVersion(%q) = %d, want %d", c.input, got, c.expected)
		}
	}
}

func TestExtractTableName(t *testing.T) {
	cases := []struct {
		stmt     string
		prefix   string
		expected string
	}{
		{"CREATE TABLE users (id int)", "CREATE TABLE", "users"},
		{"CREATE TABLE IF NOT EXISTS orders (id int)", "CREATE TABLE", "orders"},
		{"DROP TABLE sessions", "DROP TABLE", "sessions"},
		{"ALTER TABLE users ADD COLUMN email text", "ALTER TABLE", "users"},
	}
	for _, c := range cases {
		got := extractTableName(c.stmt, c.prefix)
		if got != c.expected {
			t.Errorf("extractTableName(%q) = %q, want %q", c.stmt, got, c.expected)
		}
	}
}

// ── image name builder ────────────────────────────────────────────────────────

func TestBuildImageName(t *testing.T) {
	cases := []struct {
		prefix, image, arch, version string
		expected                     string
	}{
		{"qingchun22", "wallet-service", "arm64", "v0.1.0",
			"qingchun22/wallet-service-arm64:v0.1.0"},
		{"", "wallet-service", "amd64", "v1.0.0",
			"/wallet-service-amd64:v1.0.0"},
		{"registry.cn/ns", "api", "arm64", "latest",
			"registry.cn/ns/api-arm64:latest"},
	}
	for _, c := range cases {
		got := buildImageName(c.prefix, c.image, c.arch, c.version)
		if got != c.expected {
			t.Errorf("buildImageName(%q,%q,%q,%q) = %q, want %q",
				c.prefix, c.image, c.arch, c.version, got, c.expected)
		}
	}
}

// ── plugin helpers ────────────────────────────────────────────────────────────

func TestParsePluginNameVersion(t *testing.T) {
	cases := []struct {
		input           string
		name, version   string
	}{
		{"kp-metrics", "kp-metrics", ""},
		{"kp-metrics@v1.0.0", "kp-metrics", "v1.0.0"},
		{"my-plugin@latest", "my-plugin", "latest"},
	}
	for _, c := range cases {
		n, v := parsePluginNameVersion(c.input)
		if n != c.name || v != c.version {
			t.Errorf("parsePluginNameVersion(%q) = (%q,%q), want (%q,%q)",
				c.input, n, v, c.name, c.version)
		}
	}
}

// ── preview warmup helpers ────────────────────────────────────────────────────

func TestParseIntList(t *testing.T) {
	got := parseIntList("10,50,100")
	if len(got) != 3 || got[0] != 10 || got[1] != 50 || got[2] != 100 {
		t.Errorf("parseIntList wrong: %v", got)
	}

	got = parseIntList("  5 , 20 ")
	if len(got) != 2 || got[0] != 5 || got[1] != 20 {
		t.Errorf("parseIntList trim wrong: %v", got)
	}
}

func TestParseDurationList(t *testing.T) {
	got := parseDurationList("2m,5m")
	if len(got) != 2 {
		t.Fatalf("expected 2 durations, got %d", len(got))
	}
	if got[0] != 2*time.Minute {
		t.Errorf("first duration wrong: %v", got[0])
	}
	if got[1] != 5*time.Minute {
		t.Errorf("second duration wrong: %v", got[1])
	}
}

// ── cert check result ─────────────────────────────────────────────────────────

func TestBuildCertCheckResult_OK(t *testing.T) {
	expiry := time.Now().Add(60 * 24 * time.Hour) // 60 天后
	r := buildCertCheckResult("my-tls", expiry)
	if !r.ok {
		t.Errorf("60 天应该 ok，got not ok: %s", r.detail)
	}
}

func TestBuildCertCheckResult_Warn(t *testing.T) {
	expiry := time.Now().Add(15 * 24 * time.Hour) // 15 天后
	r := buildCertCheckResult("my-tls", expiry)
	if r.ok {
		t.Errorf("15 天应该 warn，got ok")
	}
	if r.isError {
		t.Errorf("15 天应该 warn 不是 error")
	}
}

func TestBuildCertCheckResult_Error(t *testing.T) {
	expiry := time.Now().Add(3 * 24 * time.Hour) // 3 天后
	r := buildCertCheckResult("my-tls", expiry)
	if !r.isError {
		t.Errorf("3 天应该 error，got warn")
	}
}

// ── buildImageName 边界 ───────────────────────────────────────────────────────

func TestBuildImageName_EmptyPrefix(t *testing.T) {
	// 空 prefix 不应 panic，格式正确
	got := buildImageName("", "api", "amd64", "v1.0.0")
	if got == "" {
		t.Error("空 prefix 不应返回空字符串")
	}
}

// ── parseIntList 边界 ─────────────────────────────────────────────────────────

func TestParseIntList_Empty(t *testing.T) {
	got := parseIntList("")
	if len(got) != 0 {
		t.Errorf("空字符串应返回空列表, got %v", got)
	}
}

func TestParseIntList_InvalidValues(t *testing.T) {
	// 非数字跳过，不 panic
	got := parseIntList("10,abc,50")
	if len(got) != 2 || got[0] != 10 || got[1] != 50 {
		t.Errorf("非数字应跳过, got %v", got)
	}
}

func TestParseIntList_ZeroSkipped(t *testing.T) {
	// 0 应该被跳过（weight=0 无意义）
	got := parseIntList("0,10,0,50")
	if len(got) != 2 {
		t.Errorf("0 应被跳过, got %v", got)
	}
}

// ── parseDurationList 边界 ────────────────────────────────────────────────────

func TestParseDurationList_Invalid(t *testing.T) {
	// 无效时间格式跳过，不 panic
	got := parseDurationList("2m,invalid,5m")
	if len(got) != 2 {
		t.Errorf("无效时间应跳过, got %v", got)
	}
}

// ── chaos config 边界 ─────────────────────────────────────────────────────────

func TestBuildPodKillConfig_NamespaceInjected(t *testing.T) {
	cfg := buildPodKillConfig("exp", "prod", "api", "1m")
	meta := cfg["metadata"].(map[string]string)
	if meta["namespace"] != "prod" {
		t.Errorf("namespace 应为 prod, got %q", meta["namespace"])
	}
}

func TestBuildNetworkDelayConfig_DefaultJitter(t *testing.T) {
	cfg := buildNetworkDelayConfig("exp", "ns", "svc", "30s", "50ms")
	spec := cfg["spec"].(map[string]interface{})
	delay := spec["delay"].(map[string]string)
	if delay["jitter"] != "0ms" {
		t.Errorf("默认 jitter 应为 0ms, got %q", delay["jitter"])
	}
	if delay["correlation"] != "100" {
		t.Errorf("默认 correlation 应为 100, got %q", delay["correlation"])
	}
}
