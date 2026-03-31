package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// checkVolumeSnapshotCRD 检查 VolumeSnapshot CRD 是否安装
func checkVolumeSnapshotCRD() checkResult {
	out, err := exec.Command("kubectl", "get", "crd",
		"volumesnapshots.snapshot.storage.k8s.io",
		"--ignore-not-found",
		"-o", "name",
	).Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return checkResult{
			name:    "VolumeSnapshot CRD",
			ok:      false,
			isError: false,
			detail:  "未安装，kp pvc backup/restore 不可用",
			fix:     "kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/main/client/config/crd/snapshot.storage.k8s.io_volumesnapshots.yaml",
		}
	}
	return checkResult{
		name:   "VolumeSnapshot CRD",
		ok:     true,
		detail: "已安装",
	}
}

// checkVolumeSnapshotClass 检查是否有可用的 VolumeSnapshotClass
func checkVolumeSnapshotClass() checkResult {
	out, err := exec.Command("kubectl", "get", "volumesnapshotclass",
		"--ignore-not-found",
		"-o", "jsonpath={.items[*].metadata.name}",
	).Output()
	if err != nil {
		return checkResult{
			name:    "VolumeSnapshotClass",
			ok:      false,
			isError: false,
			detail:  "查询失败（可能未安装 CRD）",
			fix:     "先安装 VolumeSnapshot CRD，再配置 VolumeSnapshotClass",
		}
	}
	classes := strings.TrimSpace(string(out))
	if classes == "" {
		return checkResult{
			name:    "VolumeSnapshotClass",
			ok:      false,
			isError: false,
			detail:  "无可用 VolumeSnapshotClass",
			fix:     "为你的 CSI driver 创建 VolumeSnapshotClass，参考：https://kubernetes-csi.github.io/docs/snapshot-restore-feature.html",
		}
	}
	// 检查是否有默认 class
	defaultOut, _ := exec.Command("kubectl", "get", "volumesnapshotclass",
		"-o", "jsonpath={.items[?(@.metadata.annotations.snapshot\\.storage\\.kubernetes\\.io/is-default-class==\"true\")].metadata.name}",
	).Output()
	defaultClass := strings.TrimSpace(string(defaultOut))

	detail := fmt.Sprintf("可用: %s", classes)
	if defaultClass != "" {
		detail += fmt.Sprintf("（默认: %s）", defaultClass)
	} else {
		detail += "（无默认 class，backup 时需指定 --snapshot-class）"
	}
	return checkResult{
		name:   "VolumeSnapshotClass",
		ok:     true,
		detail: detail,
	}
}
