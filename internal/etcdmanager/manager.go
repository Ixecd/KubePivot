package etcdmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
)

// EtcdManager 运行时 etcd 集群管理。
type EtcdManager struct {
	PodIndex    int // 当前 Pod 在 StatefulSet 中的序号 (0-based)
	TotalPods   int // controller 副本数
	Namespace   string
	CertDir     string // TLS 证书目录
	CompactHour int    // compact 间隔（默认 1h）
	DefragHour  int    // defrag 间隔（默认 24h）
}

// DefaultEtcdManager 返回默认配置。
func DefaultEtcdManager(podIndex, totalPods int, namespace string) *EtcdManager {
	return &EtcdManager{
		PodIndex:    podIndex,
		TotalPods:   totalPods,
		Namespace:   namespace,
		CertDir:     "/data/etcd/certs",
		CompactHour: 1,
		DefragHour:  24,
	}
}

// Run 启动运行时管理循环（compact + defrag）。
func (m *EtcdManager) Run(ctx context.Context) {
	go m.compactLoop(ctx)
	go m.defragLoop(ctx)
}

func (m *EtcdManager) compactLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(m.CompactHour) * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rev := m.currentRevision(ctx)
			if rev > 0 {
				m.execEtcdctl(ctx, "compact", fmt.Sprintf("%d", rev))
			}
		}
	}
}

func (m *EtcdManager) defragLoop(ctx context.Context) {
	// 按 Pod 序号分布 Defrag 时间窗，避免全集群同时进入阻塞状态。
	// Pod-0: 02:00, Pod-1: 04:00, Pod-2: 06:00 ...
	hour := (2 + m.PodIndex*(24/__max(m.TotalPods, 1))) % 24
	slog.Info("etcdmanager: defrag scheduled", "pod_index", m.PodIndex, "hour_utc", hour)

	for {
		now := time.Now().UTC()
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
		if next.Before(now) {
			next = next.Add(24 * time.Hour)
		}

		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			m.safeDefrag(ctx)
		}
	}
}

// safeDefrag 安全执行 Defrag。
//
// 策略：
//  1. Leader 不做 Defrag（Defrag 阻塞性 Stop-the-world，Leader 做会触发重新选主）
//  2. Follower 做完 Defrag 后，将 Leader 转移给自己，
//     让原 Leader 变成 Follower 后在下一轮做 Defrag
//  3. 轮询保证全集群所有节点最终都被 Defrag
func (m *EtcdManager) safeDefrag(ctx context.Context) {
	// 0. 集群健康度预检：只剩 2/3 节点时不 defrag
	if !m.clusterHealthy(ctx) {
		slog.Warn("etcdmanager: skipping defrag, cluster not healthy", "pod_index", m.PodIndex)
		return
	}

	isLeader := m.isLeader(ctx)
	if isLeader {
		slog.Info("etcdmanager: skipping defrag on leader", "pod_index", m.PodIndex)
		return
	}

	slog.Info("etcdmanager: starting defrag", "pod_index", m.PodIndex)
	if err := m.execEtcdctl(ctx, "defrag"); err != nil {
		slog.Error("etcdmanager: defrag failed", "pod_index", m.PodIndex, "err", err)
		return
	}
	slog.Info("etcdmanager: defrag complete, transferring leadership", "pod_index", m.PodIndex)

	// Defrag 完成后，把 Leader 转移给自己
	// 原 Leader 在下一轮变成 Follower，可以做 Defrag
	// 这保证全集群最终所有节点都被 Defrag
	if err := m.execEtcdctl(ctx, "move-leader", fmt.Sprintf("%d", m.PodIndex)); err != nil {
		slog.Warn("etcdmanager: move-leader failed (non-critical)", "pod_index", m.PodIndex, "err", err)
	}
}

func (m *EtcdManager) clusterHealthy(ctx context.Context) bool {
	exec := executor.GetExecutor()
	out, err := exec.Kubectl(ctx, "",
		"exec", "-n", m.Namespace, m.podName(),
		"--", "etcdctl", "endpoint", "health",
		"--endpoints", "https://localhost:2379",
		"--cacert", m.CertDir+"/ca.pem",
		"--cert", m.CertDir+"/server.pem",
		"--key", m.CertDir+"/server-key.pem",
		"-w", "json",
	)
	if err != nil {
		return false
	}
	// healthy endpoints == total pods → 集群健康
	var healthList []struct {
		Health bool `json:"health"`
	}
	if json.Unmarshal(out, &healthList) != nil {
		return false
	}
	healthy := 0
	for _, h := range healthList {
		if h.Health {
			healthy++
		}
	}
	return healthy >= m.TotalPods // 全部节点健康才做 defrag
}

