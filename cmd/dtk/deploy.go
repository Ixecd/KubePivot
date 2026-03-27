package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ixecd/dev-toolkit/internal/controller"
	"github.com/Ixecd/dev-toolkit/internal/planner"
	"github.com/Ixecd/dev-toolkit/internal/state"
)

// deployConfig 部署参数
type deployConfig struct {
	components string
	namespace  string
	context    string
	kubeconfig string
	dryRun     bool
}

func runDeploy(args []string) {
	if err := checkDeps(deployDeps); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	flags := flag.NewFlagSet("deploy", flag.ExitOnError)
	cfg := &deployConfig{}
	flags.StringVar(&cfg.components, "components", "configs/components.yaml", "components config path")
	flags.StringVar(&cfg.namespace, "namespace", "", "kubernetes namespace")
	flags.StringVar(&cfg.context, "context", "", "kubernetes context")
	flags.StringVar(&cfg.kubeconfig, "kubeconfig", "", "kubeconfig 文件路径")
	flags.BoolVar(&cfg.dryRun, "dry-run", false, "print plan only, do not deploy")

	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "解析参数失败:", err)
		os.Exit(1)
	}

	cfg.kubeconfig = expandHome(cfg.kubeconfig)

	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	resolveDeployConfig(cfg, env, root)

	plan, err := planner.BuildPlan(filepath.Join(root, cfg.components))
	if err != nil {
		fmt.Fprintln(os.Stderr, "解析组件失败:", err)
		os.Exit(1)
	}

	printPlan(plan)
	if cfg.dryRun {
		return
	}

	version := envOrDefault(env, "VERSION", "v0.1.0")

	// 初始化状态机
	store := state.NewAutoStore(env["ETCD_ENDPOINTS"])
	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))

	sm, err := state.New(store, projectName, cfg.namespace, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "初始化状态机失败:", err)
		os.Exit(1)
	}

	sm.MarkFirstDeploy(!namespaceExists(cfg.kubeconfig, cfg.context, cfg.namespace))

	if err := checkPendingRollback(cfg, env, sm); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 检查当前状态，拒绝重复部署
	current := sm.State()
	if current != state.StateIdle && current != state.StateRunning && current != state.StateTerminated {
		fmt.Fprintf(os.Stderr, "当前部署状态为 %s，不能发起新部署\n如需继续，请运行: dtk resume\n", current)
		os.Exit(1)
	}

	if err := executeDeploy(sm, cfg, env, plan, root); err != nil {
		fmt.Fprintln(os.Stderr, "部署失败:", err)
		os.Exit(1)
	}
}

func runResume(args []string) {
	flags := flag.NewFlagSet("resume", flag.ExitOnError)
	namespace := flags.String("namespace", "", "kubernetes namespace")
	context := flags.String("context", "", "kubernetes context")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 文件路径")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "解析参数失败:", err)
		os.Exit(1)
	}
	*kubeconfig = expandHome(*kubeconfig)

	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	cfg := &deployConfig{
		components: "configs/components.yaml",
		namespace:  *namespace,
		context:    *context,
		kubeconfig: *kubeconfig,
	}
	resolveDeployConfig(cfg, env, root)

	version := envOrDefault(env, "VERSION", "v0.1.0")
	store := state.NewAutoStore(env["ETCD_ENDPOINTS"])
	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))

	sm, err := state.New(store, projectName, cfg.namespace, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载状态失败:", err)
		os.Exit(1)
	}

	// 推荐写法：单独定义 record，提升可读性
	record := sm.Record()
	fmt.Printf("当前状态: %s（%s）\n", record.State, record.Reason)
	fmt.Println("检查 K8s 实际状态...")

	plan, err := planner.BuildPlan(filepath.Join(root, cfg.components))
	if err != nil {
		fmt.Fprintln(os.Stderr, "解析组件失败:", err)
		os.Exit(1)
	}

	// 使用状态机统一检查（基于 resources.yaml）
	actual, err := controller.DetectActualState(
		cfg.kubeconfig,
		cfg.namespace,
		filepath.Join(root, "configs", "resources.yaml"),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "检测 K8s 状态失败:", err)
		os.Exit(1)
	}
	fmt.Printf("K8s 实际状态: %s\n", actual)

	switch actual {
	case state.StateRunning:
		fmt.Println("服务已正常运行，同步状态为 RUNNING")
		sm.Transition(state.StateRunning, "resume: K8s 检测服务正常")
	case state.StateIdle:
		if err := checkPendingRollback(cfg, env, sm); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("服务不存在，从头重新部署")
		executeDeploy(sm, cfg, env, plan, root)
	default:
		fmt.Printf("无法自动恢复状态 %s，请手动处理\n", actual)
		os.Exit(1)
	}
}

