package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── 合法转换完整覆盖 ──────────────────────────────────────────────────────────
// 按转换表逐条验证，确保每条合法路径都能走通

func TestValidTransitions_IdleToInitializing(t *testing.T) {
	sm := newTestMachine(t)
	require.NoError(t, sm.Transition(StateInitializing, "开始部署"))
	assert.Equal(t, StateInitializing, sm.State())
}

func TestValidTransitions_InitializingToDeploying(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")
	require.NoError(t, sm.Transition(StateDeploying, "执行 helm"))
	assert.Equal(t, StateDeploying, sm.State())
}

func TestValidTransitions_InitializingToCleaning(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")
	require.NoError(t, sm.Transition(StateCleaning, "初始化失败"))
	assert.Equal(t, StateCleaning, sm.State())
}

func TestValidTransitions_DeployingToValidating(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	require.NoError(t, sm.Transition(StateValidating, "部署完成，开始验证"))
	assert.Equal(t, StateValidating, sm.State())
}

func TestValidTransitions_DeployingToRollingBack(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	require.NoError(t, sm.Transition(StateRollingBack, "部署失败"))
	assert.Equal(t, StateRollingBack, sm.State())
}

func TestValidTransitions_DeployingToCleaning(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	require.NoError(t, sm.Transition(StateCleaning, "首次部署失败"))
	assert.Equal(t, StateCleaning, sm.State())
}

func TestValidTransitions_ValidatingToRunning(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateValidating, "")
	require.NoError(t, sm.Transition(StateRunning, "验证通过"))
	assert.Equal(t, StateRunning, sm.State())
}

func TestValidTransitions_ValidatingToRollingBack(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateValidating, "")
	require.NoError(t, sm.Transition(StateRollingBack, "验证超时"))
	assert.Equal(t, StateRollingBack, sm.State())
}

func TestValidTransitions_ValidatingToCleaning(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateValidating, "")
	require.NoError(t, sm.Transition(StateCleaning, "验证失败，清理"))
	assert.Equal(t, StateCleaning, sm.State())
}

func TestValidTransitions_RunningToInitializing(t *testing.T) {
	sm := runningMachine(t)
	require.NoError(t, sm.Transition(StateInitializing, "重新部署"))
	assert.Equal(t, StateInitializing, sm.State())
}

func TestValidTransitions_RunningToTerminated(t *testing.T) {
	sm := runningMachine(t)
	require.NoError(t, sm.Transition(StateTerminated, "下线"))
	assert.Equal(t, StateTerminated, sm.State())
}

func TestValidTransitions_RunningToCleaning(t *testing.T) {
	sm := runningMachine(t)
	require.NoError(t, sm.Transition(StateCleaning, "手动清理"))
	assert.Equal(t, StateCleaning, sm.State())
}

func TestValidTransitions_RollingBackToRunning(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateRollingBack, "")
	require.NoError(t, sm.Transition(StateRunning, "回滚成功"))
	assert.Equal(t, StateRunning, sm.State())
}

func TestValidTransitions_RollingBackToCleaning(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateRollingBack, "")
	require.NoError(t, sm.Transition(StateCleaning, "回滚失败"))
	assert.Equal(t, StateCleaning, sm.State())
}

func TestValidTransitions_CleaningToIdle(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")
	sm.Transition(StateCleaning, "")
	require.NoError(t, sm.Transition(StateIdle, "清理完成"))
	assert.Equal(t, StateIdle, sm.State())
}

func TestValidTransitions_CleaningToTerminated(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")
	sm.Transition(StateCleaning, "")
	require.NoError(t, sm.Transition(StateTerminated, "清理后下线"))
	assert.Equal(t, StateTerminated, sm.State())
}

// ── 非法转换完整覆盖 ──────────────────────────────────────────────────────────
// 从每个状态出发，尝试所有不在转换表里的目标状态

