package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
	"github.com/Ixecd/kubepivot/internal/state"
	"github.com/Ixecd/kubepivot/internal/supplychain"
	"gopkg.in/yaml.v3"
)

type Resource struct {
	// v2.4.0：显式 helm release 名（蓝绿 / 金丝雀场景下覆盖默认推断）
	// 不声明则走 findReleaseForResource 的默认推断（PROJECT_NAME-Name）
	HelmRelease string `yaml:"helm-release,omitempty"`

	Kind         string   `yaml:"kind"`
	Name         string   `yaml:"name"`
	Namespace    string   `yaml:"namespace"`
	OnMissing    string   `yaml:"on-missing"`
	MaxRetry     int      `yaml:"max-retry"`
	Fallback     string   `yaml:"fallback"`
	ForceSync    bool     `yaml:"force-sync"`
	NoSyncFields []string `yaml:"no-sync-fields"`

	// yaml tag 用 supply-chain 保持用户配置文件可读性
	// json tag 用 supply_chain 保持 API 一致性
	// 指针类型：nil = 不覆盖，使用全局默认策略
	SupplyChain *supplychain.SupplyChainConfig `yaml:"supply-chain,omitempty" json:"supply_chain,omitempty"`
	// 👇 豆包小姐专为 KubePivot 增加的运行时标签字段
	Labels map[string]string `yaml:"-"`
}

type ResourcesConfig struct {
	Resources []Resource `yaml:"resources"`

	// v2.6.0：流量层配置（蓝绿部署）
	// 不声明则 v2.5.0 行为不变（仅 reconcile 资源列表）
	Traffic *Traffic `yaml:"traffic,omitempty"`
}

// Traffic 是 resources.yaml 的 v2.6 流量层配置入口。
//
// 完整示例：
//
//	traffic:
//	  kind: Ingress              # 可选：Ingress / Gateway / 不写=自动检测
//	  strategy: blue-green       # v2.6.0 仅 blue-green
//	  refs:
//	    name: wallet-ingress     # Ingress 或 HTTPRoute 资源名
//	  routes:
//	    - service: wallet-service-blue
//	      weight: 100
//	    - service: wallet-service-green
//	      weight: 0
//	  validation:
//	    podReadyTimeoutSec: 60
type Traffic struct {
	// Kind 流量层后端类型（"Ingress" / "Gateway" / "GatewayAPI" / 空）
	// 空时自动检测：Gateway API 优先，Ingress 兜底
	Kind string `yaml:"kind,omitempty"`

	// Strategy 部署策略，v2.6.0 仅支持 "blue-green"
	Strategy string `yaml:"strategy"`

	// Refs 流量层资源引用
	Refs TrafficRefs `yaml:"refs"`

	// Routes 路由规则列表
	// 蓝绿场景：恰好两个 entry，weight 一个 100 一个 0
	Routes []TrafficRoute `yaml:"routes"`

	// Validation 部署后健康度验证参数
	Validation TrafficValidation `yaml:"validation,omitempty"`
}

// TrafficRefs 流量层资源引用。
type TrafficRefs struct {
	// Name 流量层资源名（Ingress 或 HTTPRoute）
	Name string `yaml:"name"`

	// Namespace 流量层资源所在 namespace（一般同项目 ns，可省略）
	Namespace string `yaml:"namespace,omitempty"`
}

// TrafficRoute 单条路由规则。
//
// 注：与 internal/route.Route 是双层结构：
//   - TrafficRoute 是 yaml 解析层
//   - route.Route 是流量层 Provider 接口的中间表示
//
// 两者通过 cmd/kp/sandbox.go 的 runBlueGreenSwitch() 转换。
type TrafficRoute struct {
	// Service 目标 K8s Service 名
	Service string `yaml:"service"`

	// Weight 流量权重 [0, 100]
	Weight int32 `yaml:"weight"`
}

// TrafficValidation 部署后的健康度验证参数。
type TrafficValidation struct {
	// PodReadyTimeoutSec Pod ready 等待超时秒数（默认 60）
	PodReadyTimeoutSec int `yaml:"podReadyTimeoutSec,omitempty"`
}

// HasBlueGreen 判断 ResourcesConfig 是否启用蓝绿流量切换。
//
// 满足条件：
//   - Traffic 字段不为 nil
//   - Traffic.Strategy == "blue-green"
//   - Traffic.Refs.Name 非空
//   - 至少一个 Route weight > 0
//
// kp sandbox commit 在 COMMITTING 阶段调用此方法决定是否执行流量切换。
func (cfg *ResourcesConfig) HasBlueGreen() bool {
	if cfg.Traffic == nil {
		return false
	}
	if cfg.Traffic.Strategy != "blue-green" {
		return false
	}
	if cfg.Traffic.Refs.Name == "" {
		return false
	}
	for _, r := range cfg.Traffic.Routes {
		if r.Weight > 0 {
			return true
		}
	}
	return false
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	args := []string{
		"get", strings.ToLower(kind), name,
		"--namespace", namespace,
		"--ignore-not-found",
	}
	out, err := executor.GetExecutor().Kubectl(ctx, kubeconfig, args...)
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
