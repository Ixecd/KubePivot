package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// ── 数据结构 ──────────────────────────────────────────────────────────────────

type volumeSnapshot struct {
	Name        string
	PVCName     string
	Service     string
	CreatedAt   time.Time
	ReadyToUse  bool
	SnapshotClass string
}

// ── 主入口 ────────────────────────────────────────────────────────────────────

func runPVC(args []string) {
	if len(args) == 0 {
		printPVCUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "backup":
		runPVCBackup(args[1:])
	case "restore":
		runPVCRestore(args[1:])
	case "list":
		runPVCList(args[1:])
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
	fmt.Println("  kp pvc list    --service <name> [--all]")
	fmt.Println()
	fmt.Println("前置要求（运行 kp doctor 检查）：")
	fmt.Println("  1. 集群已安装 VolumeSnapshot CRD")
	fmt.Println("  2. 存储类支持 CSI snapshot（local-path 不支持）")
	fmt.Println("  3. 至少有一个可用的 VolumeSnapshotClass")
}

// ── kp pvc backup ─────────────────────────────────────────────────────────────

func runPVCBackup(args []string) {
	flags := flag.NewFlagSet("pvc backup", flag.ExitOnError)
	service       := flags.String("service", "", "StatefulSet 服务名（必填）")
	pvcName       := flags.String("pvc", "", "指定备份单个 PVC，留空备份所有")
	snapshotClass := flags.String("snapshot-class", "", "VolumeSnapshotClass，留空使用集群默认")
	timeout       := flags.Duration("timeout", 5*time.Minute, "等待 Snapshot ready 超时时间")
	namespace     := flags.String("namespace", "", "kubernetes namespace")
	context       := flags.String("context", "", "kubernetes context")
	kubeconfig    := flags.String("kubeconfig", "", "kubeconfig 路径")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}
	if *service == "" {
		fmt.Fprintln(os.Stderr, "必须指定 --service")
		os.Exit(1)
	}

	cfg := resolvePVCConfig(*namespace, *context, *kubeconfig)

	// 检查 VolumeSnapshot CRD
	if !checkVolumeSnapshotCRDExists(cfg) {
		P.Fail("集群未安装 VolumeSnapshot CRD，无法创建快照")
		fmt.Printf("%s kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/main/client/config/crd/snapshot.storage.k8s.io_volumesnapshots.yaml\n",
			colorize(colorCyan, "💡 安装："))
		os.Exit(1)
	}

	// 发现 PVC
	pvcs, err := discoverPVCs(cfg, *service, *pvcName)
	if err != nil || len(pvcs) == 0 {
		P.Fail(fmt.Sprintf("未找到服务 %s 绑定的 PVC", *service))
		fmt.Printf("%s 检查标签：kubectl get pvc -n %s -l 'app.kubernetes.io/name=%s'\n",
			colorize(colorCyan, "💡"), cfg.namespace, *service)
		os.Exit(1)
	}

	// 解析 snapshot class
	snapClass := *snapshotClass
	if snapClass == "" {
		snapClass = getDefaultSnapshotClass(cfg)
	}
	if snapClass == "" {
		P.Fail("未找到默认 VolumeSnapshotClass，请通过 --snapshot-class 指定")
		os.Exit(1)
	}

	P.Info("📸", fmt.Sprintf("备份服务 %s 的 PVC（共 %d 个，使用 %s）", *service, len(pvcs), snapClass))
	fmt.Println()

	ts := time.Now().Unix()
	var snapNames []string

	for _, pvc := range pvcs {
		snapName := fmt.Sprintf("%s-snap-%d", pvc, ts)
		P.Start("📸", fmt.Sprintf("创建 Snapshot %s", snapName))

		yaml := buildSnapshotYAML(snapName, pvc, *service, snapClass, cfg.namespace)
		if err := kubectlApplyYAML(cfg, yaml); err != nil {
			P.Fail(fmt.Sprintf("创建 Snapshot %s 失败: %v", snapName, err))
			continue
		}

		// 等待 readyToUse
		if err := waitSnapshotReady(cfg, snapName, *timeout); err != nil {
			P.Fail(fmt.Sprintf("Snapshot %s 等待超时: %v", snapName, err))
			fmt.Printf("  %s kubectl get volumesnapshot %s -n %s\n",
				colorize(colorCyan, "💡 手动检查："), snapName, cfg.namespace)
			continue
		}

		P.Done(fmt.Sprintf("Snapshot %s 就绪", snapName))
		snapNames = append(snapNames, snapName)
	}

	fmt.Println()
	if len(snapNames) == len(pvcs) {
		P.Info("✅", fmt.Sprintf("备份完成，共 %d 个 Snapshot", len(snapNames)))
	} else {
		P.Info("⚠️ ", fmt.Sprintf("备份部分完成：%d/%d 成功", len(snapNames), len(pvcs)))
	}
	for _, s := range snapNames {
		fmt.Printf("    %s\n", s)
	}
}