func TestInvalidTransitions_FromIdle(t *testing.T) {
	illegal := []State{
		StateDeploying, StateValidating, StateRunning,
		StateRollingBack, StateCleaning, StateTerminated,
	}
	for _, to := range illegal {
		sm := newTestMachine(t)
		err := sm.Transition(to, "非法")
		assert.Error(t, err, "IDLE → %s 应该失败", to)
		assert.Equal(t, StateIdle, sm.State(), "状态不应改变")
	}
}

func TestInvalidTransitions_FromInitializing(t *testing.T) {
	illegal := []State{
		StateIdle, StateValidating, StateRunning,
		StateRollingBack, StateTerminated,
	}
	for _, to := range illegal {
		sm := newTestMachine(t)
		sm.Transition(StateInitializing, "")
		err := sm.Transition(to, "非法")
		assert.Error(t, err, "INITIALIZING → %s 应该失败", to)
		assert.Equal(t, StateInitializing, sm.State())
	}
}

func TestInvalidTransitions_FromDeploying(t *testing.T) {
	illegal := []State{
		StateIdle, StateInitializing, StateRunning, StateTerminated,
	}
	for _, to := range illegal {
		sm := newTestMachine(t)
		sm.Transition(StateInitializing, "")
		sm.Transition(StateDeploying, "")
		err := sm.Transition(to, "非法")
		assert.Error(t, err, "DEPLOYING → %s 应该失败", to)
		assert.Equal(t, StateDeploying, sm.State())
	}
}

func TestInvalidTransitions_FromValidating(t *testing.T) {
	illegal := []State{
		StateIdle, StateInitializing, StateDeploying, StateTerminated,
	}
	for _, to := range illegal {
		sm := newTestMachine(t)
		sm.Transition(StateInitializing, "")
		sm.Transition(StateDeploying, "")
		sm.Transition(StateValidating, "")
		err := sm.Transition(to, "非法")
		assert.Error(t, err, "VALIDATING → %s 应该失败", to)
		assert.Equal(t, StateValidating, sm.State())
	}
}

func TestInvalidTransitions_FromRunning(t *testing.T) {
	illegal := []State{
		StateDeploying, StateValidating,
	}
	for _, to := range illegal {
		sm := runningMachine(t)
		err := sm.Transition(to, "非法")
		assert.Error(t, err, "RUNNING → %s 应该失败", to)
		assert.Equal(t, StateRunning, sm.State())
	}
}

func TestInvalidTransitions_FromRollingBack(t *testing.T) {
	illegal := []State{
		StateIdle, StateInitializing, StateDeploying,
		StateValidating, StateTerminated,
	}
	for _, to := range illegal {
		sm := newTestMachine(t)
		sm.Transition(StateInitializing, "")
		sm.Transition(StateDeploying, "")
		sm.Transition(StateRollingBack, "")
		err := sm.Transition(to, "非法")
		assert.Error(t, err, "ROLLING_BACK → %s 应该失败", to)
		assert.Equal(t, StateRollingBack, sm.State())
	}
}

func TestInvalidTransitions_FromCleaning(t *testing.T) {
	illegal := []State{
		StateInitializing, StateDeploying, StateValidating,
		StateRunning, StateRollingBack,
	}
	for _, to := range illegal {
		sm := newTestMachine(t)
		sm.Transition(StateInitializing, "")
		sm.Transition(StateCleaning, "")
		err := sm.Transition(to, "非法")
		assert.Error(t, err, "CLEANING → %s 应该失败", to)
		assert.Equal(t, StateCleaning, sm.State())
	}
}

func TestInvalidTransitions_FromTerminated(t *testing.T) {
	allStates := []State{
		StateIdle, StateInitializing, StateDeploying, StateValidating,
		StateRunning, StateRollingBack, StateCleaning, StateTerminated,
	}
	for _, to := range allStates {
		sm := runningMachine(t)
		sm.Transition(StateTerminated, "")
		err := sm.Transition(to, "非法")
		assert.Error(t, err, "TERMINATED → %s 应该失败", to)
		assert.Equal(t, StateTerminated, sm.State())
	}
}

// ── 非法转换副作用验证 ────────────────────────────────────────────────────────
// 确认非法转换不会污染 history、reason、updatedAt

