package etcdmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
)

// tlsPaths 返回当前节点的 TLS 证书路径。
func (cfg BootstrapConfig) tlsPaths() (caFile, certFile, keyFile, peerCertFile, peerKeyFile string) {
	caFile = cfg.CertDir + "/ca.pem"
	certFile = cfg.CertDir + "/server.pem"
	keyFile = cfg.CertDir + "/server-key.pem"
	peerCertFile = cfg.CertDir + "/peer.pem"
	peerKeyFile = cfg.CertDir + "/peer-key.pem"
	return
}

// clientURL 返回当前节点的 client HTTPS URL。
func (cfg BootstrapConfig) clientURL(ip string) string {
	return fmt.Sprintf("https://%s:%d", ip, cfg.ClientPort)
}

// peerURL 返回当前节点的 peer HTTPS URL。
func (cfg BootstrapConfig) peerURL(ip string) string {
	return fmt.Sprintf("https://%s:%d", ip, cfg.PeerPort)
}

// etcdctlArgs 构建带 TLS 的 etcdctl 通用参数前缀。
func (cfg BootstrapConfig) etcdctlArgs(endpoint string) []string {
	ca, cert, key, _, _ := cfg.tlsPaths()
	return []string{
		"etcdctl",
		"--endpoints", endpoint,
		"--cacert", ca,
		"--cert", cert,
		"--key", key,
	}
}

// DiscoverPeers 通过 K8s API label 查询获取 Peer 列表。
func DiscoverPeers(ctx context.Context, namespace, label string) ([]Peer, error) {
	exec := executor.GetExecutor()
	out, err := exec.Kubectl(ctx, "",
		"get", "pods", "-n", namespace,
		"-l", label,
		"-o", "jsonpath={range .items[*]}{.metadata.name},{.status.podIP},{.status.phase} {end}",
	)
	if err != nil {
		return nil, fmt.Errorf("discover peers: %w", err)
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}

	var peers []Peer
	for _, token := range strings.Split(raw, " ") {
		parts := strings.Split(token, ",")
		if len(parts) != 3 {
			continue
		}
		peers = append(peers, Peer{
			Name:  parts[0],
			IP:    parts[1],
			Phase: parts[2],
		})
	}
	return peers, nil
}

// DetectClusterState 决策当前 Pod 应以何种方式启动 etcd。
func DetectClusterState(ctx context.Context, cfg BootstrapConfig, peers []Peer) (ClusterState, error) {
	if leader := findReachablePeer(ctx, peers, cfg); leader != nil {
		return StateExisting, nil
	}

	if cfg.PodIndex != 0 {
		return StateWaiting, nil
	}

	if localHasData(cfg.DataDir) {
		return StateWaiting, nil
	}

	for _, p := range peers {
		if p.Name == cfg.PodName {
			continue
		}
		hasData, err := peerHasData(ctx, cfg, p)
		if err != nil {
			// 不可达 → 等待，不误判
			return StateWaiting, nil
		}
		if hasData {
			return StateWaiting, nil
		}
	}

	return StateColdStart, nil
}

// findReachablePeer 遍历 peers，尝试连接 etcd client port。
func findReachablePeer(ctx context.Context, peers []Peer, cfg BootstrapConfig) *Peer {
	exec := executor.GetExecutor()
	for i := range peers {
		endpoint := cfg.clientURL(peers[i].IP)
		args := cfg.etcdctlArgs(endpoint)
		args = append(args, "endpoint", "health")
		kubectlArgs := []string{"exec", "-n", cfg.Namespace, peers[i].Name, "--"}
		kubectlArgs = append(kubectlArgs, args...)
		_, err := exec.Kubectl(ctx, "", kubectlArgs...)
		if err == nil {
			return &peers[i]
		}
	}
	return nil
}

func localHasData(dataDir string) bool {
	_, err := os.Stat(dataDir + "/member/snap/db")
	return err == nil
}

func peerHasData(ctx context.Context, cfg BootstrapConfig, peer Peer) (bool, error) {
	exec := executor.GetExecutor()
	out, err := exec.Kubectl(ctx, "",
		"exec", "-n", cfg.Namespace, peer.Name,
		"--", "test", "-f", cfg.DataDir+"/member/snap/db",
	)
	if err != nil {
		// kubectl exec 失败 = Pod 不可达，不是"无数据"
		return false, fmt.Errorf("peer %s unreachable: %w", peer.Name, err)
	}
	return strings.TrimSpace(string(out)) == "", nil // test -f 成功 = 文件存在
}