func runRollback(args []string) {
	flags := flag.NewFlagSet("rollback", flag.ExitOnError)
	namespace := flags.String("namespace", "", "kubernetes namespace")
	context := flags.String("context", "", "kubernetes context")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 文件路径")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "解析参数失败:", err)
		os.Exit(1)
	}
	*kubeconfig = expandHome(*kubeconfig)

	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	cfg := &deployConfig{
		namespace:  *namespace,
		context:    *context,
		kubeconfig: *kubeconfig,
	}
	resolveDeployConfig(cfg, env, root)

	version := envOrDefault(env, "VERSION", "v0.1.0")
	store := state.NewAutoStore(env["ETCD_ENDPOINTS"])
	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))

	sm, err := state.New(store, projectName, cfg.namespace, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载状态失败:", err)
		os.Exit(1)
	}

	fmt.Printf("当前状态: %s，发起回滚...\n", sm.State())
	if err := sm.Transition(state.StateRollingBack, "手动触发回滚"); err != nil {
		fmt.Fprintln(os.Stderr, "状态转换失败:", err)
		os.Exit(1)
	}

	release := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))
	if err := helmRollback(cfg.kubeconfig, cfg.context, cfg.namespace, release); err != nil {
		fmt.Fprintln(os.Stderr, "helm rollback 失败:", err)
		sm.Transition(state.StateRunning, "回滚失败，保持 RUNNING")
		os.Exit(1)
	}

	sm.Transition(state.StateRunning, "回滚成功")
	fmt.Println("✅ 回滚完成")
}

// executeDeploy 执行完整部署流程（带状态机）
func executeDeploy(sm *state.Machine, cfg *deployConfig, env map[string]string, plan []planner.Plan, root string) error {
	// IDLE/RUNNING → INITIALIZING
	if err := sm.Transition(state.StateInitializing, "开始部署 "+env["VERSION"]); err != nil {
		return err
	}

	// INITIALIZING → DEPLOYING
	if err := sm.Transition(state.StateDeploying, "执行 helm upgrade"); err != nil {
		return err
	}

	makeEnv := buildMakeEnv(env, cfg, plan)
	deployOK := false
	if err := runCmd(root, makeEnv, "make", "deploy.full"); err != nil {
		// 检查是否是 SSA 冲突，是的话自动清除 managedFields 重试一次
		if isSSAConflict(err.Error()) {
			if retryErr := retryDeployWithSSAFix(cfg, makeEnv, root); retryErr == nil {
				deployOK = true
			} else {
				err = retryErr
			}
		}
		if !deployOK {
			if sm.IsFirstDeploy() {
				sm.Transition(state.StateCleaning, "首次部署失败，清理 namespace")
				deleteNamespace(cfg.kubeconfig, cfg.context, cfg.namespace)
				sm.Transition(state.StateIdle, "清理完成")
			} else {
				sm.Transition(state.StateRollingBack, "更新失败，回滚")
				release := envOrDefault(env, "PROJECT_NAME", "")
				if rbErr := helmRollback(cfg.kubeconfig, cfg.context, cfg.namespace, release); rbErr != nil {
					sm.Transition(state.StateCleaning, "回滚失败")
				} else {
					sm.Transition(state.StateRunning, "回滚成功")
				}
			}
			return fmt.Errorf("部署失败: %w", err)
		}
	}

	// DEPLOYING → VALIDATING
	if err := sm.Transition(state.StateValidating, "验证部署结果"); err != nil {
		return err
	}

	// 验证阶段（使用状态机统一逻辑）
	if err := sm.ResumeFromValidating("部署验证通过"); err != nil {
		return err
	}

	fmt.Printf("✅ 部署完成，状态: RUNNING (version=%s)\n", env["VERSION"])

	return nil
}

