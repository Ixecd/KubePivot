package sharding

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

// 注意：这个文件里的逻辑和 controller/lease.go 中的 generateIdentity /
// tryAcquireOrRenew 是平行的（行为一致）。
//
// 为什么不复用？因为 controller 包要 import sharding 包，
// 而 sharding 反向 import controller 会造成 import cycle。
// 把这几个底层函数本地化，让 sharding 包真正自包含。
//
// 这些是底层 K8s lease 协议的工具函数，稳定不变，重复成本低。

// generateIdentity 生成本 pod 的唯一 holder identity
// 格式：<hostname>-<random8>
func generateIdentity() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
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

// tryAcquireOrRenew 单次尝试获取或续约 lease
// 返回：是否为 leader, 错误
func tryAcquireOrRenew(
	ctx context.Context,
	leaseName, namespace, identity string,
	ttl time.Duration,
	kubeconfig string,
) (bool, error) {
	exec := executor.GetExecutor()

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
	// K8s microTime 格式必须带微秒精度
	nowStr := now.Format("2006-01-02T15:04:05.000000Z07:00")
	ttlSec := int(ttl.Seconds())

	if len(strings.TrimSpace(string(out))) == 0 {
		// Lease 不存在 → 尝试创建
		manifest := fmt.Sprintf(`{
  "apiVersion": "coordination.k8s.io/v1",
  "kind": "Lease",
  "metadata": {
    "name": %q,
    "namespace": %q,
    "labels": {
      "app.kubernetes.io/managed-by": "kp",
      "app.kubernetes.io/component": "kubepivot-controller-shard"
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
			if strings.Contains(string(cmdOut), "AlreadyExists") ||
				strings.Contains(string(cmdOut), "already exists") {
				return false, nil
			}
			return false, fmt.Errorf("create lease: %w (%s)", cmdErr, string(cmdOut))
		}
		return true, nil
	}

	// Lease 存在 → 解析当前状态
	var lease leaseObject
	if err := json.Unmarshal(out, &lease); err != nil {
		return false, fmt.Errorf("parse lease json: %w", err)
	}

	holder := lease.Spec.HolderIdentity
	renewTimeStr := lease.Spec.RenewTime

	var lastRenew time.Time
	if renewTimeStr != "" {
		lastRenew, _ = time.Parse(time.RFC3339, renewTimeStr)
	}

	leaseAge := now.Sub(lastRenew)
	leaseExpired := leaseAge > ttl

	if holder == identity {
		// 自己是 holder → 续约
		return renewLease(ctx, exec, kubeconfig, leaseName, namespace, identity, nowStr, ttlSec)
	}

	if !leaseExpired {
		return false, nil
	}

	// 别人持有但已过期 → 抢占
	slog.Info("🔄 Lease 已过期，尝试抢占",
		"lease", leaseName,
		"current_holder", holder,
		"new_holder", identity,
		"expired_for", leaseAge-ttl)
	return takeOverLease(ctx, exec, kubeconfig, leaseName, namespace, identity, nowStr, ttlSec, lease.Spec.LeaseTransitions+1)
}

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
