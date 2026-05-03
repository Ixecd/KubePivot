package etcdmanager

import "fmt"

// ErrClusterNotFound 已有集群不可达。
type ErrClusterNotFound struct {
	Peers []Peer
}

func (e *ErrClusterNotFound) Error() string {
	return fmt.Sprintf("no reachable etcd cluster found among %d peers", len(e.Peers))
}

// ErrCatchUpTimeout Learner 追平数据超时。
type ErrCatchUpTimeout struct {
	PodName string
	MaxWait string
}

func (e *ErrCatchUpTimeout) Error() string {
	return fmt.Sprintf("learner %s failed to catch up within %s", e.PodName, e.MaxWait)
}
