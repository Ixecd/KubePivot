package state

import (
	"context"
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
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

// validTransitions 合法的状态转换表（严格定义）
var validTransitions = map[State][]State{
	StateIdle:         {StateInitializing},
	StateInitializing: {StateDeploying, StateCleaning},
	StateDeploying:    {StateValidating, StateRollingBack, StateCleaning},
	StateValidating:   {StateRunning, StateRollingBack, StateCleaning}, // 允许从 Validating 直接回滚或清理
	StateRunning:      {StateInitializing, StateTerminated, StateCleaning},
	StateRollingBack:  {StateRunning, StateCleaning},
	StateCleaning:     {StateIdle, StateTerminated}, // Cleaning 只能回到 Idle 或 Terminated
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
		// 首次部署，初始化 IDLE 记录
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

// Record 返回完整记录
func (m *Machine) Record() *DeployRecord {
	return m.record
}

// Transition 执行状态转换（带严格合法性检查）
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

// canTransition 严格检查转换是否合法
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
	return fmt.Errorf("非法状态转换: %s → %s (允许的状态: %v)", m.record.State, to, allowed)
}

// MarkFirstDeploy 标记是否为首次部署
func (m *Machine) MarkFirstDeploy(isFirst bool) {
	m.record.IsFirst = isFirst
}

// IsFirstDeploy 是否首次部署
func (m *Machine) IsFirstDeploy() bool {
	return m.record.IsFirst
}

// NewForDetect 创建一个仅用于 DetectActualState 的临时 Machine
// 这是唯一允许外部包构造 Machine 的地方
func NewForDetect(record *DeployRecord) *Machine {
	return &Machine{record: record}
}

// EtcdKey 返回 etcd 中存储状态的 key（已导出，controller 可直接使用）
func EtcdKey(project, namespace string) string {
	return fmt.Sprintf("dtk/%s/%s/state", project, namespace)
}

// DetectResourceExists 检查指定 K8s 资源是否存在（支持 Deployment / StatefulSet 等）
func (m *Machine) DetectResourceExists(kind, name string) (bool, error) {
	config, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBE_CONFIG"))
	if err != nil {
		return false, fmt.Errorf("加载 kubeconfig 失败: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return false, fmt.Errorf("创建 k8s client 失败: %w", err)
	}

	switch kind {
	case "Deployment":
		_, err = clientset.AppsV1().Deployments(m.record.Namespace).Get(
			context.Background(), name, metav1.GetOptions{})
	case "StatefulSet":
		_, err = clientset.AppsV1().StatefulSets(m.record.Namespace).Get(
			context.Background(), name, metav1.GetOptions{})
	default:
		return false, fmt.Errorf("不支持的资源类型: %s", kind)
	}

	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil // 资源不存在
		}
		return false, err
	}
	return true, nil
}
