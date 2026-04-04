package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// KPEnv 单个集群环境配置
type KPEnv struct {
	Name       string `yaml:"name"`
	Kubeconfig string `yaml:"kubeconfig"`
	Context    string `yaml:"context"`
	Namespace  string `yaml:"namespace"`
	Registry   string `yaml:"registry_prefix,omitempty"`
	Arch       string `yaml:"arch,omitempty"`
}

// kpEnvsDir 返回 envs 配置目录
func kpEnvsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kp", "envs")
}

// kpEnvPath 返回指定 env 的配置文件路径
func kpEnvPath(name string) string {
	return filepath.Join(kpEnvsDir(), name+".yaml")
}

// loadEnv 加载指定 env 配置
func loadEnv(name string) (*KPEnv, error) {
	data, err := os.ReadFile(kpEnvPath(name))
	if err != nil {
		return nil, fmt.Errorf("env %q 不存在，请先运行: kp context add --name %s", name, name)
	}
	var env KPEnv
	if err := yaml.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("解析 env 配置失败: %w", err)
	}
	return &env, nil
}

// applyEnvToConfig 将 KPEnv 覆盖到 deployConfig
func applyEnvToConfig(cfg *deployConfig, env *KPEnv) {
	if env.Kubeconfig != "" {
		cfg.kubeconfig = expandHome(env.Kubeconfig)
	}
	if env.Context != "" {
		cfg.context = env.Context
	}
	if env.Namespace != "" {
		cfg.namespace = env.Namespace
	}
}

// applyEnvToMap 将 KPEnv 覆盖到 project.env map
func applyEnvToMap(m map[string]string, env *KPEnv) {
	if env.Registry != "" {
		m["REGISTRY_PREFIX"] = env.Registry
	}
	if env.Arch != "" {
		m["ARCH"] = env.Arch
	}
	if env.Namespace != "" {
		m["KUBE_NAMESPACE"] = env.Namespace
	}
}

func runContext(args []string) {
	if len(args) == 0 {
		fmt.Println("用法: kp context <子命令>")
		fmt.Println("  add     添加或更新集群环境")
		fmt.Println("  list    列出所有环境")
		fmt.Println("  remove  删除环境")
		fmt.Println("  show    查看环境详情")
		os.Exit(1)
	}
	switch args[0] {
	case "add":
		runContextAdd(args[1:])
	case "list", "ls":
		runContextList()
	case "remove", "rm":
		runContextRemove(args[1:])
	case "show":
		runContextShow(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n", args[0])
		os.Exit(1)
	}
}

func runContextAdd(args []string) {
	flags := flag.NewFlagSet("context add", flag.ExitOnError)
	name := flags.String("name", "", "环境名称（必填，如 staging / prod）")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 文件路径（默认 ~/.kube/config）")
	context := flags.String("context", "", "kubernetes context 名称")
	namespace := flags.String("namespace", "", "默认 namespace")
	registry := flags.String("registry", "", "镜像仓库前缀（覆盖 project.env 的 REGISTRY_PREFIX）")
	arch := flags.String("arch", "", "镜像架构（arm64/amd64）")
	flags.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "❌ --name 必填")
		os.Exit(1)
	}

	env := &KPEnv{
		Name:       *name,
		Kubeconfig: *kubeconfig,
		Context:    *context,
		Namespace:  *namespace,
		Registry:   *registry,
		Arch:       *arch,
	}

	if err := os.MkdirAll(kpEnvsDir(), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "创建目录失败:", err)
		os.Exit(1)
	}

	data, _ := yaml.Marshal(env)
	if err := os.WriteFile(kpEnvPath(*name), data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "写入失败:", err)
		os.Exit(1)
	}

	P.Done(fmt.Sprintf("环境 %q 已保存（%s）", *name, kpEnvPath(*name)))
	fmt.Printf("\n使用方式：\n")
	fmt.Printf("  kp deploy --env %s\n", *name)
	fmt.Printf("  kp status --env %s\n", *name)
	fmt.Printf("  kp diff --from local --to %s\n\n", *name)
}

func runContextList() {
	entries, err := os.ReadDir(kpEnvsDir())
	if err != nil {
		fmt.Println("暂无已配置的环境，运行 kp context add 添加")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintf(w, "  %-12s  %-30s  %-20s  %s\n", "名称", "context", "namespace", "kubeconfig")
	fmt.Fprintf(w, "  %s\n", strings.Repeat("─", 80))

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yaml")
		env, err := loadEnv(name)
		if err != nil {
			continue
		}
		kc := env.Kubeconfig
		if kc == "" {
			kc = "~/.kube/config（默认）"
		}
		fmt.Fprintf(w, "  %-12s  %-30s  %-20s  %s\n",
			env.Name, env.Context, env.Namespace, kc)
	}
	w.Flush()
}

func runContextRemove(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "用法: kp context remove <name>")
		os.Exit(1)
	}
	name := args[0]
	path := kpEnvPath(name)
	if err := os.Remove(path); err != nil {
		fmt.Fprintf(os.Stderr, "删除失败: %v\n", err)
		os.Exit(1)
	}
	P.Done(fmt.Sprintf("环境 %q 已删除", name))
}

func runContextShow(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "用法: kp context show <name>")
		os.Exit(1)
	}
	env, err := loadEnv(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	data, _ := json.MarshalIndent(env, "  ", "  ")
	fmt.Printf("  %s\n", string(data))
}