// ── kp pvc restore ────────────────────────────────────────────────────────────

func runPVCRestore(args []string) {
	flags := flag.NewFlagSet("pvc restore", flag.ExitOnError)
	service      := flags.String("service", "", "StatefulSet 服务名（必填）")
	snapshotName := flags.String("snapshot", "", "指定快照名，留空使用最近快照")
	force        := flags.Bool("force", false, "跳过确认提示")
	namespace    := flags.String("namespace", "", "kubernetes namespace")
	context      := flags.String("context", "", "kubernetes context")
	kubeconfig   := flags.String("kubeconfig", "", "kubeconfig 路径")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}
	if *service == "" {
		fmt.Fprintln(os.Stderr, "必须指定 --service")
		os.Exit(1)
	}

	cfg := resolvePVCConfig(*namespace, *context, *kubeconfig)

	// 找到要恢复的 snapshot
	snap, err := resolveSnapshot(cfg, *service, *snapshotName)
	if err != nil {
		P.Fail(fmt.Sprintf("未找到可用 Snapshot: %v", err))
		fmt.Printf("%s 运行 kp pvc list --service %s 查看快照\n",
			colorize(colorCyan, "💡"), *service)
		os.Exit(1)
	}

	// 获取当前 StatefulSet replicas
	replicas, err := getStatefulSetReplicas(cfg, *service)
	if err != nil {
		P.Fail(fmt.Sprintf("获取 StatefulSet %s 失败: %v", *service, err))
		os.Exit(1)
	}

	// 强制确认
	if !*force {
		fmt.Printf("%s 即将执行以下操作：\n", colorize(colorYellow, "⚠️ "))
		fmt.Printf("  1. 缩容 StatefulSet %s 到 0\n", *service)
		fmt.Printf("  2. 删除旧 PVC %s\n", snap.PVCName)
		fmt.Printf("  3. 从 Snapshot %s 创建新 PVC %s\n", snap.Name, snap.PVCName)
		fmt.Printf("  4. 扩容 StatefulSet %s 回 %d 副本\n", *service, replicas)
		fmt.Print("\n确认继续？(yes/no): ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		if strings.TrimSpace(input) != "yes" {
			fmt.Println("已取消")
			return
		}
	}

	P.Info("🔄", fmt.Sprintf("从 Snapshot %s 恢复 PVC %s", snap.Name, snap.PVCName))
	fmt.Println()

	// Step 1: 缩容到 0
	P.Start("⬇️ ", fmt.Sprintf("缩容 StatefulSet %s 到 0", *service))
	if err := scaleStatefulSet(cfg, *service, 0); err != nil {
		P.Fail(fmt.Sprintf("缩容失败: %v", err))
		os.Exit(1)
	}
	P.Done("缩容完成")

	// Step 2: 删除旧 PVC
	P.Start("🗑 ", fmt.Sprintf("删除旧 PVC %s", snap.PVCName))
	if err := deletePVC(cfg, snap.PVCName); err != nil {
		P.Fail(fmt.Sprintf("删除 PVC 失败: %v", err))
		fmt.Printf("%s 请手动扩容：kubectl scale statefulset %s -n %s --replicas=%d\n",
			colorize(colorYellow, "💡"), *service, cfg.namespace, replicas)
		os.Exit(1)
	}
	P.Done("旧 PVC 已删除")

	// Step 3: 从 Snapshot 创建新 PVC
	P.Start("🆕", fmt.Sprintf("从 Snapshot 创建 PVC %s", snap.PVCName))
	pvcYAML := buildPVCFromSnapshotYAML(snap.PVCName, snap.Name, cfg.namespace)
	if err := kubectlApplyYAML(cfg, pvcYAML); err != nil {
		P.Fail(fmt.Sprintf("创建 PVC 失败: %v", err))
		os.Exit(1)
	}
	P.Done("新 PVC 创建完成")

	// Step 4: 扩容回原副本数
	P.Start("⬆️ ", fmt.Sprintf("扩容 StatefulSet %s 回 %d 副本", *service, replicas))
	if err := scaleStatefulSet(cfg, *service, replicas); err != nil {
		P.Fail(fmt.Sprintf("扩容失败: %v", err))
		os.Exit(1)
	}
	P.Done(fmt.Sprintf("扩容完成，StatefulSet %s 恢复运行", *service))

	fmt.Println()
	P.Info("✅", fmt.Sprintf("恢复完成：%s → %s", snap.Name, snap.PVCName))
}

