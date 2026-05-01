package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/Ixecd/kubepivot/internal/audit"
	"github.com/Ixecd/kubepivot/internal/rbac"
	"github.com/Ixecd/kubepivot/internal/controller"
	"github.com/Ixecd/kubepivot/internal/executor"
	"github.com/Ixecd/kubepivot/internal/route"
	"github.com/Ixecd/kubepivot/internal/state"
	"github.com/google/uuid"
)

// SandboxSession 沙盒会话
type SandboxSession struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Namespace string    `json:"namespace"`
	Phase     string    `json:"phase"`
	StartedAt time.Time `json:"started_at"`
	TTL       int       `json:"ttl_seconds"` // 默认 3600s，超期 controller 自动 GC
}

func runSandbox(args []string) {
	if len(args) == 0 {
		fmt.Println("用法: kp sandbox <子命令>")
		fmt.Println("  start   启动沙盒会话（LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING）")
		fmt.Println("  status  查看当前沙盒状态")
		fmt.Println("  unlock  强制解锁（COMMITTING 阶段禁止）")
		os.Exit(1)
	}
	switch args[0] {
	case "start":
		runSandboxStart(args[1:])
	case "status":
		runSandboxStatus(args[1:])
	case "unlock":
		runSandboxUnlock(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n", args[0])
		os.Exit(1)
	}
}

func runSandboxStart(args []string) {
	flags := flag.NewFlagSet("sandbox start", flag.ExitOnError)
	namespace := flags.String("namespace", "", "kubernetes namespace")
	contextFlag := flags.String("context", "", "kubernetes context")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	dryRun := flags.Bool("dry-run", false, "只打印执行计划，不实际执行")
	fromEnv := flags.String("from-env", "", "从指定 env 读取已验证 traffic 配置 (v2.6.1)")
	flags.Parse(args)

	root, err := Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	cfg := &deployConfig{
		namespace:  *namespace,
		context:    *contextFlag,
		kubeconfig: *kubeconfig,
	}
	resolveDeployConfig(cfg, env, root)

	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))
	version := envOrDefault(env, "VERSION", "v0.1.0")

	store := state.NewAutoStore(env["ETCD_ENDPOINTS"])
	// v2.8 B.7.2 + B.3 + B.7.3: sandbox 操作 (PermSandbox)
	mustCheck(audit.ResolveActor(), cfg.namespace, rbac.PermSandbox)

	sm, err := state.New(store, projectName, cfg.namespace, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载状态失败:", err)
		os.Exit(1)
	}

	// 检查当前状态是否允许进入 LOCKED
	cur := sm.State()
	if cur != state.StateIdle && cur != state.StateRunning {
		fmt.Fprintf(os.Stderr, "❌ 当前状态 %s 不允许启动沙盒（需要 IDLE 或 RUNNING）\n", cur)
		os.Exit(1)
	}

	sandboxID := uuid.New().String()[:8]
	session := &SandboxSession{
		ID:        sandboxID,
		Project:   projectName,
		Namespace: cfg.namespace,
		Phase:     "LOCKED",
		StartedAt: time.Now(),
		TTL:       3600,
	}

	// v2.6.1 Step 3: --from-env 加载 (Q9=A 进 LOCKED 之前读)
	var fromEnvTraffic *controller.Traffic
	if *fromEnv != "" {
		if *dryRun {
			P.Info("📥", fmt.Sprintf("[dry-run] 将从 env %q 读取 verified-traffic", *fromEnv))
		} else {
			loadCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			traffic, source, err := loadVerifiedTrafficFromEnv(loadCtx, *fromEnv)
			cancel()
			if err != nil {
				P.Fail(fmt.Sprintf("--from-env %s 加载失败: %v", *fromEnv, err))
				os.Exit(1)
			}
			fromEnvTraffic = traffic
			P.Info("📥", fmt.Sprintf("已从 env %q 加载 verified-traffic (running_since=%s, version=%s, services=%s)",
				*fromEnv, source.RunningSince, source.Version, extractServiceNames(traffic.Routes)))
		}
	}

	if *dryRun {
		fmt.Printf("📋 Sandbox 执行计划（dry-run）\n\n")
		fmt.Printf("  Sandbox ID:  %s\n", sandboxID)
		fmt.Printf("  项目:        %s\n", projectName)
		fmt.Printf("  Namespace:   %s\n", cfg.namespace)
		fmt.Printf("  TTL:         %ds\n\n", session.TTL)
		fmt.Printf("  阶段流转：\n")
		fmt.Printf("  %s LOCKED       → 获取分布式锁，阻止其他 kp deploy\n", colorize(colorCyan, "1."))
		fmt.Printf("  %s SNAPSHOTTING → 触发 kp pvc backup（有 CSI 才执行）\n", colorize(colorCyan, "2."))
		fmt.Printf("  %s SIMULATING   → 临时 Job 预跑 DB 迁移（postgres 事务 DDL）\n", colorize(colorCyan, "3."))
		fmt.Printf("  %s COMMITTING   → 真实 migrate + helm upgrade\n", colorize(colorCyan, "4."))
		fmt.Printf("  %s RUNNING      → 成功，释放锁\n\n", colorize(colorCyan, "5."))
		fmt.Printf("  %s COMMITTING 阶段禁止 force-unlock\n", colorize(colorYellow, "⚠️ "))
		return
	}

	// Step 1: LOCKED
	P.Start("🔒", fmt.Sprintf("获取沙盒锁（sandbox-id: %s）", sandboxID))
	if err := sm.Transition(state.StateLocked,
		fmt.Sprintf("sandbox start: %s", sandboxID)); err != nil {
		P.Fail(fmt.Sprintf("无法进入 LOCKED 状态: %v", err))
		os.Exit(1)
	}
	P.Done(fmt.Sprintf("已锁定，sandbox-id: %s", sandboxID))

	// 写 session 文件（Controller GC 用）
	writeSandboxSession(session, root)

	env["SANDBOX_ID"] = sandboxID

	// Step 2: SNAPSHOTTING
	P.Start("📸", "触发 PVC 快照")
	sm.Transition(state.StateSnapshotting, "sandbox: 开始快照")
	snapshotOK := trySandboxSnapshot(cfg, root, env)
	if !snapshotOK {
		P.Info("⚠️ ", "PVC 快照跳过（无 CSI 或快照失败），继续执行")
	} else {
		P.Done("PVC 快照完成")
	}

	// Step 3: SIMULATING
	P.Start("🧪", "预跑 DB 迁移（postgres 事务，失败自动回滚）")
	sm.Transition(state.StateSimulating, "sandbox: 开始迁移模拟")
	simOK := runMigrationSimulation(cfg, root, env)
	if !simOK {
		P.Fail("迁移模拟失败，触发 RESTORING")
		sm.Transition(state.StateRestoring, "sandbox: 迁移模拟失败")
		runSandboxRestore(cfg, root, env, sandboxID)
		sm.Transition(state.StateIdle, "sandbox: 已恢复")
		cleanSandboxSession(sandboxID, root)
		os.Exit(1)
	}
	P.Done("迁移模拟通过")

	// Step 4: COMMITTING
	P.Start("🚀", "执行真实迁移 + 部署（COMMITTING，此阶段禁止 force-unlock）")
	sm.Transition(state.StateCommitting, "sandbox: 开始 commit")
	commitOK := runSandboxCommit(cfg, root, env, version)
	if !commitOK {
		P.Fail("Commit 失败，触发 RESTORING")
		sm.Transition(state.StateRestoring, "sandbox: commit 失败")
		runSandboxRestore(cfg, root, env, sandboxID)
		sm.Transition(state.StateIdle, "sandbox: 已恢复")
		cleanSandboxSession(sandboxID, root)
		os.Exit(1)
	}

	// Step 4.5: 蓝绿流量切换 (v2.6.0 漏接, v2.6.1 Step 0 补)
	// v2.6.1 Step 3: fromEnvTraffic != nil 时 (用户传了 --from-env) 覆盖本地 traffic
	if err := runBlueGreenSwitch(cfg, root, fromEnvTraffic); err != nil {
		P.Fail(fmt.Sprintf("流量切换失败，触发 RESTORING: %v", err))
		sm.Transition(state.StateRestoring, "sandbox: 蓝绿切换失败")
		runSandboxRestore(cfg, root, env, sandboxID)
		sm.Transition(state.StateIdle, "sandbox: 已恢复")
		cleanSandboxSession(sandboxID, root)
		os.Exit(1)
	}

	// Step 5: RUNNING
	sm.Transition(state.StateRunning, "sandbox: commit 成功")
	P.Done("沙盒会话完成，状态: RUNNING")
	cleanSandboxSession(sandboxID, root)

	fmt.Println()
	P.Info("✅", fmt.Sprintf("Operation Sandbox 完成（sandbox-id: %s）", sandboxID))
}