func TestInvalidTransition_DoesNotPolluteSideEffects(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "合法原因")

	beforeReason := sm.Record().Reason
	beforeHistory := len(sm.Record().History)
	beforeUpdatedAt := sm.Record().UpdatedAt

	time.Sleep(time.Millisecond) // 确保时间戳会变
	err := sm.Transition(StateRunning, "非法跳跃")
	assert.Error(t, err)

	assert.Equal(t, beforeReason, sm.Record().Reason, "reason 不应改变")
	assert.Equal(t, beforeHistory, len(sm.Record().History), "history 不应增加")
	assert.Equal(t, beforeUpdatedAt, sm.Record().UpdatedAt, "updatedAt 不应改变")
}

// ── ResumeFromValidating 专项测试 ─────────────────────────────────────────────

func TestResumeFromValidating_Success(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateValidating, "")

	err := sm.ResumeFromValidating("验证通过")
	require.NoError(t, err)
	assert.Equal(t, StateRunning, sm.State())
}

func TestResumeFromValidating_RejectsNonValidatingStates(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*Machine)
		from  State
	}{
		{"from IDLE", func(sm *Machine) {}, StateIdle},
		{"from INITIALIZING", func(sm *Machine) {
			sm.Transition(StateInitializing, "")
		}, StateInitializing},
		{"from DEPLOYING", func(sm *Machine) {
			sm.Transition(StateInitializing, "")
			sm.Transition(StateDeploying, "")
		}, StateDeploying},
		{"from RUNNING", func(sm *Machine) {
			runningMachineFrom(sm)
		}, StateRunning},
		{"from ROLLING_BACK", func(sm *Machine) {
			sm.Transition(StateInitializing, "")
			sm.Transition(StateDeploying, "")
			sm.Transition(StateRollingBack, "")
		}, StateRollingBack},
		{"from CLEANING", func(sm *Machine) {
			sm.Transition(StateInitializing, "")
			sm.Transition(StateCleaning, "")
		}, StateCleaning},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sm := newTestMachine(t)
			tc.setup(sm)
			err := sm.ResumeFromValidating("尝试 resume")
			assert.Error(t, err, "非 VALIDATING 状态调用 ResumeFromValidating 应该失败")
			assert.Equal(t, tc.from, sm.State(), "状态不应改变")
			assert.Contains(t, err.Error(), "VALIDATING")
		})
	}
}

// ── 字段正确性验证 ────────────────────────────────────────────────────────────

func TestTransition_FieldsUpdatedCorrectly(t *testing.T) {
	sm := newTestMachine(t)

	before := time.Now().Add(-time.Millisecond)
	err := sm.Transition(StateInitializing, "开始部署 v1.0.0")
	require.NoError(t, err)

	record := sm.Record()
	assert.Equal(t, StateInitializing, record.State)
	assert.Equal(t, "开始部署 v1.0.0", record.Reason)
	assert.True(t, record.UpdatedAt.After(before), "UpdatedAt 应该被更新")
}

func TestTransition_HistoryFieldsComplete(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "测试原因")

	h := sm.Record().History
	require.Len(t, h, 1)

	assert.Equal(t, StateIdle, h[0].From)
	assert.Equal(t, StateInitializing, h[0].To)
	assert.Equal(t, "测试原因", h[0].Reason)
	assert.Equal(t, "v0.1.0", h[0].Version)
	assert.False(t, h[0].Timestamp.IsZero())
}

func TestNew_VersionOverridesExisting(t *testing.T) {
	store := newTestLocalStore(t)

	// 第一次用 v0.1.0 创建
	sm1, _ := New(store, "myapp", "myapp", "v0.1.0")
	sm1.Transition(StateInitializing, "")
	assert.Equal(t, "v0.1.0", sm1.Record().Version)

	// 第二次用新版本加载，版本应该被覆盖
	sm2, err := New(store, "myapp", "myapp", "v0.2.0")
	require.NoError(t, err)
	assert.Equal(t, "v0.2.0", sm2.Record().Version, "版本应该被新版本覆盖")
	assert.Equal(t, StateInitializing, sm2.State(), "状态应该从持久化恢复")
}

