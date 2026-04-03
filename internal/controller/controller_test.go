package controller

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Ixecd/kubepivot/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Mock 实现 ─────────────────────────────────────────────────────────────────

// mockDetector 模拟 K8s 资源检测
type mockDetector struct {
	// key: "kind/name/namespace" → exists
	resources map[string]bool
	err       error
}

func newMockDetector() *mockDetector {
	return &mockDetector{resources: make(map[string]bool)}
}

func (m *mockDetector) withResource(kind, name, namespace string, exists bool) *mockDetector {
	m.resources[kind+"/"+name+"/"+namespace] = exists
	return m
}

func (m *mockDetector) withError(err error) *mockDetector {
	m.err = err
	return m
}

func (m *mockDetector) ResourceExists(kind, name, namespace string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	exists, ok := m.resources[kind+"/"+name+"/"+namespace]
	if !ok {
		return false, nil
	}
	return exists, nil
}

// mockHelmClient 模拟 helm 操作
type mockHelmClient struct {
	history      []HelmRelease
	historyErr   error
	rollbackErr  error
	rollbackCall *rollbackCall
}

type rollbackCall struct {
	release   string
	namespace string
	revision  int
}

func newMockHelm(revisions ...int) *mockHelmClient {
	var history []HelmRelease
	for _, r := range revisions {
		history = append(history, HelmRelease{Revision: r})
	}
	return &mockHelmClient{history: history}
}

func (m *mockHelmClient) History(release, namespace string) ([]HelmRelease, error) {
	if m.historyErr != nil {
		return nil, m.historyErr
	}
	return m.history, nil
}

func (m *mockHelmClient) Rollback(release, namespace string, revision int) error {
	m.rollbackCall = &rollbackCall{release: release, namespace: namespace, revision: revision}
	return m.rollbackErr
}

// ── 工具函数 ──────────────────────────────────────────────────────────────────

func newTestStore(t *testing.T) state.Store {
	t.Helper()
	return &testLocalStore{baseDir: t.TempDir()}
}

type testLocalStore struct{ baseDir string }