func runSandboxStatus(args []string) {
	flags := flag.NewFlagSet("sandbox status", flag.ExitOnError)
	namespace := flags.String("namespace", "", "kubernetes namespace")
	flags.Parse(args)

	root, err := Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))
	ns := *namespace
	if ns == "" {
		ns = envOrDefault(env, "KUBE_NAMESPACE", projectName)
	}
	version := envOrDefault(env, "VERSION", "v0.1.0")

	store := state.NewAutoStore(env["ETCD_ENDPOINTS"])
	sm, err := state.New(store, projectName, ns, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载状态失败:", err)
		os.Exit(1)
	}

	cur := sm.State()
	sandboxStates := map[state.State]bool{
		state.StateLocked: true, state.StateSnapshotting: true,
		state.StateSimulating: true, state.StateCommitting: true,
		state.StateRestoring: true,
	}

	if !sandboxStates[cur] {
		fmt.Printf("当前无活跃沙盒会话（状态: %s）\n", cur)
		return
	}

	fmt.Printf("  沙盒状态:   %s\n", colorize(colorCyan, string(cur)))
	fmt.Printf("  项目:       %s\n", projectName)
	fmt.Printf("  Namespace:  %s\n", ns)

	if cur == state.StateCommitting {
		fmt.Printf("\n  %s 当前处于 COMMITTING 阶段，禁止 force-unlock\n",
			colorize(colorRed, "⚠️ "))
	}
}

