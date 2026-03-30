package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ixecd/kubepivot/internal/planner"
	"github.com/Ixecd/kubepivot/internal/state"
)

// ── isFirst 判断测试 ──────────────────────────────────────────────────────────

func TestIsFirstDeploy_NoRelease(t *testing.T) {
	// helm release 不存在 → isFirst = true
	// helmReleaseExists 连不上集群会返回 false（release 不存在）
	// 这里直接测 helmReleaseExists 的行为
	exists := helmReleaseExists("", "", "not-exist-ns", "not-exist-release")
	if exists {
		t.Error("不存在的 release 应该返回 false")
	}
}

// ── envOrDefault 测试 ─────────────────────────────────────────────────────────

func TestEnvOrDefault(t *testing.T) {
	env := map[string]string{
		"VERSION": "v1.0.0",
		"ARCH":    "",
	}

	if got := envOrDefault(env, "VERSION", "v0.1.0"); got != "v1.0.0" {
		t.Errorf("有值时应返回 env 值，got %s", got)
	}
	if got := envOrDefault(env, "ARCH", "amd64"); got != "amd64" {
		t.Errorf("空值时应返回 fallback，got %s", got)
	}
	if got := envOrDefault(env, "MISSING", "default"); got != "default" {
		t.Errorf("不存在的 key 应返回 fallback，got %s", got)
	}
}

// ── resolveDeployConfig 测试 ──────────────────────────────────────────────────

func TestResolveDeployConfig(t *testing.T) {
	env := map[string]string{
		"PROJECT_NAME":   "myapp",
		"KUBE_NAMESPACE": "myapp-ns",
		"KUBE_CONTEXT":   "prod",
		"KUBE_CONFIG":    "",
	}
	cfg := &deployConfig{}
	resolveDeployConfig(cfg, env, "/tmp/myapp")

	if cfg.namespace != "myapp-ns" {
		t.Errorf("namespace 应从 env 读取，got %s", cfg.namespace)
	}
	if cfg.context != "prod" {
		t.Errorf("context 应从 env 读取，got %s", cfg.context)
	}
}

func TestResolveDeployConfig_CLIOverridesEnv(t *testing.T) {
	env := map[string]string{
		"KUBE_NAMESPACE": "env-ns",
		"KUBE_CONTEXT":   "env-ctx",
	}
	cfg := &deployConfig{
		namespace: "cli-ns",  // CLI 指定优先
		context:   "cli-ctx", // CLI 指定优先
	}
	resolveDeployConfig(cfg, env, "/tmp")

	if cfg.namespace != "cli-ns" {
		t.Errorf("CLI 指定的 namespace 应优先，got %s", cfg.namespace)
	}
	if cfg.context != "cli-ctx" {
		t.Errorf("CLI 指定的 context 应优先，got %s", cfg.context)
	}
}

// ── buildMakeEnv 测试 ─────────────────────────────────────────────────────────

func TestBuildMakeEnv_ImagesFiltered(t *testing.T) {
	env := map[string]string{
		"VERSION":         "v1.0.0",
		"ARCH":            "arm64",
		"REGISTRY_PREFIX": "myregistry",
	}
	cfg := &deployConfig{namespace: "myapp", context: "", kubeconfig: ""}
	plan := []planner.Plan{
		{Name: "backend", Image: "backend"},
		{Name: "dtk", Image: ""}, // 空 image，应跳过
		{Name: "frontend", Image: "frontend"},
	}

	makeEnv := buildMakeEnv(env, cfg, plan)

	// 找 IMAGES 行
	var images string
	for _, e := range makeEnv {
		if len(e) > 7 && e[:7] == "IMAGES=" {
			images = e[7:]
		}
	}
	if images != "backend frontend" {
		t.Errorf("IMAGES 应只包含有 image 的组件，got %q", images)
	}
}

func TestBuildMakeEnv_VersionOverride(t *testing.T) {
	env := map[string]string{"VERSION": "v2.0.0"}
	cfg := &deployConfig{}
	plan := []planner.Plan{{Name: "app", Image: "app"}}

	makeEnv := buildMakeEnv(env, cfg, plan)

	var version string
	for _, e := range makeEnv {
		if len(e) > 8 && e[:8] == "VERSION=" {
			version = e[8:]
		}
	}
	if version != "v2.0.0" {
		t.Errorf("VERSION 应从 env 读取，got %s", version)
	}
}