func TestNew_FreshMachineDefaults(t *testing.T) {
	store := newTestLocalStore(t)
	sm, err := New(store, "myapp", "myapp", "v0.1.0")
	require.NoError(t, err)

	assert.Equal(t, StateIdle, sm.State())
	assert.True(t, sm.IsFirstDeploy())
	assert.Empty(t, sm.Record().History)
	assert.Equal(t, "v0.1.0", sm.Record().Version)
	assert.Equal(t, "myapp", sm.Record().Project)
	assert.Equal(t, "myapp", sm.Record().Namespace)
}

// ── EtcdKey 格式验证 ──────────────────────────────────────────────────────────

func TestEtcdKey_Format(t *testing.T) {
	key := EtcdKey("myapp", "production")
	assert.Equal(t, "kubepivot/myapp/production/state", key)
}

func TestEtcdKey_DifferentProjectsProduceDifferentKeys(t *testing.T) {
	key1 := EtcdKey("app1", "ns1")
	key2 := EtcdKey("app2", "ns1")
	key3 := EtcdKey("app1", "ns2")
	assert.NotEqual(t, key1, key2)
	assert.NotEqual(t, key1, key3)
	assert.NotEqual(t, key2, key3)
}

func TestEtcdKey_ContainsAllParts(t *testing.T) {
	key := EtcdKey("web3-blitz", "web3-blitz")
	assert.True(t, strings.HasPrefix(key, "kubepivot/"))
	assert.True(t, strings.Contains(key, "web3-blitz"))
	assert.True(t, strings.HasSuffix(key, "/state"))
}

// ── 完整部署场景 ──────────────────────────────────────────────────────────────

func TestScenario_FirstDeploySuccess(t *testing.T) {
	sm := newTestMachine(t)
	sm.MarkFirstDeploy(true)

	states := []State{
		StateInitializing, StateDeploying, StateValidating, StateRunning,
	}
	for _, s := range states {
		require.NoError(t, sm.Transition(s, ""))
	}
	assert.Equal(t, StateRunning, sm.State())
	assert.Len(t, sm.Record().History, 4)
}

func TestScenario_FirstDeployFailureCleansUp(t *testing.T) {
	sm := newTestMachine(t)
	sm.MarkFirstDeploy(true)

	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateCleaning, "首次部署失败")
	sm.Transition(StateIdle, "清理完成")

	assert.Equal(t, StateIdle, sm.State())
	// 清理完成后应该可以重新部署
	err := sm.Transition(StateInitializing, "重试")
	assert.NoError(t, err)
}

func TestScenario_UpdateWithRollback(t *testing.T) {
	sm := runningMachine(t)

	// 新一轮部署失败回滚
	sm.Transition(StateInitializing, "更新到 v0.2.0")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateRollingBack, "部署失败")
	sm.Transition(StateRunning, "回滚成功")

	assert.Equal(t, StateRunning, sm.State())
}

func TestScenario_ValidatingFailsAndRollsBack(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateValidating, "")
	sm.Transition(StateRollingBack, "healthz 超时")
	sm.Transition(StateRunning, "回滚成功")

	assert.Equal(t, StateRunning, sm.State())

	history := sm.Record().History
	assert.Len(t, history, 5)
	assert.Equal(t, StateRollingBack, history[3].To)
	assert.Equal(t, "healthz 超时", history[3].Reason)
}

func TestScenario_GracefulShutdown(t *testing.T) {
	sm := runningMachine(t)
	require.NoError(t, sm.Transition(StateTerminated, "手动下线"))
	assert.Equal(t, StateTerminated, sm.State())

	// 下线后任何操作都应该失败
	allStates := []State{
		StateIdle, StateInitializing, StateDeploying, StateValidating,
		StateRunning, StateRollingBack, StateCleaning,
	}
	for _, s := range allStates {
		err := sm.Transition(s, "")
		assert.Error(t, err, "TERMINATED 后转换到 %s 应该失败", s)
	}
}

