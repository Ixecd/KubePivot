package etcdmanager

import (
	"os"
	"time"
)

// Peer 代表集群中的一个 etcd 节点。
type Peer struct {
	Name  string // StatefulSet pod name (e.g. controller-0)
	IP    string // Pod IP
	Phase string // Running | Pending | ...
}

// BootstrapConfig 自举阶段配置。
type BootstrapConfig struct {
	PodName     string        // 当前 Pod 名 (POD_NAME env)
	PodIndex    int           // Pod 在 StatefulSet 中的序号 (0-based)，由 PodName 提取
	Namespace   string        // controller 所在 namespace
	PeerPort    int           // etcd peer port, default 2380
	ClientPort  int           // etcd client port, default 2379
	DataDir     string        // etcd 数据目录，默认 /data/etcd
	CertDir     string        // TLS 证书目录，默认 /data/etcd/certs
	Label       string        // Peer discovery label, default "app=kubepivot-controller"
	PeerTimeout time.Duration // 连接 peer 的超时时间，默认 5s
	MaxJoinWait time.Duration // 等待其他节点先起来的最大时间，默认 300s

	// Learner 晋升阈值
	MaxLagForPromotion int           // Raft Index 落后上限，默认 500
	StableWindow       time.Duration // 滞后稳定窗口，默认 10s

	ForceRotate bool
}

// DefaultBootstrapConfig 返回默认配置。
func DefaultBootstrapConfig() BootstrapConfig {
	return BootstrapConfig{
		PodName:            os.Getenv("POD_NAME"),
		PodIndex:           PodIndexFromName(os.Getenv("POD_NAME")),
		PeerPort:           2380,
		ClientPort:         2379,
		DataDir:            "/data/etcd",
		CertDir:            "/data/etcd/certs",
		Label:              "app=kubepivot-controller",
		PeerTimeout:        5 * time.Second,
		MaxJoinWait:        300 * time.Second,
		MaxLagForPromotion: 500,
		StableWindow:       10 * time.Second,
	}
}

// ClusterState 自举阶段发现的集群状态。
type ClusterState int

const (
	// StateUnknown 初始状态。
	StateUnknown ClusterState = iota
	// StateExisting 已有集群运行中，当前 Pod 应以 Learner 加入。
	StateExisting
	// StateColdStart 全集群冷启动，当前 Pod 应创建新集群。
	StateColdStart
	// StateWaiting 其他节点有数据但不可达，等待。
	StateWaiting
)

func (s ClusterState) String() string {
	switch s {
	case StateExisting:
		return "existing"
	case StateColdStart:
		return "cold-start"
	case StateWaiting:
		return "waiting"
	default:
		return "unknown"
	}
}
