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
		StateDeploying, StateValidating, StateRollingBack,
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
	assert.Equal(t, "dtk/myapp/production/state", key)
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
	assert.True(t, strings.HasPrefix(key, "dtk/"))
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