func runSandboxUnlock(args []string) {
	flags := flag.NewFlagSet("sandbox unlock", flag.ExitOnError)
	namespace := flags.String("namespace", "", "kubernetes namespace")
	reason := flags.String("reason", "", "解锁原因（必填）")
	force := flags.Bool("force", false, "强制解锁（COMMITTING 阶段仍然禁止）")
	flags.Parse(args)

	if *reason == "" {
		fmt.Fprintln(os.Stderr, "❌ --reason 必填")
		os.Exit(1)
	}

	root, err := Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))
	ns := *namespace
	if ns == "" {
		ns = envOrDefault(env, "KUBE_NAMESPACE", projectName)
	}
	version := envOrDefault(env, "VERSION", "v0.1.0")

	store := state.NewAutoStore(env["ETCD_ENDPOINTS"])
	sm, err := state.New(store, projectName, ns, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载状态失败:", err)
		os.Exit(1)
	}

	cur := sm.State()

	// COMMITTING 阶段永远禁止 force-unlock
	if cur == state.StateCommitting {
		fmt.Fprintf(os.Stderr,
			"❌ COMMITTING 阶段禁止 force-unlock（DB 正在迁移，强制解锁可能导致数据不一致）\n"+
				"   请等待操作完成，或联系 DBA 确认 DB 状态后手动恢复\n")
		os.Exit(1)
	}

	if !*force {
		fmt.Fprintf(os.Stderr, "❌ 需要 --force 确认解锁（当前状态: %s）\n", cur)
		os.Exit(1)
	}

	// 允许解锁的状态
	allowed := map[state.State]bool{
		state.StateLocked: true, state.StateSnapshotting: true,
		state.StateSimulating: true,
	}
	if !allowed[cur] {
		fmt.Fprintf(os.Stderr, "❌ 当前状态 %s 不支持 force-unlock\n", cur)
		os.Exit(1)
	}

	if err := sm.ForceState(state.StateIdle,
		fmt.Sprintf("force-unlock: %s", *reason)); err != nil {
		fmt.Fprintln(os.Stderr, "解锁失败:", err)
		os.Exit(1)
	}

	P.Info("🔓", fmt.Sprintf("已强制解锁（%s → IDLE），原因: %s", cur, *reason))
}

