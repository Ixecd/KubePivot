package controller_installer

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Ixecd/kubepivot/internal/executor"
)

// SizingInfo 集群规模快照，供 Recommend 算法使用。
type SizingInfo struct {
	ProjectCount    int      // P（经过过滤的有效项目数）
	Projects        []string // 项目名列表（调试用）
	CurrentShards   int      // S
	CurrentReplicas int      // C
}

// Recommend 基于 P→S→C 三维关系推导最优 (shards, replicas)。
//
// 返回 warnings（供 dry-run 展示），不阻断推荐值计算。
func Recommend(projectCount int, currentShards int, currentReplicas int, forceDownscale bool) (shards, replicas int, reason string, warnings []string) {
	// S = clamp(ceil(P/4), 3, 50)
	shards = (projectCount + 3) / 4
	if shards < 3 {
		shards = 3
	}
	if shards > 50 {
		shards = 50
	}

	// C = clamp(ceil(S/3), 3, 9) — etcd 要求奇数节点保证多数派仲裁
	replicas = (shards + 2) / 3
	if replicas < 3 {
		replicas = 3
	}
	if replicas > 9 {
		replicas = 9
	}
	// 强制奇数：偶数节点多花资源却不增加容错
	if replicas%2 == 0 {
		replicas++
	}

	// 不降配策略：仅 replicas 受保护，shards 不受限制（降低分片是纯优化）
	if !forceDownscale && replicas < currentReplicas {
		reason = fmt.Sprintf(
			"replicas 推荐值 %d 低于当前值 %d，保持当前值（使用 --force-downscale 允许缩减）",
			replicas, currentReplicas)
		replicas = currentReplicas
	}

	// 超载警告
	if projectCount >= 500 {
		warnings = append(warnings,
			"🔴 P≥500 严重超载，每分片 >10 项目，建议考虑多集群或分层 controller 架构")
	} else if projectCount >= 200 {
		warnings = append(warnings,
			"💡 P≥200 触及上限，每分片项目数可能超过甜区")
	}

	// 重平衡风暴预警：S 变化幅度 ≥50% 且 P≥50
	if currentShards > 0 {
		deltaRatio := float64(abs(shards-currentShards)) / float64(currentShards)
		if deltaRatio >= 0.5 && projectCount >= 50 {
			pct := int(deltaRatio * 100)
			warnings = append(warnings, fmt.Sprintf(
				"⚡ 分片数变化 %d%% (%d→%d)，将触发全量项目重平衡。"+
					"%d 个项目将在首个 Lease 扫描周期（~15s）内同时迁移分片，"+
					"可能引起短暂的调取风暴（Reconciliation Storm）。"+
					"建议在低峰期执行 --apply。",
				pct, currentShards, shards, projectCount,
			))
		}
	}

	// 单分片超载预警
	if projectCount/shards > 10 {
		warnings = append(warnings, fmt.Sprintf(
			"🔴 单分片承载 %d 个项目 (P/S=%.1f)，超过 etcd 建议响应延迟。"+
				"当前集群已超载，S=%d 已是硬上限。请考虑多集群部署。",
			projectCount/shards, float64(projectCount)/float64(shards), shards,
		))
	}

	return shards, replicas, reason, warnings
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// GetSizingInfo 从集群读取当前分片/副本/项目数快照。
//
// 过滤规则：
//   - 不排除任何 K8s phase（Terminating namespace 可能因 finalizer 挂起持续数小时，
//     Controller 仍在同步它们，S 不应在此期间缩得过快）
//   - 排除 kubepivot.io/phase == Error|Paused 的项目
//   - 无 kubepivot.io/phase label 视为 Active（向后兼容）
func (i *Installer) GetSizingInfo(ctx context.Context) (*SizingInfo, error) {
	exec := executor.GetExecutor()
	info := &SizingInfo{}

	// 1. 读取当前 replicas
	out, err := exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"get", "statefulset", "kubepivot-controller",
		"-n", i.cfg.Namespace,
		"-o", "jsonpath={.spec.replicas}")
	if err != nil {
		return nil, fmt.Errorf("读取 controller replicas 失败: %w", err)
	}
	info.CurrentReplicas, _ = strconv.Atoi(strings.TrimSpace(string(out)))

	// 2. 读取当前 shards：ConfigMap 优先，其次 StatefulSet env
	out, err = exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"get", "configmap", "kp-system-config",
		"-n", i.cfg.Namespace,
		"-o", "jsonpath={.data.shards}", "--ignore-not-found")
	if err != nil {
		return nil, fmt.Errorf("读取 ConfigMap shards 失败: %w", err)
	}
	cmShards := strings.TrimSpace(string(out))

	// 检查 StatefulSet 是否通过 env 直接覆盖了 shards
	out, err = exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"get", "statefulset", "kubepivot-controller",
		"-n", i.cfg.Namespace,
		"-o",
		`jsonpath={.spec.template.spec.containers[?(@.name=="controller")].env[?(@.name=="KUBEPIVOT_SHARDS")].value}`)
	envShards := ""
	if err == nil {
		envShards = strings.TrimSpace(string(out))
	}

	if envShards != "" && cmShards != "" && envShards != cmShards {
		return nil, fmt.Errorf(
			"KUBEPIVOT_SHARDS conflict: ConfigMap says %s, StatefulSet env says %s. "+
				"Remove the env override or update ConfigMap to match.", cmShards, envShards)
	}
	if envShards != "" {
		info.CurrentShards, _ = strconv.Atoi(envShards)
	} else if cmShards != "" {
		info.CurrentShards, _ = strconv.Atoi(cmShards)
	} else {
		info.CurrentShards = 10 // 默认值（与 MultiLeaseConfig 一致）
	}

	// 3. 统计 managed namespace 数量（带过滤逻辑）
	out, err = exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"get", "namespace",
		"-l", "kubepivot.io/managed=true",
		"-o", "json")
	if err != nil {
		return nil, fmt.Errorf("读取 managed namespace 失败: %w", err)
	}

	// 解析 JSON 输出
	var nsList struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &nsList); err != nil {
		return nil, fmt.Errorf("解析 namespace JSON 失败: %w", err)
	}

	for _, ns := range nsList.Items {
		// 排除 Error/Paused 项目（若 kubepivot.io/phase label 存在）
		// 注意：不排除 Terminating namespace — finalizer 挂起可能持续数小时，
		// Controller 仍在同步它们，S 不应在此期间缩得过快
		phase := ns.Metadata.Labels["kubepivot.io/phase"]
		if phase == "Error" || phase == "Paused" {
			continue
		}
		info.ProjectCount++
		info.Projects = append(info.Projects, ns.Metadata.Name)
	}

	return info, nil
}
