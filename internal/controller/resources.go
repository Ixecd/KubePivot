package controller

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Resource struct {
	Kind      string `yaml:"kind"`
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
	OnMissing string `yaml:"on_missing"`
	MaxRetry  int    `yaml:"max_retry"`
	Fallback  string `yaml:"fallback"`
}

type ResourcesConfig struct {
	Resources []Resource `yaml:"resources"`
}

// LoadResources 加载资源配置文件
// 路径优先级：RESOURCES_CONFIG 环境变量 > configs/resources.yaml
func LoadResources() (*ResourcesConfig, error) {
	path := os.Getenv("RESOURCES_CONFIG")
	if path == "" {
		path = filepath.Join("configs", "resources.yaml")
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