func TestScenario_MultipleDeployRounds(t *testing.T) {
	store := newTestLocalStore(t)
	sm, _ := New(store, "myapp", "myapp", "v0.1.0")

	// 第一轮
	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateValidating, "")
	sm.Transition(StateRunning, "v0.1.0 上线")

	// 第二轮（版本升级）
	sm2, _ := New(store, "myapp", "myapp", "v0.2.0")
	sm2.Transition(StateInitializing, "")
	sm2.Transition(StateDeploying, "")
	sm2.Transition(StateValidating, "")
	sm2.Transition(StateRunning, "v0.2.0 上线")

	assert.Equal(t, StateRunning, sm2.State())
	assert.Equal(t, "v0.2.0", sm2.Record().Version)
	// 两轮共 8 条历史
	assert.Len(t, sm2.Record().History, 8)
}

// ── 工具函数 ──────────────────────────────────────────────────────────────────

func newTestMachine(t *testing.T) *Machine {
	t.Helper()
	store := newTestLocalStore(t)
	sm, err := New(store, "test-project", "test-ns", "v0.1.0")
	require.NoError(t, err)
	return sm
}

// runningMachine 返回一个已经处于 RUNNING 状态的状态机
func runningMachine(t *testing.T) *Machine {
	t.Helper()
	sm := newTestMachine(t)
	runningMachineFrom(sm)
	return sm
}

// runningMachineFrom 把一个状态机推到 RUNNING 状态（供 setup 复用）
func runningMachineFrom(sm *Machine) {
	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateValidating, "")
	sm.Transition(StateRunning, "")
}

func newTestLocalStore(t *testing.T) Store {
	t.Helper()
	return &testLocalStore{baseDir: t.TempDir()}
}

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
	data, err := os.ReadFile(s.path(project, namespace))
	if err != nil {
		return nil, err
	}
	return unmarshalRecord(data)
}

func (s *testLocalStore) Delete(project, namespace string) error {
	err := os.Remove(s.path(project, namespace))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *testLocalStore) path(project, namespace string) string {
	return filepath.Join(s.baseDir, project, namespace+".json")
}

// ── ForceState 测试 ───────────────────────────────────────────────────────────

func TestForceState_FromAnyState(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*Machine)
		from  State
	}{
		{"from IDLE", func(m *Machine) {}, StateIdle},
		{"from DEPLOYING", func(m *Machine) {
			m.Transition(StateInitializing, "")
			m.Transition(StateDeploying, "")
		}, StateDeploying},
		{"from TERMINATED", func(m *Machine) {
			m.Transition(StateInitializing, "")
			m.Transition(StateDeploying, "")
			m.Transition(StateValidating, "")
			m.Transition(StateRunning, "")
			m.Transition(StateTerminated, "")
		}, StateTerminated},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sm := newTestMachine(t)
			tc.setup(sm)
			assert.Equal(t, tc.from, sm.State())

			err := sm.ForceState(StateRunning, "强制恢复")
			require.NoError(t, err)
			assert.Equal(t, StateRunning, sm.State())
			assert.Equal(t, "强制恢复", sm.Record().Reason)
		})
	}
}

func TestForceState_HistoryMarkedForce(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")

	err := sm.ForceState(StateRunning, "emergency recovery")
	require.NoError(t, err)

	history := sm.Record().History
	last := history[len(history)-1]
	assert.Contains(t, last.Reason, "[force]")
	assert.Equal(t, StateRunning, last.To)
}

func TestForceState_PersistsAcrossRestart(t *testing.T) {
	store := newTestLocalStore(t)
	sm, _ := New(store, "myapp", "myapp", "v0.1.0")
	sm.Transition(StateInitializing, "")
	sm.ForceState(StateRunning, "强制恢复")

	sm2, _ := New(store, "myapp", "myapp", "v0.1.0")
	assert.Equal(t, StateRunning, sm2.State())
}

// ── localStore 直接测试 ───────────────────────────────────────────────────────

