package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// etcd endpoint status 结构（etcdctl endpoint status -w json 输出）
type etcdEndpointStatus struct {
	Endpoint string `json:"Endpoint"`
	Status   struct {
		Header struct {
			ClusterId uint64 `json:"cluster_id"`
			MemberId  uint64 `json:"member_id"`
			Revision  int64  `json:"revision"`
		} `json:"header"`
		Version          string   `json:"version"`
		DbSize           int64    `json:"dbSize"`
		Leader           uint64   `json:"leader"`
		RaftIndex        uint64   `json:"raftIndex"`
		RaftAppliedIndex uint64   `json:"raftAppliedIndex"`
		Errors           []string `json:"errors"`
	} `json:"Status"`
}

// checkEtcdAll 运行所有 etcd 健康检查，返回多个 checkResult
func checkEtcdAll(root string, env map[string]string) []checkResult {
	var results []checkResult

	// 1. etcdctl 是否安装
	results = append(results, checkEtcdctl())
	if !results[0].ok {
		return results
	}

	// 解析 endpoint：优先 os.Getenv，其次 project.env
	endpoint := os.Getenv("ETCD_ENDPOINTS")
	if endpoint == "" {
		endpoint = env["ETCD_ENDPOINTS"]
	}
	if endpoint == "" {
		results = append(results, checkResult{
			name:    "etcd 连通性",
			ok:      false,
			isError: false,
			detail:  "ETCD_ENDPOINTS 未配置，跳过",
			fix:     "在 configs/project.env 中设置 ETCD_ENDPOINTS=<host>:2379",
		})
		return results
	}

	// 3. endpoint health
	results = append(results, checkEtcdHealth(endpoint))

	// 4. raft index 差值
	results = append(results, checkEtcdRaftIndex(endpoint))

	// 5. DB 磁盘使用率
	results = append(results, checkEtcdDBSize(endpoint))

	return results
}

// checkEtcdctl 检查 etcdctl 是否安装
func checkEtcdctl() checkResult {
	out, err := runEtcdctl("version")
	if err != nil {
		return checkResult{
			name:    "etcdctl",
			ok:      false,
			isError: false,
			detail:  "未安装，etcd 健康检查不可用",
			fix:     "brew install etcd  或  https://etcd.io/docs/latest/install/",
		}
	}
	version := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	return checkResult{name: "etcdctl", ok: true, detail: version}
}

// checkEtcdHealth 检查 etcd 连通性
func checkEtcdHealth(endpoint string) checkResult {
	out, err := runEtcdctlCombined(
		"--endpoints", endpoint,
		"endpoint", "health",
		"--write-out", "simple",
	)

	output := strings.TrimSpace(string(out))
	if err != nil {
		return checkResult{
			name:    "etcd 连通性",
			ok:      false,
			isError: true,
			detail:  fmt.Sprintf("不可达: %s", output),
			fix:     "检查 etcd pod 是否正常运行：kubectl get pods -n <ns>",
		}
	}
	if strings.Contains(output, "unhealthy") {
		return checkResult{
			name:    "etcd 连通性",
			ok:      false,
			isError: true,
			detail:  output,
			fix:     "etcd 节点不健康，检查日志：kubectl logs -n <ns> <etcd-pod>",
		}
	}
	return checkResult{
		name:   "etcd 连通性",
		ok:     true,
		detail: fmt.Sprintf("healthy (%s)", endpoint),
	}
}

// checkEtcdRaftIndex 检查 raft index 差值（差值过大预示脑裂或压力过大）
func checkEtcdRaftIndex(endpoint string) checkResult {
	out, err := runEtcdctl(
		"--endpoints", endpoint,
		"endpoint", "status",
		"--write-out", "json",
	)
	if err != nil {
		return checkResult{
			name:    "etcd raft index",
			ok:      false,
			isError: false,
			detail:  "查询失败，跳过",
		}
	}

	var statuses []etcdEndpointStatus
	if err := json.Unmarshal(out, &statuses); err != nil || len(statuses) == 0 {
		return checkResult{
			name:    "etcd raft index",
			ok:      false,
			isError: false,
			detail:  "解析失败，跳过",
		}
	}

	s := statuses[0].Status
	diff := int64(s.RaftIndex) - int64(s.RaftAppliedIndex)
	if diff < 0 {
		diff = -diff
	}

	detail := fmt.Sprintf("raftIndex=%d, raftAppliedIndex=%d, diff=%d",
		s.RaftIndex, s.RaftAppliedIndex, diff)

	// 阈值：diff > 1000 告警，> 10000 严重
	if diff > 10000 {
		return checkResult{
			name:    "etcd raft index",
			ok:      false,
			isError: true,
			detail:  detail,
			fix:     "raft index 差值严重（>10000），集群可能脑裂或磁盘 IO 严重不足，立即排查",
		}
	}
	if diff > 1000 {
		return checkResult{
			name:    "etcd raft index",
			ok:      false,
			isError: false,
			detail:  detail,
			fix:     "raft index 差值偏大（>1000），关注磁盘 IO 和网络延迟，预示压力过大",
		}
	}
	return checkResult{
		name:   "etcd raft index",
		ok:     true,
		detail: detail,
	}
}

// checkEtcdDBSize 检查 etcd DB 磁盘使用率
func checkEtcdDBSize(endpoint string) checkResult {
	out, err := runEtcdctl(
		"--endpoints", endpoint,
		"endpoint", "status",
		"--write-out", "json",
	)
	if err != nil {
		return checkResult{
			name:   "etcd 磁盘",
			ok:     true,
			detail: "查询失败，跳过",
		}
	}

	var statuses []etcdEndpointStatus
	if err := json.Unmarshal(out, &statuses); err != nil || len(statuses) == 0 {
		return checkResult{name: "etcd 磁盘", ok: true, detail: "解析失败，跳过"}
	}

	dbSize := statuses[0].Status.DbSize
	dbSizeMB := float64(dbSize) / 1024 / 1024

	// etcd 默认 quota 2GB，超过 80% 告警
	const defaultQuotaBytes = 2 * 1024 * 1024 * 1024
	usagePct := float64(dbSize) / float64(defaultQuotaBytes) * 100
	detail := fmt.Sprintf("%.1f MB（默认 quota 2GB，使用率 %.1f%%）", dbSizeMB, usagePct)

	if usagePct > 90 {
		return checkResult{
			name:    "etcd 磁盘",
			ok:      false,
			isError: true,
			detail:  detail,
			fix:     "DB 使用率 >90%，立即执行碎片整理：etcdctl defrag --endpoints=" + endpoint,
		}
	}
	if usagePct > 80 {
		return checkResult{
			name:    "etcd 磁盘",
			ok:      false,
			isError: false,
			detail:  detail,
			fix:     "DB 使用率 >80%，建议执行碎片整理：etcdctl defrag --endpoints=" + endpoint,
		}
	}
	return checkResult{name: "etcd 磁盘", ok: true, detail: detail}
}

func runEtcdctl(args ...string) ([]byte, error) {
	cmd := exec.Command("etcdctl", args...)
	// 继承当前环境但移除会冲突的 ETCDCTL_* 变量
	var filtered []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "ETCDCTL_") {
			filtered = append(filtered, e)
		}
	}
	cmd.Env = filtered
	return cmd.Output()
}

func runEtcdctlCombined(args ...string) ([]byte, error) {
	cmd := exec.Command("etcdctl", args...)
	var filtered []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "ETCDCTL_") {
			filtered = append(filtered, e)
		}
	}
	cmd.Env = filtered
	return cmd.CombinedOutput()
}
