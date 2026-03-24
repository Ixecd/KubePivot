package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── 状态转换测试 ──────────────────────────────────────────────────────────────

func TestTransition_HappyPath(t *testing.T) {
	sm := newTestMachine(t)

	steps := []struct {
		to     State
		reason string
	}{
		{StateInitializing, "开始部署"},
		{StateDeploying, "执行 helm upgrade"},
		{StateValidating, "验证部署结果"},
		{StateRunning, "部署成功"},
	}

	for _, s := range steps {
		err := sm.Transition(s.to, s.reason)
		require.NoError(t, err, "转换到 %s 应该成功", s.to)
		assert.Equal(t, s.to, sm.State())
	}
}

func TestTransition_IllegalTransition(t *testing.T) {
	sm := newTestMachine(t)

	// IDLE 不能直接到 RUNNING
	err := sm.Transition(StateRunning, "非法跳跃")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "非法状态转换")
	assert.Equal(t, StateIdle, sm.State(), "状态不应改变")
}

func TestTransition_FirstDeployFailure(t *testing.T) {
	sm := newTestMachine(t)
	sm.MarkFirstDeploy(true)

	sm.Transition(StateInitializing, "开始")
	sm.Transition(StateDeploying, "部署")

	// 首次部署失败 → CLEANING → IDLE
	err := sm.Transition(StateCleaning, "首次部署失败")
	require.NoError(t, err)
	assert.Equal(t, StateCleaning, sm.State())

	err = sm.Transition(StateIdle, "清理完成")
	require.NoError(t, err)
	assert.Equal(t, StateIdle, sm.State())
}

func TestTransition_UpdateFailure(t *testing.T) {
	sm := newTestMachine(t)

	// 先把状态推到 RUNNING（模拟已有部署）
	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateValidating, "")
	sm.Transition(StateRunning, "")

	// 新一轮部署失败 → ROLLING_BACK → RUNNING
	sm.Transition(StateInitializing, "更新")
	sm.Transition(StateDeploying, "")

	err := sm.Transition(StateRollingBack, "更新失败，回滚")
	require.NoError(t, err)
	assert.Equal(t, StateRollingBack, sm.State())

	err = sm.Transition(StateRunning, "回滚成功")
	require.NoError(t, err)
	assert.Equal(t, StateRunning, sm.State())
}

func TestTransition_ValidatingTimeout(t *testing.T) {
	sm := newTestMachine(t)

	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateValidating, "")

	// VALIDATING 超时 → ROLLING_BACK → RUNNING
	err := sm.Transition(StateRollingBack, "验证超时")
	require.NoError(t, err)

	err = sm.Transition(StateRunning, "回滚成功")
	require.NoError(t, err)
	assert.Equal(t, StateRunning, sm.State())
}

func TestTransition_History(t *testing.T) {
	sm := newTestMachine(t)

	sm.Transition(StateInitializing, "开始部署 v0.1.0")
	sm.Transition(StateDeploying, "执行 helm upgrade")
	sm.Transition(StateValidating, "验证")
	sm.Transition(StateRunning, "成功")

	history := sm.Record().History
	assert.Len(t, history, 4)

	assert.Equal(t, StateIdle, history[0].From)
	assert.Equal(t, StateInitializing, history[0].To)
	assert.Equal(t, "开始部署 v0.1.0", history[0].Reason)
	assert.False(t, history[0].Timestamp.IsZero())

	assert.Equal(t, StateRunning, history[3].To)
}

func TestTransition_TerminatedIsTerminal(t *testing.T) {
	sm := newTestMachine(t)

	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateValidating, "")
	sm.Transition(StateRunning, "")
	sm.Transition(StateTerminated, "下线")

	// TERMINATED 不能转换到任何状态
	err := sm.Transition(StateIdle, "")
	assert.Error(t, err)
	assert.Equal(t, StateTerminated, sm.State())
}

func TestTransition_DuplicateDeploy(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")

	// 已在 INITIALIZING，不能再次 INITIALIZING
	err := sm.Transition(StateInitializing, "重复部署")
	assert.Error(t, err)
	assert.Equal(t, StateInitializing, sm.State())
}

// ── 本地文件 Store 测试 ───────────────────────────────────────────────────────

func TestLocalStore_SaveAndLoad(t *testing.T) {
	store := newTestLocalStore(t)

	record := &DeployRecord{
		Project:   "myapp",
		Namespace: "myapp",
		State:     StateRunning,
		Version:   "v0.1.0",
		IsFirst:   false,
		Reason:    "部署成功",
		UpdatedAt: time.Now(),
	}

	err := store.Save(record)
	require.NoError(t, err)

	loaded, err := store.Load("myapp", "myapp")
	require.NoError(t, err)
	assert.Equal(t, StateRunning, loaded.State)
	assert.Equal(t, "v0.1.0", loaded.Version)
	assert.Equal(t, "部署成功", loaded.Reason)
}