// ── 内部实现 ──────────────────────────────────────────────────────────────────

func trySandboxSnapshot(cfg *deployConfig, root string, env map[string]string) bool {
	// 检查是否有 CSI VolumeSnapshot 能力
	args := kubectlBaseArgs(cfg.kubeconfig, cfg.context, "")
	args = append(args, "get", "crd", "volumesnapshots.snapshot.storage.k8s.io",
		"--ignore-not-found", "-o", "name")
	out, err := runOutput(args...)
	if err != nil || len(out) == 0 {
		return false
	}
	// 有 CSI，触发 kp pvc backup
	pvcArgs := []string{"kp", "pvc", "backup",
		"--namespace", cfg.namespace,
		"--context", cfg.context,
	}
	if cfg.kubeconfig != "" {
		pvcArgs = append(pvcArgs, "--kubeconfig", cfg.kubeconfig)
	}
	_, err = runOutput(pvcArgs...)
	return err == nil
}

func runMigrationSimulation(cfg *deployConfig, root string, env map[string]string) bool {
	dbURL := resolveDatabaseURL(&migrateConfig{}, root, env)
	if dbURL == "" {
		P.Info("⏭ ", "未配置 DATABASE_URL，跳过迁移模拟")
		return true
	}

	migDir := findMigrationsDir(root)
	if migDir == "" {
		P.Info("⏭ ", "未找到迁移目录，跳过迁移模拟")
		return true
	}

	// 生成临时 Job YAML
	sandboxID := env["SANDBOX_ID"]
	jobName := fmt.Sprintf("kp-migrate-sim-%s", sandboxID[:6])
	projectName := envOrDefault(env, "PROJECT_NAME", "app")

	// 从 Secret 获取 DB 连接信息
	secretName := projectName + "-secret"

	jobYAML := fmt.Sprintf(`apiVersion: batch/v1
kind: Job
metadata:
  name: %s
  namespace: %s
  labels:
    kubepivot.io/sandbox-id: %s
    kubepivot.io/role: migrate-sim
spec:
  ttlSecondsAfterFinished: 300
  backoffLimit: 0
  template:
    metadata:
      labels:
        kubepivot.io/sandbox-id: %s
    spec:
      restartPolicy: Never
      containers:
      - name: migrate-sim
        image: ghcr.io/golang-migrate/migrate:v4
        command: ["migrate"]
        args:
        - "-path=/migrations"
        - "-database=$(DATABASE_URL)"
        - "up"
        env:
        - name: DATABASE_URL
          valueFrom:
            secretKeyRef:
              name: %s
              key: DATABASE_URL
        volumeMounts:
        - name: migrations
          mountPath: /migrations
          readOnly: true
      volumes:
      - name: migrations
        configMap:
          name: %s-migrations
`, jobName, cfg.namespace, sandboxID, sandboxID, secretName, jobName)

	// 先创建 migrations ConfigMap
	if !createMigrationsConfigMap(cfg, jobName+"-migrations", migDir, sandboxID) {
		P.Info("⚠️ ", "创建迁移 ConfigMap 失败，降级为 dry-run 模式")
		return runMigrationSimDryRun(root, env)
	}

	// apply Job
	f, err := os.CreateTemp("", "kp-sim-job-*.yaml")
	if err != nil {
		return runMigrationSimDryRun(root, env)
	}
	defer os.Remove(f.Name())
	f.WriteString(jobYAML)
	f.Close()

	args := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	args = append(args, "apply", "-f", f.Name())
	if _, err := runOutput(args...); err != nil {
		P.Info("⚠️ ", "Job 创建失败，降级为 dry-run 模式")
		return runMigrationSimDryRun(root, env)
	}

	// 等待 Job 完成（最多 5 分钟）
	P.Start("⏳", fmt.Sprintf("等待迁移模拟 Job 完成（%s）", jobName))
	waitArgs := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	waitArgs = append(waitArgs, "wait", "job/"+jobName,
		"--for=condition=complete", "--timeout=300s")
	if _, err := runOutput(waitArgs...); err != nil {
		P.Fail("迁移模拟 Job 失败或超时")
		// 清理 Job
		cleanArgs := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
		cleanArgs = append(cleanArgs, "delete", "job", jobName, "--ignore-not-found")
		runOutput(cleanArgs...)
		return false
	}
	P.Done("迁移模拟通过")

	// 清理 Job 和 ConfigMap
	cleanArgs := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	cleanArgs = append(cleanArgs, "delete", "job", jobName,
		"configmap", jobName+"-migrations", "--ignore-not-found")
	runOutput(cleanArgs...)
	return true
}