// resumeFromValidating 从 VALIDATING 阶段恢复（使用状态机方法）
func resumeFromValidating(sm *state.Machine, cfg *deployConfig, env map[string]string, plan []planner.Plan, root string) {
	if err := sm.ResumeFromValidating("resume: 重新验证"); err != nil {
		fmt.Fprintln(os.Stderr, "ResumeFromValidating 失败:", err)
		return
	}

	for _, item := range plan {
		if item.Image == "" {
			continue
		}
		if err := state.ValidateDeployment(
			cfg.kubeconfig, cfg.context, cfg.namespace,
			item.Name, item.Port, 120*time.Second,
		); err != nil {
			fmt.Fprintln(os.Stderr, "验证失败:", err)
			sm.Transition(state.StateRollingBack, "resume 验证失败")
			release := envOrDefault(env, "PROJECT_NAME", "")
			helmRollback(cfg.kubeconfig, cfg.context, cfg.namespace, release)
			sm.Transition(state.StateRunning, "回滚完成")
			return
		}
	}
	sm.Transition(state.StateRunning, "resume 验证通过")
	fmt.Println("✅ 恢复成功，状态: RUNNING")
}

// detectActualState 通过 kubectl 检查 plan 里的服务是否存在
func detectActualState(cfg *deployConfig, plan []planner.Plan) state.State {
	for _, item := range plan {
		if item.Image == "" {
			continue
		}
		args := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
		args = append(args, "get", "deployment", item.Name)
		if _, err := runOutput(args...); err == nil {
			return state.StateRunning
		}
	}
	return state.StateIdle
}

// kubectlBaseArgs 构建 kubectl 基础参数（供 deploy.go 内部使用）
func kubectlBaseArgs(kubeconfig, context, namespace string) []string {
	var args []string
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	if context != "" {
		args = append(args, "--context", context)
	}
	if namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	return append([]string{"kubectl"}, args...)
}

// buildMakeEnv 构建 make 环境变量
func buildMakeEnv(env map[string]string, cfg *deployConfig, plan []planner.Plan) []string {
	makeEnv := os.Environ()
	if cfg.namespace != "" {
		makeEnv = append(makeEnv, "KUBE_NAMESPACE="+cfg.namespace)
	}
	if cfg.context != "" {
		makeEnv = append(makeEnv, "KUBE_CONTEXT="+cfg.context)
	}
	if cfg.kubeconfig != "" {
		makeEnv = append(makeEnv, "KUBE_CONFIG="+cfg.kubeconfig)
	}

	skipKeys := map[string]bool{"VERSION": true, "ARCH": true, "REGISTRY_PREFIX": true}
	for k, v := range env {
		if !skipKeys[k] {
			makeEnv = append(makeEnv, k+"="+v)
		}
	}
	makeEnv = append(makeEnv,
		"VERSION="+envOrDefault(env, "VERSION", "v0.1.0"),
		"ARCH="+envOrDefault(env, "ARCH", "amd64"),
		"REGISTRY_PREFIX="+envOrDefault(env, "REGISTRY_PREFIX", ""),
	)

	var imageNames []string
	for _, item := range plan {
		if item.Image != "" {
			imageNames = append(imageNames, item.Name)
		}
	}
	if len(imageNames) > 0 {
		makeEnv = append(makeEnv, "IMAGES="+strings.Join(imageNames, " "))
	}
	return makeEnv
}

// resolveDeployConfig 从 env 补全 deployConfig 的空字段
func resolveDeployConfig(cfg *deployConfig, env map[string]string, root string) {
	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))
	if cfg.namespace == "" {
		cfg.namespace = envOrDefault(env, "KUBE_NAMESPACE", projectName)
	}
	if cfg.context == "" {
		cfg.context = env["KUBE_CONTEXT"]
	}
	if cfg.kubeconfig == "" {
		cfg.kubeconfig = expandHome(env["KUBE_CONFIG"])
	}
}

func envOrDefault(env map[string]string, key, fallback string) string {
	if v, ok := env[key]; ok && v != "" {
		return v
	}
	return fallback
}
