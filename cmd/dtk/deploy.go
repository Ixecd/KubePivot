package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

	if err := checkHelmReleaseState(cfg, env, sm); err != nil {
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

	record := sm.Record()
	P.Info("📋", fmt.Sprintf("当前状态: %s（%s）", record.State, record.Reason))

	P.Start("🔍", "检查 K8s 实际状态")
	plan, err := planner.BuildPlan(filepath.Join(root, cfg.components))
	if err != nil {
		fmt.Fprintln(os.Stderr, "解析组件失败:", err)
		os.Exit(1)
	}

	actual, err := controller.DetectActualState(
		cfg.kubeconfig,
		cfg.namespace,
		filepath.Join(root, "configs", "resources.yaml"),
	)
	if err != nil {
		P.Fail("检测 K8s 状态失败")
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	P.Done(fmt.Sprintf("K8s 实际状态: %s", actual))

	switch actual {
	case state.StateRunning:
		P.Info("✅", "服务已正常运行，同步状态为 RUNNING")
		sm.Transition(state.StateRunning, "resume: K8s 检测服务正常")
	case state.StateIdle:
		if err := checkHelmReleaseState(cfg, env, sm); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		P.Info("🔄", "服务不存在，从头重新部署")
		executeDeploy(sm, cfg, env, plan, root)
	default:
		fmt.Fprintf(os.Stderr, "无法自动恢复状态 %s，请手动处理\n", actual)
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

	P.Info("⏪", fmt.Sprintf("当前状态: %s，发起回滚...", sm.State()))
	if err := sm.Transition(state.StateRollingBack, "手动触发回滚"); err != nil {
		fmt.Fprintln(os.Stderr, "状态转换失败:", err)
		os.Exit(1)
	}

	// 加载 components.yaml，走多服务拓扑逆序
	componentsPath := filepath.Join(root, "configs", "components.yaml")
	layers, err := planner.BuildLayers(componentsPath)
	if err != nil || len(layers) == 0 {
		// 降级：单 release
		release := projectName
		P.Start("⏪", fmt.Sprintf("helm rollback %s", release))
		if err := helmRollback(cfg.kubeconfig, cfg.context, cfg.namespace, release); err != nil {
			P.Fail("回滚失败")
			fmt.Fprintln(os.Stderr, err)
			sm.Transition(state.StateRunning, "回滚失败，保持 RUNNING")
			os.Exit(1)
		}
		P.Done("回滚完成")
		sm.Transition(state.StateRunning, "回滚成功")
		P.Info("✅", "回滚完成")
		return
	}

	// 多服务：拓扑逆序逐层 rollback
	P.Info("⏪", fmt.Sprintf("多服务模式：%d 层，逆序回滚", len(layers)))
	allOK := true
	for i := len(layers) - 1; i >= 0; i-- {
		layer := layers[i]
		P.Info("⏪", fmt.Sprintf("回滚第 %d 层（共 %d 层，%d 个服务）",
			len(layers)-i, len(layers), len(layer)))

		var wg sync.WaitGroup
		var mu sync.Mutex
		layerOK := true

		for _, plan := range layer {
			wg.Add(1)
			go func(p planner.Plan) {
				defer wg.Done()
				release := releaseName(projectName, p.Name)
				if !helmReleaseExists(cfg.kubeconfig, cfg.context, cfg.namespace, release) {
					P.Info("⏭ ", fmt.Sprintf("跳过 %s（未安装）", release))
					return
				}
				P.Start("⏪", fmt.Sprintf("rollback %s", release))
				if err := helmRollback(cfg.kubeconfig, cfg.context, cfg.namespace, release); err != nil {
					P.Fail(fmt.Sprintf("rollback %s 失败", release))
					mu.Lock()
					layerOK = false
					mu.Unlock()
				} else {
					P.Done(fmt.Sprintf("rollback %s 完成", release))
				}
			}(plan)
		}
		wg.Wait()

		if !layerOK {
			allOK = false
		}
	}

	if allOK {
		sm.Transition(state.StateRunning, "回滚成功")
		P.Info("✅", "全部服务回滚完成")
	} else {
		sm.Transition(state.StateRunning, "部分回滚失败，保持 RUNNING")
		P.Fail("部分服务回滚失败，请手动检查")
		os.Exit(1)
	}
}

// executeDeploy 执行完整部署流程（带状态机）
func executeDeploy(sm *state.Machine, cfg *deployConfig, env map[string]string, plan []planner.Plan, root string) error {
	version := envOrDefault(env, "VERSION", "v0.1.0")
	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))

	// IDLE/RUNNING → INITIALIZING
	if err := sm.Transition(state.StateInitializing, "开始部署 "+version); err != nil {
		return err
	}

	// INITIALIZING → DEPLOYING
	if err := sm.Transition(state.StateDeploying, "执行 helm upgrade"); err != nil {
		return err
	}

	// 尝试读取拓扑分层
	layers, err := planner.BuildLayers(filepath.Join(root, cfg.components))
	if err != nil {
		return fmt.Errorf("构建部署计划失败: %w", err)
	}

	// 多服务（多层 或 同层多个服务）走独立 release 路径
	isMultiService := len(layers) > 1 || (len(layers) == 1 && len(layers[0]) > 1)
	if isMultiService {
		P.Info("🗂 ", fmt.Sprintf("多服务模式：%d 层，独立 helm release", len(layers)))
		if err := deployLayers(sm, cfg, env, layers, root); err != nil {
			return err
		}
		goto validating
	}

	// ── 单服务：原有 make 路径 ────────────────────────────────────────────────
	{
		makeEnv := buildMakeEnv(env, cfg, plan)

		// ── 1. Build ─────────────────────────────────────────────────────────
		var imageList []string
		for _, item := range plan {
			if item.Image != "" {
				imageList = append(imageList, item.Image)
			}
		}

		if len(imageList) > 0 {
			P.Start("🏗 ", fmt.Sprintf("构建镜像 %s（%s）",
				strings.Join(imageList, ", "), version))
			if err := runCmd(root, makeEnv, "make", "deploy.build"); err != nil {
				P.Fail("构建失败")
				return handleDeployError(sm, cfg, env, err)
			}
			P.Done("构建完成")

			// ── 2. Push ───────────────────────────────────────────────────────
			P.Start("📤", fmt.Sprintf("推送镜像 %s（%s）",
				strings.Join(imageList, ", "), version))
			if err := runCmd(root, makeEnv, "make", "deploy.push"); err != nil {
				P.Fail("推送失败")
				return handleDeployError(sm, cfg, env, err)
			}
			P.Done("推送完成")
		}

		// ── 3. Install ────────────────────────────────────────────────────────
		P.Start("⛵", fmt.Sprintf("helm upgrade %s", projectName))
		installErr := runCmd(root, makeEnv, "make", "deploy.install")
		if installErr != nil {
			P.Fail("helm upgrade 失败")

			// SSA 冲突检测
			if isSSAConflict(installErr.Error()) {
				P.Info("🔧", "检测到 SSA 冲突，正在自动修复...")
				if retryErr := retryDeployWithSSAFix(cfg, makeEnv, root); retryErr == nil {
					P.Done("SSA 修复成功，继续部署")
					goto rollout
				} else {
					installErr = retryErr
				}
			}

			// 镜像拉取失败提示
			if isImagePullError(installErr.Error()) {
				fmt.Fprintln(os.Stderr, "")
				P.Info("❌", "检测到镜像拉取失败，请检查：")
				fmt.Fprintf(os.Stderr, "  1. 镜像是否已推送：docker manifest inspect %s/%s-%s:%s\n",
					env["REGISTRY_PREFIX"], projectName, env["ARCH"], version)
				fmt.Fprintln(os.Stderr, "  2. registry 是否需要登录：docker login")
				fmt.Fprintf(os.Stderr, "  3. ARCH 是否正确：当前 %s，集群节点架构是否匹配\n", env["ARCH"])
			}

			return handleDeployError(sm, cfg, env, installErr)
		}
		P.Done("helm upgrade 完成")

	rollout:
		// ── 4. Rollout ────────────────────────────────────────────────────────
		P.Start("🔍", "等待 rollout 就绪")
		if err := runCmd(root, makeEnv, "make", "deploy.run.all"); err != nil {
			P.Fail("rollout 超时")
			return handleDeployError(sm, cfg, env, err)
		}
		P.Done("服务就绪")
	}

validating:
	// DEPLOYING → VALIDATING → RUNNING
	if err := sm.Transition(state.StateValidating, "验证部署结果"); err != nil {
		return err
	}
	if err := sm.ResumeFromValidating("部署验证通过"); err != nil {
		return err
	}

	P.Info("✅", fmt.Sprintf("部署完成，状态: RUNNING (version=%s)", version))
	return nil
}

