package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ssPod StatefulSet Pod 信息
type ssPod struct {
	Ordinal int
	Name    string
	Ready   bool
	Version string
	Phase   string
}

// printStatefulSetStatus 展示 StatefulSet 的 Pod 列表（ordinal/ready/version）
func printStatefulSetStatus(cfg *deployConfig, stsName, targetVersion string) {
	// 1. 获取 StatefulSet 整体状态
	args := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	args = append(args, "get", "statefulset", stsName, "-o", "json")
	out, err := runOutput(args...)
	if err != nil {
		fmt.Printf("  （获取 StatefulSet %s 失败）\n", stsName)
		return
	}

	var sts struct {
		Spec struct {
			Replicas int `json:"replicas"`
		} `json:"spec"`
		Status struct {
			Replicas        int `json:"replicas"`
			ReadyReplicas   int `json:"readyReplicas"`
			UpdatedReplicas int `json:"updatedReplicas"`
		} `json:"status"`
	}
	if err := json.Unmarshal(out, &sts); err != nil {
		fmt.Printf("  （解析 StatefulSet %s 失败）\n", stsName)
		return
	}

	fmt.Printf("  StatefulSet %-20s 期望: %d  就绪: %d  已更新: %d\n",
		stsName,
		sts.Spec.Replicas,
		sts.Status.ReadyReplicas,
		sts.Status.UpdatedReplicas,
	)

	// 2. 获取所有 pod
	podArgs := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	podArgs = append(podArgs,
		"get", "pods",
		"-l", fmt.Sprintf("app=%s", stsName),
		"-o", "json",
	)
	podOut, err := runOutput(podArgs...)
	if err != nil {
		return
	}

	var podList struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase      string `json:"phase"`
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(podOut, &podList); err != nil {
		return
	}

	// 3. 解析并排序（按 ordinal）
	var pods []ssPod
	for _, item := range podList.Items {
		// 从 pod 名提取 ordinal：statefulset-name-{ordinal}
		ordinal := extractOrdinal(item.Metadata.Name, stsName)

		// ready 判断：ReadyCondition == True
		ready := false
		for _, c := range item.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				ready = true
				break
			}
		}

		// version：优先从 label 读 kp 打的版本，其次从镜像 tag 提取
		version := item.Metadata.Labels["version"]
		if version == "" && len(item.Spec.Containers) > 0 {
			img := item.Spec.Containers[0].Image
			if idx := strings.LastIndex(img, ":"); idx >= 0 {
				version = img[idx+1:]
			}
		}

		pods = append(pods, ssPod{
			Ordinal: ordinal,
			Name:    item.Metadata.Name,
			Ready:   ready,
			Version: version,
			Phase:   item.Status.Phase,
		})
	}

	sort.Slice(pods, func(i, j int) bool {
		return pods[i].Ordinal < pods[j].Ordinal
	})

	// 4. 打印 pod 列表
	fmt.Printf("  %-5s  %-35s  %-4s  %-12s  %s\n", "No.", "Pod", "OK", "Version", "Phase")
	fmt.Printf("  %s\n", strings.Repeat("─", 65))
	for _, p := range pods {
		ok := colorize(colorGreen, "Y")
		if !p.Ready {
			ok = colorize(colorYellow, "N")
		}
	
		// 先计算 plain 宽度，再加颜色，最后手动补空格
		versionColor := colorGreen
		if p.Version != targetVersion && targetVersion != "" {
			versionColor = colorYellow
		}
		verPad := 12 - len(p.Version)
		if verPad < 0 {
			verPad = 0
		}
		ver := colorize(versionColor, p.Version) + strings.Repeat(" ", verPad)
	
		fmt.Printf("  %-5d  %-35s  %-4s  %s  %s\n",
			p.Ordinal, p.Name, ok, ver, p.Phase)
	}
}

// extractOrdinal 从 pod 名提取 ordinal
// web3-blitz-etcd-0 → 0，web3-blitz-postgres-0 → 0
func extractOrdinal(podName, stsName string) int {
	suffix := strings.TrimPrefix(podName, stsName+"-")
	n, err := strconv.Atoi(suffix)
	if err != nil {
		return -1
	}
	return n
}