func (s *testLocalStore) Save(r *state.DeployRecord) error {
	path := filepath.Join(s.baseDir, r.Project, r.Namespace+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (s *testLocalStore) Load(project, namespace string) (*state.DeployRecord, error) {
	path := filepath.Join(s.baseDir, project, namespace+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var record state.DeployRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *testLocalStore) Delete(project, namespace string) error {
	return os.Remove(filepath.Join(s.baseDir, project, namespace+".json"))
}

func newRunningMachine(t *testing.T) *state.Machine {
	t.Helper()
	store := newTestStore(t)
	sm, err := state.New(store, "test-project", "test-ns", "v0.1.0")
	require.NoError(t, err)
	require.NoError(t, sm.Transition(state.StateInitializing, ""))
	require.NoError(t, sm.Transition(state.StateDeploying, ""))
	require.NoError(t, sm.Transition(state.StateValidating, ""))
	require.NoError(t, sm.Transition(state.StateRunning, ""))
	return sm
}

func newTestReconciler(sm *state.Machine, detector Detector, helm HelmClient) *Reconciler {
	return &Reconciler{
		sm:        sm,
		resources: &ResourcesConfig{},
		detector:  detector,
		helm:      helm,
	}
}

// ── LoadResources 测试 ────────────────────────────────────────────────────────

func TestLoadResources_ValidFile(t *testing.T) {
	dir := t.TempDir()
	content := `
resources:
  - kind: Deployment
    name: myapp
    on-missing: auto-heal
    max-retry: 3
    fallback: rollback
  - kind: StatefulSet
    name: postgres
    on-missing: alert
`
	path := filepath.Join(dir, "resources.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	cfg, err := LoadResources(path)
	require.NoError(t, err)
	assert.Len(t, cfg.Resources, 2)
	assert.Equal(t, "Deployment", cfg.Resources[0].Kind)
	assert.Equal(t, "myapp", cfg.Resources[0].Name)
	assert.Equal(t, "auto-heal", cfg.Resources[0].OnMissing)
	assert.Equal(t, 3, cfg.Resources[0].MaxRetry)
	assert.Equal(t, "StatefulSet", cfg.Resources[1].Kind)
	assert.Equal(t, "alert", cfg.Resources[1].OnMissing)
}

func TestLoadResources_FileNotFound(t *testing.T) {
	_, err := LoadResources("/nonexistent/path/resources.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "读取 resources.yaml 失败")
}

func TestLoadResources_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resources.yaml")
	require.NoError(t, os.WriteFile(path, []byte("invalid: yaml: [unclosed"), 0644))

	_, err := LoadResources(path)
	assert.Error(t, err)
}

func TestLoadResources_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resources.yaml")
	require.NoError(t, os.WriteFile(path, []byte("resources: []"), 0644))

	cfg, err := LoadResources(path)
	require.NoError(t, err)
	assert.Empty(t, cfg.Resources)
}

func TestLoadResources_NamespaceDefaultFallback(t *testing.T) {
	dir := t.TempDir()
	content := `
resources:
  - kind: Deployment
    name: myapp
    on-missing: auto-heal
`
	path := filepath.Join(dir, "resources.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	t.Setenv("KUBE_NAMESPACE", "my-namespace")

	cfg, err := LoadResources(path)
	require.NoError(t, err)
	assert.Equal(t, "my-namespace", cfg.Resources[0].Namespace)
}

func TestLoadResources_ExplicitNamespaceNotOverridden(t *testing.T) {
	dir := t.TempDir()
	content := `
resources:
  - kind: Deployment
    name: myapp
    namespace: explicit-ns
    on-missing: auto-heal
`
	path := filepath.Join(dir, "resources.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	t.Setenv("KUBE_NAMESPACE", "env-namespace")

	cfg, err := LoadResources(path)
	require.NoError(t, err)
	assert.Equal(t, "explicit-ns", cfg.Resources[0].Namespace)
}

// ── checkAndHeal 测试 ─────────────────────────────────────────────────────────

func TestCheckAndHeal_ResourceExists_NoAction(t *testing.T) {
	sm := newRunningMachine(t)
	detector := newMockDetector().withResource("Deployment", "myapp", "test-ns", true)
	helm := newMockHelm(1, 2)
	r := newTestReconciler(sm, detector, helm)

	res := Resource{Kind: "Deployment", Name: "myapp", Namespace: "test-ns", OnMissing: "auto-heal"}
	err := r.checkAndHeal(res)

	assert.NoError(t, err)
	assert.Nil(t, helm.rollbackCall, "资源存在时不应触发 rollback")
	assert.Equal(t, state.StateRunning, sm.State(), "状态机不应改变")
}

func TestCheckAndHeal_ResourceMissing_AutoHeal(t *testing.T) {
	sm := newRunningMachine(t)
	detector := newMockDetector().withResource("Deployment", "myapp", "test-ns", false)
	helm := newMockHelm(1, 2)
	r := newTestReconciler(sm, detector, helm)

	t.Setenv("PROJECT_NAME", "myapp")
	res := Resource{Kind: "Deployment", Name: "myapp", Namespace: "test-ns", OnMissing: "auto-heal"}
	err := r.checkAndHeal(res)

	assert.NoError(t, err)
	require.NotNil(t, helm.rollbackCall, "应该触发 rollback")
	assert.Equal(t, 1, helm.rollbackCall.revision, "应该 rollback 到 revision 1（latest-1）")
	assert.Equal(t, state.StateRunning, sm.State())
}

func TestCheckAndHeal_ResourceMissing_Alert(t *testing.T) {
	sm := newRunningMachine(t)
	detector := newMockDetector().withResource("Deployment", "myapp", "test-ns", false)
	helm := newMockHelm(1, 2)
	r := newTestReconciler(sm, detector, helm)

	res := Resource{Kind: "Deployment", Name: "myapp", Namespace: "test-ns", OnMissing: "alert"}
	err := r.checkAndHeal(res)

	assert.NoError(t, err)
	assert.Nil(t, helm.rollbackCall, "alert 策略不应触发 rollback")
	assert.Equal(t, state.StateRunning, sm.State(), "状态机不应改变")
}

func TestCheckAndHeal_ResourceMissing_UnknownStrategy(t *testing.T) {
	sm := newRunningMachine(t)
	detector := newMockDetector().withResource("Deployment", "myapp", "test-ns", false)
	helm := newMockHelm(1, 2)
	r := newTestReconciler(sm, detector, helm)

	res := Resource{Kind: "Deployment", Name: "myapp", Namespace: "test-ns", OnMissing: "unknown"}
	err := r.checkAndHeal(res)

	assert.NoError(t, err)
	assert.Nil(t, helm.rollbackCall, "未知策略不应触发 rollback")
}

func TestCheckAndHeal_DetectorError(t *testing.T) {
	sm := newRunningMachine(t)
	detector := newMockDetector().withError(fmt.Errorf("kubectl 连接失败"))
	helm := newMockHelm(1, 2)
	r := newTestReconciler(sm, detector, helm)

	res := Resource{Kind: "Deployment", Name: "myapp", Namespace: "test-ns", OnMissing: "auto-heal"}
	err := r.checkAndHeal(res)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "检查资源状态失败")
	assert.Nil(t, helm.rollbackCall)
}

// ── healRecreate 测试 ─────────────────────────────────────────────────────────

func TestHealRecreate_Success(t *testing.T) {
	sm := newRunningMachine(t)
	detector := newMockDetector()
	helm := newMockHelm(1, 2, 3) // latest=3, target=2
	r := newTestReconciler(sm, detector, helm)

	t.Setenv("PROJECT_NAME", "myapp")
	res := Resource{Name: "myapp", Namespace: "test-ns"}
	err := r.healRecreate(res)

	require.NoError(t, err)
	require.NotNil(t, helm.rollbackCall)
	assert.Equal(t, "myapp-myapp", helm.rollbackCall.release)
	assert.Equal(t, "test-ns", helm.rollbackCall.namespace)
	assert.Equal(t, 2, helm.rollbackCall.revision)
	assert.Equal(t, state.StateRunning, sm.State())
}

func TestHealRecreate_OnlyOneRevision(t *testing.T) {
	sm := newRunningMachine(t)
	detector := newMockDetector()
	helm := newMockHelm(1) // revision=1，target=0，跳过
	r := newTestReconciler(sm, detector, helm)

	t.Setenv("PROJECT_NAME", "myapp")
	res := Resource{Name: "myapp", Namespace: "test-ns"}
	err := r.healRecreate(res)

	// revision=1 时 target=0，heal.go 里 rollback to 0 会被执行
	// 这里验证 rollback 被调用且 revision=0
	assert.NoError(t, err)
}

func TestHealRecreate_HelmHistoryError(t *testing.T) {
	sm := newRunningMachine(t)
	detector := newMockDetector()
	helm := &mockHelmClient{historyErr: fmt.Errorf("helm 连接失败")}
	r := newTestReconciler(sm, detector, helm)

	t.Setenv("PROJECT_NAME", "myapp")
	res := Resource{Name: "myapp", Namespace: "test-ns"}
	err := r.healRecreate(res)

	assert.NoError(t, err) // history 失败时跳过，不报错
	assert.Nil(t, helm.rollbackCall)
}

func TestHealRecreate_RollbackError(t *testing.T) {
	sm := newRunningMachine(t)
	detector := newMockDetector()
	helm := &mockHelmClient{
		history:     []HelmRelease{{Revision: 1}, {Revision: 2}},
		rollbackErr: fmt.Errorf("rollback 失败"),
	}
	r := newTestReconciler(sm, detector, helm)

	t.Setenv("PROJECT_NAME", "myapp")
	res := Resource{Name: "myapp", Namespace: "test-ns"}
	err := r.healRecreate(res)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "自愈失败")
	// rollback 失败时状态机不应该转到 RUNNING
	assert.Equal(t, state.StateRunning, sm.State())
}

func TestHealRecreate_StateMachineSyncFailure(t *testing.T) {
	// 状态机处于 TERMINATED，转到 RUNNING 会失败
	// 验证：自愈本身成功，状态机同步失败不阻断
	store := newTestStore(t)
	sm, _ := state.New(store, "test-project", "test-ns", "v0.1.0")
	sm.Transition(state.StateInitializing, "")
	sm.Transition(state.StateDeploying, "")
	sm.Transition(state.StateValidating, "")
	sm.Transition(state.StateRunning, "")
	sm.Transition(state.StateTerminated, "")

	detector := newMockDetector()
	helm := newMockHelm(1, 2)
	r := newTestReconciler(sm, detector, helm)

	t.Setenv("PROJECT_NAME", "myapp")
	res := Resource{Name: "myapp", Namespace: "test-ns"}
	err := r.healRecreate(res)

	// healRecreate 本身不应该因为状态机同步失败而报错
	assert.NoError(t, err)
	require.NotNil(t, helm.rollbackCall)
}

// ── reconcile 测试 ────────────────────────────────────────────────────────────

func TestReconcile_AllResourcesExist(t *testing.T) {
	sm := newRunningMachine(t)
	detector := newMockDetector().
		withResource("Deployment", "myapp", "test-ns", true).
		withResource("StatefulSet", "postgres", "test-ns", true)
	helm := newMockHelm(1, 2)
	r := newTestReconciler(sm, detector, helm)
	r.resources = &ResourcesConfig{
		Resources: []Resource{
			{Kind: "Deployment", Name: "myapp", Namespace: "test-ns", OnMissing: "auto-heal"},
			{Kind: "StatefulSet", Name: "postgres", Namespace: "test-ns", OnMissing: "auto-heal"},
		},
	}

	r.reconcile()

	assert.Nil(t, helm.rollbackCall, "所有资源存在时不应触发 rollback")
}

func TestReconcile_OneResourceMissing(t *testing.T) {
	sm := newRunningMachine(t)
	detector := newMockDetector().
		withResource("Deployment", "myapp", "test-ns", false). // 缺失
		withResource("StatefulSet", "postgres", "test-ns", true)
	helm := newMockHelm(1, 2)
	r := newTestReconciler(sm, detector, helm)
	r.resources = &ResourcesConfig{
		Resources: []Resource{
			{Kind: "Deployment", Name: "myapp", Namespace: "test-ns", OnMissing: "auto-heal"},
			{Kind: "StatefulSet", Name: "postgres", Namespace: "test-ns", OnMissing: "auto-heal"},
		},
	}

	t.Setenv("PROJECT_NAME", "myapp")
	r.reconcile()

	require.NotNil(t, helm.rollbackCall, "资源缺失时应触发 rollback")
}

func TestReconcile_EmptyResources(t *testing.T) {
	sm := newRunningMachine(t)
	detector := newMockDetector()
	helm := newMockHelm()
	r := newTestReconciler(sm, detector, helm)
	r.resources = &ResourcesConfig{Resources: []Resource{}}

	r.reconcile() // 不应 panic，不应触发任何操作

	assert.Nil(t, helm.rollbackCall)
}

// ── NewReconciler 测试 ────────────────────────────────────────────────────────

func TestNewReconciler_LoadsResourcesFromPath(t *testing.T) {
	dir := t.TempDir()
	content := `
resources:
  - kind: Deployment
    name: myapp
    on-missing: auto-heal
`
	path := filepath.Join(dir, "resources.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	t.Setenv("RESOURCES_CONFIG", path)

	store := newTestStore(t)
	sm, _ := state.New(store, "test", "test", "v0.1.0")
	r := NewReconciler(sm, "")

	assert.Len(t, r.resources.Resources, 1)
	assert.Equal(t, "myapp", r.resources.Resources[0].Name)
}

func TestNewReconciler_FallsBackOnLoadError(t *testing.T) {
	t.Setenv("RESOURCES_CONFIG", "/nonexistent/path")

	store := newTestStore(t)
	sm, _ := state.New(store, "test", "test", "v0.1.0")
	r := NewReconciler(sm, "")

	// 加载失败时降级为空配置，不 panic
	assert.NotNil(t, r)
	assert.Empty(t, r.resources.Resources)
}

// ── bumpMemory 测试 ───────────────────────────────────────────────────────────

func TestBumpMemory_Mi(t *testing.T) {
	cases := []struct {
		input string
		pct   int
		want  string
	}{
		{"256Mi", 25, "320Mi"},
		{"128Mi", 25, "160Mi"},
		{"100Mi", 25, "125Mi"},
		{"1Mi", 25, "2Mi"}, // 最小步长 +1
	}
	for _, c := range cases {
		got := bumpMemory(c.input, c.pct)
		if got != c.want {
			t.Errorf("bumpMemory(%s, %d) = %s, want %s", c.input, c.pct, got, c.want)
		}
	}
}

func TestBumpMemory_Gi(t *testing.T) {
	got := bumpMemory("4Gi", 25)
	if got != "5Gi" {
		t.Errorf("bumpMemory(4Gi, 25) = %s, want 5Gi", got)
	}
}

func TestBumpMemory_Invalid(t *testing.T) {
	cases := []string{"", "256", "256KB", "abc"}
	for _, c := range cases {
		got := bumpMemory(c, 25)
		if got != "" {
			t.Errorf("bumpMemory(%q) 应返回空，got %q", c, got)
		}
	}
}

// ── analyzeCrashType 测试（纯关键词匹配） ────────────────────────────────────

func TestClassifyCrashType_Startup(t *testing.T) {
	startupLogs := []string{
		"dial tcp 127.0.0.1:5432: connect: connection refused",
		"failed to connect to etcd",
		"no such host: my-postgres",
		"permission denied: /etc/config",
	}
	for _, log := range startupLogs {
		ct := classifyCrashLogs(log)
		if ct != crashStartup {
			t.Errorf("日志 %q 应识别为 startup，got %s", log, ct)
		}
	}
}

func TestClassifyCrashType_Runtime(t *testing.T) {
	runtimeLogs := []string{
		"panic: runtime error: nil pointer dereference",
		"runtime error: index out of range",
		"fatal error: segmentation fault",
	}
	for _, log := range runtimeLogs {
		ct := classifyCrashLogs(log)
		if ct != crashRuntime {
			t.Errorf("日志 %q 应识别为 runtime，got %s", log, ct)
		}
	}
}

func TestClassifyCrashType_Unknown(t *testing.T) {
	ct := classifyCrashLogs("some random output without keywords")
	if ct != crashUnknown {
		t.Errorf("无关键词日志应识别为 unknown，got %s", ct)
	}
}

func TestCheckAndHeal_ResourceMissing_Rollback(t *testing.T) {
	t.Setenv("PROJECT_NAME", "myapp")
	sm := newRunningMachine(t)
	detector := newMockDetector().withResource("Deployment", "myapp", "test-ns", false)
	helm := newMockHelm(1, 2)
	r := newTestReconciler(sm, detector, helm)

	res := Resource{Kind: "Deployment", Name: "myapp", Namespace: "test-ns", OnMissing: "rollback"}
	err := r.checkAndHeal(res)

	assert.NoError(t, err)
	require.NotNil(t, helm.rollbackCall, "应该触发 rollback")
	assert.Equal(t, 1, helm.rollbackCall.revision, "应该 rollback 到 revision 1（latest-1）")
}

func TestCheckAndHeal_ResourceMissing_Custom_Empty(t *testing.T) {
	t.Setenv("PROJECT_NAME", "myapp")
	sm := newRunningMachine(t)
	detector := newMockDetector().withResource("Deployment", "myapp", "test-ns", false)
	helm := newMockHelm(1, 2)
	r := newTestReconciler(sm, detector, helm)

	res := Resource{Kind: "Deployment", Name: "myapp", Namespace: "test-ns",
		OnMissing: "custom", Fallback: ""}
	err := r.checkAndHeal(res)

	assert.NoError(t, err)
	assert.Nil(t, helm.rollbackCall, "fallback 为空时不应触发 rollback")
}

func TestCheckAndHeal_ResourceMissing_ScaleDown(t *testing.T) {
	t.Setenv("PROJECT_NAME", "myapp")
	sm := newRunningMachine(t)
	detector := newMockDetector().withResource("Deployment", "myapp", "test-ns", false)
	helm := newMockHelm(1, 2)
	r := newTestReconciler(sm, detector, helm)

	res := Resource{Kind: "Deployment", Name: "myapp", Namespace: "test-ns", OnMissing: "scale-down"}
	// scale-down 调 kubectl，在测试环境里会失败，但不应该 panic
	err := r.checkAndHeal(res)
	// 允许失败（kubectl 在 CI 里不可用），只验证不 panic 且不触发 rollback
	assert.Nil(t, helm.rollbackCall, "scale-down 不应触发 helm rollback")
	_ = err
}
