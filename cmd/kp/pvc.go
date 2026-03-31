package main

import (
	"fmt"
	"os"
)

// runPVC kp pvc 命令入口
//
// TODO(v1.5.1): 实现完整 kp pvc 子命令，待 CSI VolumeSnapshot 环境验证
// web3-blitz 使用 local-path provisioner（rancher.io/local-path），不支持 CSI snapshot
// 在支持 CSI 的集群（Longhorn/Ceph RBD/AWS EBS CSI/GCE PD CSI）验证后去掉此 TODO
//
// 设计要点：
// 1. backup:
//    - 通过 kp.io/service=<service> 标签发现该服务下所有 PVC
//    - 支持 --pvc <name> 指定单个 PVC，默认备份所有
//    - Snapshot 命名格式：<pvc-name>-snap-<unix-timestamp>
//    - 给 Snapshot 打标签：kp.io/service=<service>, kp.io/pvc=<pvc-name>
//    - 等待 readyToUse=true，超时默认 5 分钟，支持 --timeout
//    - 支持 --snapshot-class 指定 VolumeSnapshotClass，默认用集群默认 class
//
// 2. restore:
//    - 默认从最近 readyToUse=true 的 Snapshot 恢复，支持 --snapshot <name> 指定
//    - 恢复前置：强制确认（--force 跳过）→ 缩容 StatefulSet 到 0 → 删除旧 PVC
//    - 恢复执行：从 Snapshot 创建同名新 PVC → 扩容 StatefulSet 回原副本数
//    - PVC 名必须和旧 PVC 完全一致，StatefulSet 扩容后自动绑定
//
// 3. list:
//    - 默认按 kp.io/service 标签过滤当前 namespace 下的 Snapshot
//    - 支持 --all 展示所有 Snapshot，支持 -A 跨 namespace
//    - 展示字段：NAME / PVC / SERVICE / CREATED AT / READY TO USE
//    - 默认按 CREATED AT 倒序

func runPVC(args []string) {
	if len(args) == 0 {
		printPVCUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "backup":
		runPVCNotAvailable("backup")
	case "restore":
		runPVCNotAvailable("restore")
	case "list":
		runPVCNotAvailable("list")
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n", args[0])
		printPVCUsage()
		os.Exit(1)
	}
}

func printPVCUsage() {
	fmt.Println("用法: kp pvc <子命令>")
	fmt.Println("  kp pvc backup  --service <name> [--pvc <name>] [--snapshot-class <class>] [--timeout 5m]")
	fmt.Println("  kp pvc restore --service <name> [--snapshot <name>] [--force]")
	fmt.Println("  kp pvc list    --service <name> [--all] [-A]")
	fmt.Println()
	fmt.Println("前置要求（运行 kp doctor 检查）：")
	fmt.Println("  1. 集群已安装 VolumeSnapshot CRD")
	fmt.Println("  2. 存储类支持 CSI snapshot（local-path 不支持）")
	fmt.Println("  3. 至少有一个可用的 VolumeSnapshotClass")
	fmt.Println()
	fmt.Printf("%s 完整功能将在 v1.5.1 推出，当前环境需要 CSI 支持\n",
		colorize(colorYellow, "💡"))
}

func runPVCNotAvailable(sub string) {
	fmt.Printf("%s kp pvc %s 需要 CSI VolumeSnapshot 支持（v1.5.1 推出）\n",
		colorize(colorYellow, "⚠️ "), sub)
	fmt.Println()
	fmt.Println("当前集群（local-path）不支持 VolumeSnapshot。")
	fmt.Println("支持的存储驱动：Longhorn / Ceph RBD / AWS EBS CSI / GCE PD CSI")
	fmt.Println()
	fmt.Printf("%s 运行 kp doctor 检查 VolumeSnapshot 环境\n", colorize(colorCyan, "💡"))
}