// GracefulShutdown 优雅退出。
//
// 缩容时主动 remove member，滚动更新时不操作。
func (m *EtcdManager) GracefulShutdown(ctx context.Context, isScaleDown bool) error {
	if !isScaleDown {
		return nil
	}
	// 从 etcd member list 中找到自己的 member ID 并 remove
	memberID, err := m.getMemberID(ctx)
	if err != nil {
		return fmt.Errorf("get member ID: %w", err)
	}
	if memberID == "" {
		return nil
	}
	return m.execEtcdctl(ctx, "member", "remove", memberID)
}

// ── 内部 helper ──────────────────────────────────────────────────────────────

func (m *EtcdManager) isLeader(ctx context.Context) bool {
	exec := executor.GetExecutor()
	out, err := exec.Kubectl(ctx, "",
		"exec", "-n", m.Namespace, m.podName(),
		"--", "etcdctl", "endpoint", "status",
		"--endpoints", "https://localhost:2379",
		"--cacert", m.CertDir+"/ca.pem",
		"--cert", m.CertDir+"/server.pem",
		"--key", m.CertDir+"/server-key.pem",
		"-w", "json",
	)
	if err != nil {
		return false
	}

	type endpointStatus struct {
		Header struct {
			MemberId uint64 `json:"member_id"`
		} `json:"header"`
		Leader uint64 `json:"leader"`
	}
	var statusList []endpointStatus
	if json.Unmarshal(out, &statusList) != nil || len(statusList) == 0 {
		return false
	}
	// 自己就是 Leader：Leader ID == 自己的 MemberId
	return statusList[0].Leader == statusList[0].Header.MemberId
}

func (m *EtcdManager) currentRevision(ctx context.Context) int64 {
	exec := executor.GetExecutor()
	out, err := exec.Kubectl(ctx, "",
		"exec", "-n", m.Namespace, m.podName(),
		"--", "etcdctl", "get", "/", "--prefix",
		"--endpoints", "https://localhost:2379",
		"--cacert", m.CertDir+"/ca.pem",
		"--cert", m.CertDir+"/server.pem",
		"--key", m.CertDir+"/server-key.pem",
		"-w", "json",
	)
	if err != nil {
		return 0
	}

	var resp struct {
		Header struct {
			Revision int64 `json:"revision"`
		} `json:"header"`
	}
	if json.Unmarshal(out, &resp) != nil {
		return 0
	}
	return resp.Header.Revision
}

func (m *EtcdManager) getMemberID(ctx context.Context) (string, error) {
	exec := executor.GetExecutor()
	out, err := exec.Kubectl(ctx, "",
		"exec", "-n", m.Namespace, m.podName(),
		"--", "etcdctl", "member", "list",
		"--endpoints", "https://localhost:2379",
		"--cacert", m.CertDir+"/ca.pem",
		"--cert", m.CertDir+"/server.pem",
		"--key", m.CertDir+"/server-key.pem",
		"-w", "json",
	)
	if err != nil {
		return "", err
	}

	var members struct {
		Members []struct {
			ID   string `json:"ID"`
			Name string `json:"name"`
		} `json:"members"`
	}
	if json.Unmarshal(out, &members) != nil {
		return "", fmt.Errorf("parse member list")
	}
	for _, mem := range members.Members {
		if mem.Name == m.podName() {
			return mem.ID, nil
		}
	}
	return "", nil
}

func (m *EtcdManager) execEtcdctl(ctx context.Context, args ...string) error {
	exec := executor.GetExecutor()
	kubectlArgs := []string{
		"exec", "-n", m.Namespace, m.podName(),
		"--", "etcdctl",
		"--endpoints", "https://localhost:2379",
		"--cacert", m.CertDir + "/ca.pem",
		"--cert", m.CertDir + "/server.pem",
		"--key", m.CertDir + "/server-key.pem",
	}
	kubectlArgs = append(kubectlArgs, args...)
	_, err := exec.Kubectl(ctx, "", kubectlArgs...)
	return err
}

func (m *EtcdManager) podName() string {
	return fmt.Sprintf("kubepivot-controller-%d", m.PodIndex)
}

func __max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
