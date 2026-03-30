package controller

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/Ixecd/kubepivot/internal/state"
	"gopkg.in/yaml.v3"
)

type Resource struct {
	Kind      string `yaml:"kind"`
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
	OnMissing string `yaml:"on-missing"`
	MaxRetry  int    `yaml:"max-retry"`
	Fallback  string `yaml:"fallback"`
}

type ResourcesConfig struct {
	Resources []Resource `yaml:"resources"`
}

// Detector K8s 资源检测接口，测试时可注入 mock
type Detector interface {
	ResourceExists(kind, name, namespace string) (bool, error)
}

// KubectlDetector 真实实现，用 kubectl CLI
type KubectlDetector struct {
	kubeconfig string
}

func NewKubectlDetector(kubeconfig string) Detector {
	return &KubectlDetector{kubeconfig: kubeconfig}
}

func (d *KubectlDetector) ResourceExists(kind, name, namespace string) (bool, error) {
	return DetectResourceExists(d.kubeconfig, namespace, kind, name)
}

// LoadResources 加载资源配置文件
// path 为空时降级到环境变量 RESOURCES_CONFIG，再降级到 configs/resources.yaml
func LoadResources(path string) (*ResourcesConfig, error) {
	if path == "" {
		path = os.Getenv("RESOURCES_CONFIG")
	}
	if path == "" {
		path = "configs/resources.yaml"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 resources.yaml 失败（路径: %s）: %w", path, err)
	}

	var cfg ResourcesConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析 resources.yaml 失败: %w", err)
	}

	// 补全 namespace：未填则用环境变量 KUBE_NAMESPACE
	defaultNS := getenv("KUBE_NAMESPACE", "default")
	for i := range cfg.Resources {
		if cfg.Resources[i].Namespace == "" {
			cfg.Resources[i].Namespace = defaultNS
		}
	}

	return &cfg, nil
}

// DetectResourceExists 用 kubectl CLI 检查单个资源是否存在
func DetectResourceExists(kubeconfig, namespace, kind, name string) (bool, error) {
	args := []string{"kubectl"}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	args = append(args,
		"get", strings.ToLower(kind), name,
		"--namespace", namespace,
		"--ignore-not-found",
	)
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("kubectl get 失败: %w\n%s", err, out)
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

// DetectActualState 遍历 resources.yaml，有任意资源不存在就返回 IDLE
func DetectActualState(kubeconfig, namespace, resourcesConfigPath string) (state.State, error) {
	cfg, err := LoadResources(resourcesConfigPath)
	if err != nil {
		return "", err
	}
	if len(cfg.Resources) == 0 {
		return "", fmt.Errorf("resources.yaml 为空，无法判断状态")
	}
	for _, res := range cfg.Resources {
		ns := namespace
		if res.Namespace != "" {
			ns = res.Namespace
		}
		exists, err := DetectResourceExists(kubeconfig, ns, res.Kind, res.Name)
		if err != nil {
			return "", fmt.Errorf("检测 %s/%s 失败: %w", res.Kind, res.Name, err)
		}
		if !exists {
			slog.Info("资源不存在，判定为 IDLE", "kind", res.Kind, "name", res.Name)
			return state.StateIdle, nil
		}
	}
	return state.StateRunning, nil
}