func TestLocalStore_DirectSaveLoad(t *testing.T) {
	// 用真实 localStore，覆盖 store.go 里的实际实现
	// 通过 NewAutoStore 空 endpoints 触发
	store := NewAutoStore("")
	record := &DeployRecord{
		Project:   "test",
		Namespace: "test",
		State:     StateRunning,
		Version:   "v1.0.0",
		Reason:    "测试",
	}

	err := store.Save(record)
	require.NoError(t, err)

	loaded, err := store.Load("test", "test")
	require.NoError(t, err)
	assert.Equal(t, StateRunning, loaded.State)
	assert.Equal(t, "v1.0.0", loaded.Version)

	// 清理
	store.Delete("test", "test")
}

func TestLocalStore_LoadNotExist(t *testing.T) {
	store := NewAutoStore("")
	_, err := store.Load("nonexistent", "nonexistent")
	assert.Error(t, err)
}

func TestLocalStore_DeleteNotExist(t *testing.T) {
	store := NewAutoStore("")
	err := store.Delete("nonexistent", "nonexistent")
	assert.NoError(t, err)
}

func TestNewAutoStore_EmptyEndpoints_ReturnsLocalStore(t *testing.T) {
	store := NewAutoStore("")
	assert.NotNil(t, store)
	// 验证是本地文件 store：能正常 Save/Load
	record := &DeployRecord{
		Project: "autostore-test", Namespace: "ns",
		State: StateIdle,
	}
	require.NoError(t, store.Save(record))
	loaded, err := store.Load("autostore-test", "ns")
	require.NoError(t, err)
	assert.Equal(t, StateIdle, loaded.State)
	store.Delete("autostore-test", "ns")
}

// ── localPath / etcdKey 测试 ──────────────────────────────────────────────────

func TestLocalPath_Format(t *testing.T) {
	path, err := localPath("myapp", "production")
	require.NoError(t, err)
	assert.Contains(t, path, ".kp")
	assert.Contains(t, path, "myapp")
	assert.Contains(t, path, "production.json")
}

func TestLocalPath_DifferentProjects(t *testing.T) {
	p1, _ := localPath("app1", "ns1")
	p2, _ := localPath("app2", "ns1")
	p3, _ := localPath("app1", "ns2")
	assert.NotEqual(t, p1, p2)
	assert.NotEqual(t, p1, p3)
}

func TestEtcdKeyFunc_Format(t *testing.T) {
	key := etcdKey("myapp", "production")
	assert.Equal(t, "kubepivot/myapp/production/state", key)
}

// ── marshalRecord / unmarshalRecord ───────────────────────────────────────────