// ── readEnvFile 测试 ──────────────────────────────────────────────────────────

func TestReadEnvFile(t *testing.T) {
	content := `# comment
PROJECT_NAME=myapp
VERSION=v0.1.0
EMPTY=
`
	f, err := os.CreateTemp("", "project*.env")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(content)
	f.Close()

	env, err := readEnvFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}

	if env["PROJECT_NAME"] != "myapp" {
		t.Errorf("PROJECT_NAME 解析错误，got %s", env["PROJECT_NAME"])
	}
	if env["VERSION"] != "v0.1.0" {
		t.Errorf("VERSION 解析错误，got %s", env["VERSION"])
	}
	if _, ok := env["EMPTY"]; !ok {
		t.Error("空值的 key 应该存在")
	}
}

func TestReadEnvFile_IgnoresComments(t *testing.T) {
	content := `# this is a comment
# another comment
KEY=value
`
	f, err := os.CreateTemp("", "*.env")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(content)
	f.Close()

	env, _ := readEnvFile(f.Name())
	if _, ok := env["# this is a comment"]; ok {
		t.Error("注释行不应该被解析")
	}
	if env["KEY"] != "value" {
		t.Errorf("正常 key 应该被解析，got %s", env["KEY"])
	}
}

// ── projectRoot 测试 ──────────────────────────────────────────────────────────

func TestProjectRoot_FindsMakefile(t *testing.T) {
	// 创建临时目录模拟项目根
	dir := t.TempDir()
	subdir := filepath.Join(dir, "internal", "state")
	os.MkdirAll(subdir, 0o755)
	os.WriteFile(filepath.Join(dir, "Makefile"), []byte(""), 0o644)

	// 切换到子目录
	original, _ := os.Getwd()
	defer os.Chdir(original)
	os.Chdir(subdir)

	root, err := projectRoot()
	if err != nil {
		t.Fatal(err)
	}

	realRoot, _ := filepath.EvalSymlinks(root)
	realDir, _ := filepath.EvalSymlinks(dir)

	if realRoot != realDir {
		t.Errorf("projectRoot 应返回 Makefile 所在目录，got %s want %s", root, dir)
	}
}

func TestProjectRoot_NotFound(t *testing.T) {
	dir := t.TempDir() // 没有 Makefile
	original, _ := os.Getwd()
	defer os.Chdir(original)
	os.Chdir(dir)

	_, err := projectRoot()
	if err == nil {
		t.Error("没有 Makefile 时应该返回错误")
	}
}

// ── state machine 集成：deploy → state 流转 ───────────────────────────────────

func TestStateMachine_DeployFlow(t *testing.T) {
	project := fmt.Sprintf("test-%d", time.Now().UnixNano())
	store := state.NewLocalStore()
	sm, err := state.New(store, project, "test-ns", "v0.1.0")

	if err != nil {
		t.Fatal(err)
	}

	// 模拟完整部署流程
	steps := []struct {
		to     state.State
		reason string
	}{
		{state.StateInitializing, "开始部署"},
		{state.StateDeploying, "helm upgrade"},
		{state.StateValidating, "验证"},
		{state.StateRunning, "成功"},
	}

	for _, s := range steps {
		if err := sm.Transition(s.to, s.reason); err != nil {
			t.Fatalf("转换到 %s 失败: %v", s.to, err)
		}
	}

	if sm.State() != state.StateRunning {
		t.Errorf("最终状态应为 RUNNING，got %s", sm.State())
	}
	if len(sm.Record().History) != 4 {
		t.Errorf("历史记录应有 4 条，got %d", len(sm.Record().History))
	}

	// 测试后清理
	defer store.Delete(project, "test-ns")
}

func TestStateMachine_BlocksDuplicateDeploy(t *testing.T) {
	store := state.NewLocalStore()
	sm, _ := state.New(store, "test-project", "test-ns-2", "v0.1.0")
	sm.Transition(state.StateInitializing, "")
	sm.Transition(state.StateDeploying, "")

	// DEPLOYING 状态下不能再次 INITIALIZING
	if err := sm.Transition(state.StateInitializing, "重复部署"); err == nil {
		t.Error("DEPLOYING 状态下不应允许发起新部署")
	}
}