// BootstrapSeed 创建新 etcd 集群（Pod-0 冷启动），启用 TLS。
//
// 前置条件：InitCerts() 已生成 CA + server/peer 证书。
func BootstrapSeed(ctx context.Context, cfg BootstrapConfig, podIP string) error {
	exec := executor.GetExecutor()
	ca, cert, key, peerCert, peerKey := cfg.tlsPaths()

	args := []string{
		"etcd",
		"--name", cfg.PodName,
		"--data-dir", cfg.DataDir,
		"--initial-cluster-state", "new",
		"--initial-cluster", fmt.Sprintf("%s=%s", cfg.PodName, cfg.peerURL(podIP)),

		// Client TLS（localhost → etcd）
		"--listen-client-urls", fmt.Sprintf("https://0.0.0.0:%d", cfg.ClientPort),
		"--advertise-client-urls", cfg.clientURL(podIP),
		"--client-cert-auth",
		"--trusted-ca-file", ca,
		"--cert-file", cert,
		"--key-file", key,

		// Peer TLS（etcd→etcd）
		"--listen-peer-urls", fmt.Sprintf("https://0.0.0.0:%d", cfg.PeerPort),
		"--initial-advertise-peer-urls", cfg.peerURL(podIP),
		"--peer-trusted-ca-file", ca,
		"--peer-cert-file", peerCert,
		"--peer-key-file", peerKey,
	}

	kubectlArgs := []string{"exec", "-n", cfg.Namespace, cfg.PodName, "--", "/usr/local/bin/kp"}
	kubectlArgs = append(kubectlArgs, args...)
	_, err := exec.Kubectl(ctx, "", kubectlArgs...)
	return err
}

// JoinAsLearner 以 Learner 身份加入已有集群。
func JoinAsLearner(ctx context.Context, cfg BootstrapConfig, leaderPeer Peer, podIP string) error {
	exec := executor.GetExecutor()

	// Step 0: 检查是否已是 Voter（幂等性保护）
	if role := getMemberRole(ctx, cfg, leaderPeer, cfg.PodName); role == "Voter" {
		// 直接以 existing 状态启动，跳过 Learner 流程
		joinArgs := cfg.buildETCDArgs(cfg.peerURL(podIP), cfg.clientURL(podIP))
		kubectlArgs := []string{"exec", "-n", cfg.Namespace, cfg.PodName, "--", "/usr/local/bin/kp"}
		kubectlArgs = append(kubectlArgs, joinArgs...)
		_, err := exec.Kubectl(ctx, "", kubectlArgs...)
		return err
	}

	// Step 1: 在 leader 上执行 member add --learner
	addArgs := cfg.etcdctlArgs(cfg.clientURL(leaderPeer.IP))
	addArgs = append(addArgs, "member", "add", cfg.PodName,
		"--learner",
		"--peer-urls", cfg.peerURL(podIP),
	)
	kubectlArgs := []string{"exec", "-n", cfg.Namespace, leaderPeer.Name, "--", "/usr/local/bin/kp"}
	kubectlArgs = append(kubectlArgs, addArgs...)
	_, err := exec.Kubectl(ctx, "", kubectlArgs...)
	if err != nil {
		return fmt.Errorf("member add --learner: %w", err)
	}

	// Step 2: 启动本节点 etcd，以 existing 状态 join
	joinArgs := cfg.buildETCDArgs(cfg.peerURL(podIP), cfg.clientURL(podIP))

	kubectlArgs = []string{"exec", "-n", cfg.Namespace, cfg.PodName, "--", "/usr/local/bin/kp"}
	kubectlArgs = append(kubectlArgs, joinArgs...)
	_, err = exec.Kubectl(ctx, "", kubectlArgs...)
	return err
}

// buildETCDArgs 构建 etcd 启动参数（existing join / restart 复用）。
func (cfg BootstrapConfig) buildETCDArgs(peerURL, clientURL string) []string {
	ca, cert, key, peerCert, peerKey := cfg.tlsPaths()

	return []string{
		"etcd",
		"--name", cfg.PodName,
		"--data-dir", cfg.DataDir,
		"--initial-cluster-state", "existing",

		// Client TLS
		"--listen-client-urls", fmt.Sprintf("https://0.0.0.0:%d", cfg.ClientPort),
		"--advertise-client-urls", clientURL,
		"--client-cert-auth",
		"--trusted-ca-file", ca,
		"--cert-file", cert,
		"--key-file", key,

		// Peer TLS
		"--listen-peer-urls", fmt.Sprintf("https://0.0.0.0:%d", cfg.PeerPort),
		"--initial-advertise-peer-urls", peerURL,
		"--peer-trusted-ca-file", ca,
		"--peer-cert-file", peerCert,
		"--peer-key-file", peerKey,
	}
}