func TestMarshalUnmarshal_RoundTrip(t *testing.T) {
	original := &DeployRecord{
		Project:   "myapp",
		Namespace: "production",
		State:     StateRunning,
		Version:   "v1.2.3",
		IsFirst:   false,
		Reason:    "部署成功",
	}

	data, err := marshalRecord(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	loaded, err := unmarshalRecord(data)
	require.NoError(t, err)
	assert.Equal(t, original.Project, loaded.Project)
	assert.Equal(t, original.State, loaded.State)
	assert.Equal(t, original.Version, loaded.Version)
	assert.Equal(t, original.Reason, loaded.Reason)
}

func TestUnmarshalRecord_InvalidJSON(t *testing.T) {
	_, err := unmarshalRecord([]byte("not json"))
	assert.Error(t, err)
}

// ── autoMigrateToEtcd 测试 ────────────────────────────────────────────────────

func TestAutoMigrateToEtcd_LocalStoreNoMigration(t *testing.T) {
	// store 是 localStore，不应该触发迁移
	store := newTestLocalStore(t)
	// 不应该报错，直接跳过（localStore 不触发迁移）
	err := autoMigrateToEtcd(store, "myapp", "ns")
	assert.NoError(t, err)
}

// ── v1.8.0 Sandbox 状态机测试 ─────────────────────────────────────────────────

func TestSandboxTransitions_HappyPath(t *testing.T) {
	store := newTestLocalStore(t)
	sm, err := New(store, "myapp", "test-ns", "v1.0.0")
	require.NoError(t, err)

	// IDLE → LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING
	transitions := []State{
		StateLocked, StateSnapshotting, StateSimulating,
		StateCommitting, StateRunning,
	}
	for _, to := range transitions {
		err := sm.Transition(to, "sandbox test")
		assert.NoError(t, err, "应允许转换到 %s", to)
	}
	assert.Equal(t, StateRunning, sm.State())
}

func TestSandboxTransitions_FailPath(t *testing.T) {
	store := newTestLocalStore(t)
	sm, _ := New(store, "myapp", "test-ns", "v1.0.0")

	// IDLE → LOCKED → SNAPSHOTTING → SIMULATING → RESTORING → IDLE
	sm.Transition(StateLocked, "start")
	sm.Transition(StateSnapshotting, "snapshot")
	sm.Transition(StateSimulating, "simulate")

	err := sm.Transition(StateRestoring, "sim failed")
	assert.NoError(t, err)
	err = sm.Transition(StateIdle, "restored")
	assert.NoError(t, err)
	assert.Equal(t, StateIdle, sm.State())
}

func TestSandboxTransitions_CommittingForbidsIdle(t *testing.T) {
	store := newTestLocalStore(t)
	sm, _ := New(store, "myapp", "test-ns", "v1.0.0")

	sm.Transition(StateLocked, "start")
	sm.Transition(StateSnapshotting, "snap")
	sm.Transition(StateSimulating, "sim")
	sm.Transition(StateCommitting, "commit")

	// COMMITTING 只允许 RUNNING 或 RESTORING，禁止 IDLE（force-unlock 拦截点）
	err := sm.Transition(StateIdle, "force-unlock attempt")
	assert.Error(t, err, "COMMITTING 不应允许直接转换到 IDLE")
	assert.Contains(t, err.Error(), "非法状态转换")
}

func TestSandboxTransitions_LockedAllowsForceUnlock(t *testing.T) {
	store := newTestLocalStore(t)
	sm, _ := New(store, "myapp", "test-ns", "v1.0.0")

	sm.Transition(StateLocked, "start")
	// LOCKED 允许回 IDLE（force-unlock 合法）
	err := sm.Transition(StateIdle, "force-unlock")
	assert.NoError(t, err)
}

func TestSandboxTransitions_RunningCanEnterLocked(t *testing.T) {
	store := newTestLocalStore(t)
	sm, _ := New(store, "myapp", "test-ns", "v1.0.0")

	// 模拟到 RUNNING
	sm.Transition(StateInitializing, "init")
	sm.Transition(StateDeploying, "deploy")
	sm.Transition(StateValidating, "validate")
	sm.Transition(StateRunning, "running")

	// RUNNING → LOCKED（正常入沙盒）
	err := sm.Transition(StateLocked, "sandbox start")
	assert.NoError(t, err)
}

// ── RunningSinceFromHistory 测试（v2.6.1 verifiedTrafficWriter 前置依赖）────

func TestRunningSinceFromHistory_FirstDeploy(t *testing.T) {
	sm := newTestMachine(t)
	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateValidating, "")

	beforeRunning := time.Now()
	sm.Transition(StateRunning, "首次部署 v1.0")
	afterRunning := time.Now()

	got := RunningSinceFromHistory(sm.Record())
	require.NotNil(t, got, "RUNNING 状态应该有 since 时间戳")
	assert.True(t, !got.Before(beforeRunning), "since 应该 >= 进入 RUNNING 之前的时间")
	assert.True(t, !got.After(afterRunning), "since 应该 <= 进入 RUNNING 之后的时间")
}

