package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
)

// canUseK8sLease 探测 K8s Lease API 是否可用 + 权限是否充分
//
// 通过 kubectl auth can-i 检查能否在 kubepivot-system namespace 创建 lease。
// 如果集群版本 < 1.14 没有 coordination.k8s.io，或 RBAC 没授权，返回 false。
func canUseK8sLease(ctx context.Context, kubeconfig string) bool {
	out, err := executor.GetExecutor().Kubectl(ctx, kubeconfig,
		"auth", "can-i", "create", "leases.coordination.k8s.io",
		"-n", "kubepivot-system",
	)
	if err != nil {
		slog.Warn("K8s Lease 权限探测失败", "err", err)
		return false
	}
	return strings.TrimSpace(string(out)) == "yes"
}

// RunWithK8sLeaseElection 通过 K8s coordination.k8s.io/leases API 实现 leader 选举
//
// 用途：当未配置 ETCD_ENDPOINTS 时，作为 etcd 选举的降级路径
//
// 核心逻辑：
//  1. 每个 pod 有唯一 holder identity（hostname + 随机后缀）
//  2. 周期性 kubectl get lease，看 spec.holderIdentity 和 renewTime
//  3. 如果 lease 不存在 → 尝试 create（race 中的胜者成 leader）
//  4. 如果 lease 已存在但 holder 是自己 → 续约（更新 renewTime）
//  5. 如果 lease 已存在但 holder 不是自己且过期 → 抢占（更新 holderIdentity）
//  6. 如果 lease 已存在且 holder 不是自己且未过期 → 等待，下个周期再试
//
// 不引入 client-go，全部走 kubectl exec
func RunWithK8sLeaseElection(
	ctx context.Context,
	leaseName, namespace string,
	ttl time.Duration,
	kubeconfig string,
	run func(context.Context),
) {
	// 每个 pod 生成独一无二的 holder identity
	identity := generateIdentity()

	slog.Info("🗳  启动 K8s Lease 选举",
		"lease", leaseName,
		"namespace", namespace,
		"identity", identity,
		"ttl", ttl)

	// 续约周期 = ttl / 3（标准选举模式：3 次失败才丢失 leader 身份）
	renewInterval := ttl / 3
	if renewInterval < 2*time.Second {
		renewInterval = 2 * time.Second
	}

	// 当前是否为 leader
	var leaderCtx context.Context
	var leaderCancel context.CancelFunc

	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if leaderCancel != nil {
				leaderCancel()
			}
			return
		case <-ticker.C:
		}

		// 尝试拿/续 leader
		isLeader, err := tryAcquireOrRenew(ctx, leaseName, namespace, identity, ttl, kubeconfig)
		if err != nil {
			slog.Warn("Lease 操作失败", "err", err)
			// 失败时如果当前是 leader，继续保留状态等下个周期再试
			// 不立即 cancel leader 任务，给一次容错机会
			continue
		}

		if isLeader && leaderCtx == nil {
			// 刚成为 leader，启动业务
			slog.Info("👑 已成为全局 Leader（K8s Lease）", "identity", identity)
			leaderCtx, leaderCancel = context.WithCancel(ctx)
			go run(leaderCtx)
		} else if !isLeader && leaderCtx != nil {
			// 失去 leader 身份，停止业务
			slog.Warn("失去 Leader 身份，停止 reconcile", "identity", identity)
			leaderCancel()
			leaderCtx, leaderCancel = nil, nil
		}
	}
}