// handleDeployError 统一处理部署失败：首次清理 namespace，否则回滚
func handleDeployError(sm *state.Machine, cfg *deployConfig, env map[string]string, err error) error {
	if sm.IsFirstDeploy() {
		P.Info("🧹", "首次部署失败，清理 namespace")
		sm.Transition(state.StateCleaning, "首次部署失败，清理 namespace")
		deleteNamespace(cfg.kubeconfig, cfg.context, cfg.namespace)
		sm.Transition(state.StateIdle, "清理完成")
	} else {
		P.Start("⏪", "部署失败，自动回滚")
		sm.Transition(state.StateRollingBack, "更新失败，回滚")
		release := envOrDefault(env, "PROJECT_NAME", "")
		if rbErr := helmRollback(cfg.kubeconfig, cfg.context, cfg.namespace, release); rbErr != nil {
			P.Fail("回滚失败")
			sm.Transition(state.StateCleaning, "回滚失败")
		} else {
			P.Done("回滚完成")
			sm.Transition(state.StateRunning, "回滚成功")
		}
	}
	return fmt.Errorf("部署失败: %w", err)
}

// resumeFromValidating 从 VALIDATING 阶段恢复
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
	P.Info("✅", "恢复成功，状态: RUNNING")
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

// kubectlBaseArgs 构建 kubectl 基础参数
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

	arch := envOrDefault(env, "ARCH", "")
	if arch == "" {
		if out, err := exec.Command("go", "env", "GOARCH").Output(); err == nil {
			arch = strings.TrimSpace(string(out))
		}
	}
	if arch == "" {
		arch = "amd64"
	}

	makeEnv = append(makeEnv,
		"VERSION="+envOrDefault(env, "VERSION", "v0.1.0"),
		"ARCH="+arch,
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