func TestRunningSinceFromHistory_AfterRedeploy(t *testing.T) {
	sm := runningMachine(t)
	firstSince := *RunningSinceFromHistory(sm.Record())

	time.Sleep(time.Millisecond) // 确保时间戳能区分

	// 重新部署：RUNNING → INITIALIZING → ... → RUNNING
	sm.Transition(StateInitializing, "重新部署 v2.0")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateValidating, "")
	sm.Transition(StateRunning, "v2.0 上线")

	got := RunningSinceFromHistory(sm.Record())
	require.NotNil(t, got)
	assert.True(t, got.After(firstSince),
		"重新部署后 since 应该是新的 RUNNING 时间，不是首次部署时间")
}

func TestRunningSinceFromHistory_AfterRollback(t *testing.T) {
	sm := runningMachine(t)
	firstSince := *RunningSinceFromHistory(sm.Record())

	time.Sleep(time.Millisecond)

	// 重新部署 → 失败 → 回滚回 RUNNING
	sm.Transition(StateInitializing, "更新")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateRollingBack, "更新失败")
	sm.Transition(StateRunning, "回滚成功")

	got := RunningSinceFromHistory(sm.Record())
	require.NotNil(t, got)
	assert.True(t, got.After(firstSince),
		"回滚后 RUNNING 也算最新 since（writer 写实际状态，逻辑自洽）")
}

func TestRunningSinceFromHistory_NotInRunning(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*Machine)
	}{
		{"IDLE", func(sm *Machine) {}},
		{"INITIALIZING", func(sm *Machine) {
			sm.Transition(StateInitializing, "")
		}},
		{"DEPLOYING", func(sm *Machine) {
			sm.Transition(StateInitializing, "")
			sm.Transition(StateDeploying, "")
		}},
		{"ROLLING_BACK", func(sm *Machine) {
			sm.Transition(StateInitializing, "")
			sm.Transition(StateDeploying, "")
			sm.Transition(StateRollingBack, "")
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sm := newTestMachine(t)
			tc.setup(sm)

			got := RunningSinceFromHistory(sm.Record())
			assert.Nil(t, got, "非 RUNNING 状态应返回 nil")
		})
	}
}

func TestRunningSinceFromHistory_NilRecord(t *testing.T) {
	got := RunningSinceFromHistory(nil)
	assert.Nil(t, got, "nil record 应返回 nil（防御性）")
}

func TestRunningSinceFromHistory_RunningWithoutHistoryEntry(t *testing.T) {
	// 边界场景：State == RUNNING 但 History 没有 →RUNNING 转换
	// 实际 Transition() 不会产生这种状态，但 ForceState 可能直接强制到 RUNNING
	// 此时 [force] 标记的 history 记录的 To 仍然是 StateRunning，所以会被找到
	// 这里测试 History 完全为空的极端防御场景
	record := &DeployRecord{
		State:   StateRunning,
		History: []Transition{},
	}
	got := RunningSinceFromHistory(record)
	assert.Nil(t, got, "RUNNING 状态但 History 为空应返回 nil（防御性）")
}

func TestRunningSinceFromHistory_MultipleRunningTakesLatest(t *testing.T) {
	sm := runningMachine(t)
	first := *RunningSinceFromHistory(sm.Record())

	time.Sleep(time.Millisecond)

	// 第二次 RUNNING（重部署）
	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateValidating, "")
	sm.Transition(StateRunning, "")
	second := *RunningSinceFromHistory(sm.Record())

	time.Sleep(time.Millisecond)

	// 第三次 RUNNING（再次重部署）
	sm.Transition(StateInitializing, "")
	sm.Transition(StateDeploying, "")
	sm.Transition(StateValidating, "")
	sm.Transition(StateRunning, "")
	third := *RunningSinceFromHistory(sm.Record())

	// 三次时间戳应严格递增
	assert.True(t, second.After(first), "第二次 RUNNING since 应晚于第一次")
	assert.True(t, third.After(second), "第三次 RUNNING since 应晚于第二次")

	// History 中应有 3 个 →RUNNING 记录，但 RunningSinceFromHistory 只返回最近一个
	count := 0
	for _, h := range sm.Record().History {
		if h.To == StateRunning {
			count++
		}
	}
	assert.Equal(t, 3, count, "History 中应记录所有 3 次 →RUNNING")
}
