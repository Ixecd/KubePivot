package state

import (
	"fmt"
	"time"
)

// State 部署状态
type State string

const (
	StateIdle         State = "IDLE"
	StateInitializing State = "INITIALIZING"
	StateDeploying    State = "DEPLOYING"
	StateValidating   State = "VALIDATING"
	StateRunning      State = "RUNNING"
	StateRollingBack  State = "ROLLING_BACK"
	StateCleaning     State = "CLEANING"
	StateTerminated   State = "TERMINATED"
)

// validTransitions 合法的状态转换表
var validTransitions = map[State][]State{
	StateIdle:         {StateInitializing},
	StateInitializing: {StateDeploying, StateCleaning},
	StateDeploying:    {StateValidating, StateRollingBack, StateCleaning},
	StateValidating:   {StateRunning, StateRollingBack, StateCleaning},
	StateRunning:      {StateInitializing, StateTerminated, StateCleaning},
	StateRollingBack:  {StateRunning, StateCleaning},
	StateCleaning:     {StateIdle, StateTerminated},
	StateTerminated:   {},
}

// DeployRecord 持久化的部署记录
type DeployRecord struct {
	Project   string       `json:"project"`
	Namespace string       `json:"namespace"`
	State     State        `json:"state"`
	Version   string       `json:"version"`
	IsFirst   bool         `json:"is_first"`
	Reason    string       `json:"reason"`
	UpdatedAt time.Time    `json:"updated_at"`
	History   []Transition `json:"history"`
}

// Transition 状态转换日志
type Transition struct {
	From      State     `json:"from"`
	To        State     `json:"to"`
	Reason    string    `json:"reason"`
	Version   string    `json:"version"`
	Timestamp time.Time `json:"timestamp"`
}

// Machine 状态机
type Machine struct {
	record *DeployRecord
	store  Store
}

// New 创建状态机，从持久化存储加载已有状态
func New(store Store, project, namespace, version string) (*Machine, error) {
	record, err := store.Load(project, namespace)
	if err != nil {
		record = &DeployRecord{
			Project:   project,
			Namespace: namespace,
			State:     StateIdle,
			Version:   version,
			IsFirst:   true,
			UpdatedAt: time.Now(),
		}
	} else {
		record.Version = version
	}
	return &Machine{record: record, store: store}, nil
}

// State 返回当前状态
func (m *Machine) State() State {
	return m.record.State
}

// Record 返回完整记录（只读）
func (m *Machine) Record() *DeployRecord {
	return m.record
}

// Transition 执行状态转换
func (m *Machine) Transition(to State, reason string) error {
	if err := m.canTransition(to); err != nil {
		return fmt.Errorf("状态转换失败: %w", err)
	}

	from := m.record.State
	m.record.History = append(m.record.History, Transition{
		From:      from,
		To:        to,
		Reason:    reason,
		Version:   m.record.Version,
		Timestamp: time.Now(),
	})
	m.record.State = to
	m.record.Reason = reason
	m.record.UpdatedAt = time.Now()

	return m.store.Save(m.record)
}

// canTransition 检查转换是否合法
func (m *Machine) canTransition(to State) error {
	allowed, ok := validTransitions[m.record.State]
	if !ok {
		return fmt.Errorf("未知当前状态: %s", m.record.State)
	}
	for _, s := range allowed {
		if s == to {
			return nil
		}
	}
	return fmt.Errorf("非法状态转换: %s → %s（允许: %v）", m.record.State, to, allowed)
}

// MarkFirstDeploy 标记是否首次部署
func (m *Machine) MarkFirstDeploy(isFirst bool) {
	m.record.IsFirst = isFirst
}

// IsFirstDeploy 是否首次部署
func (m *Machine) IsFirstDeploy() bool {
	return m.record.IsFirst
}

// ResumeFromValidating 从 VALIDATING 状态恢复，带合法性检查
func (m *Machine) ResumeFromValidating(reason string) error {
	if m.record.State != StateValidating {
		return fmt.Errorf("只能从 VALIDATING 状态 resume，当前状态: %s", m.record.State)
	}
	return m.Transition(StateRunning, reason)
}

// EtcdKey 返回 etcd 存储 key，供 controller 使用
func EtcdKey(project, namespace string) string {
	return fmt.Sprintf("dtk/%s/%s/state", project, namespace)
}