// WaitForCatchUp 等待 Learner 追平 Leader 的 Raft Log。
func WaitForCatchUp(ctx context.Context, cfg BootstrapConfig, leaderPeer Peer, totalPods int) error {
	ctx, cancel := context.WithTimeout(ctx, cfg.MaxJoinWait)
	defer cancel()

	var belowThresholdSince time.Time
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return &ErrCatchUpTimeout{PodName: cfg.PodName, MaxWait: cfg.MaxJoinWait.String()}
		case <-ticker.C:
			leaderIdx := getRaftIndex(ctx, cfg, leaderPeer)
			myIdx := getRaftIndex(ctx, cfg, Peer{Name: cfg.PodName})
			lag := leaderIdx - myIdx

			// 自适应频率：lag < 1000 加速到 200ms
			if lag < 1000 {
				ticker.Reset(200 * time.Millisecond)
			} else {
				ticker.Reset(2 * time.Second)
			}

			// 动态稳定窗口：3 节点 5s, 5 节点 8s, 7+ 节点 10s
			stableWindow := cfg.StableWindow
			switch {
			case totalPods <= 3:
				stableWindow = 5 * time.Second
			case totalPods <= 5:
				stableWindow = 8 * time.Second
			}

			if lag < int64(cfg.MaxLagForPromotion) {
				if belowThresholdSince.IsZero() {
					belowThresholdSince = time.Now()
				} else if time.Since(belowThresholdSince) >= stableWindow {
					return nil
				}
			} else {
				belowThresholdSince = time.Time{}
			}
		}
	}
}

// PromoteLearner 将当前 Learner 提升为 Voter。
func PromoteLearner(ctx context.Context, cfg BootstrapConfig, leaderPeer Peer) error {
	// 幂等：先查角色，已是 Voter 则跳过
	role := getMemberRole(ctx, cfg, leaderPeer, cfg.PodName)
	if role == "Voter" {
		return nil // 已经提升过，跳过
	}

	exec := executor.GetExecutor()
	args := cfg.etcdctlArgs(cfg.clientURL(leaderPeer.IP))
	args = append(args, "member", "promote", cfg.PodName)

	kubectlArgs := []string{"exec", "-n", cfg.Namespace, leaderPeer.Name, "--", "/usr/local/bin/kp"}
	kubectlArgs = append(kubectlArgs, args...)
	_, err := exec.Kubectl(ctx, "", kubectlArgs...)
	return err
}

// etcdStatus etcdctl endpoint status -w json 的输出格式。
type etcdStatus struct {
	Header struct {
		RaftTerm uint64 `json:"raft_term"`
	} `json:"header"`
	RaftIndex uint64 `json:"raft_index"`
}

// getRaftIndex 获取指定 etcd 节点的 Raft Index。
func getRaftIndex(ctx context.Context, cfg BootstrapConfig, peer Peer) int64 {
	exec := executor.GetExecutor()
	args := cfg.etcdctlArgs(cfg.clientURL(peer.IP))
	args = append(args, "endpoint", "status", "-w", "json")

	kubectlArgs := []string{"exec", "-n", cfg.Namespace, peer.Name, "--"}
	kubectlArgs = append(kubectlArgs, args...)
	out, err := exec.Kubectl(ctx, "", kubectlArgs...)
	if err != nil {
		return 0
	}

	var statusList []etcdStatus
	if err := json.Unmarshal(out, &statusList); err != nil || len(statusList) == 0 {
		return 0
	}
	return int64(statusList[0].RaftIndex)
}

// getMemberRole 查询指定 member 在集群中的角色。
func getMemberRole(ctx context.Context, cfg BootstrapConfig, leaderPeer Peer, memberName string) string {
	exec := executor.GetExecutor()
	args := cfg.etcdctlArgs(cfg.clientURL(leaderPeer.IP))
	args = append(args, "member", "list", "-w", "json")

	kubectlArgs := []string{"exec", "-n", cfg.Namespace, leaderPeer.Name, "--"}
	kubectlArgs = append(kubectlArgs, args...)
	out, err := exec.Kubectl(ctx, "", kubectlArgs...)
	if err != nil {
		return ""
	}

	var resp struct {
		Members []struct {
			Name      string `json:"name"`
			IsLearner bool   `json:"isLearner"`
		} `json:"members"`
	}
	if json.Unmarshal(out, &resp) != nil {
		return ""
	}
	for _, m := range resp.Members {
		if m.Name == memberName {
			if m.IsLearner {
				return "Learner"
			}
			return "Voter"
		}
	}
	return ""
}

// PodIndexFromName 从 StatefulSet Pod name 提取序号。
func PodIndexFromName(podName string) int {
	idx := strings.LastIndex(podName, "-")
	if idx < 0 {
		return 0
	}
	n, err := strconv.Atoi(podName[idx+1:])
	if err != nil {
		return 0
	}
	return n
}