// createMigrationsConfigMap 将迁移文件打包进 ConfigMap
func createMigrationsConfigMap(cfg *deployConfig, name, migDir, sandboxID string) bool {
	args := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	args = append(args, "create", "configmap", name,
		"--from-file="+migDir,
		"--dry-run=client", "-o", "yaml")
	out, err := runOutput(args...)
	if err != nil {
		return false
	}

	// 加 sandbox-id label
	yaml := string(out) + fmt.Sprintf(
		"\n  labels:\n    kubepivot.io/sandbox-id: %s\n", sandboxID)

	f, err := os.CreateTemp("", "kp-cm-*.yaml")
	if err != nil {
		return false
	}
	defer os.Remove(f.Name())
	f.WriteString(yaml)
	f.Close()

	applyArgs := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	applyArgs = append(applyArgs, "apply", "-f", f.Name())
	_, err = runOutput(applyArgs...)
	return err == nil
}

// runMigrationSimDryRun dry-run 降级模式
func runMigrationSimDryRun(root string, env map[string]string) bool {
	P.Info("💡", "使用 dry-run 降级模式（不创建 K8s Job）")
	_, err := runOutput("kp", "migrate", "run", "--dry-run")
	return err == nil
}

func runSandboxCommit(cfg *deployConfig, root string, env map[string]string, version string) bool {
	// 真实迁移
	dbURL := resolveDatabaseURL(&migrateConfig{}, root, env)
	if dbURL != "" {
		if _, err := runOutput("kp", "migrate", "run"); err != nil {
			return false
		}
	}
	// helm upgrade（kp deploy）
	deployArgs := []string{"kp", "deploy",
		"--namespace", cfg.namespace,
	}
	if cfg.context != "" {
		deployArgs = append(deployArgs, "--context", cfg.context)
	}
	if cfg.kubeconfig != "" {
		deployArgs = append(deployArgs, "--kubeconfig", cfg.kubeconfig)
	}
	_, err := runOutput(deployArgs...)
	return err == nil
}

func runSandboxRestore(cfg *deployConfig, root string, env map[string]string, sandboxID string) {
	P.Start("⏪", "恢复 PVC 快照 + helm rollback")
	// kp pvc restore
	runOutput("kp", "pvc", "restore", "--namespace", cfg.namespace)
	// helm rollback
	runOutput("kp", "rollback", "--namespace", cfg.namespace)
	P.Done("恢复完成")
}

func writeSandboxSession(session *SandboxSession, root string) {
	dir := filepath.Join(root, ".kp", "sandbox")
	os.MkdirAll(dir, 0o755)
	// 简单写文件，Controller GC 用
	path := filepath.Join(dir, session.ID+".json")
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, `{"id":"%s","project":"%s","namespace":"%s","started_at":"%s","ttl":%d}`,
		session.ID, session.Project, session.Namespace,
		session.StartedAt.Format(time.RFC3339), session.TTL)
}

func cleanSandboxSession(sandboxID, root string) {
	path := filepath.Join(root, ".kp", "sandbox", sandboxID+".json")
	os.Remove(path)
}