func TestLocalStore_LoadNotFound(t *testing.T) {
	store := newTestLocalStore(t)

	_, err := store.Load("not-exist", "not-exist")
	assert.Error(t, err)
}

func TestLocalStore_Delete(t *testing.T) {
	store := newTestLocalStore(t)

	record := &DeployRecord{
		Project:   "myapp",
		Namespace: "myapp",
		State:     StateIdle,
		UpdatedAt: time.Now(),
	}
	store.Save(record)

	err := store.Delete("myapp", "myapp")
	require.NoError(t, err)

	_, err = store.Load("myapp", "myapp")
	assert.Error(t, err, "删除后应该找不到记录")
}

func TestLocalStore_DeleteNotFound(t *testing.T) {
	store := newTestLocalStore(t)

	// 删除不存在的记录不应该报错
	err := store.Delete("not-exist", "not-exist")
	assert.NoError(t, err)
}

// ── 状态机 + Store 集成 ───────────────────────────────────────────────────────

func TestMachine_PersistsAcrossRestart(t *testing.T) {
	store := newTestLocalStore(t)

	// 第一次：部署到 DEPLOYING
	sm1, err := New(store, "myapp", "myapp", "v0.1.0")
	require.NoError(t, err)
	sm1.Transition(StateInitializing, "开始")
	sm1.Transition(StateDeploying, "部署中")

	// 模拟重启：重新加载状态
	sm2, err := New(store, "myapp", "myapp", "v0.1.0")
	require.NoError(t, err)
	assert.Equal(t, StateDeploying, sm2.State(), "重启后应恢复到 DEPLOYING")

	// 继续完成部署
	sm2.Transition(StateValidating, "验证")
	sm2.Transition(StateRunning, "成功")
	assert.Equal(t, StateRunning, sm2.State())
}

func TestMachine_FirstDeployFlag(t *testing.T) {
	store := newTestLocalStore(t)

	sm, _ := New(store, "myapp", "myapp", "v0.1.0")
	assert.True(t, sm.IsFirstDeploy(), "新项目默认是首次部署")

	sm.MarkFirstDeploy(false)
	sm.Transition(StateInitializing, "")

	// 重新加载，首次部署标志应该持久化
	sm2, _ := New(store, "myapp", "myapp", "v0.1.0")
	assert.False(t, sm2.IsFirstDeploy())
}

func TestMachine_HistoryPersists(t *testing.T) {
	store := newTestLocalStore(t)

	sm, _ := New(store, "myapp", "myapp", "v0.1.0")
	sm.Transition(StateInitializing, "第一步")
	sm.Transition(StateDeploying, "第二步")

	// 重新加载
	sm2, _ := New(store, "myapp", "myapp", "v0.1.0")
	assert.Len(t, sm2.Record().History, 2, "历史记录应该持久化")
	assert.Equal(t, "第一步", sm2.Record().History[0].Reason)
}

// ── 工具函数 ──────────────────────────────────────────────────────────────────

// newTestMachine 创建一个使用临时目录的测试状态机
func newTestMachine(t *testing.T) *Machine {
	t.Helper()
	store := newTestLocalStore(t)
	sm, err := New(store, "test-project", "test-ns", "v0.1.0")
	require.NoError(t, err)
	return sm
}

// newTestLocalStore 创建一个使用临时目录的本地 Store
func newTestLocalStore(t *testing.T) Store {
	t.Helper()
	tmpDir := t.TempDir()

	// 覆盖 localPath 使用临时目录
	return &testLocalStore{baseDir: tmpDir}
}

// testLocalStore 测试用的本地 Store，使用临时目录
type testLocalStore struct {
	baseDir string
}

func (s *testLocalStore) Save(record *DeployRecord) error {
	path := s.path(record.Project, record.Namespace)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, _ := marshalRecord(record)
	return os.WriteFile(path, data, 0o644)
}

func (s *testLocalStore) Load(project, namespace string) (*DeployRecord, error) {
	path := s.path(project, namespace)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return unmarshalRecord(data)
}

func (s *testLocalStore) Delete(project, namespace string) error {
	path := s.path(project, namespace)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *testLocalStore) path(project, namespace string) string {
	return filepath.Join(s.baseDir, project, namespace+".json")
}