// ── kp pvc list ───────────────────────────────────────────────────────────────

func runPVCList(args []string) {
	flags := flag.NewFlagSet("pvc list", flag.ExitOnError)
	service    := flags.String("service", "", "按服务过滤，留空列出所有")
	all        := flags.Bool("all", false, "展示所有 Snapshot（忽略 kp 标签过滤）")
	namespace  := flags.String("namespace", "", "kubernetes namespace")
	context    := flags.String("context", "", "kubernetes context")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	cfg := resolvePVCConfig(*namespace, *context, *kubeconfig)

	snaps, err := listSnapshots(cfg, *service, *all)
	if err != nil {
		P.Fail(fmt.Sprintf("查询 Snapshot 失败: %v", err))
		os.Exit(1)
	}

	if len(snaps) == 0 {
		P.Info("📭", "未找到 VolumeSnapshot")
		return
	}

	// 按 CreatedAt 倒序
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].CreatedAt.After(snaps[j].CreatedAt)
	})

	fmt.Printf("  %-35s  %-25s  %-20s  %-20s  %s\n",
		"NAME", "PVC", "SERVICE", "CREATED AT", "READY")
	fmt.Printf("  %s\n", strings.Repeat("─", 110))
	for _, s := range snaps {
		ready := colorize(colorGreen, "True")
		if !s.ReadyToUse {
			ready = colorize(colorYellow, "False")
		}
		fmt.Printf("  %-35s  %-25s  %-20s  %-20s  %s\n",
			s.Name,
			s.PVCName,
			s.Service,
			s.CreatedAt.Local().Format("2006-01-02 15:04:05"),
			ready,
		)
	}
	fmt.Printf("\n  共 %d 个 Snapshot\n", len(snaps))
}

// ── 辅助函数 ──────────────────────────────────────────────────────────────────

type pvcConfig struct {
	namespace  string
	context    string
	kubeconfig string
}

func resolvePVCConfig(ns, ctx, kc string) pvcConfig {
	if ns == "" {
		root, err := projectRoot()
		if err == nil {
			env, _ := readEnvFile(root + "/configs/project.env")
			ns = envOrDefault(env, "KUBE_NAMESPACE", "default")
			if ctx == "" {
				ctx = env["KUBE_CONTEXT"]
			}
		}
	}
	return pvcConfig{namespace: ns, context: ctx, kubeconfig: expandHome(kc)}
}

func kubectlPVCArgs(cfg pvcConfig) []string {
	args := []string{"kubectl"}
	if cfg.kubeconfig != "" {
		args = append(args, "--kubeconfig", cfg.kubeconfig)
	}
	if cfg.context != "" {
		args = append(args, "--context", cfg.context)
	}
	return args
}