// tryAcquireOrRenew 单次尝试获取或续约 lease
// 返回：是否为 leader, 错误
func tryAcquireOrRenew(
	ctx context.Context,
	leaseName, namespace, identity string,
	ttl time.Duration,
	kubeconfig string,
) (bool, error) {
	exec := executor.GetExecutor()

	// 1. 查看 lease 当前状态
	out, err := exec.Kubectl(ctx, kubeconfig,
		"get", "lease", leaseName,
		"-n", namespace,
		"-o", "json",
		"--ignore-not-found",
	)
	if err != nil {
		return false, fmt.Errorf("get lease: %w", err)
	}

	now := time.Now().UTC()
	nowStr := now.Format("2006-01-02T15:04:05.000000Z07:00")
	ttlSec := int(ttl.Seconds())

	if len(strings.TrimSpace(string(out))) == 0 {
		// Lease 不存在 → 尝试创建（race 时第一个 create 成功的成为 leader）
		manifest := fmt.Sprintf(`{
  "apiVersion": "coordination.k8s.io/v1",
  "kind": "Lease",
  "metadata": {
    "name": %q,
    "namespace": %q,
    "labels": {
      "app.kubernetes.io/managed-by": "kp",
      "app.kubernetes.io/component": "kubepivot-controller"
    }
  },
  "spec": {
    "holderIdentity": %q,
    "leaseDurationSeconds": %d,
    "acquireTime": %q,
    "renewTime": %q,
    "leaseTransitions": 0
  }
}`, leaseName, namespace, identity, ttlSec, nowStr, nowStr)

		cmd := exec.CmdKubectl(ctx, kubeconfig, "create", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		if cmdOut, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
			// AlreadyExists 错误：另一个 pod 抢先创建了，当作非 leader 处理
			if strings.Contains(string(cmdOut), "AlreadyExists") ||
				strings.Contains(string(cmdOut), "already exists") {
				return false, nil
			}
			return false, fmt.Errorf("create lease: %w (%s)", cmdErr, string(cmdOut))
		}
		// 创建成功 → 自己是 leader
		return true, nil
	}

	// Lease 存在 → 解析当前状态
	var lease leaseObject
	if err := json.Unmarshal(out, &lease); err != nil {
		return false, fmt.Errorf("parse lease json: %w", err)
	}

	holder := lease.Spec.HolderIdentity
	renewTimeStr := lease.Spec.RenewTime

	// 解析续约时间
	var lastRenew time.Time
	if renewTimeStr != "" {
		lastRenew, _ = time.Parse(time.RFC3339, renewTimeStr)
	}

	leaseAge := now.Sub(lastRenew)
	leaseExpired := leaseAge > ttl

	if holder == identity {
		// 自己是 leader → 续约
		return renewLease(ctx, exec, kubeconfig, leaseName, namespace, identity, nowStr, ttlSec)
	}

	// holder 不是自己
	if !leaseExpired {
		// 别人持有且未过期 → 等待
		return false, nil
	}

	// 别人持有但已过期 → 抢占
	slog.Info("🔄 Lease 已过期，尝试抢占",
		"current_holder", holder,
		"new_holder", identity,
		"expired_for", leaseAge-ttl)
	return takeOverLease(ctx, exec, kubeconfig, leaseName, namespace, identity, nowStr, ttlSec, lease.Spec.LeaseTransitions+1)
}

// renewLease 自己是 leader 时的续约
func renewLease(
	ctx context.Context,
	exec *executor.KpExecutor,
	kubeconfig, leaseName, namespace, identity, nowStr string,
	ttlSec int,
) (bool, error) {
	patch := fmt.Sprintf(`{"spec":{"renewTime":%q,"holderIdentity":%q,"leaseDurationSeconds":%d}}`,
		nowStr, identity, ttlSec)

	_, err := exec.Kubectl(ctx, kubeconfig,
		"patch", "lease", leaseName,
		"-n", namespace,
		"--type=merge",
		"-p", patch,
	)
	if err != nil {
		return false, fmt.Errorf("renew lease: %w", err)
	}
	return true, nil
}

// takeOverLease 抢占过期的 lease
func takeOverLease(
	ctx context.Context,
	exec *executor.KpExecutor,
	kubeconfig, leaseName, namespace, identity, nowStr string,
	ttlSec, transitions int,
) (bool, error) {
	patch := fmt.Sprintf(
		`{"spec":{"holderIdentity":%q,"acquireTime":%q,"renewTime":%q,"leaseDurationSeconds":%d,"leaseTransitions":%d}}`,
		identity, nowStr, nowStr, ttlSec, transitions)

	_, err := exec.Kubectl(ctx, kubeconfig,
		"patch", "lease", leaseName,
		"-n", namespace,
		"--type=merge",
		"-p", patch,
	)
	if err != nil {
		// 抢占失败可能是另一 pod 同时也在抢——返回 false 等下一周期
		return false, nil
	}
	return true, nil
}

// generateIdentity 生成本 pod 的唯一 holder identity
// 格式：<hostname>-<random8>
// hostname 在 K8s pod 里是 pod name，加随机后缀防止 pod 重启时复用旧身份
func generateIdentity() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	// 8 字节随机后缀
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("%s-%s", host, hex.EncodeToString(buf))
}

// leaseObject K8s Lease 的最小 JSON 结构
type leaseObject struct {
	Spec struct {
		HolderIdentity       string `json:"holderIdentity"`
		LeaseDurationSeconds int    `json:"leaseDurationSeconds"`
		AcquireTime          string `json:"acquireTime"`
		RenewTime            string `json:"renewTime"`
		LeaseTransitions     int    `json:"leaseTransitions"`
	} `json:"spec"`
}