// runBlueGreenSwitch 在 COMMITTING 阶段执行蓝绿流量切换。
//
// 流程：
//  1. 读取 configs/resources.yaml
//  2. 检查 Traffic 字段——未配置 / 不是 blue-green → 跳过（return nil）
//  3. 构造 Provider（自动检测或显式 kind）
//  4. ApplyRoutes 切换流量
//  5. 等 Pod ready（health check）
//
// 任何步骤失败都返回 error，由调用方触发 RESTORING。
//
// 不打印 "跳过" 日志——蓝绿不是必选项，不配置就静默放过。
func runBlueGreenSwitch(cfg *deployConfig, root string, override *controller.Traffic) error {
	resourcesPath := filepath.Join(root, "configs", "resources.yaml")
	if _, err := os.Stat(resourcesPath); errors.Is(err, fs.ErrNotExist) {
		// v2.6.1 Q12: --from-env 但项目无 resources.yaml → fail-fast
		if override != nil {
			return fmt.Errorf("项目无 resources.yaml, 不能用 --from-env (hint: kp init 或确认 configs/resources.yaml 存在)")
		}
		return nil // 没有 resources.yaml = 老项目，跳过
	}
	rc, err := controller.LoadResources(resourcesPath)
	if err != nil {
		return fmt.Errorf("读取 resources.yaml 失败: %w", err)
	}

	if !rc.HasBlueGreen() {
		// v2.6.1 Q12: --from-env 但项目自身没声明蓝绿 → fail-fast
		if override != nil {
			return fmt.Errorf("项目自身 resources.yaml 没声明蓝绿 traffic, 不能用 --from-env (hint: 先在 resources.yaml 声明 traffic 字段, 详见 docs/design/traffic-layer.md)")
		}
		return nil // 未启用，安静跳过
	}

	// v2.6.1 Step 3: --from-env 注入覆盖 (在 HasBlueGreen gate 之后, 保证 prod 自身也声明了 traffic)
	if override != nil {
		rc.Traffic = override
		P.Info("📥", fmt.Sprintf("使用 --from-env 注入的 traffic (services: %s)",
			extractServiceNames(override.Routes)))
	}

	ctx := context.Background()
	provider, err := route.ProviderForKind(ctx, rc.Traffic.Kind)
	if err != nil {
		return fmt.Errorf("流量层 provider 不可用: %w", err)
	}
	P.Info("🔀", fmt.Sprintf("蓝绿流量切换 (provider=%s)", provider.Name()))

	// yaml route → route.Route
	routes := make([]route.Route, 0, len(rc.Traffic.Routes))
	for _, r := range rc.Traffic.Routes {
		routes = append(routes, route.Route{
			Service: r.Service,
			Weight:  r.Weight,
		})
	}

	// 解析目标 Ingress / HTTPRoute 的 ns + name
	ns := cfg.namespace
	if rc.Traffic.Refs.Namespace != "" {
		ns = rc.Traffic.Refs.Namespace
	}
	name := rc.Traffic.Refs.Name

	P.Start("⚡", fmt.Sprintf("切换流量: %s/%s", ns, name))
	if err := provider.ApplyRoutes(ctx, ns, name, routes); err != nil {
		P.Fail("切流失败")
		return fmt.Errorf("ApplyRoutes 失败: %w", err)
	}
	P.Done("流量切换完成")

	// Pod ready 健康判定
	timeoutSec := rc.Traffic.Validation.PodReadyTimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 60
	}

	var greenService string
	for _, r := range routes {
		if r.Weight == 100 {
			greenService = r.Service
			break
		}
	}
	if greenService == "" {
		// 蓝绿场景应该有 weight=100，没有也不阻塞
		return nil
	}

	P.Start("🏥", fmt.Sprintf("等待 %s Pod ready (timeout=%ds)", greenService, timeoutSec))
	if err := waitDeploymentReady(ctx, ns, greenService, time.Duration(timeoutSec)*time.Second); err != nil {
		P.Fail("Pod ready 超时")
		return fmt.Errorf("waitDeploymentReady: %w", err)
	}
	P.Done("Pod ready")

	return nil
}

// waitDeploymentReady 轮询等待 Deployment 完全 ready。
//
// 实现：用 kubectl rollout status，与项目其他地方一致。
// 蓝绿约定：deployment 名等于 service 名（后缀法）。
func waitDeploymentReady(ctx context.Context, ns, name string, timeout time.Duration) error {
	timeoutSec := int(timeout / time.Second)
	if timeoutSec < 5 {
		timeoutSec = 5
	}
	e := executor.GetExecutor()
	_, err := e.Kubectl(ctx, "",
		"rollout", "status",
		"deployment", name,
		"-n", ns,
		fmt.Sprintf("--timeout=%ds", timeoutSec),
	)
	if err != nil {
		return fmt.Errorf("kubectl rollout status: %w", err)
	}
	return nil
}