// checkVolumeSnapshotCRDExists 检查 CRD 是否存在
func checkVolumeSnapshotCRDExists(cfg pvcConfig) bool {
	args := kubectlPVCArgs(cfg)
	args = append(args, "get", "crd",
		"volumesnapshots.snapshot.storage.k8s.io",
		"--ignore-not-found", "-o", "name")
	out, err := runOutput(args...)
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// discoverPVCs 发现服务下的 PVC
// 优先用 app.kubernetes.io/name，同时尝试 app=<service>
func discoverPVCs(cfg pvcConfig, service, specificPVC string) ([]string, error) {
	if specificPVC != "" {
		return []string{specificPVC}, nil
	}

	args := kubectlPVCArgs(cfg)
	args = append(args, "get", "pvc",
		"--namespace", cfg.namespace,
		"-l", fmt.Sprintf("app.kubernetes.io/name=%s", service),
		"-o", "jsonpath={.items[*].metadata.name}",
	)
	out, err := runOutput(args...)
	names := strings.Fields(strings.TrimSpace(string(out)))
	if err == nil && len(names) > 0 {
		return names, nil
	}

	// 降级：尝试 app=<service>
	args2 := kubectlPVCArgs(cfg)
	args2 = append(args2, "get", "pvc",
		"--namespace", cfg.namespace,
		"-l", fmt.Sprintf("app=%s", service),
		"-o", "jsonpath={.items[*].metadata.name}",
	)
	out2, err2 := runOutput(args2...)
	names2 := strings.Fields(strings.TrimSpace(string(out2)))
	if err2 == nil && len(names2) > 0 {
		return names2, nil
	}

	return nil, fmt.Errorf("未找到 PVC")
}

// getDefaultSnapshotClass 获取默认 VolumeSnapshotClass
func getDefaultSnapshotClass(cfg pvcConfig) string {
	args := kubectlPVCArgs(cfg)
	args = append(args,
		"get", "volumesnapshotclass",
		"-o", `jsonpath={.items[?(@.metadata.annotations.snapshot\.storage\.kubernetes\.io/is-default-class=="true")].metadata.name}`,
	)
	out, err := runOutput(args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// buildSnapshotYAML 构建 VolumeSnapshot YAML
func buildSnapshotYAML(name, pvcName, service, snapClass, namespace string) string {
	return fmt.Sprintf(`apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: %s
  namespace: %s
  labels:
    kp.io/service: %s
    kp.io/pvc: %s
spec:
  volumeSnapshotClassName: %s
  source:
    persistentVolumeClaimName: %s
`, name, namespace, service, pvcName, snapClass, pvcName)
}

// buildPVCFromSnapshotYAML 从 Snapshot 创建 PVC 的 YAML
func buildPVCFromSnapshotYAML(pvcName, snapName, namespace string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  dataSource:
    name: %s
    kind: VolumeSnapshot
    apiGroup: snapshot.storage.k8s.io
`, pvcName, namespace, snapName)
}

// kubectlApplyYAML 通过 stdin 执行 kubectl apply
func kubectlApplyYAML(cfg pvcConfig, yaml string) error {
	args := kubectlPVCArgs(cfg)
	args = append(args, "apply", "-f", "-")
	// 用临时文件写入
	f, err := os.CreateTemp("", "kp-pvc-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(yaml); err != nil {
		return err
	}
	f.Close()

	args2 := kubectlPVCArgs(cfg)
	args2 = append(args2, "apply", "-f", f.Name())
	_, err = runOutput(args2...)
	return err
}

// waitSnapshotReady 等待 Snapshot readyToUse=true
func waitSnapshotReady(cfg pvcConfig, snapName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	tick := 5 * time.Second
	elapsed := 0 * time.Second

	for time.Now().Before(deadline) {
		args := kubectlPVCArgs(cfg)
		args = append(args, "get", "volumesnapshot", snapName,
			"--namespace", cfg.namespace,
			"-o", "jsonpath={.status.readyToUse}",
		)
		out, err := runOutput(args...)
		if err == nil && strings.TrimSpace(string(out)) == "true" {
			return nil
		}
		fmt.Printf("\r  等待 Snapshot ready... 已等待 %ds", int(elapsed.Seconds()))
		time.Sleep(tick)
		elapsed += tick
	}
	fmt.Println()
	return fmt.Errorf("等待超时（%s）", timeout)
}

// resolveSnapshot 找到要恢复的 Snapshot
func resolveSnapshot(cfg pvcConfig, service, snapName string) (*volumeSnapshot, error) {
	if snapName != "" {
		snaps, err := listSnapshots(cfg, service, false)
		if err != nil {
			return nil, err
		}
		for _, s := range snaps {
			if s.Name == snapName {
				return &s, nil
			}
		}
		return nil, fmt.Errorf("未找到 Snapshot %s", snapName)
	}

	// 取最近 readyToUse=true 的
	snaps, err := listSnapshots(cfg, service, false)
	if err != nil || len(snaps) == 0 {
		return nil, fmt.Errorf("未找到 Snapshot")
	}
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].CreatedAt.After(snaps[j].CreatedAt)
	})
	for _, s := range snaps {
		if s.ReadyToUse {
			return &s, nil
		}
	}
	return nil, fmt.Errorf("未找到 readyToUse=true 的 Snapshot")
}

// listSnapshots 列出 Snapshot
func listSnapshots(cfg pvcConfig, service string, all bool) ([]volumeSnapshot, error) {
	args := kubectlPVCArgs(cfg)
	args = append(args, "get", "volumesnapshot",
		"--namespace", cfg.namespace,
		"-o", "json",
	)
	if !all && service != "" {
		args = append(args, "-l", fmt.Sprintf("kp.io/service=%s", service))
	} else if !all {
		args = append(args, "-l", "kp.io/service")
	}

	out, err := runOutput(args...)
	if err != nil {
		return nil, err
	}

	var list struct {
		Items []struct {
			Metadata struct {
				Name              string            `json:"name"`
				Labels            map[string]string `json:"labels"`
				CreationTimestamp string            `json:"creationTimestamp"`
			} `json:"metadata"`
			Spec struct {
				VolumeSnapshotClassName string `json:"volumeSnapshotClassName"`
				Source                  struct {
					PersistentVolumeClaimName string `json:"persistentVolumeClaimName"`
				} `json:"source"`
			} `json:"spec"`
			Status struct {
				ReadyToUse *bool `json:"readyToUse"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, err
	}

	var result []volumeSnapshot
	for _, item := range list.Items {
		t, _ := time.Parse(time.RFC3339, item.Metadata.CreationTimestamp)
		ready := item.Status.ReadyToUse != nil && *item.Status.ReadyToUse
		result = append(result, volumeSnapshot{
			Name:          item.Metadata.Name,
			PVCName:       item.Metadata.Labels["kp.io/pvc"],
			Service:       item.Metadata.Labels["kp.io/service"],
			CreatedAt:     t,
			ReadyToUse:    ready,
			SnapshotClass: item.Spec.VolumeSnapshotClassName,
		})
	}
	return result, nil
}

// getStatefulSetReplicas 获取当前副本数
func getStatefulSetReplicas(cfg pvcConfig, service string) (int, error) {
	args := kubectlPVCArgs(cfg)
	args = append(args, "get", "statefulset", service,
		"--namespace", cfg.namespace,
		"-o", "jsonpath={.spec.replicas}",
	)
	out, err := runOutput(args...)
	if err != nil {
		return 0, err
	}
	var n int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n)
	return n, nil
}

// scaleStatefulSet 缩/扩容 StatefulSet
func scaleStatefulSet(cfg pvcConfig, service string, replicas int) error {
	args := kubectlPVCArgs(cfg)
	args = append(args, "scale", "statefulset", service,
		"--namespace", cfg.namespace,
		fmt.Sprintf("--replicas=%d", replicas),
	)
	_, err := runOutput(args...)
	return err
}

// deletePVC 删除 PVC
func deletePVC(cfg pvcConfig, pvcName string) error {
	args := kubectlPVCArgs(cfg)
	args = append(args, "delete", "pvc", pvcName,
		"--namespace", cfg.namespace,
		"--wait=true",
	)
	_, err := runOutput(args...)
	return err
}
