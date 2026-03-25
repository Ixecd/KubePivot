package controller

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Resource struct {
	Kind      string `yaml:"kind"`
	Name      string `yaml:"name"`
	OnMissing string `yaml:"on-missing"`
	MaxRetry  int    `yaml:"max-retry"`
	Fallback  string `yaml:"fallback"`
}

type ResourcesConfig struct {
	Resources []Resource `yaml:"resources"`
}

func LoadResources() (*ResourcesConfig, error) {
	path := filepath.Join("configs", "resources.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg ResourcesConfig
	return &cfg, yaml.Unmarshal(data, &cfg)
}
